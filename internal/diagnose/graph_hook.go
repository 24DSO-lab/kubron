// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"slices"
	"strings"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// Хук сбора 100 строит граф зависимостей цели (ADR-0031 §4), пост-хук 100 кладёт его слепок в отчёт. Порядок 100
// зарезервирован контрактом: граф строится раньше метрик (200), которым он может понадобиться.
//
// Граф — необязательный источник: любой его сбой (запрет RBAC, недоступность, пределы, неподдерживаемая цель)
// становится Partial и MissingContext, а не ошибкой расследования. Если граф не построился совсем, отчёт совпадает
// с прежним (без цепочек), кроме этих записей.
func init() {
	RegisterCollectHook(100, "graph", collectGraph)
	registerPostHook(100, "graph", reportGraph)
}

// graphWarnKinds — префиксы предупреждений depgraph о сбое чтения вида: «forbidden:Service» и т. п. Значение —
// код причины, как в errorReason.
var graphWarnKinds = map[string]string{"forbidden": "forbidden", "unavailable": "unavailable", "too_large": "too_large"}

func collectGraph(ctx context.Context, r Reader, c *Context) {
	var o depgraph.Options
	if c.opts.Graph != nil {
		o = *c.opts.Graph
	}
	g, err := depgraph.Build(ctx, r, toGraphRef(c.Target), o)
	if err != nil {
		// Единственная ошибка Build — неподдерживаемая цель: программная, а не сбой кластера.
		c.addMissing("graph.unavailable", c.Target, map[string]string{"reason": "unsupported"})
		return
	}
	c.Graph = g
	graphGaps(c, g)
	addGraphEvents(c, g)
}

// graphGaps переводит пробелы графа в Partial и MissingContext по правилам остального сбора: недоступный объект или
// вид — «source.unavailable» с причиной, усечённый граф или выборка — Partial.
func graphGaps(c *Context, g *depgraph.Graph) {
	for _, n := range g.Nodes() {
		if n.Health != depgraph.Unknown {
			continue
		}
		switch n.Unknown {
		case depgraph.UnknownForbidden, depgraph.UnknownUnavailable, depgraph.UnknownTooLarge:
			c.addMissing("source.unavailable", fromGraphRef(n.Ref), map[string]string{"reason": string(n.Unknown)})
		}
		// limit — пределы графа (их показывает Partial), policy — намеренный отказ читать Secret: не пробел чтения.
	}
	for _, w := range g.Warnings() {
		kind, arg, _ := strings.Cut(w, ":")
		switch {
		case kind == "list_truncated":
			c.addPartial(arg)
		case graphWarnKinds[kind] != "":
			reason := graphWarnKinds[kind]
			// Запись уровня вида лишняя, если тот же отказ уже записан по конкретному объекту этого вида.
			if !slices.ContainsFunc(c.Missing, func(m MissingContext) bool {
				return m.Code == "source.unavailable" && m.Resource.Kind == arg && m.Params["reason"] == reason
			}) {
				c.addMissing("source.unavailable", ResourceRef{Kind: arg}, map[string]string{"reason": reason})
			}
		case strings.HasPrefix(kind, "limit.") || kind == "canceled":
			c.addPartial("Graph")
		}
	}
	if g.Truncated() {
		c.addPartial("Graph")
	}
}

// addGraphEvents добавляет в контекст события объектов, которые нашёл граф (PVC, Service и др.): базовый сбор
// оставляет только события подов, владельцев и цели, а ProvisioningFailed и FailedMount лежат на PVC и Pod.
func addGraphEvents(c *Context, g *depgraph.Graph) {
	nodes := map[ResourceRef]bool{}
	for _, n := range g.Nodes() {
		if n.Placeholder {
			continue
		}
		nodes[ResourceRef{Kind: n.Kind, Namespace: n.Namespace, Name: n.Name}] = true
	}
	added := false
	for _, ev := range c.eventPool {
		ref := ResourceRef{Kind: ev.Object.Kind, Namespace: ev.Object.Namespace, Name: ev.Object.Name}
		if nodes[ref] && !slices.Contains(c.Events, ev) {
			c.Events = append(c.Events, ev)
			added = true
		}
	}
	if added {
		sortEvents(c.Events)
	}
}

// reportGraph кладёт слепок графа в отчёт. Слепок содержит только состояния и коды причин, без значений из кластера.
func reportGraph(b *builder) {
	if b.c.Graph == nil {
		return
	}
	s := b.c.Graph.Snapshot()
	b.rep.Graph = &s
}

// toGraphRef и fromGraphRef переводят ссылку между диагностикой и графом. Группа сохраняется: отчёты этапа 1 строят
// ссылки с группой (ADR-0031 §4); у прежних ссылок диагностики она пуста и однозначна по виду.
func toGraphRef(r ResourceRef) depgraph.Ref {
	return depgraph.Ref{Group: r.Group, Kind: r.Kind, Namespace: r.Namespace, Name: r.Name}
}

func fromGraphRef(r depgraph.Ref) ResourceRef {
	return ResourceRef{Group: r.Group, Kind: r.Kind, Namespace: r.Namespace, Name: r.Name}
}
