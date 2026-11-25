// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"cmp"
	"math"
	"slices"
	"strconv"

	corev1 "k8s.io/api/core/v1"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/signals"
)

// Метрики, этап B (ADR-0032 §4): сигналы хранилища, автомасштабирования, доступности реплик и входящего трафика.
// Субъект сигнала берётся только из узлов графа (Context.Graph) и ResourceRef цели: объекта нет в графе — сигнал не
// запрашивается. Правила не зависят от реализации видов D1–D4: чужую гипотезу они ищут по коду в отчёте уже после
// правил (пост-хук 200), а её отсутствие — штатный случай, тогда выдаётся собственная гипотеза с префиксом metrics.

// Пороги этапа B (ADR-0032 §4). Изменение порога меняет вывод отчёта и требует поднять эпоху правила.
const (
	// pvcFull — том занят не менее чем на 90%: приложение вот-вот упрётся в «no space left on device».
	pvcFull = 0.90
	// pvcAlmostFull — не менее 97%: свободного места почти нет, гипотеза «том заполнен» перестаёт быть слабой.
	pvcAlmostFull = 0.97
	// hpaAtMax — HPA упирался в maxReplicas в окне сигнала (значение сигнала — флаг 1).
	hpaAtMax = 1.0
	// ingress5xxHigh — не менее 5% ответов 5xx за окно: ошибки сервера заметны большой доле запросов.
	ingress5xxHigh = 0.05
	// ingressSlowSeconds — p95 задержки не менее 2 секунд: каждый двадцатый запрос ждёт дольше.
	ingressSlowSeconds = 2.0
)

// Сколько объектов каждого вида берётся в метрики: вместе с сигналами этапа A укладывается в MaxSignalQueries.
const (
	maxPVCSignals        = 3 // 1 сигнал на PVC
	maxHPASignals        = 2 // 1 сигнал на HPA
	maxDeploymentSignals = 2 // 1 сигнал на Deployment
	maxIngressSignals    = 2 // 2 сигнала на Ingress
)

// Идентификаторы правил этапа B (контракт §2) и их эпоха (ADR-0034 §3). Эпоха растёт при изменении смысла правила.
const (
	ruleMetricsPVCFull             = "metrics.pvc_full"
	ruleMetricsHPASaturated        = "metrics.hpa_saturated"
	ruleMetricsUnavailableReplicas = "metrics.unavailable_replicas"
	ruleMetricsIngressErrors       = "metrics.ingress_errors"
	metricsStageBEpoch             = 1
)

// Коды гипотез, добавляемых метриками этапа B, и коды чужих гипотез, которые метрики усиливают (контракт §2).
const (
	hypPVCNearlyFull         = "metrics.pvc_nearly_full"
	hypHPASaturated          = "metrics.hpa_saturated"
	hypDeploymentUnavailable = "metrics.deployment_unavailable"
	hypIngressDegraded       = "metrics.ingress_degraded"

	hypStsPVCPending      = "sts.pvc_pending"
	hypHPAMaxReached      = "hpa.max_replicas_reached"
	hypIngressNoEndpoints = "ingress.backend_no_endpoints"
)

// Группы и виды узлов графа (контракт §1).
const (
	groupApps        = "apps"
	groupAutoscaling = "autoscaling"
)

// graphObjects возвращает прочитанные узлы графа вида в порядке ключей (граф отдаёт их отсортированными): заглушки
// Missing и нечитаемые узлы не имеют ни метрик, ни права на запрос. ok отбирает узлы по состоянию.
func graphObjects(c *Context, group, kind string, ok func(depgraph.Node) bool) []depgraph.Node {
	if c == nil || c.Graph == nil {
		return nil
	}
	var out []depgraph.Node
	for _, n := range c.Graph.Nodes() {
		if n.Group != group || n.Kind != kind || n.Placeholder || n.Health == depgraph.Missing || n.Health == depgraph.Unknown {
			continue
		}
		if ok == nil || ok(n) {
			out = append(out, n)
		}
	}
	return out
}

// targetIs сообщает, что цель расследования — один из видов.
func targetIs(c *Context, kinds ...string) bool { return slices.Contains(kinds, c.Target.Kind) }

