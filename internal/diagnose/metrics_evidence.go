// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/24DSO-lab/kubron/internal/signals"
)

// Пороги интерпретации метрик (ADR-0032 §6). Это эвристики для подтверждения гипотез, а не диагнозы: метрики
// поднимают уверенность не выше medium (high доказывает только сам Kubernetes: статус, событие). Каждый порог —
// константа с обоснованием; изменение порога меняет вывод отчёта и требует версии набора правил.
const (
	// memNearLimit — пик рабочего набора за час не менее 90% лимита: запас меньше десятой части, и обычный всплеск
	// потребления упирается в лимит (cgroup убивает контейнер при превышении).
	memNearLimit = 0.90
	// memFarFromLimit — пик ниже 60% лимита: запаса много, «лимит слишком мал» по этой метрике маловероятно.
	memFarFromLimit = 0.60
	// cpuThrottled — не менее 25% периодов CFS с троттлингом: задержка заметна приложению (тот же порог у
	// стандартного предупреждения kubernetes-mixin CPUThrottlingHigh).
	cpuThrottled = 0.25
	// cpuNearLimit — пик использования CPU не менее 90% лимита: троттлинг вызван лимитом, а не редким всплеском.
	cpuNearLimit = 0.90
	// restartsGrowing и restartsGrowingStrong — число рестартов за 6 часов. Нижняя граница совпадает с порогом
	// правила pod.crash_loop (RestartCount >= 3): метрика показывает, что рестарты недавние, а не накопленные давно.
	restartsGrowing       = 3
	restartsGrowingStrong = 6
	// nodeMemLow — минимум доступной памяти Node за час не выше 10%: Node близка к вытеснению Pod'ов.
	nodeMemLow = 0.10
	// nodeMemHealthy — минимум доступной памяти не ниже 30%: Node за час не подходила к нехватке памяти.
	nodeMemHealthy = 0.30
	// Диск и CPU Node (node_disk_available_ratio, node_cpu_utilization) на этапе A дают только факты: порогов и
	// гипотез по ним нет, пока правила не просят этих сигналов.
)

// Коды гипотез, добавляемых метриками.
const (
	hypCPULimited      = "metrics.cpu_limited"
	hypMemoryNearLimit = "metrics.memory_near_limit"
	hypRestartsGrowing = "metrics.restarts_growing"
	hypNodePressure    = "metrics.node_pressure"
)

// sourceMetric — вид источника «метрика». После слияния ядра D3 заменяется его SourceMetric с полем Signal; точка
// замены одна — metricSource.
const sourceMetric SourceKind = "metric"

func metricSource(sig signals.Signal, res ResourceRef) Source {
	return Source{Kind: sourceMetric, Resource: res, Field: "signal/" + string(sig)}
}

// applyMetrics — тело пост-хука: добавляет факты метрик, корректирует уверенность существующих гипотез не более чем
// на один уровень, добавляет новые гипотезы (не выше medium), снимает шаги «проверить вручную» и записывает подсказки
// о недостающем. nil — метрик не было: отчёт не меняется ни на байт.
func applyMetrics(b *builder, o *signalOutcome) {
	if o == nil {
		return
	}
	if o.Set != nil && o.Set.StackReason() == "" {
		e := &evidence{b: b, o: o, fact: map[signals.Want]string{}, val: map[signals.Want]signals.Value{}}
		e.addFacts()
		e.adjustExisting()
		e.addHypotheses()
		e.dropManualSteps()
	}
	addMetricsMissing(b, o)
}

// evidence — состояние оценки метрик одного отчёта.
type evidence struct {
	b    *builder
	o    *signalOutcome
	fact map[signals.Want]string        // идентификатор факта по запрошенному сигналу
	val  map[signals.Want]signals.Value // полученные значения
}

func (e *evidence) get(sig signals.Signal, s signals.Subject) (signals.Value, string, bool) {
	w := signals.Want{Signal: sig, Subject: s}
	v, ok := e.val[w]
	return v, e.fact[w], ok
}

