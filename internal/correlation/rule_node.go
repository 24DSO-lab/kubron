// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation

import (
	"context"
	"sort"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// Коды фактов правила shared_node.
const (
	factNodeRunsOn        = "correlation.shared_node.runs_on"
	factNodeCondition     = "correlation.shared_node.condition"
	factNodeUnschedule    = "correlation.shared_node.unschedulable"
	factNodeTaint         = "correlation.shared_node.taint_noexecute"
	factNodeWarnings      = "correlation.shared_node.warning_events"
	factNodeTemporal      = "correlation.shared_node.temporal"
	factNodeMultiWorkload = "correlation.shared_node.multi_workload"
)

// nodeEventsLimit — сколько событий узла читается (учитываются только предупреждения).
const nodeEventsLimit = 100

func init() { registerRule("shared_node", ruleSharedNode) }

// ruleSharedNode: несколько нагрузок с симптомами на одном узле — гипотеза «общий источник — узел» (ADR-0033 §2).
func ruleSharedNode(ctx context.Context, e *env) []Candidate {
	var out []Candidate
	for _, name := range e.nodeNames() {
		if c, ok := e.sharedNode(ctx, name); ok {
			out = append(out, c)
		}
	}
	return out
}

// nodeNames — узлы, на которых работают Pod'ы цели, и сам узел, если цель — Node. Отсортированы.
func (e *env) nodeNames() []string {
	set := map[string]bool{}
	if e.dc.Target.Kind == "Node" && e.dc.Target.Name != "" {
		set[e.dc.Target.Name] = true
	}
	for _, p := range e.dc.Pods {
		if p != nil && p.Spec.NodeName != "" {
			set[p.Spec.NodeName] = true
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (e *env) sharedNode(ctx context.Context, name string) (Candidate, bool) {
	src := diagnose.ResourceRef{Kind: "Node", Name: name}
	node := e.dc.Nodes[name]
	if node == nil {
		if u, err := e.r.Get(ctx, gvkNode, "", name); err != nil {
			e.unavailable(src, reasonOf(err))
		} else {
			var n corev1.Node
			if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &n) == nil {
				node = &n
			}
		}
	}
	if node != nil {
		src.UID = string(node.UID)
	}

	set, truncated := e.problemPodsOnNode(ctx, name)
	if set.total() == 0 {
		return Candidate{}, false
	}
	first := set.firstFailure()

	abnormal, condTimes := nodeAbnormality(node, src)
	warnings := e.nodeWarnings(ctx, src)
	if warnings > 0 {
		abnormal = append(abnormal, factSpec{factNodeWarnings, src, map[string]string{"count": strconv.Itoa(warnings)}})
	}

	ev := evidence{structural: true, abnormal: len(abnormal) > 0, multi: set.total() >= minWorkloadsForMulti}
	var gap time.Duration
	for _, t := range condTimes {
		if withinWindow(t, first) {
			if g := first.Sub(t); !ev.temporal || g < gap {
				gap = g
			}
			ev.temporal = true
		}
	}
	conf, ok := ev.confidence()
	if !ok {
		return Candidate{}, false
	}

	specs := []factSpec{{factNodeRunsOn, src, map[string]string{
		"workloads": strconv.Itoa(set.total()), "pods": strconv.Itoa(set.pods)}}}
	specs = append(specs, abnormal...)
	if ev.temporal {
		specs = append(specs, factSpec{factNodeTemporal, src, map[string]string{"gapSeconds": strconv.Itoa(int(gap.Seconds()))}})
	}
	if ev.multi {
		specs = append(specs, factSpec{factNodeMultiWorkload, src, map[string]string{"workloads": strconv.Itoa(set.total())}})
	}

	since := first
	if since.IsZero() {
		for _, t := range condTimes {
			if since.IsZero() || t.Before(since) {
				since = t
			}
		}
	}
	c := Candidate{
		Kind: KindSharedNode, Source: src, Confidence: conf, Since: since,
		Affected: set.sorted(), Total: set.total(), Truncated: truncated,
		Potential:  e.potentialFromGraph(ctx, depgraph.Ref{Kind: "Node", Name: name}, set),
		Supporting: e.addFacts(specs),
	}
	c.ID = candidateID(c)
	return c, true
}

// problemPodsOnNode читает Pod'ы узла одним запросом и группирует проблемные по нагрузкам верхнего уровня.
// Если прочитать нельзя, берутся Pod'ы из контекста диагностики, а список помечается усечённым.
func (e *env) problemPodsOnNode(ctx context.Context, node string) (*workloadSet, bool) {
	var pods []*corev1.Pod
	truncated := false
	res, err := e.r.List(ctx, gvkPod, diagnose.ListOptions{FieldSelector: "spec.nodeName=" + node, Limit: podListLimit})
	if err != nil {
		e.unavailable(diagnose.ResourceRef{Kind: "Pod"}, reasonOf(err))
		truncated = true
		for _, p := range e.dc.Pods {
			if p != nil && p.Spec.NodeName == node {
				pods = append(pods, p)
			}
		}
	} else {
		truncated = res.Truncated
		if truncated {
			e.unavailable(diagnose.ResourceRef{Kind: "Pod"}, reasonTooLarge)
		}
		for i := range res.Items {
			if p, ok := toPod(&res.Items[i]); ok {
				pods = append(pods, p)
			}
		}
	}
	set := newWorkloadSet()
	for _, p := range pods {
		if info := inspectPod(p); info.problem() {
			set.add(e.workloadOf(ctx, p), info)
		}
	}
	return set, truncated
}

// nodeAbnormality — признаки ненормального состояния узла и времена переходов тех условий, что их имеют.
func nodeAbnormality(n *corev1.Node, src diagnose.ResourceRef) ([]factSpec, []time.Time) {
	if n == nil {
		return nil, nil
	}
	var specs []factSpec
	var times []time.Time
	for _, c := range n.Status.Conditions {
		var name string
		switch {
		case c.Type == corev1.NodeReady && c.Status != corev1.ConditionTrue:
			name = "NotReady"
		case c.Status == corev1.ConditionTrue && (c.Type == corev1.NodeMemoryPressure ||
			c.Type == corev1.NodeDiskPressure || c.Type == corev1.NodePIDPressure):
			name = string(c.Type)
		default:
			continue
		}
		params := map[string]string{"condition": name}
		if !c.LastTransitionTime.IsZero() {
			t := c.LastTransitionTime.UTC()
			params["since"] = t.Format(time.RFC3339)
			times = append(times, t)
		}
		specs = append(specs, factSpec{factNodeCondition, src, params})
	}
	if n.Spec.Unschedulable {
		specs = append(specs, factSpec{factNodeUnschedule, src, nil})
	}
	for _, t := range n.Spec.Taints {
		if t.Effect == corev1.TaintEffectNoExecute {
			specs = append(specs, factSpec{factNodeTaint, src, nil}) // ключ и значение taint — данные кластера, в снимок не идут
			break
		}
	}
	return specs, times
}

// nodeWarnings возвращает число предупреждающих событий узла. Недоступность чтения — в Missing, не ошибка.
func (e *env) nodeWarnings(ctx context.Context, node diagnose.ResourceRef) int {
	res, err := e.r.List(ctx, gvkEvent, diagnose.ListOptions{
		FieldSelector: "involvedObject.kind=Node,involvedObject.name=" + node.Name, Limit: nodeEventsLimit})
	if err != nil {
		e.unavailable(diagnose.ResourceRef{Kind: "Event"}, reasonOf(err))
		return 0
	}
	n := 0
	for _, it := range res.Items {
		if t, _ := it.Object["type"].(string); t == corev1.EventTypeWarning {
			n++
		}
	}
	return n
}
