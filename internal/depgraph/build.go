// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Limits — верхние границы построения. В коде, не в настройках; меняются только новым ADR (ADR-0031 §3).
type Limits struct{ MaxNodes, MaxEdges, MaxDepth, MaxReads int }

// DefaultLimits — стартовые значения ADR-0031 §3: 200 вершин, 600 рёбер, глубина 4, 40 запросов чтения.
var DefaultLimits = Limits{MaxNodes: 200, MaxEdges: 600, MaxDepth: 4, MaxReads: 40}

// Options — параметры построения. Виды этапов 2–3 подключаются реестром (kind_registry.go), а не флагами;
// ExpandConfigMapRefs остаётся до переноса ссылок на ConfigMap в реестр.
type Options struct {
	Limits              Limits
	ExpandConfigMapRefs bool
}

// ErrUnsupportedSeed — программная ошибка: вид цели не поддерживается графом или цель задана неполно.
var ErrUnsupportedSeed = errors.New("depgraph: unsupported or incomplete seed")

var errReadLimit = errors.New("depgraph: read limit reached")

// Решения реализации, не меняющие пределы ADR-0031 §3.
const (
	listLimit       = 100 // размер одной выборки (Pod'ы, PVC, Service, RS)
	maxExpandedPods = 5   // сколько найденных списком Pod'ов раскрывается дальше (владельцы, PVC, Node, Service)
	maxServices     = 10  // сколько Service над одним Pod'ом попадает в граф
	maxSlices       = 10  // сколько EndpointSlice на Service
)

// Коды предупреждений: «limit.<что>», «list_truncated:<Kind>», «forbidden|unavailable|too_large:<Kind>», «canceled».
const (
	warnNodes    = "limit.nodes"
	warnEdges    = "limit.edges"
	warnDepth    = "limit.depth"
	warnReads    = "limit.reads"
	warnCanceled = "canceled"
)

type item struct {
	ref   Ref
	depth int
	down  bool // искать и «вниз» (детей, потребителей); false — только вверх по владельцам и зависимостям
}

type listEntry struct {
	res *ListResult
	err error
}

type builder struct {
	r      Reader
	o      Options
	g      *Graph
	reads  int
	queue  []item
	queued map[string]int
	gets   map[string]error // кэш неудачных Get
	lists  map[string]listEntry
	pods   int // раскрыто Pod'ов из списков
}

// Build строит граф от цели seed. Ошибка — только программная (неподдерживаемая цель); недоступность, запрет и
// пределы дают Unknown, Truncated и Warnings. Отмена ctx возвращает частичный граф с предупреждением «canceled».
func Build(ctx context.Context, r Reader, seed Ref, o Options) (*Graph, error) {
	if o.Limits == (Limits{}) {
		o.Limits = DefaultLimits
	}
	spec, ok := specFor(seed.Group, seed.Kind)
	if r == nil || !ok || !seedable(spec) || seed.Name == "" || (spec.namespaced && seed.Namespace == "") {
		return nil, fmt.Errorf("%w: %s/%s", ErrUnsupportedSeed, seed.Group, seed.Kind)
	}
	ref := spec.ref(seed.Namespace, seed.Name)
	b := &builder{
		r: r, o: o, g: New(ref),
		queued: map[string]int{}, gets: map[string]error{}, lists: map[string]listEntry{},
	}
	b.get(ctx, spec, ref.Namespace, ref.Name)
	b.enqueue(ref, 0, true)
	for len(b.queue) > 0 {
		if ctx.Err() != nil {
			b.g.SetTruncated(warnCanceled)
			break
		}
		it := b.queue[0]
		b.queue = b.queue[1:]
		b.expand(ctx, it)
	}
	b.finalize()
	return b.g, nil
}

func (b *builder) enqueue(ref Ref, depth int, down bool) {
	level := 1
	if down {
		level = 2
	}
	k := ref.Key()
	if b.queued[k] >= level {
		return
	}
	b.queued[k] = level
	b.queue = append(b.queue, item{ref, depth, down})
}

// reach проверяет, что вершина на расстоянии depth ещё в пределах глубины; иначе помечает граф усечённым.
func (b *builder) reach(depth int) bool {
	if depth > b.o.Limits.MaxDepth {
		b.g.SetTruncated(warnDepth)
		return false
	}
	return true
}

func (b *builder) read(ctx context.Context) bool {
	if ctx.Err() != nil {
		b.g.SetTruncated(warnCanceled)
		return false
	}
	if b.reads >= b.o.Limits.MaxReads {
		b.g.SetTruncated(warnReads)
		return false
	}
	b.reads++
	return true
}

func (b *builder) putNode(n Node) bool {
	if _, exists := b.g.nodes[n.Key()]; !exists && len(b.g.nodes) >= b.o.Limits.MaxNodes {
		b.g.SetTruncated(warnNodes)
		return false
	}
	b.g.AddNode(n)
	return true
}

// edge добавляет ребро; состояние уточняет finalize. Обе вершины должны быть в графе.
func (b *builder) edge(from, to Ref, t EdgeType) {
	if _, ok := b.g.nodes[from.Key()]; !ok {
		return
	}
	if _, ok := b.g.nodes[to.Key()]; !ok {
		return
	}
	k := edgeKey{from.Key(), to.Key(), t}
	if _, exists := b.g.edges[k]; !exists && len(b.g.edges) >= b.o.Limits.MaxEdges {
		b.g.SetTruncated(warnEdges)
		return
	}
	b.g.AddEdge(Edge{From: from, To: to, Type: t, State: EdgeOK})
}

func (b *builder) setHealth(ref Ref, h Health, reason string) {
	if n, ok := b.g.nodes[ref.Key()]; ok {
		n.Health, n.Reason = h, reason
		b.g.nodes[ref.Key()] = n
	}
}

