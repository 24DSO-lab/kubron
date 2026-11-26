// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"cmp"
	"slices"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/feedback"
)

// maxOwnerDepth — предел подъёма по цепочке владельцев; защита от циклов в графе.
const maxOwnerDepth = 16

// workloadKinds — виды нагрузок, которые считаются владельцем-workload, если граф не построен.
var workloadKinds = []string{"Deployment", "StatefulSet", "DaemonSet", "CronJob", "Job"}

// signatureOf возвращает сигнатуру расследования для поиска повторений (ADR-0034 §4): владелец-workload цели,
// коды симптомов отчёта (без «none») и пары «правило, код» гипотез с уверенностью medium и выше. Имя пода в
// сигнатуру не входит: оно меняется при каждом перезапуске, а владелец стабилен. Пустая строка — сигнатуру
// построить нельзя. Вызывается из Open до сохранения, результат кладётся в Incident.Signature.
func signatureOf(in *Incident, rep *diagnose.Report) string {
	if rep == nil {
		return ""
	}
	target := in.Target
	if target.Name == "" {
		target = rep.Target
	}
	owner := ownerWorkload(target, rep)

	var symptoms []string
	for _, s := range rep.Symptoms {
		if s != diagnose.SymptomNone && s != "" {
			symptoms = append(symptoms, s)
		}
	}
	var hyps []feedback.Ref
	for _, h := range rep.Hypotheses {
		if h.RuleID != "" && confidenceRank(h.Confidence) >= confidenceRank(diagnose.ConfidenceMedium) {
			hyps = append(hyps, feedback.Ref{RuleID: h.RuleID, Epoch: h.RuleEpoch, Code: h.Code})
		}
	}
	return feedback.Signature(feedback.SignatureInput{
		WorkloadKind: owner.Kind, WorkloadNamespace: owner.Namespace, WorkloadName: owner.Name,
		Symptoms: symptoms, Hypotheses: hyps,
	})
}

// ownerWorkload находит владельца верхнего уровня для цели. Сначала по графу зависимостей (рёбра owns), затем,
// без графа, по ресурсам отчёта: единственная нагрузка того же namespace; иначе — сама цель.
func ownerWorkload(target diagnose.ResourceRef, rep *diagnose.Report) diagnose.ResourceRef {
	if rep.Graph != nil {
		if ref, ok := topOwner(rep.Graph, target); ok {
			return ref
		}
	}
	if slices.Contains(workloadKinds, target.Kind) {
		return target
	}
	var found []diagnose.ResourceRef
	for _, r := range rep.Resources {
		if slices.Contains(workloadKinds, r.Kind) && r.Namespace == target.Namespace &&
			!slices.ContainsFunc(found, func(f diagnose.ResourceRef) bool { return f.Kind == r.Kind && f.Name == r.Name }) {
			found = append(found, r)
		}
	}
	if len(found) == 1 {
		return found[0]
	}
	return target
}

// topOwner поднимается по рёбрам owns от цели до верхнего владельца. ok=false — цель в графе не найдена или
// владельца у неё нет.
func topOwner(g *depgraph.Snapshot, target diagnose.ResourceRef) (diagnose.ResourceRef, bool) {
	key := func(n depgraph.SnapshotNode) string {
		return depgraph.Ref{Group: n.Group, Kind: n.Kind, Namespace: n.Namespace, Name: n.Name}.Key()
	}
	nodes := map[string]depgraph.SnapshotNode{}
	start := ""
	for _, n := range g.Nodes {
		nodes[key(n)] = n
		if n.Kind == target.Kind && n.Namespace == target.Namespace && n.Name == target.Name {
			start = key(n)
		}
	}
	if start == "" {
		return diagnose.ResourceRef{}, false
	}
	owner := map[string]string{} // потомок → владелец; при нескольких — наименьший ключ (детерминизм)
	for _, e := range g.Edges {
		if e.Type != string(depgraph.EdgeOwns) {
			continue
		}
		if cur, ok := owner[e.To]; !ok || cmp.Compare(e.From, cur) < 0 {
			owner[e.To] = e.From
		}
	}
	cur := start
	for range maxOwnerDepth {
		up, ok := owner[cur]
		if !ok {
			break
		}
		if _, known := nodes[up]; !known || up == start {
			break
		}
		cur = up
	}
	if cur == start {
		return diagnose.ResourceRef{}, false
	}
	n := nodes[cur]
	return diagnose.ResourceRef{Group: n.Group, Kind: n.Kind, Namespace: n.Namespace, Name: n.Name}, true
}
