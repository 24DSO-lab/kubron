// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation

import (
	"context"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// Коды фактов правила shared_dependency.
const (
	factDepConsumers  = "correlation.shared_dependency.consumers"
	factDepReferrers  = "correlation.shared_dependency.referrers"
	factDepMissing    = "correlation.shared_dependency.missing"
	factDepNoEndpoint = "correlation.shared_dependency.no_endpoints"
	factDepTemporal   = "correlation.shared_dependency.temporal"
	factDepMulti      = "correlation.shared_dependency.multi_workload"
)

// minDependencyWorkloads — сколько потребителей нужно общему источнику, чтобы стать кандидатом
// (ADR-0033 §3, contracts.md §5).
const minDependencyWorkloads = 2

// Виды и группы, которые правило узнаёт по вершине графа.
const (
	networkingGroup = "networking.k8s.io"
	kindConfigMap   = "ConfigMap"
	kindService     = "Service"
	kindIngress     = "Ingress"
	kindIngressCls  = "IngressClass"
)

var gvkIngress = schema.GroupVersionKind{Group: networkingGroup, Version: "v1", Kind: kindIngress}

func init() { registerRule("shared_dependency", ruleSharedDependency) }

// ruleSharedDependency: несколько нагрузок зависят от одного отсутствующего объекта или от Service без готовых
// endpoints — гипотеза «общий источник — зависимость» (ADR-0033 §2–§3). Источники берутся из графа цели:
// вершины-заглушки Missing любого вида и Service в состоянии Degraded без endpoints. Потребителей находят граф
// (входящие рёбра вершины) и живые чтения того же namespace; всё читается с Limit и в пределах бюджета.
func ruleSharedDependency(ctx context.Context, e *env) []Candidate {
	g := e.dc.Graph
	if g == nil {
		return nil
	}
	reads := &depReads{e: e, pods: map[string]*podRead{}, ings: map[string]*ingressRead{}}
	var out []Candidate
	for _, n := range g.Nodes() {
		absent, ok := dependencySource(n)
		if !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			e.unavailable(e.dc.Target, reasonDeadline)
			break
		}
		if c, ok := e.dependencyCandidate(n, absent, reads.consumers(ctx, n)); ok {
			out = append(out, c)
		}
	}
	return out
}

// dependencySource сообщает, может ли вершина быть общим источником зависимости. absent — объект не найден
// (заглушка Missing); иначе это Service без готовых endpoints. Node и StorageClass уже разбирают shared_node и
// shared_storage, а Unknown («не прочитали») источником не бывает: «нет» не равно «не знаем».
func dependencySource(n depgraph.Node) (absent, ok bool) {
	switch {
	case n.Placeholder || n.Health == depgraph.Missing:
		if n.Group == "" && n.Kind == "Node" || n.Group == storageGroup && n.Kind == "StorageClass" {
			return false, false
		}
		return true, true
	case n.Group == "" && n.Kind == kindService && n.Health == depgraph.Degraded:
		switch n.Reason {
		case depgraph.ReasonNoEndpoints, depgraph.ReasonNoReadyEndpoints, depgraph.ReasonSelectorMatchesNothing:
			return false, true
		}
	}
	return false, false
}

// podRead и ingressRead — результат одного чтения списка. failed — прочитать не удалось: полнота не заявляется.
type podRead struct {
	items     []*corev1.Pod
	truncated bool
	failed    bool
}

type ingressRead struct {
	items     []*networkingv1.Ingress
	truncated bool
	failed    bool
}

// depReads кеширует списки на время одного запуска правила: источники одного namespace делят одно чтение.
type depReads struct {
	e    *env
	pods map[string]*podRead
	ings map[string]*ingressRead
}

// podsIn читает Pod'ы namespace.
func (d *depReads) podsIn(ctx context.Context, ns string) *podRead {
	if r, ok := d.pods[ns]; ok {
		return r
	}
	r := &podRead{}
	d.pods[ns] = r
	src := diagnose.ResourceRef{Kind: "Pod", Namespace: ns}
	res, err := d.e.r.List(ctx, gvkPod, diagnose.ListOptions{Namespace: ns, Limit: podListLimit})
	if err != nil {
		d.e.unavailable(src, reasonOf(err))
		r.failed = true
		return r
	}
	if r.truncated = res.Truncated; r.truncated {
		d.e.unavailable(src, reasonTooLarge)
	}
	for i := range res.Items {
		if p, ok := toPod(&res.Items[i]); ok {
			r.items = append(r.items, p)
		}
	}
	return r
}