// addFacts добавляет факт metric.<signal> по каждому полученному значению в порядке запроса. Сигналы без значения
// фактов не дают: нет данных не превращается в наблюдение.
func (e *evidence) addFacts() {
	for _, w := range e.o.Wants {
		v, ok := e.o.Set.Get(w.Signal, w.Subject)
		if !ok {
			continue
		}
		if _, dup := e.val[w]; dup {
			continue
		}
		res := subjectRef(e.b.c, w.Subject)
		id := e.b.fact(res, "metric."+string(w.Signal), metricSource(w.Signal, res), factParams(w, v))
		e.b.rep.Facts[len(e.b.rep.Facts)-1].ObservedAt = v.ObservedAt
		e.fact[w], e.val[w] = id, v
	}
}

// factParams — параметры факта: значения строками с фиксированной точностью (отчёт сохраняется и сравнивается).
func factParams(w signals.Want, v signals.Value) map[string]string {
	p := map[string]string{"signal": string(w.Signal), "value": formatMetric(v), "unit": v.Unit, "agg": v.Agg}
	if v.Unit == "ratio" {
		p["percent"] = strconv.FormatFloat(math.Round(v.Value*100), 'f', 0, 64)
	}
	if v.Window > 0 {
		p["window"] = formatWindow(v.Window)
	}
	if w.Subject.Container != "" {
		p["container"] = w.Subject.Container
	}
	if w.Subject.Node != "" {
		p["node"] = w.Subject.Node
	}
	if v.Limit != nil {
		p["limit"] = strconv.FormatFloat(*v.Limit, 'f', 3, 64)
	}
	return p
}

func formatMetric(v signals.Value) string {
	switch v.Unit {
	case "count", "flag":
		return strconv.FormatFloat(math.Round(v.Value), 'f', 0, 64)
	}
	return strconv.FormatFloat(v.Value, 'f', 3, 64)
}

func formatWindow(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d%time.Minute == 0:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	}
	return strconv.Itoa(int(d/time.Second)) + "s"
}

// subjectRef — ресурс отчёта для цели сигнала: Pod из контекста (с UID) либо Node.
func subjectRef(c *Context, s signals.Subject) ResourceRef {
	if s.Pod == "" && s.Node != "" {
		r := ResourceRef{Kind: "Node", Name: s.Node}
		if n := c.Nodes[s.Node]; n != nil {
			r.UID = string(n.UID)
		}
		return r
	}
	for _, p := range c.Pods {
		if p.Namespace == s.Namespace && p.Name == s.Pod {
			return podRef(p)
		}
	}
	return ResourceRef{Kind: "Pod", Namespace: s.Namespace, Name: s.Pod}
}

// verdict — вывод метрики о гипотезе.
type verdict int

const (
	neutral    verdict = 0
	confirms   verdict = 1
	contradict verdict = -1
)

// judgment — вывод вместе с фактом, на котором он основан.
type judgment struct {
	v    verdict
	fact string
}

// raise поднимает уверенность на один уровень, но не выше medium: high метриками не достигается.
func raise(c Confidence) Confidence {
	if c == ConfidenceLow {
		return ConfidenceMedium
	}
	return c
}

// lower опускает уверенность на один уровень.
func lower(c Confidence) Confidence {
	switch c {
	case ConfidenceHigh:
		return ConfidenceMedium
	case ConfidenceMedium:
		return ConfidenceLow
	}
	return c
}

// adjustExisting подтверждает или опровергает существующие гипотезы. Итог по гипотезе — один: подтверждения без
// опровержений поднимают уверенность на один уровень, опровержения без подтверждений опускают; когда метрики
// расходятся друг с другом, уверенность остаётся прежней, а факты записываются с обеих сторон.
func (e *evidence) adjustExisting() {
	for i := range e.b.rep.Hypotheses {
		h := &e.b.rep.Hypotheses[i]
		var js []judgment
		switch h.Code {
		case "oom.limit_too_low":
			js = e.judgeLimitTooLow(h)
		case "oom.node_memory_pressure":
			js = e.judgeNodePressure(h)
		}
		var up, down bool
		for _, j := range js {
			switch j.v {
			case confirms:
				up = true
				h.Supporting = appendUnique(h.Supporting, j.fact)
			case contradict:
				down = true
				h.Contradicting = appendUnique(h.Contradicting, j.fact)
			}
		}
		switch {
		case up && !down:
			h.Confidence = raise(h.Confidence)
		case down && !up:
			h.Confidence = lower(h.Confidence)
		}
	}
}

func appendUnique(s []string, id string) []string {
	if id == "" || slices.Contains(s, id) {
		return s
	}
	return append(s, id)
}

