// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"slices"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// Правила StatefulSet (ADR-0031 §6, этап 3): застрявший упорядоченный запуск, PVC шаблона, обновление, остановленное
// partition. Причины на уровне Pod и PVC здесь не разбираются: их объясняют существующие правила Pod и хранилища,
// а эти гипотезы только связывают их с StatefulSet (Explains и цепочка StatefulSet > Pod / StatefulSet > PVC).

const (
	ruleStatefulSetID = "chain.statefulset"

	// SymptomStatefulSetStuck — запуск StatefulSet остановился на одном Pod'е.
	SymptomStatefulSetStuck = "sts.start_stuck"

	// maxClaimFindings — сколько PVC шаблонов получают собственную гипотезу: остальные видны в графе.
	maxClaimFindings = 3

	labelRevisionHash = "controller-revision-hash"
)

func init() {
	registerChainRule(ruleStatefulSetID, 1, ruleStatefulSet)
	registerPostHook(110, "statefulset_chains", statefulSetChains) // после «chains» (100): их результат он дополняет
}

// stsView — разобранный StatefulSet цели и его вершины в графе.
type stsView struct {
	ref      ResourceRef // ссылка цели (так её видит отчёт)
	node     depgraph.Node
	sts      appsv1.StatefulSet
	replicas int32
	ordinals depgraph.OrdinalRange // [start, start+replicas): единый расчёт с PVC графа (depgraph.StatefulSetOrdinals)
}

func stsFromGraph(b *builder) (stsView, bool) {
	t := b.c.Target
	g := b.c.Graph
	if g == nil || t.Kind != "StatefulSet" {
		return stsView{}, false
	}
	n, ok := g.Node(depgraph.Ref{Group: "apps", Kind: "StatefulSet", Namespace: t.Namespace, Name: t.Name})
	if !ok || n.Object == nil {
		return stsView{}, false
	}
	sts, err := decode[appsv1.StatefulSet](n.Object)
	if err != nil {
		return stsView{}, false
	}
	replicas := int32(1)
	if sts.Spec.Replicas != nil {
		replicas = *sts.Spec.Replicas
	}
	return stsView{ref: t, node: n, sts: *sts, replicas: replicas, ordinals: depgraph.StatefulSetOrdinals(sts)}, true
}

func ruleStatefulSet(b *builder) {
	v, ok := stsFromGraph(b)
	if !ok {
		return
	}
	stuckOrderedStart(b, v)
	claimsPending(b, v)
	updatePartitioned(b, v)
}

// ownedNodes — вершины вида kind, которыми StatefulSet владеет в графе, по Key().
func ownedNodes(g *depgraph.Graph, v stsView, kind string) []depgraph.Node {
	var out []depgraph.Node
	for _, e := range g.EdgesFrom(v.node.Ref, depgraph.EdgeOwns) {
		if e.To.Kind != kind {
			continue
		}
		if n, ok := g.Node(e.To); ok && !n.Placeholder {
			out = append(out, n)
		}
	}
	return out
}

// stuckOrderedStart: podManagementPolicy OrderedReady (значение по умолчанию), последний созданный Pod N не готов, а
// Pod'ов после него ещё нет — контроллер не создаёт следующие, пока N не станет Ready. Кандидат — Pod N; почему он не
// готов, объясняют правила Pod (их гипотезы получают Explains на эту).
//
// Реплики занимают ordinal'ы [start, start+replicas) (spec.ordinals.start): Pod'ы вне диапазона (остаток после
// уменьшения replicas или сдвига start) активными репликами не считаются и «последним созданным» быть не могут, а число
// созданных считается от start.
func stuckOrderedStart(b *builder, v stsView) {
	g := b.c.Graph
	if graphLimited(g) { // «следующих Pod'ов нет» нельзя утверждать по неполному списку
		return
	}
	if p := v.sts.Spec.PodManagementPolicy; p != "" && p != appsv1.OrderedReadyPodManagement {
		return
	}
	last, found := depgraph.Node{}, false
	lastOrd := -1
	for _, n := range ownedNodes(g, v, "Pod") {
		if o, ok := depgraph.PodOrdinal(n.Name, v.ref.Name); ok && v.ordinals.Contains(o) && o > lastOrd {
			last, lastOrd, found = n, o, true
		}
	}
	if !found || lastOrd+1 >= v.ordinals.End() || (last.Health != depgraph.Degraded && last.Health != depgraph.Progressing) {
		return
	}
	pod := ResourceRef{Kind: "Pod", Namespace: last.Namespace, Name: last.Name, UID: last.UID}
	ord := strconv.Itoa(lastOrd)
	b.symptom(SymptomStatefulSetStuck)
	sup := []string{
		b.fact(v.ref, "sts.ordered_start", Source{Kind: SourceResource, Resource: v.ref, Field: "spec.podManagementPolicy"},
			map[string]string{"sts": v.ref.Name}),
		b.fact(pod, "sts.pod_not_ready", Source{Kind: SourceDerived, Resource: pod, Field: "status.conditions"},
			map[string]string{"pod": pod.Name, "ordinal": ord}),
		b.fact(v.ref, "sts.pods_not_created", Source{Kind: SourceDerived, Resource: v.ref, Field: "status.replicas"},
			map[string]string{"created": strconv.Itoa(v.ordinals.Created(lastOrd)), "replicas": strconv.Itoa(v.ordinals.Count)}),
	}
	conf := ConfidenceMedium // Pod, который ещё запускается, может стать готовым сам
	if last.Health == depgraph.Degraded {
		conf = ConfidenceHigh
	}
	params := map[string]string{"sts": v.ref.Name, "pod": pod.Name, "ordinal": ord}
	id := b.hypothesis(pod, "sts.stuck_ordered_start", conf, params, sup, nil)
	b.recommend(pod, "rec.fix_blocking_pod", params, id)
}