// ingressesIn читает Ingress namespace; пустой ns — всего кластера (нужен для IngressClass).
func (d *depReads) ingressesIn(ctx context.Context, ns string) *ingressRead {
	if r, ok := d.ings[ns]; ok {
		return r
	}
	r := &ingressRead{}
	d.ings[ns] = r
	src := diagnose.ResourceRef{Group: networkingGroup, Kind: kindIngress, Namespace: ns}
	res, err := d.e.r.List(ctx, gvkIngress, diagnose.ListOptions{Namespace: ns, Limit: podListLimit})
	if err != nil {
		d.e.unavailable(src, reasonOf(err))
		r.failed = true
		return r
	}
	if r.truncated = res.Truncated; r.truncated {
		d.e.unavailable(src, reasonTooLarge)
	}
	for i := range res.Items {
		var ing networkingv1.Ingress
		if runtime.DefaultUnstructuredConverter.FromUnstructured(res.Items[i].Object, &ing) == nil {
			r.items = append(r.items, &ing)
		}
	}
	return r
}

// depConsumers — потребители общего источника. Pod'ы группируются в нагрузки верхнего уровня; остальные
// потребители (Ingress, HPA и т. п.) считаются сами по себе: ссылка на сломанный источник уже делает их затронутыми.
type depConsumers struct {
	pods      *workloadSet
	seenPods  map[string]bool
	refs      map[wkey]*WorkloadRef
	potential map[wkey]bool // нагрузки с Pod'ами, которые ссылаются на источник, но пока без симптомов
	truncated bool
}

func (c *depConsumers) addRef(kind, ns, name string) {
	k := wkey{kind, ns, name}
	if _, ok := c.refs[k]; !ok {
		c.refs[k] = &WorkloadRef{Kind: kind, Namespace: ns, Name: name}
	}
}

func (e *env) addPodConsumer(ctx context.Context, c *depConsumers, p *corev1.Pod) {
	id := p.Namespace + "/" + p.Name
	if c.seenPods[id] {
		return
	}
	c.seenPods[id] = true
	k := e.workloadOf(ctx, p)
	if info := inspectPod(p); info.problem() {
		c.pods.add(k, info)
	} else {
		c.potential[k] = true
	}
}

// consumers находит потребителей источника: входящие рёбра вершины в графе и живые чтения по виду источника.
func (d *depReads) consumers(ctx context.Context, src depgraph.Node) *depConsumers {
	e := d.e
	c := &depConsumers{pods: newWorkloadSet(), seenPods: map[string]bool{}, refs: map[wkey]*WorkloadRef{}, potential: map[wkey]bool{}}
	g := e.dc.Graph
	for _, ed := range g.EdgesTo(src.Ref, "") {
		from, ok := g.Node(ed.From)
		if !ok || from.Placeholder {
			continue
		}
		if from.Group == "" && from.Kind == "Pod" {
			if p := podOf(from); p != nil {
				e.addPodConsumer(ctx, c, p)
			}
			continue
		}
		c.addRef(from.Kind, from.Namespace, from.Name)
	}
	switch {
	case src.Group == "" && src.Kind == kindConfigMap:
		r := d.podsIn(ctx, src.Namespace)
		c.truncated = c.truncated || r.truncated || r.failed
		for _, p := range r.items {
			if refsConfigMap(p, src.Name) {
				e.addPodConsumer(ctx, c, p)
			}
		}
	case src.Group == "" && src.Kind == kindService:
		r := d.ingressesIn(ctx, src.Namespace)
		c.truncated = c.truncated || r.truncated || r.failed
		for _, ing := range r.items {
			if ingressRoutesTo(ing, src.Name) {
				c.addRef(kindIngress, ing.Namespace, ing.Name)
			}
		}
	case src.Group == networkingGroup && src.Kind == kindIngressCls:
		r := d.ingressesIn(ctx, "")
		c.truncated = c.truncated || r.truncated || r.failed
		for _, ing := range r.items {
			if name, _ := depgraph.IngressClassOf(ing); name == src.Name {
				c.addRef(kindIngress, ing.Namespace, ing.Name)
			}
		}
	}
	return c
}

// refsConfigMap — Pod ссылается на ConfigMap обязательно (volumes, projected, envFrom, env.valueFrom). Ссылка с
// optional: true Pod'у не мешает, поэтому потребителем не считается: так же решает граф.
func refsConfigMap(p *corev1.Pod, name string) bool {
	required := func(n string, optional *bool) bool { return n == name && (optional == nil || !*optional) }
	for _, v := range p.Spec.Volumes {
		if v.ConfigMap != nil && required(v.ConfigMap.Name, v.ConfigMap.Optional) {
			return true
		}
		if v.Projected == nil {
			continue
		}
		for _, s := range v.Projected.Sources {
			if s.ConfigMap != nil && required(s.ConfigMap.Name, s.ConfigMap.Optional) {
				return true
			}
		}
	}
	for _, list := range [][]corev1.Container{p.Spec.InitContainers, p.Spec.Containers} {
		for _, c := range list {
			for _, ef := range c.EnvFrom {
				if ef.ConfigMapRef != nil && required(ef.ConfigMapRef.Name, ef.ConfigMapRef.Optional) {
					return true
				}
			}
			for _, ev := range c.Env {
				if ev.ValueFrom != nil && ev.ValueFrom.ConfigMapKeyRef != nil &&
					required(ev.ValueFrom.ConfigMapKeyRef.Name, ev.ValueFrom.ConfigMapKeyRef.Optional) {
					return true
				}
			}
		}
	}
	return false
}