// containerSubjectOf находит цель контейнера гипотезы: Pod гипотезы и параметр container.
func (e *evidence) containerSubjectOf(h *Hypothesis) (signals.Subject, bool) {
	name := h.Params["container"]
	if name == "" || h.Resource.Kind != "Pod" {
		return signals.Subject{}, false
	}
	return signals.Subject{Namespace: h.Resource.Namespace, Pod: h.Resource.Name, Container: name}, true
}

// judgeLimitTooLow: пик памяти у лимита и OOM-события подтверждают гипотезу; пик, далёкий от лимита, опровергает её,
// но только если убийство по памяти случилось внутри окна метрики: иначе метрика о нём ничего не знает.
func (e *evidence) judgeLimitTooLow(h *Hypothesis) []judgment {
	s, ok := e.containerSubjectOf(h)
	if !ok {
		return nil
	}
	var js []judgment
	if v, id, ok := e.get(signals.MemoryUsageVsLimit, s); ok {
		switch {
		case v.Value >= memNearLimit:
			js = append(js, judgment{confirms, id})
		case v.Value < memFarFromLimit && e.oomWithinWindow(s, v):
			js = append(js, judgment{contradict, id})
		}
	}
	if v, id, ok := e.get(signals.OOMEvents, s); ok && v.Value > 0 {
		js = append(js, judgment{confirms, id})
	}
	return js
}

// oomWithinWindow сообщает, что последнее убийство контейнера по памяти попало в окно значения v.
func (e *evidence) oomWithinWindow(s signals.Subject, v signals.Value) bool {
	for _, p := range e.b.c.Pods {
		if p.Namespace != s.Namespace || p.Name != s.Pod {
			continue
		}
		for _, cv := range containers(p) {
			if cv.Name != s.Container {
				continue
			}
			if oom := firstOOM(cv); oom != nil && !oom.FinishedAt.IsZero() {
				return oom.FinishedAt.After(v.ObservedAt.Add(-v.Window))
			}
		}
	}
	return false
}

// judgeNodePressure: давление на Node в истории и малый минимум доступной памяти подтверждают гипотезу; Node, у
// которой доступной памяти за окно не меньше 30%, опровергает её (и только при отсутствии давления в истории).
func (e *evidence) judgeNodePressure(h *Hypothesis) []judgment {
	node := h.Params["node"]
	if node == "" {
		return nil
	}
	s := signals.Subject{Node: node}
	var js []judgment
	pv, pid, hasPressure := e.get(signals.NodePressureHistory, s)
	mv, mid, hasMem := e.get(signals.NodeMemoryAvailableRatio, s)
	if hasPressure && pv.Value >= 1 {
		js = append(js, judgment{confirms, pid})
	}
	switch {
	case hasMem && mv.Value <= nodeMemLow:
		js = append(js, judgment{confirms, mid})
	case hasMem && mv.Value >= nodeMemHealthy && (!hasPressure || pv.Value < 1):
		js = append(js, judgment{contradict, mid})
	}
	return js
}

// hasHypothesis сообщает, что гипотеза с кодом и параметром уже есть (метрики не дублируют правила).
func (e *evidence) hasHypothesis(code, key, val string) bool {
	return slices.ContainsFunc(e.b.rep.Hypotheses, func(h Hypothesis) bool { return h.Code == code && h.Params[key] == val })
}

func (e *evidence) hasSymptom(codes ...string) bool {
	return slices.ContainsFunc(e.b.rep.Symptoms, func(s string) bool { return slices.Contains(codes, s) })
}

// addHypotheses добавляет гипотезы, которых правила не знают, но метрики о которых говорят. Только при уже найденном
// симптоме (метрики объясняют проблему, а не ищут её) и не выше medium.
func (e *evidence) addHypotheses() {
	if !e.hasSymptom(SymptomCrashLoop, SymptomOOMKilled) {
		return
	}
	seenCtr := map[signals.Subject]bool{}
	seenNode := map[string]bool{}
	for _, w := range e.o.Wants {
		s := w.Subject
		switch {
		case s.Container != "" && !seenCtr[s]:
			seenCtr[s] = true
			e.containerHypotheses(s)
		case s.Container == "" && s.Node != "" && !seenNode[s.Node]:
			seenNode[s.Node] = true
			e.nodeHypotheses(s)
		}
	}
}