var unboundClaimReasons = []string{depgraph.ReasonUnbound, depgraph.ReasonNoDefaultClass}

// claimsPending: PVC, созданный по шаблону, не связан. Причины хранилища даёт существующее правило PVC; здесь только
// связь «PVC шаблона → StatefulSet», чтобы расследование StatefulSet вело к ним.
func claimsPending(b *builder, v stsView) {
	n := 0
	for _, c := range ownedNodes(b.c.Graph, v, "PersistentVolumeClaim") {
		if c.Health != depgraph.Degraded || !slices.Contains(unboundClaimReasons, c.Reason) {
			continue
		}
		if n++; n > maxClaimFindings {
			return
		}
		claim := fromGraphRef(c.Ref)
		b.symptom(SymptomClaimPending)
		phase := claimPhase(c)
		sup := []string{b.fact(claim, "sts.claim_not_bound", Source{Kind: SourceResource, Resource: claim, Field: "status.phase"},
			map[string]string{"pvc": claim.Name, "phase": phase})}
		params := map[string]string{"sts": v.ref.Name, "pvc": claim.Name}
		id := b.hypothesis(claim, "sts.pvc_pending", ConfidenceMedium, params, sup, nil)
		b.recommend(claim, "rec.fix_statefulset_claim", params, id)
	}
}

// updatePartitioned: partition > 0 и не все реплики обновлены. Это может быть намеренно (канарейковое обновление),
// поэтому уверенность средняя, а гипотеза — информационная.
func updatePartitioned(b *builder, v stsView) {
	ru := v.sts.Spec.UpdateStrategy.RollingUpdate
	if ru == nil || ru.Partition == nil || *ru.Partition <= 0 || v.sts.Status.UpdatedReplicas >= v.replicas {
		return
	}
	b.symptom(SymptomRollout)
	params := map[string]string{
		"sts": v.ref.Name, "partition": strconv.Itoa(int(*ru.Partition)),
		"updated": strconv.Itoa(int(v.sts.Status.UpdatedReplicas)), "replicas": strconv.Itoa(int(v.replicas)),
	}
	sup := []string{b.fact(v.ref, "sts.update_partitioned", Source{Kind: SourceResource, Resource: v.ref, Field: "spec.updateStrategy.rollingUpdate.partition"}, params)}
	if old := outdatedPods(b.c.Pods, v.sts.Status.UpdateRevision); old > 0 {
		sup = append(sup, b.fact(v.ref, "sts.pods_on_old_revision", Source{Kind: SourceDerived, Resource: v.ref, Field: "metadata.labels." + labelRevisionHash},
			map[string]string{"count": strconv.Itoa(old)}))
	}
	id := b.hypothesis(v.ref, "sts.update_partitioned", ConfidenceMedium, params, sup, nil)
	b.recommend(v.ref, "rec.lower_statefulset_partition", params, id)
}

// outdatedPods — сколько Pod'ов контекста не на ревизии обновления (по метке controller-revision-hash).
func outdatedPods(pods []*corev1.Pod, updateRevision string) int {
	if updateRevision == "" {
		return 0
	}
	n := 0
	for _, p := range pods {
		if h := p.Labels[labelRevisionHash]; h != "" && h != updateRevision {
			n++
		}
	}
	return n
}

// linkStatefulSetCauses связывает гипотезы правил Pod и хранилища с гипотезами StatefulSet: причина Pod'а или PVC
// объясняет остановку запуска и PVC шаблона. Связь строится после всех правил: порядок правил зависит от имён файлов,
// а правило хранилища идёт после этого.
func linkStatefulSetCauses(b *builder) {
	for _, mine := range slices.Clone(b.rep.Hypotheses) {
		if mine.RuleID != ruleStatefulSetID {
			continue
		}
		for i := range b.rep.Hypotheses {
			c := &b.rep.Hypotheses[i]
			if c.RuleID == ruleStatefulSetID || !sameResource(c.Resource, mine.Resource) {
				continue
			}
			switch {
			case mine.Code == "sts.stuck_ordered_start" && c.Resource.Kind == "Pod",
				mine.Code == "sts.pvc_pending" && (strings.HasPrefix(c.Code, "pvc.") || strings.HasPrefix(c.Code, "storage.")):
				if !slices.Contains(c.Explains, mine.ID) {
					c.Explains = append(c.Explains, mine.ID)
				}
			}
		}
	}
}

// statefulSetChains дополняет Report.Chains цепочками StatefulSet > Pod и StatefulSet > PVC: общий построитель идёт
// «от затронутого к кандидату» по зависимостям, где владелец — зависимость Pod'а, а не наоборот, поэтому путь от
// владельца к его Pod'у и PVC (по рёбрам owns) он не строит.
func statefulSetChains(b *builder) {
	v, ok := stsFromGraph(b)
	if !ok {
		return
	}
	linkStatefulSetCauses(b)
	g := b.c.Graph
	for _, h := range b.rep.Hypotheses {
		if h.RuleID != ruleStatefulSetID || (h.Code != "sts.stuck_ordered_start" && h.Code != "sts.pvc_pending") {
			continue
		}
		to, ok := g.Node(toGraphRef(h.Resource))
		if !ok || !slices.ContainsFunc(g.EdgesFrom(v.node.Ref, depgraph.EdgeOwns), func(e depgraph.Edge) bool { return e.To == to.Ref }) {
			continue
		}
		if len(b.rep.Chains) >= maxChains {
			return
		}
		b.rep.Chains = append(b.rep.Chains, Chain{Path: chainSteps([]depgraph.Node{v.node, to}), Candidate: h.ID})
	}
}
