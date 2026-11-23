// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"slices"
	"strconv"

	corev1 "k8s.io/api/core/v1"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// Цепочка Pod → Node (ADR-0031 §6, этап 1): узел не Ready или недоступен, давление памяти, диска или PID, сеть
// недоступна, узел закрыт для новых Pod'ов. Состояние узла читается из его условий, а не из текстов: граф даёт
// лишь «первую» причину узла, а здесь нужны все. Гипотеза узла объясняет гипотезы Pod'ов, которые зависят от него
// (пост-хук связей, chains.go): например, давление памяти объясняет oom.node_memory_pressure.

// SymptomNodeUnhealthy — симптом «узел в плохом состоянии».
const SymptomNodeUnhealthy = "node.unhealthy"

const ruleNodeChainID = "node.chain"

func init() { registerChainRule(ruleNodeChainID, 1, ruleNodeChain) }

func ruleNodeChain(b *builder) {
	g := b.c.Graph
	if g == nil {
		return
	}
	for _, n := range nodesToExplain(b, g) {
		nodeFindings(b, n)
	}
}

// nodesToExplain — узел цели (если цель — Node) и узлы, на которых работают неготовые Pod'ы контекста. Порядок — по Key().
func nodesToExplain(b *builder, g *depgraph.Graph) []depgraph.Node {
	byKey := map[string]depgraph.Node{}
	if t := b.c.Target; t.Kind == "Node" {
		if n, ok := g.Node(toGraphRef(t)); ok {
			byKey[n.Key()] = n
		}
	}
	for _, p := range b.c.Pods {
		if podReady(p) {
			continue
		}
		for _, e := range g.EdgesFrom(depgraph.Ref{Kind: "Pod", Namespace: p.Namespace, Name: p.Name}, depgraph.EdgeRunsOn) {
			if n, ok := g.Node(e.To); ok {
				byKey[n.Key()] = n
			}
		}
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]depgraph.Node, 0, len(keys))
	for _, k := range keys {
		out = append(out, byKey[k])
	}
	return out
}

func nodeCondition(node *corev1.Node, t corev1.NodeConditionType) *corev1.NodeCondition {
	for i := range node.Status.Conditions {
		if node.Status.Conditions[i].Type == t {
			return &node.Status.Conditions[i]
		}
	}
	return nil
}

// nodeFinding — одна находка по условию узла: код гипотезы, её уверенность и рекомендация.
type nodeFinding struct {
	code, rec string
	cond      corev1.NodeConditionType
	conf      Confidence
	params    map[string]string
}

func nodeFindings(b *builder, n depgraph.Node) {
	if n.Placeholder || n.Health == depgraph.Unknown || n.Object == nil {
		return // узел не прочитан: пробел уже записан в Missing
	}
	node, err := decode[corev1.Node](n.Object)
	if err != nil {
		return
	}
	ref := fromGraphRef(n.Ref)
	var found []nodeFinding
	switch c := nodeCondition(node, corev1.NodeReady); {
	case c != nil && c.Status == corev1.ConditionFalse:
		found = append(found, nodeFinding{"node.not_ready", "rec.check_node", corev1.NodeReady, ConfidenceHigh,
			map[string]string{"node": ref.Name, "status": string(c.Status), "reason": c.Reason}})
	case c != nil && c.Status == corev1.ConditionUnknown:
		// Kubelet перестал отчитываться: узел недоступен или завис. Это слабее прямого «не готов».
		found = append(found, nodeFinding{"node.not_ready", "rec.check_node", corev1.NodeReady, ConfidenceMedium,
			map[string]string{"node": ref.Name, "status": string(c.Status), "reason": c.Reason}})
	}
	for _, p := range []struct {
		t    corev1.NodeConditionType
		code string
	}{
		{corev1.NodeMemoryPressure, "node.pressure_memory"},
		{corev1.NodeDiskPressure, "node.pressure_disk"},
		{corev1.NodePIDPressure, "node.pressure_pid"},
	} {
		if c := nodeCondition(node, p.t); c != nil && c.Status == corev1.ConditionTrue {
			found = append(found, nodeFinding{p.code, "rec.relieve_node", p.t, ConfidenceHigh, map[string]string{"node": ref.Name}})
		}
	}
	if c := nodeCondition(node, corev1.NodeNetworkUnavailable); c != nil && c.Status == corev1.ConditionTrue {
		found = append(found, nodeFinding{"node.network_unavailable", "rec.check_node", corev1.NodeNetworkUnavailable, ConfidenceHigh,
			map[string]string{"node": ref.Name}})
	}
	if len(found) == 0 && !node.Spec.Unschedulable {
		return
	}
	// Симптом «узел в плохом состоянии» ставится, только когда узел — цель. Для Pod узел — причина (гипотеза и цепочка),
	// а симптомы отчёта описывают то, что болит у самой цели.
	if b.c.Target.Kind == "Node" {
		b.symptom(SymptomNodeUnhealthy)
	}
	pods := nodePodsFact(b, ref)
	for _, f := range found {
		cond := nodeCondition(node, f.cond)
		src := Source{Kind: SourceResource, Resource: ref, Field: "status.conditions[" + string(f.cond) + "]"}
		facts := []string{b.fact(ref, "node.condition", src, map[string]string{
			"node": ref.Name, "condition": string(f.cond), "status": string(cond.Status)})}
		if pods != "" {
			facts = append(facts, pods)
		}
		facts = append(facts, nodeEventFacts(b, ref)...)
		h := b.hypothesis(ref, f.code, f.conf, f.params, facts, nil)
		b.recommend(ref, f.rec, map[string]string{"node": ref.Name}, h)
	}
	if node.Spec.Unschedulable {
		// Закрытый для новых Pod'ов узел — часто осознанное действие (cordon), поэтому уверенность низкая.
		src := Source{Kind: SourceResource, Resource: ref, Field: "spec.unschedulable"}
		f := b.fact(ref, "node.cordoned", src, map[string]string{"node": ref.Name})
		h := b.hypothesis(ref, "node.unschedulable", ConfidenceLow, map[string]string{"node": ref.Name}, []string{f}, nil)
		b.recommend(ref, "rec.uncordon_node", map[string]string{"node": ref.Name}, h)
	}
}

// nodePodsFact — факт «сколько Pod'ов контекста на узле неготовы»; пусто, если Pod'ов узла в контексте нет.
func nodePodsFact(b *builder, ref ResourceRef) string {
	total, unready := 0, 0
	for _, p := range b.c.Pods {
		if p.Spec.NodeName != ref.Name {
			continue
		}
		total++
		if !podReady(p) {
			unready++
		}
	}
	if total == 0 {
		return ""
	}
	return b.fact(ref, "node.pods_unready", Source{Kind: SourceDerived, Resource: ref, Field: "pods"},
		map[string]string{"node": ref.Name, "unready": strconv.Itoa(unready), "total": strconv.Itoa(total)})
}

// nodeEventFacts — самые свежие события самого узла (читает их сбор цели Node), не больше двух.
func nodeEventFacts(b *builder, ref ResourceRef) []string {
	var evs []Event
	for _, ev := range b.c.Events {
		if ev.Object.Kind == "Node" && ev.Object.Name == ref.Name {
			evs = append(evs, ev)
		}
	}
	if len(evs) > 2 {
		evs = evs[len(evs)-2:]
	}
	var out []string
	for _, ev := range evs {
		out = append(out, b.factFromEvent(ref, ev))
	}
	return out
}
