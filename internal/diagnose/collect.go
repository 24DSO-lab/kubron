// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Пределы сбора: диагностика короткая и синхронная (ADR-0026 §8), нагрузка на кластер ограничена.
const (
	maxPods        = 20   // подов в контексте (сначала неготовые)
	maxReplicaSets = 10   // ReplicaSet в контексте (самые новые)
	maxNodes       = 5    // узлов, читаемых для объяснения состояния
	maxEvents      = 1000 // событий namespace за один запрос
	maxListObjects = 500  // объектов в выборке подов и ReplicaSet
	maxLogTargets  = 4    // контейнеров, чьи логи читаются
	logTailLines   = 400  // строк хвоста лога: окно вокруг якоря выбирается из них
	logTailRetry   = 100  // хвост при ErrTooLarge
	logLimitBytes  = 512 << 10
)

// ErrUnsupportedTarget — вид цели не поддерживается: MVP диагностирует Pod, Deployment и ReplicaSet.
var ErrUnsupportedTarget = errors.New("diagnostics are not supported for this resource kind")

// CollectOptions — параметры сбора.
type CollectOptions struct {
	// Logs — читать ли логи проблемных контейнеров (для фрагментов-доказательств). false — политика запрещает
	// сохранять фрагменты (выключатель, исключённый namespace, отключено для расследования): тогда лог не читается.
	Logs bool
	// Now — источник времени; nil — time.Now. Нужен тестам.
	Now func() time.Time
}

func (o CollectOptions) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

// collector — состояние одного сбора.
type collector struct {
	r    Reader
	opts CollectOptions
	c    *Context
}

// Collect читает контекст цели. Ошибка возвращается, только если не удалось прочитать саму цель (или отменён ctx);
// недоступность остальных источников попадает в Context.Missing / Context.Partial, а не роняет сбор.
func Collect(ctx context.Context, r Reader, target ResourceRef, opts CollectOptions) (*Context, error) {
	if target.Name == "" {
		return nil, fmt.Errorf("target name is empty: %w", ErrNotFound)
	}
	switch target.Kind {
	case "Pod", "Deployment", "ReplicaSet":
		if target.Namespace == "" {
			return nil, fmt.Errorf("target namespace is empty: %w", ErrNotFound)
		}
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedTarget, target.Kind)
	}
	col := &collector{r: r, opts: opts, c: &Context{Target: target, ObservedAt: opts.now(), Nodes: map[string]*corev1.Node{}}}
	if err := col.run(ctx); err != nil {
		return nil, err
	}
	return col.c, nil
}

func (col *collector) run(ctx context.Context) error {
	c := col.c
	var err error
	switch c.Target.Kind {
	case "Pod":
		err = col.fromPod(ctx)
	case "Deployment":
		err = col.fromDeployment(ctx)
	case "ReplicaSet":
		err = col.fromReplicaSet(ctx)
	}
	if err != nil {
		return err
	}
	for _, step := range []func(context.Context){col.readNamespace, col.readNodes, col.readEvents, col.readLogs} {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		step(ctx)
	}
	slices.SortFunc(c.Pods, func(a, b *corev1.Pod) int { return strings.Compare(a.Name, b.Name) })
	return nil
}

// getTyped читает объект и разбирает его. Ошибка чтения — первая; разбор не удался — ErrUnavailable с причиной.
func getTyped[T any](ctx context.Context, col *collector, gvk schema.GroupVersionKind, ns, name string) (*T, error) {
	u, err := col.r.Get(ctx, gvk, ns, name)
	if err != nil {
		return nil, err
	}
	out, err := decode[T](u)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	col.c.markObserved(ResourceRef{Kind: gvk.Kind, Namespace: ns, Name: name, UID: string(u.GetUID())}, col.opts.now())
	return out, nil
}

// optional читает объект, не обязательный для отчёта: любая ошибка — MissingContext, кроме отмены.
func optional[T any](ctx context.Context, col *collector, gvk schema.GroupVersionKind, ns, name string) *T {
	out, err := getTyped[T](ctx, col, gvk, ns, name)
	if err != nil {
		col.c.addMissing("source.unavailable", ResourceRef{Kind: gvk.Kind, Namespace: ns, Name: name},
			map[string]string{"reason": errorReason(err)})
		return nil
	}
	return out
}

// errorReason — стабильный код причины для MissingContext (не текст ошибки).
func errorReason(err error) string {
	switch {
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrForbidden):
		return "forbidden"
	case errors.Is(err, ErrTooLarge):
		return "too_large"
	default:
		return "unavailable"
	}
}

// primary читает саму цель: отказ — ошибка сбора.
func primary[T any](ctx context.Context, col *collector, gvk schema.GroupVersionKind, ns, name string) (*T, error) {
	out, err := getTyped[T](ctx, col, gvk, ns, name)
	if err != nil {
		return nil, fmt.Errorf("reading %s %s/%s: %w", gvk.Kind, ns, name, err)
	}
	return out, nil
}

