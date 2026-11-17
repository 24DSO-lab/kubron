// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package depgraph — ВРЕМЕННАЯ заглушка контракта internal/depgraph (docs/prompts/contracts.md §1).
//
// Существует только пока скелет D2 не слит в refactor: ядро diagnose (D3) не должно ждать его, чтобы выдать
// свои контракты. Здесь лишь то, что нужно скелету: типы чтения, ошибки, Ref, Snapshot, Limits/Options и
// непрозрачный Graph. Построения графа (Build) и обхода здесь НЕТ — их реализует D2, и до слияния цепочечные
// правила (Фаза 1) не пишутся.
//
// Замена на настоящий пакет — механическая: в reader.go, types.go, context.go и collect.go заменить путь импорта
// «internal/diagnose/stubs/depgraph» на «internal/depgraph» и удалить этот каталог. Имена и формы типов
// скопированы из контракта, поэтому остальной код менять не нужно.
package depgraph

import (
	"context"
	"errors"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Ошибки чтения. Тексты — те же, что в diagnose до введения псевдонимов: шаблоны уже есть в backend.json.
var (
	ErrNotFound    = errors.New("resource not found")
	ErrForbidden   = errors.New("access to the resource is forbidden")
	ErrUnavailable = errors.New("cluster source is unavailable")
	ErrTooLarge    = errors.New("response is too large")
)

// ListOptions — параметры выборки (подмножество платформенных).
type ListOptions struct {
	Namespace     string
	LabelSelector string
	FieldSelector string
	Limit         int64
}

// ListResult — результат List. Truncated=true — объектов больше, чем вернулось.
type ListResult struct {
	Items      []unstructured.Unstructured
	Truncated  bool
	ObservedAt time.Time
}

// Reader — read-only чтение кластера (Get и List).
type Reader interface {
	Get(ctx context.Context, gvk schema.GroupVersionKind, ns, name string) (*unstructured.Unstructured, error)
	List(ctx context.Context, gvk schema.GroupVersionKind, o ListOptions) (*ListResult, error)
}

// Ref — идентификатор вершины графа.
type Ref struct{ Group, Kind, Namespace, Name string }

// Key — «group/kind/namespace/name»; пустая группа записывается как «core».
func (r Ref) Key() string {
	g := r.Group
	if g == "" {
		g = "core"
	}
	return g + "/" + r.Kind + "/" + r.Namespace + "/" + r.Name
}

// SnapshotNode — вершина в отчёте (контракт §1: все поля, кроме kind, name и health, omitempty).
type SnapshotNode struct {
	Group       string `json:"group,omitempty"`
	Kind        string `json:"kind"`
	Namespace   string `json:"namespace,omitempty"`
	Name        string `json:"name"`
	Health      string `json:"health"`
	Reason      string `json:"reason,omitempty"`
	Unknown     string `json:"unknown,omitempty"`
	Placeholder bool   `json:"placeholder,omitempty"`
}

// SnapshotEdge — связь в отчёте; From и To — Ref.Key().
type SnapshotEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Type   string `json:"type"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// Snapshot — сериализуемый слепок графа для Report.Graph.
type Snapshot struct {
	Nodes     []SnapshotNode `json:"nodes"`
	Edges     []SnapshotEdge `json:"edges"`
	Truncated bool           `json:"truncated,omitempty"`
	Warnings  []string       `json:"warnings,omitempty"`
}

// Limits — пределы построения графа (ADR-0031 §3).
type Limits struct{ MaxNodes, MaxEdges, MaxDepth, MaxReads int }

// DefaultLimits — пределы по умолчанию.
var DefaultLimits = Limits{MaxNodes: 200, MaxEdges: 600, MaxDepth: 4, MaxReads: 40}

// Options — параметры построения. Этап 1: все флаги расширения выключены.
type Options struct {
	Limits                                        Limits
	ExpandIngress, ExpandHPA, ExpandConfigMapRefs bool
}

// Graph — граф зависимостей. В заглушке непрозрачен: обход и построение — у настоящего пакета (D2).
type Graph struct {
	seed      Ref
	truncated bool
}

// New создаёт пустой граф с заданной начальной вершиной.
func New(seed Ref) *Graph { return &Graph{seed: seed} }

// Seed — начальная вершина.
func (g *Graph) Seed() Ref { return g.seed }

// Truncated — граф усечён пределами.
func (g *Graph) Truncated() bool { return g != nil && g.truncated }

// Snapshot — слепок для отчёта; у пустого графа списки не nil.
func (g *Graph) Snapshot() Snapshot {
	return Snapshot{Nodes: []SnapshotNode{}, Edges: []SnapshotEdge{}, Truncated: g.Truncated()}
}
