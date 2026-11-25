// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/24DSO-lab/kubron/internal/signals"
)

// Метрики — необязательный источник доказательств (ADR-0032): сбор, оценка и подсказки о недостающем живут в трёх
// файлах — collect_signals.go (что запросить и запрос), metrics_evidence.go (факты и гипотезы), metrics_missing.go
// (MissingContext). Логика написана чистыми функциями над Context, builder и signalOutcome; хуки ядра D3
// (RegisterCollectHook(200, "signals"), registerPostHook(200, "metrics")) лишь вызывают collectSignals и applyMetrics.
// Пока ядро не влито, хуки не зарегистрированы, и поведение отчёта не меняется.

// minSignalBudget — наименьший остаток времени операции, при котором метрики ещё запрашиваются (ADR-0032 §6):
// запросы идут параллельно, но каждый может занять секунды, а диагностика без метрик
// важнее метрик без диагностики.
const minSignalBudget = 5 * time.Second

// Сколько контейнеров берётся на симптом: вместе с сигналами Node укладывается в бюджет MaxSignalQueries.
const (
	crashLoopContainers = 2 // 5 сигналов на контейнер + 2 сигнала Node = 12
	oomContainers       = 3 // 3 сигнала на контейнер + 2 сигнала Node = 11
)

// signalOutcome — итог сбора метрик одной операции: что запрашивалось и что получено. nil — метрики не запрашивались
// (провайдера нет или нет симптома, которому они нужны): отчёт при этом не отличается от отчёта без метрик.
type signalOutcome struct {
	Wants []signals.Want
	Set   *signals.Set
	// Skipped — метрики не запрашивались из-за недостатка времени операции либо провайдер вернул программную ошибку.
	Skipped bool
}

// signalWants — реестр правил диагностики: какие сигналы нужны правилу найденного симптома. Функция смотрит на
// Context (сбор идёт до правил) и возвращает пусто, если симптом правила не обнаружен: тогда метрики не запрашиваются
// и подсказок о них нет. Порядок записей — порядок приоритета при нехватке бюджета.
var signalWants = []struct {
	rule  string
	wants func(c *Context) []signals.Want
}{
	{"pod.crash_loop", wantsCrashLoop},
	{"pod.oom_killed", wantsOOMKilled},
	// Этап B (metrics_stageb.go): после сигналов Pod, чтобы нехватка бюджета не отнимала у них запросы.
	{"metrics.pvc_full", wantsPVC},
	{"metrics.hpa_saturated", wantsHPA},
	{"metrics.unavailable_replicas", wantsUnavailable},
	{"metrics.ingress_errors", wantsIngress},
}

// collectWants объединяет сигналы всех сработавших правил без повторов и не больше MaxSignalQueries.
func collectWants(c *Context) []signals.Want {
	var out []signals.Want
	seen := map[signals.Want]bool{}
	for _, r := range signalWants {
		for _, w := range r.wants(c) {
			if !seen[w] && len(out) < signals.MaxSignalQueries {
				seen[w] = true
				out = append(out, w)
			}
		}
	}
	return out
}

// crashLoopContainer повторяет условие правила pod.crash_loop (rules.go): цикл перезапусков контейнера. Дублирование
// осознанное — rules.go принадлежит ядру; согласованность держит TestSignalWantsFollowSymptoms.
func crashLoopContainer(c containerView) bool {
	return c.WaitingReason == "CrashLoopBackOff" || (c.Last != nil && c.Last.ExitCode != 0 && c.RestartCount >= 3 && !c.Ready)
}

// podContainer — контейнер пода, ставший целью метрик.
type podContainer struct {
	pod *corev1.Pod
	c   containerView
}

