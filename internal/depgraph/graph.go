// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"cmp"
	"slices"
)

type edgeKey struct {
	from, to string
	typ      EdgeType
}

// Graph — снимок зависимостей на момент расследования. Не потокобезопасен: строится в одной горутине,
// после Build только читается.
type Graph struct {
	seed      Ref
	nodes     map[string]Node
	edges     map[edgeKey]Edge
	truncated bool
	warnings  map[string]struct{}
}

// New создаёт пустой граф с целью seed. Сама вершина-цель добавляется вызывающим (AddNode).
func New(seed Ref) *Graph {
	return &Graph{seed: seed, nodes: map[string]Node{}, edges: map[edgeKey]Edge{}, warnings: map[string]struct{}{}}
}

// Seed возвращает цель расследования.
func (g *Graph) Seed() Ref { return g.seed }

// AddNode добавляет вершину или заменяет существующую с тем же ключом. Заглушка (Placeholder) не затирает
// вершину, прочитанную из кластера.
func (g *Graph) AddNode(n Node) {
	k := n.Key()
	if old, ok := g.nodes[k]; ok && n.Placeholder && !old.Placeholder {
		return
	}
	g.nodes[k] = n
}

// AddEdge добавляет ребро; повтор (From, To, Type) заменяет предыдущее.
func (g *Graph) AddEdge(e Edge) {
	g.edges[edgeKey{e.From.Key(), e.To.Key(), e.Type}] = e
}

// SetTruncated помечает граф как усечённый предела́ми; code — стабильный код предупреждения (пустой — без записи).
func (g *Graph) SetTruncated(code string) {
	g.truncated = true
	g.AddWarning(code)
}

// AddWarning записывает предупреждение — короткий стабильный код без имён ресурсов и значений. Повторы схлопываются.
func (g *Graph) AddWarning(code string) {
	if code != "" {
		g.warnings[code] = struct{}{}
	}
}

// Node возвращает вершину по ссылке.
func (g *Graph) Node(r Ref) (Node, bool) {
	n, ok := g.nodes[r.Key()]
	return n, ok
}

// Nodes возвращает все вершины, отсортированные по Key().
func (g *Graph) Nodes() []Node {
	out := make([]Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		out = append(out, n)
	}
	slices.SortFunc(out, func(a, b Node) int { return cmp.Compare(a.Key(), b.Key()) })
	return out
}

func compareEdges(a, b Edge) int {
	if c := cmp.Compare(a.From.Key(), b.From.Key()); c != 0 {
		return c
	}
	if c := cmp.Compare(a.To.Key(), b.To.Key()); c != 0 {
		return c
	}
	return cmp.Compare(a.Type, b.Type)
}

// Edges возвращает все рёбра, отсортированные по (From, To, Type).
func (g *Graph) Edges() []Edge {
	out := make([]Edge, 0, len(g.edges))
	for _, e := range g.edges {
		out = append(out, e)
	}
	slices.SortFunc(out, compareEdges)
	return out
}

// EdgesFrom — рёбра, исходящие из r; t == "" — любого типа.
func (g *Graph) EdgesFrom(r Ref, t EdgeType) []Edge {
	k := r.Key()
	var out []Edge
	for ek, e := range g.edges {
		if ek.from == k && (t == "" || ek.typ == t) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, compareEdges)
	return out
}

// EdgesTo — рёбра, входящие в r; t == "" — любого типа.
func (g *Graph) EdgesTo(r Ref, t EdgeType) []Edge {
	k := r.Key()
	var out []Edge
	for ek, e := range g.edges {
		if ek.to == k && (t == "" || ek.typ == t) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, compareEdges)
	return out
}

// Truncated — граф усечён пределами (вершины, рёбра, глубина, число чтений) или списками, не уместившимися в Limit.
func (g *Graph) Truncated() bool { return g.truncated }

// Warnings возвращает предупреждения, отсортированные и без повторов.
func (g *Graph) Warnings() []string {
	out := make([]string, 0, len(g.warnings))
	for w := range g.warnings {
		out = append(out, w)
	}
	slices.Sort(out)
	return out
}

// walk — обход в ширину от r: next даёт соседей текущей вершины. Порядок результата: расстояние, затем Key().
// r в результат не входит; циклы не зацикливают обход.
func (g *Graph) walk(r Ref, next func(Ref) []Ref) []Node {
	seen := map[string]bool{r.Key(): true}
	level := []Ref{r}
	var out []Node
	for len(level) > 0 {
		var cand []Ref
		for _, cur := range level {
			for _, n := range next(cur) {
				if !seen[n.Key()] {
					seen[n.Key()] = true
					cand = append(cand, n)
				}
			}
		}
		slices.SortFunc(cand, func(a, b Ref) int { return cmp.Compare(a.Key(), b.Key()) })
		level = level[:0]
		for _, c := range cand {
			if n, ok := g.nodes[c.Key()]; ok {
				out = append(out, n)
				level = append(level, c)
			}
		}
	}
	return out
}

// Owners — владельцы r: вверх по рёбрам owns до корня, ближайший первым.
func (g *Graph) Owners(r Ref) []Node {
	return g.walk(r, func(cur Ref) []Ref {
		var out []Ref
		for _, e := range g.EdgesTo(cur, EdgeOwns) {
			out = append(out, e.From)
		}
		return out
	})
}

// Dependents — ресурсы, которые страдают при отказе r («область влияния»): потребители хранилища и узла
// (входящие mounts, binds, runs_on), Service над Pod'ами (входящие selects), Ingress над Service (routes), HPA над
// целью (scales) и всё, чем r владеет (исходящие owns). Обход транзитивный: Node → Pod'ы → Service → Ingress.
// Для Node результат — Pod'ы на ней; для Deployment — его ReplicaSet и Pod'ы.
func (g *Graph) Dependents(r Ref) []Node {
	return g.walk(r, func(cur Ref) []Ref {
		var out []Ref
		for _, t := range []EdgeType{EdgeMounts, EdgeBinds, EdgeRunsOn, EdgeSelects, EdgeRoutes, EdgeScales} {
			for _, e := range g.EdgesTo(cur, t) {
				out = append(out, e.From)
			}
		}
		for _, e := range g.EdgesFrom(cur, EdgeOwns) {
			out = append(out, e.To)
		}
		return out
	})
}
