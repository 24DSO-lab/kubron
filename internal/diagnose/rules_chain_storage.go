// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"slices"

	corev1 "k8s.io/api/core/v1"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// Цепочка Pod → PVC → PV / StorageClass (ADR-0031 §6, этап 1). Проблема находится по графу, а не по тексту сообщения
// планировщика: класс не найден, нет класса по умолчанию, WaitForFirstConsumer без потребителя, ошибки provisioner по
// Events, PVC в Lost, PVC, которого нет. Текстовое правило pending.unbound_pvc остаётся запасным: цепочка понижает его
// уверенность (lowerTextPath), а шаг «проверить PVC» заменяют факты.

// SymptomClaimPending — симптом «PVC не привязан»; без него отчёт о проблеме тома был бы «проблем не найдено».
const SymptomClaimPending = "storage.claim_pending"

const ruleStorageChainID = "pvc.chain"

func init() { registerChainRule(ruleStorageChainID, 1, ruleStorageChain) }

// claimUse — PVC, который стоит объяснить, и неготовые Pod'ы контекста, которые его монтируют.
type claimUse struct {
	node depgraph.Node
	pods []*corev1.Pod
}

// maxConsumerFacts — сколько Pod'ов-потребителей попадает в факты гипотезы: остальные видны в графе.
const maxConsumerFacts = 3

var provisioningReasons = []string{"ProvisioningFailed", "FailedBinding", "VolumeMismatch"}

func ruleStorageChain(b *builder) {
	g := b.c.Graph
	if g == nil {
		return
	}
	for _, u := range claimsToExplain(b, g) {
		storageFindings(b, g, u)
	}
}

// claimsToExplain — PVC цели (если цель — PVC) и PVC, которые монтируют неготовые Pod'ы контекста. Порядок — по Key().
func claimsToExplain(b *builder, g *depgraph.Graph) []*claimUse {
	byKey := map[string]*claimUse{}
	add := func(n depgraph.Node, p *corev1.Pod) {
		u := byKey[n.Key()]
		if u == nil {
			u = &claimUse{node: n}
			byKey[n.Key()] = u
		}
		if p != nil {
			u.pods = append(u.pods, p)
		}
	}
	if t := b.c.Target; t.Kind == "PersistentVolumeClaim" {
		if n, ok := g.Node(toGraphRef(t)); ok {
			add(n, nil)
		}
	}
	for _, p := range b.c.Pods {
		if podReady(p) {
			continue
		}
		for _, e := range g.EdgesFrom(depgraph.Ref{Kind: "Pod", Namespace: p.Namespace, Name: p.Name}, depgraph.EdgeMounts) {
			if e.To.Kind != "PersistentVolumeClaim" {
				continue
			}
			if n, ok := g.Node(e.To); ok {
				add(n, p)
			}
		}
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]*claimUse, 0, len(keys))
	for _, k := range keys {
		out = append(out, byKey[k])
	}
	return out
}

func storageFindings(b *builder, g *depgraph.Graph, u *claimUse) {
	n := u.node
	claim := fromGraphRef(n.Ref)
	if n.Placeholder {
		missingClaim(b, u, claim)
		return
	}
	switch n.Reason {
	case depgraph.ReasonLost:
		lostClaim(b, u, claim)
	case depgraph.ReasonNoDefaultClass:
		noDefaultClass(b, u, claim)
	case depgraph.ReasonWaitingFirstConsumer:
		waitingFirstConsumer(b, g, u, claim)
	case depgraph.ReasonUnbound:
		unboundClaim(b, g, u, claim)
	}
}

// claimPhase читает status.phase из объекта вершины; пусто, если объект не разобрался.
func claimPhase(n depgraph.Node) string {
	if n.Object == nil {
		return ""
	}
	pvc, err := decode[corev1.PersistentVolumeClaim](n.Object)
	if err != nil {
		return ""
	}
	return string(pvc.Status.Phase)
}

// stateFact — факт о состоянии самого PVC: источник — поле status.phase объекта.
func stateFact(b *builder, u *claimUse, claim ResourceRef, code string) string {
	return b.fact(claim, code, Source{Kind: SourceResource, Resource: claim, Field: "status.phase"},
		map[string]string{"pvc": claim.Name, "phase": claimPhase(u.node)})
}

// mountFacts — факты «Pod монтирует PVC» для потребителей (связь mounts графа, поле spec.volumes).
func mountFacts(b *builder, u *claimUse, claim ResourceRef) []string {
	var out []string
	for i, p := range u.pods {
		if i == maxConsumerFacts {
			break
		}
		ref := podRef(p)
		out = append(out, b.fact(ref, "pvc.mounted", Source{Kind: SourceDerived, Resource: ref, Field: "spec.volumes"},
			map[string]string{"pod": p.Name, "pvc": claim.Name}))
	}
	return out
}

// claimEvents — события самого PVC с перечисленными причинами; в контексте они появляются из пула событий (хук графа).
func claimEvents(b *builder, claim ResourceRef, reasons []string) []Event {
	var out []Event
	for _, ev := range b.c.Events {
		if ev.Object.Kind == "PersistentVolumeClaim" && ev.Object.Namespace == claim.Namespace && ev.Object.Name == claim.Name &&
			slices.Contains(reasons, ev.Reason) {
			out = append(out, ev)
		}
	}
	return out
}

func eventFacts(b *builder, claim ResourceRef, evs []Event) []string {
	var out []string
	for _, ev := range evs {
		out = append(out, b.factFromEvent(claim, ev))
	}
	return out
}

