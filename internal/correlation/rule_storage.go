// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation

import (
	"context"
	"slices"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// Коды фактов правила shared_storage.
const (
	factStorageConsumers  = "correlation.shared_storage.consumers"
	factStorageNoClass    = "correlation.shared_storage.class_missing"
	factStorageNoProvider = "correlation.shared_storage.no_provisioner"
	factStorageNoDefault  = "correlation.shared_storage.no_default_class"
	factStorageProvFailed = "correlation.shared_storage.provision_failed"
	factStorageTemporal   = "correlation.shared_storage.temporal"
	factStorageMulti      = "correlation.shared_storage.multi_workload"
)

const (
	storageGroup      = "storage.k8s.io"
	noProvisioner     = "kubernetes.io/no-provisioner"
	reasonProvFailed  = "ProvisioningFailed"
	bindingWaitFirst  = "WaitForFirstConsumer"
	annDefaultClass   = "storageclass.kubernetes.io/is-default-class"
	annDefaultBeta    = "storageclass.beta.kubernetes.io/is-default-class"
	annSelectedNode   = "volume.kubernetes.io/selected-node"
	storageEventLimit = 200
	// minProvFailedPVCs — сколько PVC класса должно иметь событие ProvisioningFailed, чтобы это считалось признаком (б).
	minProvFailedPVCs = 2
)

func init() { registerRule("shared_storage", ruleSharedStorage) }

// storageData — данные кластера, общие для всех классов хранилища одного запуска: каждое читается не более одного раза.
type storageData struct {
	pvcs      []*corev1.PersistentVolumeClaim
	pvcsOK    bool
	pods      []*corev1.Pod
	truncated bool
	events    []provEvent
}

// provEvent — событие ProvisioningFailed по PVC.
type provEvent struct {
	ns, name string
	at       time.Time
}

// ruleSharedStorage: несколько PVC одного StorageClass не привязаны, а использующие их нагрузки в сбое — гипотеза
// «общий источник — класс хранилища» (ADR-0033 §2). Классы берутся из графа зависимостей цели.
func ruleSharedStorage(ctx context.Context, e *env) []Candidate {
	g := e.dc.Graph
	if g == nil {
		return nil
	}
	var classes []depgraph.Node
	noDefault := false
	for _, n := range g.Nodes() {
		switch {
		case n.Kind == "StorageClass" && n.Group == storageGroup:
			classes = append(classes, n)
		case n.Kind == "PersistentVolumeClaim" && n.Reason == depgraph.ReasonNoDefaultClass:
			noDefault = true // граф уже установил: PVC без класса, а класса по умолчанию в кластере нет
		}
	}
	if len(classes) == 0 && !noDefault {
		return nil
	}
	data := e.readStorage(ctx)
	if !data.pvcsOK {
		return nil
	}
	var out []Candidate
	for _, cls := range classes {
		if c, ok := e.sharedStorage(ctx, cls, data); ok {
			out = append(out, c)
		}
	}
	if noDefault {
		if c, ok := e.noDefaultClass(ctx, data); ok {
			out = append(out, c)
		}
	}
	return out
}

func (e *env) readStorage(ctx context.Context) *storageData {
	d := &storageData{}
	res, err := e.r.List(ctx, gvkPVC, diagnose.ListOptions{Limit: podListLimit})
	if err != nil {
		e.unavailable(diagnose.ResourceRef{Kind: "PersistentVolumeClaim"}, reasonOf(err))
		return d
	}
	d.pvcsOK = true
	d.truncated = res.Truncated
	for i := range res.Items {
		var p corev1.PersistentVolumeClaim
		if runtime.DefaultUnstructuredConverter.FromUnstructured(res.Items[i].Object, &p) == nil {
			d.pvcs = append(d.pvcs, &p)
		}
	}
	if pres, perr := e.r.List(ctx, gvkPod, diagnose.ListOptions{Limit: podListLimit}); perr != nil {
		e.unavailable(diagnose.ResourceRef{Kind: "Pod"}, reasonOf(perr))
		d.pods, d.truncated = e.dc.Pods, true
	} else {
		d.truncated = d.truncated || pres.Truncated
		for i := range pres.Items {
			if p, ok := toPod(&pres.Items[i]); ok {
				d.pods = append(d.pods, p)
			}
		}
	}
	if eres, eerr := e.r.List(ctx, gvkEvent, diagnose.ListOptions{FieldSelector: "reason=" + reasonProvFailed, Limit: storageEventLimit}); eerr != nil {
		e.unavailable(diagnose.ResourceRef{Kind: "Event"}, reasonOf(eerr))
	} else {
		for i := range eres.Items {
			if ev, ok := provEventOf(&eres.Items[i]); ok {
				d.events = append(d.events, ev)
			}
		}
	}
	return d
}

func provEventOf(u *unstructured.Unstructured) (provEvent, bool) {
	if k, _, _ := unstructured.NestedString(u.Object, "involvedObject", "kind"); k != "PersistentVolumeClaim" {
		return provEvent{}, false
	}
	ns, _, _ := unstructured.NestedString(u.Object, "involvedObject", "namespace")
	name, _, _ := unstructured.NestedString(u.Object, "involvedObject", "name")
	ev := provEvent{ns: ns, name: name}
	for _, f := range []string{"lastTimestamp", "eventTime", "firstTimestamp"} {
		if s, _ := u.Object[f].(string); s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				ev.at = t.UTC()
				break
			}
		}
	}
	return ev, true
}