func (col *collector) fromPod(ctx context.Context) error {
	t := col.c.Target
	pod, err := primary[corev1.Pod](ctx, col, gvkPod, t.Namespace, t.Name)
	if err != nil {
		return err
	}
	col.c.Target.UID = string(pod.UID)
	col.c.Pods = []*corev1.Pod{pod}
	// Цепочка владельцев: Pod → ReplicaSet → Deployment.
	if o := controllerOf(pod.OwnerReferences, "ReplicaSet"); o != nil {
		if rs := optional[appsv1.ReplicaSet](ctx, col, gvkReplicaSet, t.Namespace, o.Name); rs != nil {
			col.c.ReplicaSets = []*appsv1.ReplicaSet{rs}
			col.readOwnerDeployment(ctx, rs)
		}
	}
	return nil
}

func (col *collector) readOwnerDeployment(ctx context.Context, rs *appsv1.ReplicaSet) {
	if o := controllerOf(rs.OwnerReferences, "Deployment"); o != nil {
		col.c.Deployment = optional[appsv1.Deployment](ctx, col, gvkDeployment, rs.Namespace, o.Name)
	}
}

func (col *collector) fromDeployment(ctx context.Context) error {
	t := col.c.Target
	dep, err := primary[appsv1.Deployment](ctx, col, gvkDeployment, t.Namespace, t.Name)
	if err != nil {
		return err
	}
	col.c.Target.UID = string(dep.UID)
	col.c.Deployment = dep
	sel := selectorString(dep.Spec.Selector)
	if sel == "" {
		col.c.addMissing("deployment.selector_missing", depRef(dep), nil)
		return nil
	}
	col.listReplicaSets(ctx, dep.Namespace, sel, func(rs *appsv1.ReplicaSet) bool {
		o := controllerOf(rs.OwnerReferences, "Deployment")
		return o != nil && (o.UID == dep.UID || (o.UID == "" && o.Name == dep.Name))
	})
	col.listPods(ctx, dep.Namespace, sel)
	return nil
}

func (col *collector) fromReplicaSet(ctx context.Context) error {
	t := col.c.Target
	rs, err := primary[appsv1.ReplicaSet](ctx, col, gvkReplicaSet, t.Namespace, t.Name)
	if err != nil {
		return err
	}
	col.c.Target.UID = string(rs.UID)
	col.c.ReplicaSets = []*appsv1.ReplicaSet{rs}
	col.readOwnerDeployment(ctx, rs)
	sel := selectorString(rs.Spec.Selector)
	if sel == "" {
		col.c.addMissing("replicaset.selector_missing", rsRef(rs), nil)
		return nil
	}
	col.listPods(ctx, rs.Namespace, sel)
	return nil
}

func (col *collector) listReplicaSets(ctx context.Context, ns, selector string, keep func(*appsv1.ReplicaSet) bool) {
	res, err := col.r.List(ctx, gvkReplicaSet, ListOptions{Namespace: ns, LabelSelector: selector, Limit: maxListObjects})
	if err != nil {
		col.c.addMissing("source.unavailable", ResourceRef{Kind: "ReplicaSet", Namespace: ns}, map[string]string{"reason": errorReason(err)})
		return
	}
	if res.Truncated {
		col.c.addPartial("ReplicaSet")
	}
	var all []*appsv1.ReplicaSet
	for i := range res.Items {
		rs, err := decode[appsv1.ReplicaSet](&res.Items[i])
		if err != nil {
			col.c.addMissing("object.unreadable", ResourceRef{Kind: "ReplicaSet", Namespace: ns, Name: res.Items[i].GetName()}, nil)
			continue
		}
		if keep(rs) {
			all = append(all, rs)
		}
	}
	// Самые новые ReplicaSet важнее: именно они объясняют текущий rollout.
	slices.SortFunc(all, func(a, b *appsv1.ReplicaSet) int { return b.CreationTimestamp.Compare(a.CreationTimestamp.Time) })
	if len(all) > maxReplicaSets {
		all = all[:maxReplicaSets]
		col.c.addPartial("ReplicaSet")
	}
	col.c.ReplicaSets = all
	col.c.markObserved(ResourceRef{Kind: "ReplicaSet", Namespace: ns}, res.ObservedAt)
}

