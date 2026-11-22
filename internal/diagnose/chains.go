// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"slices"
	"strings"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// Связи между находками цепочечных правил (ADR-0031 §5): пост-хук 100 проставляет Hypothesis.Explains и строит
// Report.Chains. Это связь «кандидат первопричины → то, что он объясняет», а не вердикт: подтвердить причину может
// только человек (ADR-0024 §3), поэтому ни поля root_cause, ни значения «установлено» здесь нет.
func init() {
	registerPostHook(100, "chains", linkChains)
}

// chainRuleIDs — правила, которые читают граф. Их гипотезы — кандидаты первопричины: для них строятся цепочки, а
// текстовые правила, на которые они указывают, теряют уверенность.
var chainRuleIDs = map[string]bool{}

// registerChainRule регистрирует цепочечное правило: обычное правило таблицы плюс пометка для пост-хука связей.
func registerChainRule(id string, epoch int, apply func(b *builder)) {
	registerRule(id, epoch, apply)
	chainRuleIDs[id] = true
}

func isChainRule(id string) bool { return chainRuleIDs[id] }

// maxChains — сколько цепочек кладётся в отчёт: цепочки нужны, чтобы показать «почему», а не перечислить граф.
const maxChains = 5

func linkChains(b *builder) {
	g := b.c.Graph
	if g == nil {
		return
	}
	linkExplains(b, g)
	lowerTextPath(b)
	buildChains(b, g)
}

// Какие гипотезы-следствия может объяснить гипотеза-причина данного класса. Остальные пары не связываются, даже если
// ресурсы зависят друг от друга в графе: «PVC не привязан» не объясняет, например, падение контейнера.
var (
	storageEffects = map[string]bool{"pending.unbound_pvc": true, "startup.volume_mount_failed": true}
	nodeEffects    = map[string]bool{"oom.node_memory_pressure": true}
)

// explainable — допустима ли связь «cause объясняет effect» по видам ресурсов и кодам гипотез.
func explainable(cause, effect Hypothesis) bool {
	switch {
	case strings.HasPrefix(cause.Code, "pvc.") || strings.HasPrefix(cause.Code, "storage."):
		return effect.Resource.Kind == "Pod" && storageEffects[effect.Code]
	case strings.HasPrefix(cause.Code, "node."):
		return effect.Resource.Kind == "Pod" && nodeEffects[effect.Code]
	case cause.Resource.Kind == "Pod":
		// Неготовый Pod объясняет отсутствие готовых endpoints у Service, который его выбирает (но не неверный
		// селектор или порт: их причина в самом Service).
		return effect.Resource.Kind == "Service" && effect.Code == "service.no_ready_endpoints"
	}
	return false
}

// graphLimited — граф ограничен пределами, усечением выборки или отменой: отсутствие объекта в нём ничего не доказывает.
// Сбой чтения вида (forbidden, unavailable) ограничением не считается: он записан как пробел (Missing), а данные
// остальных видов остаются верными.
func graphLimited(g *depgraph.Graph) bool {
	for _, w := range g.Warnings() {
		kind, _, _ := strings.Cut(w, ":")
		if kind == "list_truncated" || kind == "canceled" || strings.HasPrefix(kind, "limit.") {
			return true
		}
	}
	return false
}

func sameResource(a, b ResourceRef) bool {
	return a.Kind == b.Kind && a.Namespace == b.Namespace && a.Name == b.Name
}

// linkExplains: причина объясняет следствие, если пара допустима и следствие стоит в «области влияния» причины в графе
// (Dependents: потребители хранилища и узла, Service над Pod'ами).
func linkExplains(b *builder, g *depgraph.Graph) {
	hs := b.rep.Hypotheses
	affected := map[string]map[string]bool{}
	reach := func(r ResourceRef) map[string]bool {
		key := toGraphRef(r).Key()
		if set, ok := affected[key]; ok {
			return set
		}
		set := map[string]bool{}
		for _, n := range g.Dependents(toGraphRef(r)) {
			set[n.Key()] = true
		}
		affected[key] = set
		return set
	}
	for i := range hs {
		for j := range hs {
			cause, effect := hs[i], hs[j]
			if i == j || sameResource(cause.Resource, effect.Resource) || !explainable(cause, effect) {
				continue
			}
			if reach(cause.Resource)[toGraphRef(effect.Resource).Key()] {
				hs[i].Explains = append(hs[i].Explains, effect.ID)
			}
		}
	}
}

// lowerTextPath понижает уверенность текстового правила «PVC не привязан», когда граф уже дал цепочку: найденное по
// сообщению планировщика подтверждено или опровергнуто структурой, и шаг «проверить PVC» заменён фактами цепочки.
// Само правило pending.unbound_pvc не меняется (ADR-0031 §6): без графа оно работает как раньше.
func lowerTextPath(b *builder) {
	explained := map[string]bool{}
	for _, h := range b.rep.Hypotheses {
		if isChainRule(h.RuleID) {
			for _, id := range h.Explains {
				explained[id] = true
			}
		}
	}
	for i := range b.rep.Hypotheses {
		h := &b.rep.Hypotheses[i]
		if h.Code != "pending.unbound_pvc" || !explained[h.ID] {
			continue
		}
		h.Confidence = ConfidenceLow
		id := h.ID
		b.rep.Steps = slices.DeleteFunc(b.rep.Steps, func(s Step) bool {
			return s.Code == "step.check_pvc" && slices.Contains(s.Hypotheses, id)
		})
	}
}