func ingressRoutesTo(ing *networkingv1.Ingress, service string) bool {
	for _, be := range depgraph.IngressBackends(ing) {
		if be.Service == service {
			return true
		}
	}
	return false
}

func refKey(w WorkloadRef) wkey { return wkey{w.Kind, w.Namespace, w.Name} }

// sortRefs упорядочивает нагрузки по (вид, namespace, имя) и ограничивает список MaxAffected.
func sortRefs(refs []WorkloadRef) []WorkloadRef {
	sort.Slice(refs, func(i, j int) bool { return refKey(refs[i]).less(refKey(refs[j])) })
	if len(refs) > MaxAffected {
		refs = refs[:MaxAffected]
	}
	return refs
}

// dependencyCandidate строит кандидата по источнику и его потребителям. Признаки ADR-0033 §4: (а) структурная
// связь — потребители привязаны к источнику; (б) источник ненормален — по построению (Missing или Degraded);
// (в) первые сбои потребителей-нагрузок уложились в окно корреляции; (г) затронуто не менее трёх нагрузок.
func (e *env) dependencyCandidate(src depgraph.Node, absent bool, cons *depConsumers) (Candidate, bool) {
	total := cons.pods.total() + len(cons.refs)
	if total < minDependencyWorkloads {
		return Candidate{}, false
	}
	ref := diagnose.ResourceRef{Group: src.Group, Kind: src.Kind, Namespace: src.Namespace, Name: src.Name, UID: src.UID}

	ev := evidence{structural: true, abnormal: true, multi: total >= minWorkloadsForMulti}
	var times []time.Time
	for _, w := range cons.pods.m {
		if !w.Since.IsZero() {
			times = append(times, w.Since)
		}
	}
	var span time.Duration
	if len(times) >= 2 {
		lo, hi := times[0], times[0]
		for _, t := range times {
			if t.Before(lo) {
				lo = t
			}
			if t.After(hi) {
				hi = t
			}
		}
		if span = hi.Sub(lo); span <= CorrelationWindow {
			ev.temporal = true
		}
	}
	conf, ok := ev.confidence()
	if !ok {
		return Candidate{}, false
	}

	var specs []factSpec
	if n := cons.pods.total(); n > 0 {
		specs = append(specs, factSpec{factDepConsumers, ref, map[string]string{"workloads": itoa(n), "pods": itoa(cons.pods.pods)}})
	}
	if n := len(cons.refs); n > 0 {
		specs = append(specs, factSpec{factDepReferrers, ref, map[string]string{"count": itoa(n)}})
	}
	if absent {
		specs = append(specs, factSpec{factDepMissing, ref, nil})
	} else {
		specs = append(specs, factSpec{factDepNoEndpoint, ref, nil})
	}
	if ev.temporal {
		specs = append(specs, factSpec{factDepTemporal, ref, map[string]string{"spanSeconds": itoa(int(span.Seconds()))}})
	}
	if ev.multi {
		specs = append(specs, factSpec{factDepMulti, ref, map[string]string{"workloads": itoa(total)}})
	}

	affected := make([]WorkloadRef, 0, total)
	for _, w := range cons.pods.m {
		r := *w
		sort.Strings(r.Symptoms)
		affected = append(affected, r)
	}
	for _, w := range cons.refs {
		affected = append(affected, *w)
	}
	var potential []WorkloadRef
	for k := range cons.potential {
		if _, hit := cons.pods.m[k]; !hit {
			potential = append(potential, WorkloadRef{Kind: k.Kind, Namespace: k.Namespace, Name: k.Name})
		}
	}

	since := cons.pods.firstFailure()
	if since.IsZero() {
		since = e.now
	}
	c := Candidate{
		Kind: KindSharedDependency, Source: ref, Confidence: conf, Since: since,
		Affected: sortRefs(affected), Total: total, Truncated: cons.truncated,
		Potential:  sortRefs(potential),
		Supporting: e.addFacts(specs),
	}
	c.ID = candidateID(c)
	return c, true
}