// storageClassEdge — ребро PVC → StorageClass, если оно есть в графе.
func storageClassEdge(g *depgraph.Graph, pvc depgraph.Ref) (depgraph.Edge, bool) {
	for _, e := range g.EdgesFrom(pvc, depgraph.EdgeBinds) {
		if e.To.Kind == "StorageClass" {
			return e, true
		}
	}
	return depgraph.Edge{}, false
}

// missingClaim: Pod ссылается на PVC, которого нет (кластер ответил NotFound).
func missingClaim(b *builder, u *claimUse, claim ResourceRef) {
	if len(u.pods) == 0 {
		return
	}
	b.symptom(SymptomClaimPending)
	f := b.fact(claim, "pvc.missing", Source{Kind: SourceDerived, Resource: podRef(u.pods[0]), Field: "spec.volumes"},
		map[string]string{"pvc": claim.Name})
	sup := append([]string{f}, mountFacts(b, u, claim)...)
	h := b.hypothesis(claim, "pvc.missing", ConfidenceHigh, map[string]string{"pvc": claim.Name}, sup, nil)
	b.recommend(claim, "rec.create_missing_claim", map[string]string{"pvc": claim.Name}, h)
}

func lostClaim(b *builder, u *claimUse, claim ResourceRef) {
	b.symptom(SymptomClaimPending)
	sup := append([]string{stateFact(b, u, claim, "pvc.lost")}, mountFacts(b, u, claim)...)
	h := b.hypothesis(claim, "pvc.lost", ConfidenceHigh, map[string]string{"pvc": claim.Name}, sup, nil)
	b.recommend(claim, "rec.restore_volume", map[string]string{"pvc": claim.Name}, h)
}

func noDefaultClass(b *builder, u *claimUse, claim ResourceRef) {
	b.symptom(SymptomClaimPending)
	sup := []string{
		stateFact(b, u, claim, "pvc.not_bound"),
		b.fact(claim, "storage.default_class_missing", Source{Kind: SourceDerived, Resource: claim, Field: "spec.storageClassName"},
			map[string]string{"pvc": claim.Name}),
	}
	sup = append(sup, mountFacts(b, u, claim)...)
	sup = append(sup, eventFacts(b, claim, claimEvents(b, claim, provisioningReasons))...)
	// Без класса PVC может связаться и с заранее созданным PV, поэтому уверенность средняя, а не высокая.
	h := b.hypothesis(claim, "storage.no_default_class", ConfidenceMedium, map[string]string{"pvc": claim.Name}, sup, nil)
	b.recommend(claim, "rec.set_default_storage_class", map[string]string{"pvc": claim.Name}, h)
}

// waitingFirstConsumer: PVC ждёт первого потребителя. Это поломка только если потребителей нет вовсе; если Pod его
// монтирует, ожидание штатное и причина Pending — в другом месте.
func waitingFirstConsumer(b *builder, g *depgraph.Graph, u *claimUse, claim ResourceRef) {
	if len(g.EdgesTo(u.node.Ref, depgraph.EdgeMounts)) > 0 {
		return
	}
	b.symptom(SymptomClaimPending)
	sup := []string{
		stateFact(b, u, claim, "pvc.not_bound"),
		b.fact(claim, "storage.no_consumers", Source{Kind: SourceDerived, Resource: claim, Field: "status.phase"},
			map[string]string{"pvc": claim.Name}),
	}
	h := b.hypothesis(claim, "storage.waiting_first_consumer", ConfidenceMedium, map[string]string{"pvc": claim.Name}, sup, nil)
	b.recommend(claim, "rec.use_or_delete_claim", map[string]string{"pvc": claim.Name}, h)
}

// unboundClaim: PVC не привязан при заданном классе. Сначала ищется отсутствующий класс, затем ошибки provisioner по
// Events, иначе — общая гипотеза с низкой уверенностью.
func unboundClaim(b *builder, g *depgraph.Graph, u *claimUse, claim ResourceRef) {
	b.symptom(SymptomClaimPending)
	state := stateFact(b, u, claim, "pvc.not_bound")
	mounts := mountFacts(b, u, claim)
	evs := claimEvents(b, claim, provisioningReasons)
	params := map[string]string{"pvc": claim.Name}

	if e, ok := storageClassEdge(g, u.node.Ref); ok && e.State == depgraph.EdgeMissing {
		class := e.To.Name
		classFact := b.fact(claim, "storage.class_missing", Source{Kind: SourceDerived, Resource: claim, Field: "spec.storageClassName"},
			map[string]string{"pvc": claim.Name, "class": class})
		sup := append([]string{state, classFact}, mounts...)
		sup = append(sup, eventFacts(b, claim, evs)...)
		conf := ConfidenceMedium
		if len(evs) > 0 {
			conf = ConfidenceHigh // и граф, и provisioner говорят об одном
		}
		h := b.hypothesis(claim, "storage.class_not_found", conf, map[string]string{"pvc": claim.Name, "class": class}, sup, nil)
		b.recommend(claim, "rec.fix_storage_class", map[string]string{"pvc": claim.Name, "class": class}, h)
		return
	}
	if len(evs) > 0 {
		sup := append([]string{state}, mounts...)
		sup = append(sup, eventFacts(b, claim, evs)...)
		h := b.hypothesis(claim, "storage.provisioning_failed", ConfidenceMedium, params, sup, nil)
		b.recommend(claim, "rec.fix_volume_claim", params, h)
		b.step(claim, "step.check_provisioner", params, h)
		return
	}
	h := b.hypothesis(claim, "pvc.unbound", ConfidenceLow, params, append([]string{state}, mounts...), nil)
	b.recommend(claim, "rec.fix_volume_claim", params, h)
}