func (e *evidence) containerHypotheses(s signals.Subject) {
	res := subjectRef(e.b.c, s)
	params := map[string]string{"container": s.Container}

	// CPU ограничен лимитом: троттлинг сам по себе — малая причина; с пиком у лимита — заметная.
	if t, tid, ok := e.get(signals.CPUThrottlingRatio, s); ok && t.Value >= cpuThrottled && e.hasSymptom(SymptomCrashLoop) &&
		!e.hasHypothesis(hypCPULimited, "container", s.Container) {
		conf, sup := ConfidenceLow, []string{tid}
		if u, uid, ok := e.get(signals.CPUUsageVsLimit, s); ok && u.Value >= cpuNearLimit {
			conf, sup = ConfidenceMedium, append(sup, uid)
		}
		h := e.b.hypothesis(res, hypCPULimited, conf, params, sup, nil)
		e.b.recommend(res, "rec.review_cpu_limit", params, h)
	}

	// Память у лимита: если правило OOM уже выдвинуло «лимит слишком мал», метрики подтверждают его, а не дублируют.
	if m, mid, ok := e.get(signals.MemoryUsageVsLimit, s); ok && m.Value >= memNearLimit &&
		!e.hasHypothesis("oom.limit_too_low", "container", s.Container) && !e.hasHypothesis(hypMemoryNearLimit, "container", s.Container) {
		conf, sup := ConfidenceLow, []string{mid}
		if o, oid, ok := e.get(signals.OOMEvents, s); ok && o.Value > 0 {
			conf, sup = ConfidenceMedium, append(sup, oid)
		}
		h := e.b.hypothesis(res, hypMemoryNearLimit, conf, params, sup, nil)
		e.b.recommend(res, "rec.review_memory_limit", params, h)
	}

	// Рестарты растут: недавние, а не накопленные давно.
	if r, rid, ok := e.get(signals.ContainerRestarts, s); ok && r.Value >= restartsGrowing && e.hasSymptom(SymptomCrashLoop) &&
		!e.hasHypothesis(hypRestartsGrowing, "container", s.Container) {
		conf := ConfidenceLow
		if r.Value >= restartsGrowingStrong {
			conf = ConfidenceMedium
		}
		e.b.hypothesis(res, hypRestartsGrowing, conf, params, []string{rid}, nil)
	}
}

func (e *evidence) nodeHypotheses(s signals.Subject) {
	if e.hasHypothesis("oom.node_memory_pressure", "node", s.Node) || e.hasHypothesis(hypNodePressure, "node", s.Node) {
		return
	}
	p, pid, hasP := e.get(signals.NodePressureHistory, s)
	m, mid, hasM := e.get(signals.NodeMemoryAvailableRatio, s)
	pressure := hasP && p.Value >= 1
	low := hasM && m.Value <= nodeMemLow
	if !pressure && !low {
		return
	}
	var sup []string
	conf := ConfidenceLow
	if pressure {
		sup, conf = append(sup, pid), ConfidenceMedium
	}
	if low {
		sup = append(sup, mid)
	}
	res := subjectRef(e.b.c, s)
	params := map[string]string{"node": s.Node}
	h := e.b.hypothesis(res, hypNodePressure, conf, params, sup, nil)
	e.b.recommend(res, "rec.relieve_node_pressure", params, h)
}

// dropManualSteps снимает шаги «проверить вручную», когда соответствующий сигнал получен: человеку незачем идти за
// тем, что уже в отчёте.
func (e *evidence) dropManualSteps() {
	e.b.rep.Steps = slices.DeleteFunc(e.b.rep.Steps, func(st Step) bool {
		switch st.Code {
		case "step.check_memory_metrics":
			if st.Resource.Kind != "Pod" || st.Params["container"] == "" {
				return false
			}
			s := signals.Subject{Namespace: st.Resource.Namespace, Pod: st.Resource.Name, Container: st.Params["container"]}
			_, _, ok := e.get(signals.MemoryUsageVsLimit, s)
			return ok
		case "step.check_node_capacity":
			node := st.Params["node"]
			if node == "" {
				return false
			}
			s := signals.Subject{Node: node}
			_, _, mem := e.get(signals.NodeMemoryAvailableRatio, s)
			_, _, cpu := e.get(signals.NodeCPUUtilization, s)
			return mem || cpu
		}
		return false
	})
}
