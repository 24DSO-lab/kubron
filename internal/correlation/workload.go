// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation

import (
	"context"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// Коды симптомов Pod'а. Только коды: тексты сообщений и значения полей в снимок не попадают (ADR-0022).
const (
	symptomCrashLoop = "pod.crash_loop"
	symptomOOMKilled = "pod.oom_killed"
	symptomNotReady  = "pod.not_ready"
	symptomEvicted   = "pod.evicted"
	symptomPending   = "pod.pending"
)

// podInfo — Pod с вычисленными симптомами и временем сбоя.
type podInfo struct {
	pod      *corev1.Pod
	symptoms []string
	failedAt time.Time // нулевое, если по меткам объекта время не определить
}

func (p podInfo) problem() bool { return len(p.symptoms) > 0 }

func toPod(u *unstructured.Unstructured) (*corev1.Pod, bool) {
	var p corev1.Pod
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &p); err != nil {
		return nil, false
	}
	return &p, true
}

// inspectPod определяет симптомы: CrashLoopBackOff, OOMKilled, Ready=false, вытеснение и ожидание планирования.
func inspectPod(p *corev1.Pod) podInfo {
	info := podInfo{pod: p}
	if p.Status.Phase == corev1.PodSucceeded {
		return info
	}
	statuses := make([]corev1.ContainerStatus, 0, len(p.Status.InitContainerStatuses)+len(p.Status.ContainerStatuses))
	statuses = append(statuses, p.Status.InitContainerStatuses...)
	statuses = append(statuses, p.Status.ContainerStatuses...)

	var crash, oom bool
	var lastFinished time.Time
	for _, cs := range statuses {
		if w := cs.State.Waiting; w != nil && w.Reason == "CrashLoopBackOff" {
			crash = true
		}
		for _, t := range []*corev1.ContainerStateTerminated{cs.LastTerminationState.Terminated, cs.State.Terminated} {
			if t == nil {
				continue
			}
			if t.Reason == "OOMKilled" {
				oom = true
			}
			if t.FinishedAt.Time.After(lastFinished) {
				lastFinished = t.FinishedAt.Time
			}
		}
	}
	evicted := p.Status.Phase == corev1.PodFailed && p.Status.Reason == "Evicted"
	pending := p.Status.Phase == corev1.PodPending
	var readyFalse bool
	var readyAt, scheduledAt time.Time
	for _, c := range p.Status.Conditions {
		switch {
		case c.Type == corev1.PodReady && c.Status != corev1.ConditionTrue:
			readyFalse, readyAt = true, c.LastTransitionTime.Time
		case c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse:
			scheduledAt = c.LastTransitionTime.Time
		}
	}
	notReady := readyFalse && !pending && p.Status.Phase != corev1.PodFailed

	for _, s := range []struct {
		on   bool
		code string
	}{{crash, symptomCrashLoop}, {oom, symptomOOMKilled}, {notReady, symptomNotReady}, {evicted, symptomEvicted}, {pending, symptomPending}} {
		if s.on {
			info.symptoms = append(info.symptoms, s.code)
		}
	}
	sort.Strings(info.symptoms)
	if !info.problem() {
		return info
	}

	// Время сбоя — по меткам объекта: переход Ready, завершение последнего запуска, ожидание планирования.
	switch {
	case !readyAt.IsZero():
		info.failedAt = readyAt
	case !lastFinished.IsZero():
		info.failedAt = lastFinished
	case !scheduledAt.IsZero():
		info.failedAt = scheduledAt
	case pending:
		info.failedAt = p.CreationTimestamp.Time
	case evicted && p.Status.StartTime != nil:
		info.failedAt = p.Status.StartTime.Time
	}
	info.failedAt = info.failedAt.UTC()
	return info
}

// wkey — нагрузка верхнего уровня.
type wkey struct{ Kind, Namespace, Name string }

func (k wkey) less(o wkey) bool {
	if k.Kind != o.Kind {
		return k.Kind < o.Kind
	}
	if k.Namespace != o.Namespace {
		return k.Namespace < o.Namespace
	}
	return k.Name < o.Name
}

var gvkJob = schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}