// troubled — расследование касается неисправности: Pod не готов, у Deployment есть недоступные реплики или в графе
// есть вершина Degraded либо Missing. Метрики этапа B объясняют найденную проблему, а не ищут её, поэтому для
// здорового ресурса они не запрашиваются и подсказок «с мониторингом точнее» нет.
func troubled(c *Context) bool {
	for _, p := range c.Pods {
		if p.DeletionTimestamp == nil && p.Status.Phase != corev1.PodSucceeded && !podReady(p) {
			return true
		}
	}
	if c.Deployment != nil && c.Deployment.Status.UnavailableReplicas > 0 {
		return true
	}
	if c.Graph != nil {
		for _, n := range c.Graph.Nodes() {
			if (!n.Placeholder && n.Health == depgraph.Degraded) || n.Health == depgraph.Missing {
				return true
			}
		}
	}
	return false
}

// wantsPVC — занятость привязанных томов: только при неисправности и только для Bound PVC (у остальных нет статистики).
func wantsPVC(c *Context) []signals.Want {
	if !troubled(c) && !targetIs(c, "StatefulSet", "PersistentVolumeClaim") {
		return nil
	}
	var out []signals.Want
	for _, n := range graphObjects(c, "", "PersistentVolumeClaim", func(n depgraph.Node) bool { return n.Health == depgraph.Healthy }) {
		if len(out) == maxPVCSignals {
			break
		}
		out = append(out, signals.Want{Signal: signals.PVCUsageRatio, Subject: signals.Subject{Namespace: n.Namespace, PVC: n.Name}})
	}
	return out
}

// wantsHPA — упирался ли HPA из графа в максимум: для цели HPA всегда, для остальных — при неисправности.
func wantsHPA(c *Context) []signals.Want {
	if !troubled(c) && !targetIs(c, "HorizontalPodAutoscaler") {
		return nil
	}
	var out []signals.Want
	for _, n := range graphObjects(c, groupAutoscaling, "HorizontalPodAutoscaler", nil) {
		if len(out) == maxHPASignals {
			break
		}
		out = append(out, signals.Want{Signal: signals.HPAAtMaxReplicas, Subject: signals.Subject{Namespace: n.Namespace, HPA: n.Name}})
	}
	return out
}

// wantsUnavailable — недоступные реплики Deployment из контекста и графа при неисправности.
func wantsUnavailable(c *Context) []signals.Want {
	if !troubled(c) {
		return nil
	}
	seen := map[signals.Subject]bool{}
	var subs []signals.Subject
	add := func(ns, name string) {
		s := signals.Subject{Namespace: ns, Deployment: name}
		if !seen[s] {
			seen[s] = true
			subs = append(subs, s)
		}
	}
	if c.Deployment != nil {
		add(c.Deployment.Namespace, c.Deployment.Name)
	}
	for _, n := range graphObjects(c, groupApps, "Deployment", nil) {
		add(n.Namespace, n.Name)
	}
	slices.SortFunc(subs, func(a, b signals.Subject) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Deployment, b.Deployment))
	})
	var out []signals.Want
	for _, s := range subs {
		if len(out) == maxDeploymentSignals {
			break
		}
		out = append(out, signals.Want{Signal: signals.DeploymentUnavailableReplicas, Subject: s})
	}
	return out
}

// wantsIngress — ошибки и задержка Ingress из графа: для цели Ingress всегда, для остальных — при неисправности.
// Сигналы выполняются, только если ingress-nginx обнаружен (семейство ingress, ADR-0032 §5): иначе — пробел family_missing.
func wantsIngress(c *Context) []signals.Want {
	if !troubled(c) && !targetIs(c, "Ingress") {
		return nil
	}
	var out []signals.Want
	for i, n := range graphObjects(c, groupNetworking, "Ingress", nil) {
		if i == maxIngressSignals {
			break
		}
		s := signals.Subject{Namespace: n.Namespace, Ingress: n.Name}
		out = append(out, signals.Want{Signal: signals.Ingress5xxRatio, Subject: s}, signals.Want{Signal: signals.IngressLatencyP95, Subject: s})
	}
	return out
}

