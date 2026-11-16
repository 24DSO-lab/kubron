// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// Ref — идентификатор вершины. Key() = "group/kind/namespace/name"; у core-ресурсов группа в ключе — "core".
type Ref struct{ Group, Kind, Namespace, Name string }

// Key возвращает стабильный строковый идентификатор вершины.
func (r Ref) Key() string {
	g := r.Group
	if g == "" {
		g = "core"
	}
	return g + "/" + r.Kind + "/" + r.Namespace + "/" + r.Name
}

// Health — состояние вершины (словарь здоровья графа + Unknown).
type Health string

const (
	Healthy     Health = "Healthy"
	Degraded    Health = "Degraded"
	Progressing Health = "Progressing"
	Suspended   Health = "Suspended"
	Missing     Health = "Missing" // только при NotFound
	Unknown     Health = "Unknown" // ресурс не удалось прочитать; причина — в Node.Unknown
)

// UnknownReason — почему вершина Unknown.
type UnknownReason string

const (
	UnknownForbidden   UnknownReason = "forbidden"
	UnknownUnavailable UnknownReason = "unavailable"
	UnknownTooLarge    UnknownReason = "too_large"
	UnknownLimit       UnknownReason = "limit"
	UnknownPolicy      UnknownReason = "policy"
)

// EdgeType — тип связи (ADR-0031 §2).
type EdgeType string

const (
	EdgeOwns     EdgeType = "owns"
	EdgeSelects  EdgeType = "selects"
	EdgeBackedBy EdgeType = "backed_by"
	EdgeRoutes   EdgeType = "routes"
	EdgeMounts   EdgeType = "mounts"
	EdgeBinds    EdgeType = "binds"
	EdgeRunsOn   EdgeType = "runs_on"
	EdgeScales   EdgeType = "scales"
)

// EdgeState — состояние связи.
type EdgeState string

const (
	EdgeOK      EdgeState = "ok"
	EdgeBroken  EdgeState = "broken"  // обе стороны есть, но связь не работает
	EdgeMissing EdgeState = "missing" // цель ссылки не найдена (NotFound)
	EdgeUnknown EdgeState = "unknown" // прочитать не удалось
)

// Node — вершина графа.
type Node struct {
	Ref
	UID         string
	Health      Health
	Reason      string        // код состояния, без значений полей
	Unknown     UnknownReason // заполнено только при Health == Unknown
	Placeholder bool          // ссылка по имени на несуществующий объект
	// Object — объект только в памяти; в Snapshot не попадает. У ConfigMap data/binaryData уже отброшены.
	Object *unstructured.Unstructured `json:"-"`
}

// Edge — типизированная связь From → To.
type Edge struct {
	From, To Ref
	Type     EdgeType
	State    EdgeState
	Reason   string
}