// Типы рёбер, по которым ресурс зависит от другого (From зависит от To); владелец (owns) идёт в обратную сторону.
var dependencyEdges = []depgraph.EdgeType{
	depgraph.EdgeMounts, depgraph.EdgeBinds, depgraph.EdgeRunsOn, depgraph.EdgeSelects, depgraph.EdgeRoutes, depgraph.EdgeScales,
}

func dependencies(g *depgraph.Graph, cur depgraph.Ref) []depgraph.Ref {
	var out []depgraph.Ref
	for _, t := range dependencyEdges {
		for _, e := range g.EdgesFrom(cur, t) {
			out = append(out, e.To)
		}
	}
	for _, e := range g.EdgesTo(cur, depgraph.EdgeOwns) {
		out = append(out, e.From)
	}
	slices.SortFunc(out, func(a, b depgraph.Ref) int { return strings.Compare(a.Key(), b.Key()) })
	return out
}

// dependencyPath — кратчайший путь по зависимостям от from до to (оба включены); nil, если пути нет. Обход в ширину
// с порядком по Key(), поэтому результат детерминирован; циклы не зацикливают обход.
func dependencyPath(g *depgraph.Graph, from, to depgraph.Ref) []depgraph.Node {
	if from.Key() == to.Key() {
		return nil
	}
	prev := map[string]depgraph.Ref{}
	seen := map[string]bool{from.Key(): true}
	level := []depgraph.Ref{from}
	for len(level) > 0 {
		var next []depgraph.Ref
		for _, cur := range level {
			for _, n := range dependencies(g, cur) {
				if seen[n.Key()] {
					continue
				}
				seen[n.Key()] = true
				prev[n.Key()] = cur
				if n.Key() != to.Key() {
					next = append(next, n)
					continue
				}
				var path []depgraph.Node
				for at := n; ; at = prev[at.Key()] {
					node, ok := g.Node(at)
					if !ok {
						return nil
					}
					path = append(path, node)
					if at.Key() == from.Key() {
						break
					}
				}
				slices.Reverse(path)
				return path
			}
		}
		level = next
	}
	return nil
}

// extendToBroken продлевает путь за кандидата до сломанной зависимости: у PVC без StorageClass цепочка заканчивается
// не на PVC, а на отсутствующем классе (ребро не ok). Не более двух звеньев.
func extendToBroken(g *depgraph.Graph, path []depgraph.Node) []depgraph.Node {
	for range 2 {
		last := path[len(path)-1]
		var step *depgraph.Node
		for _, t := range dependencyEdges {
			for _, e := range g.EdgesFrom(last.Ref, t) {
				if n, ok := g.Node(e.To); ok && e.State != depgraph.EdgeOK && !slices.ContainsFunc(path, func(p depgraph.Node) bool { return p.Key() == n.Key() }) {
					step = &n
					break
				}
			}
			if step != nil {
				break
			}
		}
		if step == nil {
			break
		}
		path = append(path, *step)
	}
	return path
}

func chainSteps(path []depgraph.Node) []ChainStep {
	out := make([]ChainStep, 0, len(path))
	for _, n := range path {
		out = append(out, ChainStep{Ref: fromGraphRef(n.Ref), Health: string(n.Health), Reason: n.Reason})
	}
	return out
}

// affectedBy — ресурсы, страдающие от гипотезы h: то, что она объясняет, а для гипотезы
// цепочечного правила без объяснённых записей — неисправные Pod и Service из «области влияния» в графе.
func affectedBy(b *builder, g *depgraph.Graph, h Hypothesis) []depgraph.Ref {
	var out []depgraph.Ref
	for _, id := range h.Explains {
		if i := slices.IndexFunc(b.rep.Hypotheses, func(x Hypothesis) bool { return x.ID == id }); i >= 0 {
			out = append(out, toGraphRef(b.rep.Hypotheses[i].Resource))
		}
	}
	if len(out) > 0 || !isChainRule(h.RuleID) {
		return out
	}
	for _, n := range g.Dependents(toGraphRef(h.Resource)) {
		if (n.Kind == "Pod" || n.Kind == "Service") && (n.Health == depgraph.Degraded || n.Health == depgraph.Progressing) {
			out = append(out, n.Ref)
		}
	}
	return out
}

// buildChains строит Report.Chains: для каждого кандидата путь по графу от затронутого ресурса до ресурса кандидата
// (и дальше до сломанной зависимости). Один путь на пару «кандидат — затронутый ресурс», не больше maxChains.
func buildChains(b *builder, g *depgraph.Graph) {
	seen := map[string]bool{}
	var chains []Chain
	for _, h := range b.rep.Hypotheses {
		if !isChainRule(h.RuleID) && len(h.Explains) == 0 {
			continue
		}
		cause := toGraphRef(h.Resource)
		for _, from := range affectedBy(b, g, h) {
			key := h.ID + ">" + from.Key()
			if seen[key] {
				continue
			}
			seen[key] = true
			path := dependencyPath(g, from, cause)
			if len(path) < 2 {
				continue
			}
			chains = append(chains, Chain{Path: chainSteps(extendToBroken(g, path)), Candidate: h.ID})
			if len(chains) == maxChains {
				b.rep.Chains = chains
				return
			}
		}
	}
	b.rep.Chains = chains
}