// listPods выбирает поды по селектору; оставляет принадлежащие известным ReplicaSet (если они есть в контексте).
func (col *collector) listPods(ctx context.Context, ns, selector string) {
	res, err := col.r.List(ctx, gvkPod, ListOptions{Namespace: ns, LabelSelector: selector, Limit: maxListObjects})
	if err != nil {
		col.c.addMissing("source.unavailable", ResourceRef{Kind: "Pod", Namespace: ns}, map[string]string{"reason": errorReason(err)})
		return
	}
	if res.Truncated {
		col.c.addPartial("Pod")
	}
	owners := map[string]bool{}
	for _, rs := range col.c.ReplicaSets {
		owners[rs.Name] = true
	}
	var pods []*corev1.Pod
	for i := range res.Items {
		p, err := decode[corev1.Pod](&res.Items[i])
		if err != nil {
			col.c.addMissing("object.unreadable", ResourceRef{Kind: "Pod", Namespace: ns, Name: res.Items[i].GetName()}, nil)
			continue
		}
		if o := controllerOf(p.OwnerReferences, "ReplicaSet"); len(owners) > 0 && (o == nil || !owners[o.Name]) {
			continue
		}
		pods = append(pods, p)
	}
	// Неготовые поды первыми: они и есть предмет диагностики.
	slices.SortStableFunc(pods, func(a, b *corev1.Pod) int {
		return boolRank(podReady(a)) - boolRank(podReady(b))
	})
	if len(pods) > maxPods {
		pods = pods[:maxPods]
		col.c.addPartial("Pod")
	}
	col.c.Pods = pods
	for _, p := range pods {
		col.c.markObserved(podRef(p), res.ObservedAt)
	}
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (col *collector) readNamespace(ctx context.Context) {
	col.c.Namespace = optional[corev1.Namespace](ctx, col, gvkNamespace, "", col.c.Target.Namespace)
}

// readNodes читает узлы неготовых подов: состояние узла объясняет Pending и вытеснение.
func (col *collector) readNodes(ctx context.Context) {
	var names []string
	for _, p := range col.c.Pods {
		if p.Spec.NodeName != "" && !podReady(p) && !slices.Contains(names, p.Spec.NodeName) {
			names = append(names, p.Spec.NodeName)
		}
	}
	if len(names) > maxNodes {
		names = names[:maxNodes]
		col.c.addPartial("Node")
	}
	for _, n := range names {
		if node := optional[corev1.Node](ctx, col, gvkNode, "", n); node != nil {
			col.c.Nodes[n] = node
		}
	}
}

// readEvents читает события namespace одним запросом и оставляет относящиеся к объектам контекста.
func (col *collector) readEvents(ctx context.Context) {
	c := col.c
	res, err := col.r.List(ctx, gvkEvent, ListOptions{Namespace: c.Target.Namespace, Limit: maxEvents})
	if err != nil {
		c.addMissing("events.unavailable", c.Target, map[string]string{"reason": errorReason(err)})
		return
	}
	if res.Truncated {
		c.addPartial("Event")
		c.addMissing("events.truncated", c.Target, nil)
	}
	known := map[string]bool{} // "Kind/name"
	for _, p := range c.Pods {
		known["Pod/"+p.Name] = true
	}
	for _, rs := range c.ReplicaSets {
		known["ReplicaSet/"+rs.Name] = true
	}
	if c.Deployment != nil {
		known["Deployment/"+c.Deployment.Name] = true
	}
	known[c.Target.Kind+"/"+c.Target.Name] = true
	for i := range res.Items {
		e, err := decode[corev1.Event](&res.Items[i])
		if err != nil {
			continue // нечитаемое событие — не повод ронять сбор
		}
		if ev := NormalizeEvent(e); known[ev.Object.Kind+"/"+ev.Object.Name] {
			c.Events = append(c.Events, ev)
		}
	}
	sortEvents(c.Events)
	for _, r := range c.eventRefs() {
		c.markObserved(r, res.ObservedAt)
	}
}

func (c *Context) eventRefs() []ResourceRef {
	seen := map[ResourceRef]bool{}
	var out []ResourceRef
	for _, e := range c.Events {
		r := ResourceRef{Kind: "Event", Namespace: e.Object.Namespace, Name: e.Reason}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// readLogs читает логи проблемных контейнеров. Сохранение (редакция, лимиты, право) — не здесь.
func (col *collector) readLogs(ctx context.Context) {
	if !col.opts.Logs {
		return
	}
	c := col.c
	n := 0
	for _, p := range c.Pods {
		for _, t := range logTargets(p) {
			if n >= maxLogTargets {
				c.addPartial("Log")
				return
			}
			n++
			raw, err := col.podLog(ctx, p, t)
			if err != nil {
				c.addMissing("logs.unavailable", podRef(p), map[string]string{
					"container": t.Container, "reason": errorReason(err)})
				continue
			}
			c.Logs = append(c.Logs, LogCapture{Pod: podRef(p), Container: t.Container, Previous: t.Previous, Raw: raw})
		}
	}
}

func (col *collector) podLog(ctx context.Context, p *corev1.Pod, t LogRef) ([]byte, error) {
	req := LogRequest{Namespace: p.Namespace, Pod: p.Name, Container: t.Container, Previous: t.Previous,
		TailLines: logTailLines, LimitBytes: logLimitBytes}
	raw, err := col.r.PodLogs(ctx, req)
	if errors.Is(err, ErrTooLarge) {
		req.TailLines = logTailRetry
		raw, err = col.r.PodLogs(ctx, req)
	}
	return raw, err
}

func controllerOf(refs []metav1.OwnerReference, kind string) *metav1.OwnerReference {
	for i := range refs {
		if refs[i].Kind == kind && (refs[i].Controller == nil || *refs[i].Controller) {
			return &refs[i]
		}
	}
	return nil
}

func selectorString(s *metav1.LabelSelector) string {
	if s == nil {
		return ""
	}
	sel, err := metav1.LabelSelectorAsSelector(s)
	if err != nil || sel.Empty() {
		return ""
	}
	return sel.String()
}