// graphResource — ссылка на ресурс отчёта по узлу графа: для цели расследования берётся сама цель (с UID и группой),
// иначе ссылка графа. Так факты метрик и гипотезы правил D1–D4 затрагивают один и тот же ресурс.
func graphResource(c *Context, group, kind, ns, name string) ResourceRef {
	if t := c.Target; t.Kind == kind && t.Namespace == ns && t.Name == name {
		if t.Group == "" {
			t.Group = group
		}
		return t
	}
	return ResourceRef{Group: group, Kind: kind, Namespace: ns, Name: name}
}

// applyStageB — правила этапа B. Каждое — отдельное правило с идентификатором из контракта: сбой одного не мешает
// остальным и не ломает отчёт.
func (e *evidence) applyStageB() {
	for _, r := range []struct {
		id string
		fn func()
	}{
		{ruleMetricsPVCFull, e.pvcFull},
		{ruleMetricsHPASaturated, e.hpaSaturated},
		{ruleMetricsUnavailableReplicas, e.unavailableReplicas},
		{ruleMetricsIngressErrors, e.ingressErrors},
	} {
		e.asRule(r.id, r.fn)
	}
}

// asRule выполняет правило под его идентификатором и эпохой и возвращает прежние. Паника правила — MissingContext
// «rule.failed», как у обычных правил.
func (e *evidence) asRule(id string, fn func()) {
	b := e.b
	prevID, prevEpoch := b.ruleID, b.ruleEpoch
	b.ruleID, b.ruleEpoch = id, metricsStageBEpoch
	defer func() {
		b.ruleID, b.ruleEpoch = prevID, prevEpoch
		if recover() != nil {
			b.missing("rule.failed", b.c.Target, map[string]string{"rule": id})
		}
	}()
	fn()
}

// strengthen поднимает уверенность чужой гипотезы не более чем на один уровень (high метриками не достигается) и
// добавляет подтверждающий факт. Индекс — позиция в отчёте: гипотезы выдаются другими правилами.
func (e *evidence) strengthen(i int, facts ...string) {
	h := &e.b.rep.Hypotheses[i]
	for _, f := range facts {
		h.Supporting = appendUnique(h.Supporting, f)
	}
	h.Confidence = raise(h.Confidence)
}

// findHypotheses ищет гипотезы с кодом об объекте: по ресурсу (вид и имя) либо по параметру из keys. Порядок отчёта.
func (e *evidence) findHypotheses(code, kind, ns, name string, keys ...string) []int {
	var out []int
	for i, h := range e.b.rep.Hypotheses {
		if h.Code != code {
			continue
		}
		byRes := h.Resource.Kind == kind && h.Resource.Name == name && h.Resource.Namespace == ns
		byParam := slices.ContainsFunc(keys, func(k string) bool { return h.Params[k] == name }) && (h.Resource.Namespace == ns || h.Resource.Namespace == "")
		if byRes || byParam {
			out = append(out, i)
		}
	}
	return out
}

// wantsOf возвращает запрошенные цели сигнала в порядке запроса.
func (e *evidence) wantsOf(sig signals.Signal) []signals.Subject {
	var out []signals.Subject
	for _, w := range e.o.Wants {
		if w.Signal == sig && !slices.Contains(out, w.Subject) {
			out = append(out, w.Subject)
		}
	}
	return out
}

func percentOf(v float64) string { return strconv.FormatFloat(math.Round(v*100), 'f', 0, 64) }

// pvcFull — metrics.pvc_full: том занят не менее чем на 90%. Усиливает гипотезу хранилища sts.pvc_pending, если она
// есть, иначе выдаёт metrics.pvc_nearly_full.
func (e *evidence) pvcFull() {
	for _, s := range e.wantsOf(signals.PVCUsageRatio) {
		v, fid, ok := e.get(signals.PVCUsageRatio, s)
		if !ok || v.Value < pvcFull {
			continue
		}
		if idx := e.findHypotheses(hypStsPVCPending, "PersistentVolumeClaim", s.Namespace, s.PVC, "pvc", "claim"); len(idx) > 0 {
			for _, i := range idx {
				e.strengthen(i, fid)
			}
			continue
		}
		res := subjectRef(e.b.c, s)
		if e.hasHypothesis(hypPVCNearlyFull, "pvc", s.PVC) {
			continue
		}
		conf := ConfidenceLow
		if v.Value >= pvcAlmostFull {
			conf = ConfidenceMedium
		}
		params := map[string]string{"pvc": s.PVC, "percent": percentOf(v.Value)}
		h := e.b.hypothesis(res, hypPVCNearlyFull, conf, params, []string{fid}, nil)
		e.b.recommend(res, "rec.expand_pvc", map[string]string{"pvc": s.PVC}, h)
	}
}