func (e *env) sharedStorage(ctx context.Context, cls depgraph.Node, d *storageData) (Candidate, bool) {
	src := diagnose.ResourceRef{Group: storageGroup, Kind: "StorageClass", Name: cls.Name, UID: cls.UID}
	waitFirst := false
	provisioner, havePrv := "", false
	if cls.Object != nil {
		provisioner, havePrv, _ = unstructured.NestedString(cls.Object.Object, "provisioner")
		mode, _, _ := unstructured.NestedString(cls.Object.Object, "volumeBindingMode")
		waitFirst = mode == bindingWaitFirst
	}

	isDefault := cls.Object != nil && (cls.Object.GetAnnotations()[annDefaultClass] == "true" ||
		cls.Object.GetAnnotations()[annDefaultBeta] == "true")
	claims := map[string]bool{}
	for _, p := range d.pvcs {
		// PVC без storageClassName относится к классу по умолчанию; явно пустой класс — статическая привязка, не наш случай.
		own := p.Spec.StorageClassName != nil && *p.Spec.StorageClassName == cls.Name ||
			p.Spec.StorageClassName == nil && isDefault
		if !own || p.Status.Phase == corev1.ClaimBound {
			continue
		}
		// WaitForFirstConsumer: PVC ждёт Pod'а — это не поломка привязки.
		if waitFirst && p.Status.Phase == corev1.ClaimPending && p.Annotations[annSelectedNode] == "" {
			continue
		}
		claims[p.Namespace+"/"+p.Name] = true
	}
	if len(claims) == 0 {
		return Candidate{}, false
	}

	// Признаки класса считаются до чтения потребителей: от них зависит только набор фактов.
	var abnormal []factSpec
	switch {
	case cls.Placeholder:
		abnormal = append(abnormal, factSpec{factStorageNoClass, src, nil})
	case cls.Object != nil && (!havePrv || provisioner == "" || provisioner == noProvisioner):
		abnormal = append(abnormal, factSpec{factStorageNoProvider, src, nil})
	}
	return e.storageCandidate(ctx, storageInput{src: src, claims: claims, base: abnormal, d: d,
		potential: &depgraph.Ref{Group: storageGroup, Kind: "StorageClass", Name: cls.Name}})
}

// storageInput — то, что отличает источники shared_storage друг от друга: сам источник, его PVC, признаки
// ненормальности источника и ссылка на него в графе (nil — зависимых по графу нет).
type storageInput struct {
	src       diagnose.ResourceRef
	claims    map[string]bool
	base      []factSpec
	d         *storageData
	potential *depgraph.Ref
}