// workloadResolver определяет нагрузку верхнего уровня Pod'а. ReplicaSet → Deployment требует чтения владельцев
// ReplicaSet: один List на запуск (лениво, кеш). Если прочитать нельзя — имя Deployment выводится из имени
// ReplicaSet и метки pod-template-hash, иначе нагрузкой остаётся сам ReplicaSet.
// Job → CronJob устроен так же (один ленивый List Job), но без выведения по имени: если Job не прочитан,
// нагрузкой остаётся сам Job.
type workloadResolver struct {
	loaded   bool
	complete bool            // список прочитан целиком: отсутствие ReplicaSet в нём означает самостоятельный ReplicaSet
	owners   map[string]wkey // «ns/имя ReplicaSet» → контроллер-владелец (Deployment, Rollout и т. п.)

	jobsLoaded bool
	jobOwners  map[string]wkey // «ns/имя Job» → CronJob-владелец; у самостоятельного Job записи нет
}

func (e *env) loadReplicaSets(ctx context.Context) {
	if e.rs.loaded {
		return
	}
	e.rs.loaded = true
	e.rs.owners = map[string]wkey{}
	res, err := e.r.List(ctx, gvkReplicaSet, diagnose.ListOptions{Limit: podListLimit})
	if err != nil {
		e.unavailable(diagnose.ResourceRef{Kind: "ReplicaSet", Group: "apps"}, reasonOf(err))
		return
	}
	if res.Truncated {
		e.unavailable(diagnose.ResourceRef{Kind: "ReplicaSet", Group: "apps"}, reasonTooLarge)
	}
	e.rs.complete = !res.Truncated
	for _, it := range res.Items {
		refs := it.GetOwnerReferences()
		if len(refs) == 0 {
			continue
		}
		o := refs[0]
		for _, r := range refs {
			if r.Controller != nil && *r.Controller {
				o = r
				break
			}
		}
		e.rs.owners[it.GetNamespace()+"/"+it.GetName()] = wkey{o.Kind, it.GetNamespace(), o.Name}
	}
}

// loadJobs читает Job одним запросом и запоминает владельцев-CronJob. Недоступность — в Missing; тогда Pod'ы
// от Job остаются нагрузкой «Job».
func (e *env) loadJobs(ctx context.Context) {
	if e.rs.jobsLoaded {
		return
	}
	e.rs.jobsLoaded = true
	e.rs.jobOwners = map[string]wkey{}
	res, err := e.r.List(ctx, gvkJob, diagnose.ListOptions{Limit: podListLimit})
	if err != nil {
		e.unavailable(diagnose.ResourceRef{Kind: "Job", Group: "batch"}, reasonOf(err))
		return
	}
	if res.Truncated {
		e.unavailable(diagnose.ResourceRef{Kind: "Job", Group: "batch"}, reasonTooLarge)
	}
	for _, it := range res.Items {
		for _, o := range it.GetOwnerReferences() {
			if o.Kind == "CronJob" {
				e.rs.jobOwners[it.GetNamespace()+"/"+it.GetName()] = wkey{"CronJob", it.GetNamespace(), o.Name}
				break
			}
		}
	}
}

func controllerOf(p *corev1.Pod) (kind, name string, ok bool) {
	for _, o := range p.OwnerReferences {
		if o.Controller != nil && *o.Controller {
			return o.Kind, o.Name, true
		}
	}
	if len(p.OwnerReferences) > 0 {
		return p.OwnerReferences[0].Kind, p.OwnerReferences[0].Name, true
	}
	return "", "", false
}

func (e *env) workloadOf(ctx context.Context, p *corev1.Pod) wkey {
	self := wkey{"Pod", p.Namespace, p.Name}
	kind, name, ok := controllerOf(p)
	switch {
	case !ok, kind == "Node": // одиночный и статический Pod
		return self
	case kind == "ReplicaSet":
		e.loadReplicaSets(ctx)
		if k, found := e.rs.owners[p.Namespace+"/"+name]; found {
			return k
		}
		if !e.rs.complete {
			// ReplicaSet не прочитан: имя Deployment выводится из имени ReplicaSet и метки pod-template-hash.
			if h := p.Labels["pod-template-hash"]; h != "" && strings.HasSuffix(name, "-"+h) {
				if base := strings.TrimSuffix(name, "-"+h); base != "" {
					return wkey{"Deployment", p.Namespace, base}
				}
			}
		}
		return wkey{"ReplicaSet", p.Namespace, name}
	case kind == "Job":
		e.loadJobs(ctx)
		if k, found := e.rs.jobOwners[p.Namespace+"/"+name]; found {
			return k
		}
		return wkey{"Job", p.Namespace, name}
	default:
		return wkey{kind, p.Namespace, name}
	}
}