// hpaSaturated — metrics.hpa_saturated: HPA упирался в maxReplicas. Усиливает hpa.max_replicas_reached, если она
// есть, иначе выдаёт metrics.hpa_saturated.
func (e *evidence) hpaSaturated() {
	for _, s := range e.wantsOf(signals.HPAAtMaxReplicas) {
		v, fid, ok := e.get(signals.HPAAtMaxReplicas, s)
		if !ok || v.Value < hpaAtMax {
			continue
		}
		if idx := e.findHypotheses(hypHPAMaxReached, "HorizontalPodAutoscaler", s.Namespace, s.HPA, "hpa"); len(idx) > 0 {
			for _, i := range idx {
				e.strengthen(i, fid)
			}
			continue
		}
		if e.hasHypothesis(hypHPASaturated, "hpa", s.HPA) {
			continue
		}
		res := subjectRef(e.b.c, s)
		params := map[string]string{"hpa": s.HPA}
		h := e.b.hypothesis(res, hypHPASaturated, ConfidenceLow, params, []string{fid}, nil)
		e.b.recommend(res, "rec.review_hpa_max_replicas", params, h)
	}
}

// unavailableReplicas — metrics.unavailable_replicas: у Deployment были недоступные реплики в окне. Уверенность
// medium, если Kubernetes показывает их и сейчас (статус Deployment), иначе low: окно могло захватить уже прошедшее.
func (e *evidence) unavailableReplicas() {
	for _, s := range e.wantsOf(signals.DeploymentUnavailableReplicas) {
		v, fid, ok := e.get(signals.DeploymentUnavailableReplicas, s)
		if !ok || v.Value <= 0 || e.hasHypothesis(hypDeploymentUnavailable, "deployment", s.Deployment) {
			continue
		}
		conf := ConfidenceLow
		if d := e.b.c.Deployment; d != nil && d.Namespace == s.Namespace && d.Name == s.Deployment && d.Status.UnavailableReplicas > 0 {
			conf = ConfidenceMedium
		}
		params := map[string]string{"deployment": s.Deployment, "replicas": formatMetric(v), "window": formatWindow(v.Window)}
		e.b.hypothesis(subjectRef(e.b.c, s), hypDeploymentUnavailable, conf, params, []string{fid}, nil)
	}
}

// ingressErrors — metrics.ingress_errors: не менее 5% ответов 5xx или p95 задержки не менее 2 секунд. Усиливает
// ingress.backend_no_endpoints, если она есть, иначе выдаёт metrics.ingress_degraded (medium, если плохи оба сигнала).
func (e *evidence) ingressErrors() {
	for _, s := range e.wantsOf(signals.Ingress5xxRatio) {
		var bad []string
		if v, fid, ok := e.get(signals.Ingress5xxRatio, s); ok && v.Value >= ingress5xxHigh {
			bad = append(bad, fid)
		}
		if v, fid, ok := e.get(signals.IngressLatencyP95, s); ok && v.Value >= ingressSlowSeconds {
			bad = append(bad, fid)
		}
		if len(bad) == 0 {
			continue
		}
		if idx := e.findHypotheses(hypIngressNoEndpoints, "Ingress", s.Namespace, s.Ingress, "ingress"); len(idx) > 0 {
			for _, i := range idx {
				e.strengthen(i, bad...)
			}
			continue
		}
		if e.hasHypothesis(hypIngressDegraded, "ingress", s.Ingress) {
			continue
		}
		conf := ConfidenceLow
		if len(bad) == 2 {
			conf = ConfidenceMedium
		}
		res := subjectRef(e.b.c, s)
		params := map[string]string{"ingress": s.Ingress}
		h := e.b.hypothesis(res, hypIngressDegraded, conf, params, bad, nil)
		e.b.recommend(res, "rec.review_ingress_backend", params, h)
	}
}