// storageCandidate строит кандидата по PVC источника: потребители, события ProvisioningFailed, признаки уверенности.
func (e *env) storageCandidate(ctx context.Context, in storageInput) (Candidate, bool) {
	src, claims, d := in.src, in.claims, in.d
	abnormal := slices.Clone(in.base)
	set := newWorkloadSet()
	for _, p := range d.pods {
		if !usesClaim(p, claims) {
			continue
		}
		if info := inspectPod(p); info.problem() {
			set.add(e.workloadOf(ctx, p), info)
		}
	}
	if set.total() == 0 {
		return Candidate{}, false
	}

	failedPVCs := map[string]bool{}
	var evTimes []time.Time
	for _, ev := range d.events {
		if k := ev.ns + "/" + ev.name; claims[k] {
			failedPVCs[k] = true
			if !ev.at.IsZero() {
				evTimes = append(evTimes, ev.at)
			}
		}
	}
	if len(failedPVCs) >= minProvFailedPVCs {
		abnormal = append(abnormal, factSpec{factStorageProvFailed, src, map[string]string{"pvcs": strconv.Itoa(len(failedPVCs))}})
	}

	ev := evidence{structural: true, abnormal: len(abnormal) > 0, multi: set.total() >= minWorkloadsForMulti}
	var evFirst time.Time
	var span time.Duration
	if len(failedPVCs) >= minProvFailedPVCs && len(evTimes) >= minProvFailedPVCs {
		lo, hi := evTimes[0], evTimes[0]
		for _, t := range evTimes {
			if t.Before(lo) {
				lo = t
			}
			if t.After(hi) {
				hi = t
			}
		}
		if span = hi.Sub(lo); span <= CorrelationWindow {
			ev.temporal, evFirst = true, lo
		}
	}
	conf, ok := ev.confidence()
	if !ok {
		return Candidate{}, false
	}

	specs := []factSpec{{factStorageConsumers, src, map[string]string{
		"workloads": strconv.Itoa(set.total()), "pods": strconv.Itoa(set.pods), "pvcs": strconv.Itoa(len(claims))}}}
	specs = append(specs, abnormal...)
	if ev.temporal {
		specs = append(specs, factSpec{factStorageTemporal, src, map[string]string{"spanSeconds": strconv.Itoa(int(span.Seconds()))}})
	}
	if ev.multi {
		specs = append(specs, factSpec{factStorageMulti, src, map[string]string{"workloads": strconv.Itoa(set.total())}})
	}

	since := set.firstFailure()
	if since.IsZero() {
		since = evFirst
	}
	var potential []WorkloadRef
	if in.potential != nil {
		potential = e.potentialFromGraph(ctx, *in.potential, set)
	}
	c := Candidate{
		Kind: KindSharedStorage, Source: src, Confidence: conf, Since: since,
		Affected: set.sorted(), Total: set.total(), Truncated: d.truncated,
		Potential:  potential,
		Supporting: e.addFacts(specs),
	}
	c.ID = candidateID(c)
	return c, true
}

// noDefaultClass: в кластере нет класса по умолчанию, а PVC без storageClassName остаются неприсоединёнными.
// Источника-объекта нет, поэтому источником служит «безымянный» StorageClass (Name пуст): ключ
// shared_storage:storage.k8s.io/StorageClass// один на кластер, и автоприсоединение собирает такие
// расследования в одну проблему. Условие подтверждено графом (причина no_default_storageclass у PVC).
func (e *env) noDefaultClass(ctx context.Context, d *storageData) (Candidate, bool) {
	claims := map[string]bool{}
	for _, p := range d.pvcs {
		if p.Spec.StorageClassName == nil && p.Status.Phase != corev1.ClaimBound {
			claims[p.Namespace+"/"+p.Name] = true
		}
	}
	if len(claims) == 0 {
		return Candidate{}, false
	}
	src := diagnose.ResourceRef{Group: storageGroup, Kind: "StorageClass"}
	return e.storageCandidate(ctx, storageInput{src: src, claims: claims, d: d,
		base: []factSpec{{factStorageNoDefault, src, nil}}})
}

func usesClaim(p *corev1.Pod, claims map[string]bool) bool {
	for _, v := range p.Spec.Volumes {
		if v.PersistentVolumeClaim != nil && claims[p.Namespace+"/"+v.PersistentVolumeClaim.ClaimName] {
			return true
		}
	}
	return false
}