// workloadSet накапливает затронутые нагрузки.
type workloadSet struct {
	m    map[wkey]*WorkloadRef
	pods int
}

func newWorkloadSet() *workloadSet { return &workloadSet{m: map[wkey]*WorkloadRef{}} }

func (w *workloadSet) add(k wkey, info podInfo) {
	ref := w.m[k]
	if ref == nil {
		ref = &WorkloadRef{Kind: k.Kind, Namespace: k.Namespace, Name: k.Name}
		w.m[k] = ref
	}
	ref.ProblemPods++
	w.pods++
	for _, s := range info.symptoms {
		if !contains(ref.Symptoms, s) {
			ref.Symptoms = append(ref.Symptoms, s)
		}
	}
	if !info.failedAt.IsZero() && (ref.Since.IsZero() || info.failedAt.Before(ref.Since)) {
		ref.Since = info.failedAt
	}
}

func (w *workloadSet) total() int { return len(w.m) }

// firstFailure — время первого сбоя среди всех затронутых нагрузок (до усечения MaxAffected).
func (w *workloadSet) firstFailure() time.Time {
	var t time.Time
	for _, ref := range w.m {
		if !ref.Since.IsZero() && (t.IsZero() || ref.Since.Before(t)) {
			t = ref.Since
		}
	}
	return t
}

// sorted возвращает нагрузки по возрастанию (вид, namespace, имя) и общее число, список усечён MaxAffected.
func (w *workloadSet) sorted() []WorkloadRef {
	keys := make([]wkey, 0, len(w.m))
	for k := range w.m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].less(keys[j]) })
	out := make([]WorkloadRef, 0, len(keys))
	for _, k := range keys {
		ref := *w.m[k]
		sort.Strings(ref.Symptoms)
		out = append(out, ref)
	}
	if len(out) > MaxAffected {
		out = out[:MaxAffected]
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// potentialFromGraph — нагрузки, зависимые от источника по графу, но без симптомов (ADR-0033 §3).
// Нагрузка Pod'а определяется по владельцам в графе, а если их там нет — по ownerReferences объекта из графа
// (так же, как у затронутых). Pod'ы с симптомами и нагрузки, уже попавшие в affected, пропускаются.
// Граф может быть nil или неполным: тогда список пуст или короче.
func (e *env) potentialFromGraph(ctx context.Context, src depgraph.Ref, affected *workloadSet) []WorkloadRef {
	g := e.dc.Graph
	if g == nil {
		return nil
	}
	seen := map[wkey]bool{}
	var out []WorkloadRef
	for _, n := range g.Dependents(src) {
		if n.Kind != "Pod" || n.Placeholder {
			continue
		}
		var top wkey
		if owners := g.Owners(n.Ref); len(owners) > 0 {
			last := owners[len(owners)-1]
			top = wkey{last.Kind, last.Namespace, last.Name}
		} else if p := podOf(n); p != nil {
			if inspectPod(p).problem() {
				continue
			}
			top = e.workloadOf(ctx, p)
		} else {
			top = wkey{"Pod", n.Namespace, n.Name}
		}
		if _, hit := affected.m[top]; hit || seen[top] {
			continue
		}
		seen[top] = true
		out = append(out, WorkloadRef{Kind: top.Kind, Namespace: top.Namespace, Name: top.Name})
	}
	sort.Slice(out, func(i, j int) bool {
		return wkey{out[i].Kind, out[i].Namespace, out[i].Name}.less(wkey{out[j].Kind, out[j].Namespace, out[j].Name})
	})
	if len(out) > MaxAffected {
		out = out[:MaxAffected]
	}
	return out
}

func podOf(n depgraph.Node) *corev1.Pod {
	if n.Object == nil {
		return nil
	}
	p, ok := toPod(n.Object)
	if !ok {
		return nil
	}
	return p
}