// addObject кладёт прочитанный объект в граф. Возвращает false, если вершина не поместилась в предел.
func (b *builder) addObject(spec kindSpec, obj *unstructured.Unstructured) (Node, bool) {
	if obj.GetKind() == "" {
		obj.SetGroupVersionKind(spec.gvk)
	}
	ref := spec.ref(obj.GetNamespace(), obj.GetName())
	if old, ok := b.g.nodes[ref.Key()]; ok && old.Object != nil {
		return old, true // уже в графе: не затираем уточнённое здоровье
	}
	SanitizeConfigMap(obj)
	h, reason := HealthOf(obj)
	n := Node{Ref: ref, UID: string(obj.GetUID()), Health: h, Reason: reason, Object: obj}
	if h == Unknown {
		n.Unknown = UnknownUnavailable
	}
	return n, b.putNode(n)
}

func (b *builder) failureNode(ref Ref, err error) Node {
	n := Node{Ref: ref}
	switch {
	case errors.Is(err, ErrNotFound):
		n.Health, n.Placeholder, n.Reason = Missing, true, "not_found"
	case errors.Is(err, ErrForbidden):
		n.Health, n.Unknown = Unknown, UnknownForbidden
	case errors.Is(err, ErrTooLarge):
		n.Health, n.Unknown = Unknown, UnknownTooLarge
	case errors.Is(err, errReadLimit):
		n.Health, n.Unknown = Unknown, UnknownLimit
	default:
		n.Health, n.Unknown = Unknown, UnknownUnavailable
	}
	return n
}

func (b *builder) noteFailure(kind string, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		b.g.AddWarning("forbidden:" + kind)
	case errors.Is(err, ErrTooLarge):
		b.g.AddWarning("too_large:" + kind)
	case errors.Is(err, ErrNotFound), errors.Is(err, errReadLimit):
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		b.g.SetTruncated(warnCanceled)
	default:
		b.g.AddWarning("unavailable:" + kind)
	}
}

// get читает один объект (через кэш графа и кэш отказов) и гарантирует вершину в графе: настоящую, заглушку
// Missing или Unknown. Второй результат — объект, если он прочитан.
func (b *builder) get(ctx context.Context, spec kindSpec, ns, name string) (Node, *unstructured.Unstructured) {
	ref := spec.ref(ns, name)
	if n, ok := b.g.nodes[ref.Key()]; ok && n.Object != nil {
		return n, n.Object
	}
	if err, bad := b.gets[ref.Key()]; bad {
		n := b.failureNode(ref, err)
		b.putNode(n)
		return n, nil
	}
	var obj *unstructured.Unstructured
	var err error
	if b.read(ctx) {
		obj, err = b.r.Get(ctx, spec.gvk, spec.ref(ns, name).Namespace, name)
	} else if ctx.Err() != nil {
		err = ctx.Err()
	} else {
		err = errReadLimit
	}
	if err != nil || obj == nil {
		if err == nil {
			err = ErrNotFound
		}
		b.gets[ref.Key()] = err
		b.noteFailure(spec.gvk.Kind, err)
		n := b.failureNode(ref, err)
		b.putNode(n)
		return n, nil
	}
	n, _ := b.addObject(spec, obj)
	return n, obj
}

// list выполняет List через кэш; отказ или усечение отражается в предупреждениях и Truncated.
func (b *builder) list(ctx context.Context, spec kindSpec, o ListOptions) (*ListResult, error) {
	key := spec.gvk.Kind + "|" + o.Namespace + "|" + o.LabelSelector + "|" + o.FieldSelector
	if e, ok := b.lists[key]; ok {
		return e.res, e.err
	}
	if o.Limit == 0 {
		o.Limit = listLimit
	}
	var res *ListResult
	var err error
	if b.read(ctx) {
		res, err = b.r.List(ctx, spec.gvk, o)
	} else if ctx.Err() != nil {
		err = ctx.Err()
	} else {
		err = errReadLimit
	}
	switch {
	case err != nil:
		res = nil
		b.noteFailure(spec.gvk.Kind, err)
		b.g.SetTruncated("")
	case res == nil:
		res = &ListResult{}
	case res.Truncated:
		b.g.SetTruncated("list_truncated:" + spec.gvk.Kind)
	}
	b.lists[key] = listEntry{res, err}
	return res, err
}

func (b *builder) takePod() bool {
	if b.pods >= maxExpandedPods {
		return false
	}
	b.pods++
	return true
}

// finalize вычисляет состояния рёбер по итоговому здоровью вершин.
func (b *builder) finalize() {
	for k, e := range b.g.edges {
		e.State, e.Reason = edgeState(b.g, e)
		b.g.edges[k] = e
	}
}

func edgeState(g *Graph, e Edge) (EdgeState, string) {
	tgt := e.To
	if e.Type == EdgeOwns { // владелец — «цель ссылки» ownerReference
		tgt = e.From
	}
	n, ok := g.nodes[tgt.Key()]
	if !ok {
		return EdgeUnknown, string(UnknownLimit)
	}
	switch {
	case n.Placeholder || n.Health == Missing:
		return EdgeMissing, "not_found"
	case n.Health == Unknown:
		return EdgeUnknown, string(n.Unknown)
	}
	switch e.Type {
	case EdgeOwns:
	case EdgeRunsOn:
		if n.Health == Degraded {
			return EdgeBroken, n.Reason
		}
	case EdgeSelects:
		if n.Health != Healthy && n.Health != Suspended {
			return EdgeBroken, nonEmpty(n.Reason, ReasonNotReady)
		}
	default:
		if n.Health != Healthy {
			return EdgeBroken, nonEmpty(n.Reason, string(n.Health))
		}
	}
	return EdgeOK, e.Reason
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
