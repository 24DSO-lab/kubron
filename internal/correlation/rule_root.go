// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation

import (
	"context"
	"sort"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// Коды фактов правила shared_root.
const (
	factRootCandidate = "correlation.shared_root.candidate"
	factRootLayers    = "correlation.shared_root.layers"
	factRootMulti     = "correlation.shared_root.multi_workload"
)

// minRootLayers — сколько слоёв цепочки должно быть затронуто, чтобы кандидат имел смысл: сам workload и хотя бы
// одно звено над ним (ADR-0033 §3, contracts.md §5: минимум 2 расследования разных слоёв).
const minRootLayers = 2

const (
	autoscalingGroup = "autoscaling"
	kindHPA          = "HorizontalPodAutoscaler"
)

func init() { registerRule("shared_root", ruleSharedRoot) }

// ruleSharedRoot: кандидат первопричины отчёта (Hypothesis.Explains, ADR-0031 §5) нормализуется до workload-владельца.
// Расследования Ingress, Service, Deployment и Pod одной цепочки приходят к одному workload и получают один ключ
// shared_root:<workload>, поэтому автоприсоединение собирает их в одну проблему. Чужие расследования правило не
// читает (ADR-0033 §1): каждое из них считает то же самое по своим живым данным. Чтений кластера правило не делает.
func ruleSharedRoot(_ context.Context, e *env) []Candidate {
	g := e.dc.Graph
	if g == nil {
		return nil
	}
	rep := diagnose.Analyze(e.dc)
	byID := make(map[string]diagnose.Hypothesis, len(rep.Hypotheses))
	for _, h := range rep.Hypotheses {
		byID[h.ID] = h
	}
	roots := rep.RootCandidates()
	if len(roots) == 0 && isWorkloadKind(e.dc.Target.Kind) {
		// Расследование нижнего слоя цепочки (Pod, Deployment…): звеньев над целью в его отчёте нет, поэтому и
		// Explains нет. Собственные гипотезы цели — самое нижнее звено, то есть кандидаты; значимыми их делает
		// только то, что над workload в графе затронуто не меньше одного слоя (проверяет sharedRoot).
		for _, h := range rep.Hypotheses {
			roots = append(roots, h.ID)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []Candidate
	for _, id := range roots {
		h, ok := byID[id]
		if !ok {
			continue
		}
		w, ok := rootWorkload(g, graphRefOf(h.Resource))
		if !ok || seen[w.Key()] {
			continue
		}
		seen[w.Key()] = true
		if c, ok := e.sharedRoot(g, w); ok {
			out = append(out, c)
		}
	}
	return out
}

// groupOfKind восстанавливает группу по виду для ссылок диагностики первой итерации, у которых Group пуста и
// однозначна по виду (ADR-0031 §4).
func groupOfKind(kind string) string {
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
		return "apps"
	case "Job", "CronJob":
		return "batch"
	case kindHPA:
		return autoscalingGroup
	case kindIngress, kindIngressCls:
		return networkingGroup
	case "StorageClass":
		return storageGroup
	}
	return ""
}

func graphRefOf(r diagnose.ResourceRef) depgraph.Ref {
	g := r.Group
	if g == "" {
		g = groupOfKind(r.Kind)
	}
	return depgraph.Ref{Group: g, Kind: r.Kind, Namespace: r.Namespace, Name: r.Name}
}

// isWorkloadKind — вид, который сам может быть нагрузкой верхнего уровня.
func isWorkloadKind(kind string) bool {
	switch kind {
	case "Pod", "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob":
		return true
	}
	return false
}

// topOwner возвращает нагрузку верхнего уровня вершины по рёбрам owns. false — владельца определить нельзя: у Pod
// есть ownerReferences, а владельца в графе нет (граф усечён). Тогда ключ получился бы другим, чем у соседних
// расследований, и кандидата лучше не строить.
func topOwner(g *depgraph.Graph, n depgraph.Node) (depgraph.Node, bool) {
	if owners := g.Owners(n.Ref); len(owners) > 0 {
		return owners[len(owners)-1], true
	}
	if n.Object != nil {
		for _, o := range n.Object.GetOwnerReferences() {
			if o.Kind != "Node" {
				return depgraph.Node{}, false
			}
		}
	}
	return n, isWorkloadKind(n.Kind)
}

// rootWorkload нормализует ресурс гипотезы до workload-владельца. Pod, ReplicaSet и прочие нагрузки — их владелец
// верхнего уровня; Service — владелец его Pod'ов, если он один. Остальное (Node, ConfigMap, Ingress, класс
// хранилища) нагрузкой не владеет: это общие источники других правил.
func rootWorkload(g *depgraph.Graph, ref depgraph.Ref) (depgraph.Node, bool) {
	n, ok := g.Node(ref)
	if !ok || n.Placeholder {
		return depgraph.Node{}, false
	}
	switch {
	case n.Group == "" && n.Kind == kindService:
		tops := map[string]depgraph.Node{}
		for _, ed := range g.EdgesFrom(n.Ref, depgraph.EdgeSelects) {
			pn, ok := g.Node(ed.To)
			if !ok {
				continue
			}
			top, ok := topOwner(g, pn)
			if !ok {
				return depgraph.Node{}, false
			}
			tops[top.Key()] = top
		}
		if len(tops) != 1 {
			return depgraph.Node{}, false
		}
		for _, t := range tops {
			return t, true
		}
	case isWorkloadKind(n.Kind):
		return topOwner(g, n)
	}
	return depgraph.Node{}, false
}

func brokenHealth(h depgraph.Health) bool { return h == depgraph.Degraded || h == depgraph.Missing }

// routesToBroken — Ingress маршрутизирует на Service, который сломан или отсутствует.
func routesToBroken(g *depgraph.Graph, ing depgraph.Node) bool {
	for _, ed := range g.EdgesFrom(ing.Ref, depgraph.EdgeRoutes) {
		if to, ok := g.Node(ed.To); ok && brokenHealth(to.Health) {
			return true
		}
	}
	return false
}

func nodeWorkloadRef(n depgraph.Node) WorkloadRef {
	return WorkloadRef{Kind: n.Kind, Namespace: n.Namespace, Name: n.Name}
}

// sharedRoot считает слои цепочки, затронутые сбоем workload w: сам workload (есть Pod'ы с симптомами или он в
// состоянии Degraded) и звенья над ним по графу — Service, Ingress и HPA в плохом состоянии. Признаки ADR-0033 §4:
// (а) звенья связаны с workload по графу; (б) кандидат первопричины найден; (в) временной связи у правила нет;
// (г) затронуто не менее трёх слоёв. Поэтому уверенность не выше medium.
func (e *env) sharedRoot(g *depgraph.Graph, w depgraph.Node) (Candidate, bool) {
	own := nodeWorkloadRef(w)
	var pods []depgraph.Node
	var layers []WorkloadRef
	if w.Group == "" && w.Kind == "Pod" {
		pods = append(pods, w)
	}
	for _, d := range g.Dependents(w.Ref) {
		switch {
		case d.Group == "" && d.Kind == "Pod":
			pods = append(pods, d)
		case d.Group == "" && d.Kind == kindService:
			if brokenHealth(d.Health) {
				layers = append(layers, nodeWorkloadRef(d))
			}
		case d.Group == networkingGroup && d.Kind == kindIngress:
			if brokenHealth(d.Health) || routesToBroken(g, d) {
				layers = append(layers, nodeWorkloadRef(d))
			}
		case d.Group == autoscalingGroup && d.Kind == kindHPA:
			if brokenHealth(d.Health) {
				layers = append(layers, nodeWorkloadRef(d))
			}
		}
	}
	for _, pn := range pods {
		p := podOf(pn)
		if p == nil {
			continue
		}
		info := inspectPod(p)
		if !info.problem() {
			continue
		}
		own.ProblemPods++
		for _, s := range info.symptoms {
			if !contains(own.Symptoms, s) {
				own.Symptoms = append(own.Symptoms, s)
			}
		}
		if !info.failedAt.IsZero() && (own.Since.IsZero() || info.failedAt.Before(own.Since)) {
			own.Since = info.failedAt
		}
	}
	sort.Strings(own.Symptoms)
	if own.ProblemPods == 0 && w.Health != depgraph.Degraded {
		return Candidate{}, false // сам workload не затронут: «первопричиной» его называть не на чем
	}
	total := 1 + len(layers)
	if total < minRootLayers {
		return Candidate{}, false
	}

	ref := diagnose.ResourceRef{Group: w.Group, Kind: w.Kind, Namespace: w.Namespace, Name: w.Name, UID: w.UID}
	ev := evidence{structural: true, abnormal: true, multi: total >= minWorkloadsForMulti}
	conf, ok := ev.confidence()
	if !ok {
		return Candidate{}, false
	}
	specs := []factSpec{
		{factRootCandidate, ref, nil},
		{factRootLayers, ref, map[string]string{"layers": itoa(total)}},
	}
	if ev.multi {
		specs = append(specs, factSpec{factRootMulti, ref, map[string]string{"workloads": itoa(total)}})
	}
	since := own.Since
	if since.IsZero() {
		since = e.now
	}
	c := Candidate{
		Kind: KindSharedRoot, Source: ref, Confidence: conf, Since: since,
		Affected: sortRefs(append([]WorkloadRef{own}, layers...)), Total: total,
		Supporting: e.addFacts(specs),
	}
	c.ID = candidateID(c)
	return c, true
}