// candidates выбирает контейнеры (не init), удовлетворяющие условию, в детерминированном порядке: больше рестартов —
// раньше, затем по имени пода и контейнера. Порядок входного Context не влияет на результат.
func candidates(c *Context, match func(containerView) bool, limit int) []podContainer {
	var out []podContainer
	for _, p := range c.Pods {
		for _, cv := range containers(p) {
			if !cv.Init && match(cv) {
				out = append(out, podContainer{p, cv})
			}
		}
	}
	slices.SortFunc(out, func(a, b podContainer) int {
		return cmp.Or(
			cmp.Compare(b.c.RestartCount, a.c.RestartCount),
			cmp.Compare(a.pod.Namespace, b.pod.Namespace),
			cmp.Compare(a.pod.Name, b.pod.Name),
			cmp.Compare(a.c.Name, b.c.Name),
		)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func containerSubject(pc podContainer) signals.Subject {
	return signals.Subject{Namespace: pc.pod.Namespace, Pod: pc.pod.Name, Container: pc.c.Name}
}

// nodeWants — сигналы Node, на которой работает первый из выбранных подов. Пода без Node (ещё не назначен) нет в
// выборке симптомов crash/OOM, но проверка дёшева.
func nodeWants(cs []podContainer) []signals.Want {
	if len(cs) == 0 || cs[0].pod.Spec.NodeName == "" {
		return nil
	}
	n := signals.Subject{Node: cs[0].pod.Spec.NodeName}
	return []signals.Want{{Signal: signals.NodePressureHistory, Subject: n}, {Signal: signals.NodeMemoryAvailableRatio, Subject: n}}
}

// wantsCrashLoop — рестарты, троттлинг и запас по ресурсам худших контейнеров и состояние их Node.
func wantsCrashLoop(c *Context) []signals.Want {
	cs := candidates(c, crashLoopContainer, crashLoopContainers)
	var out []signals.Want
	for _, pc := range cs {
		s := containerSubject(pc)
		for _, sig := range []signals.Signal{
			signals.ContainerRestarts, signals.CPUThrottlingRatio, signals.MemoryUsageVsLimit, signals.OOMEvents, signals.CPUUsageVsLimit,
		} {
			out = append(out, signals.Want{Signal: sig, Subject: s})
		}
	}
	return append(out, nodeWants(cs)...)
}

// wantsOOMKilled — запас по памяти, OOM-события и рестарты контейнеров, убитых по памяти, и состояние их Node.
func wantsOOMKilled(c *Context) []signals.Want {
	cs := candidates(c, func(cv containerView) bool { return firstOOM(cv) != nil }, oomContainers)
	var out []signals.Want
	for _, pc := range cs {
		s := containerSubject(pc)
		for _, sig := range []signals.Signal{signals.MemoryUsageVsLimit, signals.OOMEvents, signals.ContainerRestarts} {
			out = append(out, signals.Want{Signal: sig, Subject: s})
		}
	}
	return append(out, nodeWants(cs)...)
}

// collectSignals — тело хука сбора. Возвращает nil, если метрики не нужны (провайдера нет или симптомов нет). Не
// возвращает ошибку и не паникует: метрики необязательны, и их сбой отчёт не ломает. Источник, прочитанный частично
// (unavailable у существующего стека), попадает в Context.Partial.
func collectSignals(ctx context.Context, c *Context, p signals.Provider) *signalOutcome {
	if p == nil || c == nil {
		return nil
	}
	wants := collectWants(c)
	if len(wants) == 0 {
		return nil
	}
	out := &signalOutcome{Wants: wants}
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < minSignalBudget {
		out.Skipped = true
		return out
	}
	set, err := fetchSignals(ctx, p, signals.Request{At: c.ObservedAt, Want: wants})
	if err != nil || set == nil {
		out.Skipped = true
		return out
	}
	out.Set = set
	if src := set.PartialSource(); src != "" {
		c.addPartial(src)
	}
	return out
}

// fetchSignals изолирует панику провайдера: она становится ошибкой, а не падением расследования.
func fetchSignals(ctx context.Context, p signals.Provider, req signals.Request) (set *signals.Set, err error) {
	defer func() {
		if recover() != nil {
			set, err = nil, errors.New("signals provider failed")
		}
	}()
	return p.Fetch(ctx, req)
}

// Идентификатор и эпоха правил метрик у записей, которые создаёт пост-хук (ADR-0034 §3). Эпоха растёт при изменении
// смысла: порогов, состава гипотез, условий их добавления.
const (
	metricsRuleID    = "metrics.evidence"
	metricsRuleEpoch = 1
)

func init() {
	RegisterCollectHook(200, "signals", func(ctx context.Context, _ Reader, c *Context) {
		o := collectSignals(ctx, c, c.opts.Signals)
		if o == nil {
			return
		}
		c.metrics = o
		c.Signals = o.Set
	})
	registerPostHook(200, "metrics", func(b *builder) {
		b.ruleID, b.ruleEpoch = metricsRuleID, metricsRuleEpoch
		applyMetrics(b, b.c.metrics)
	})
}
