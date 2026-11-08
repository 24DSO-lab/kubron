// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
)

// Граф цепочки «источник → Kustomization/HelmRelease → ресурсы» (ADR-0023, раздел 7). Ресурсы и связи между ними
// (owns, selects, routes) строит общий построитель graphbuild.go по записям status.inventory; здесь добавляются
// вершины и рёбра Flux: sourceRef, dependsOn и inventory.

const (
	// fluxGraphTimeout — предел построения графа (как у графа Argo).
	fluxGraphTimeout = 30 * time.Second
	// fluxGraphMaxFlux — предел вершин Flux (сам объект, источник и цепочка dependsOn); сверх него цепочка обрезается.
	fluxGraphMaxFlux = 50
)

// fluxGraphVersions — источник обслуживаемых версий API; переменная нужна тестам.
var fluxGraphVersions = fluxServedVersions

// GetFluxGraph строит граф цепочки «источник → объект → ресурсы» для Kustomization или HelmRelease.
//
// Для HelmRelease без status.inventory (Flux до 2.8) граф недоступен: Available=false и пустые вершины. Если у объекта
// задан spec.kubeConfig, ресурсы лежат в другом кластере и не разрешаются локально: Remote=true, в графе только вершины
// Flux. Недоступное по правам или несуществующее (источник, зависимость, pods) превращается в Warnings, а не в ошибку.
func (m *Manager) GetFluxGraph(ref FluxRef) (*FluxGraph, error) {
	return m.getFluxGraph(ref, nil)
}

// getFluxGraph — тело GetFluxGraph; kube == nil означает клиент Kubernetes активного подключения (тесты подставляют fake).
func (m *Manager) getFluxGraph(ref FluxRef, kube kubernetes.Interface) (*FluxGraph, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ref = FluxRef{Kind: ref.Kind, Namespace: strings.TrimSpace(ref.Namespace), Name: strings.TrimSpace(ref.Name)}
	if len(ref.Namespace) > fluxMaxActionObjects || len(ref.Name) > fluxMaxActionObjects {
		return nil, errFluxInvalidRef
	}
	// Граф строится только для объектов, у которых есть inventory и цепочка: Kustomization и HelmRelease.
	if _, err := checkFluxRef(ref, fluxActReadOnly); err != nil {
		return nil, fmt.Errorf("%w: %s", err, ref.Kind)
	}
	if ref.Kind != FluxKindKustomization && ref.Kind != FluxKindHelmRelease {
		return nil, fmt.Errorf("%w: %s", errFluxKindUnsupported, ref.Kind)
	}
	versions, err := fluxGraphVersions(conn)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), fluxGraphTimeout)
	defer cancel()
	if kube == nil && conn.Client != nil { // типизированный nil в интерфейсе не годится: проверяем указатель
		kube = conn.Client
	}
	return buildFluxGraph(ctx, conn, kube, versions, ref)
}

// fluxGraphBuilder накапливает вершины и рёбра Flux.
type fluxGraphBuilder struct {
	ctx      context.Context
	conn     *ClusterConnection
	kube     kubernetes.Interface // nil — клиента нет, ресурсы не разрешаются
	versions map[string]string

	nodes     map[string]FluxGraphNode
	edges     map[FluxGraphEdge]struct{}
	truncated bool
	warnings  []string
}

func (b *fluxGraphBuilder) warn(format string, args ...any) {
	b.warnings = append(b.warnings, fmt.Sprintf(format, args...))
}

func (b *fluxGraphBuilder) link(from, to, typ string) {
	if from != to {
		b.edges[FluxGraphEdge{From: from, To: to, Type: typ}] = struct{}{}
	}
}

// read читает объект Flux. Нет версии вида в кластере, нет прав или объекта — nil и предупреждение.
func (b *fluxGraphBuilder) read(kind fluxKind, ns, name string) (*unstructured.Unstructured, string) {
	version := b.versions[kind.Kind]
	if version == "" || !kind.supports(version) {
		b.warn("%s %s/%s is not available: the API version is not served or not supported", kind.Kind, ns, name)
		return nil, version
	}
	obj, err := b.conn.DynamicClient.Resource(kind.GVR(version)).Namespace(ns).Get(b.ctx, name, metav1.GetOptions{})
	switch {
	case err == nil:
		return obj, version
	case apierrors.IsForbidden(err):
		b.warn("no access to %s %s/%s", kind.Kind, ns, name)
	case apierrors.IsNotFound(err):
		b.warn("%s %s/%s was not found", kind.Kind, ns, name)
	default:
		b.warn("failed to read %s %s/%s: %v", kind.Kind, ns, name, err)
	}
	return nil, version
}

// add кладёт вершину Flux. obj == nil — объект не прочитан: вершина остаётся без состояния.
func (b *fluxGraphBuilder) add(kind fluxKind, version, ns, name, role string, obj *unstructured.Unstructured) string {
	n := FluxGraphNode{
		ID: graphID(kind.Group, kind.Kind, ns, name), Kind: kind.Kind, Group: kind.Group, Version: version,
		Namespace: ns, Name: name, Role: role,
	}
	if obj != nil {
		n.State = string(FluxStateOf(fluxConditions(obj.Object), boolAt(obj.Object, "spec", "suspend")))
	}
	if _, ok := b.nodes[n.ID]; !ok {
		b.nodes[n.ID] = n
	}
	return n.ID
}

// fluxGraphSourceOf находит источник объекта (spec.sourceRef у Kustomization, spec.chart.spec.sourceRef или
// spec.chartRef у HelmRelease). Вид должен быть источником из таблицы; namespace по умолчанию — namespace объекта.
func fluxGraphSourceOf(kind string, obj *unstructured.Unstructured) (fluxKind, FluxRef, bool) {
	var raw map[string]any
	switch kind {
	case FluxKindKustomization:
		raw, _ = mapAt(obj.Object, "spec", "sourceRef")
	case FluxKindHelmRelease:
		raw, _ = mapAt(obj.Object, "spec", "chart", "spec", "sourceRef")
		if raw == nil {
			raw, _ = mapAt(obj.Object, "spec", "chartRef")
		}
	}
	k, ok := fluxKindByName(strAt(raw, "kind"))
	name := strAt(raw, "name")
	if !ok || k.Controller != fluxCtrlSource || name == "" {
		return fluxKind{}, FluxRef{}, false
	}
	ns := cmp.Or(strAt(raw, "namespace"), obj.GetNamespace())
	return k, FluxRef{Kind: k.Kind, Namespace: ns, Name: name}, true
}

func buildFluxGraph(ctx context.Context, conn *ClusterConnection, kube kubernetes.Interface, versions map[string]string, ref FluxRef) (*FluxGraph, error) {
	kind, _ := fluxKindByName(ref.Kind)
	version := versions[kind.Kind]
	if version == "" {
		return nil, fmt.Errorf("%w: %s", errFluxNotInstalled, kind.Kind)
	}
	if !kind.supports(version) {
		return nil, fmt.Errorf("%w: %s", errFluxVersionUnsupported, kind.Kind)
	}
	obj, err := conn.DynamicClient.Resource(kind.GVR(version)).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	g := &FluxGraph{Nodes: []FluxGraphNode{}, Edges: []FluxGraphEdge{}, Warnings: []string{}}
	entries, hasInventory, skipped := fluxInventory(obj.Object)
	if kind.Kind == FluxKindHelmRelease && !hasInventory {
		return g, nil // Flux до 2.8: ресурсов нет, граф недоступен
	}
	g.Available = true
	kubeConfig, _ := mapAt(obj.Object, "spec", "kubeConfig")
	g.Remote = len(kubeConfig) > 0

	b := &fluxGraphBuilder{
		ctx: ctx, conn: conn, kube: kube, versions: versions,
		nodes: map[string]FluxGraphNode{}, edges: map[FluxGraphEdge]struct{}{}, warnings: []string{},
	}
	rootID := b.add(kind, version, ref.Namespace, ref.Name, FluxRoleFlux, obj)

	// Источник: источник → объект.
	if sk, sref, ok := fluxGraphSourceOf(kind.Kind, obj); ok {
		if sobj, sver := b.read(sk, sref.Namespace, sref.Name); sver != "" {
			b.link(b.add(sk, sver, sref.Namespace, sref.Name, FluxRoleSource, sobj), rootID, FluxEdgeSourceRef)
		}
	}

	b.addDependsOn(kind, version, rootID, obj)

	if skipped > 0 {
		b.warn("%d inventory entries were skipped: unknown format", skipped)
	}
	if g.Remote {
		if len(entries) > 0 {
			b.warn("the resources are in another cluster (spec.kubeConfig) and are not resolved")
		}
		entries = nil
	}
	b.addResources(rootID, entries)

	g.Truncated = b.truncated
	g.Warnings = b.warnings
	for _, n := range b.nodes {
		g.Nodes = append(g.Nodes, n)
	}
	slices.SortFunc(g.Nodes, func(a, c FluxGraphNode) int { return cmp.Compare(a.ID, c.ID) })
	for e := range b.edges {
		if _, ok := b.nodes[e.From]; !ok {
			continue
		}
		if _, ok := b.nodes[e.To]; !ok {
			continue
		}
		g.Edges = append(g.Edges, e)
	}
	slices.SortFunc(g.Edges, func(a, c FluxGraphEdge) int {
		return cmp.Or(cmp.Compare(a.From, c.From), cmp.Compare(a.To, c.To), cmp.Compare(a.Type, c.Type))
	})
	return g, nil
}

// addDependsOn обходит spec.dependsOn вглубь: зависимость → зависимый объект. Зависимости того же вида, что и сам
// объект (Kustomization зависит от Kustomization). Обход безопасен для циклов: каждая вершина читается один раз.
func (b *fluxGraphBuilder) addDependsOn(kind fluxKind, version, rootID string, root *unstructured.Unstructured) {
	type item struct {
		id  string
		obj *unstructured.Unstructured
	}
	queue := []item{{rootID, root}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		deps, _ := sliceAt(cur.obj.Object, "spec", "dependsOn")
		for _, d := range deps {
			dm, ok := d.(map[string]any)
			if !ok {
				continue
			}
			name := strAt(dm, "name")
			if name == "" {
				continue
			}
			ns := cmp.Or(strAt(dm, "namespace"), cur.obj.GetNamespace())
			id := graphID(kind.Group, kind.Kind, ns, name)
			if _, seen := b.nodes[id]; seen {
				b.link(id, cur.id, FluxEdgeDependsOn)
				continue
			}
			if len(b.nodes) >= fluxGraphMaxFlux {
				b.truncated = true
				continue
			}
			dobj, _ := b.read(kind, ns, name)
			b.add(kind, version, ns, name, FluxRoleFlux, dobj)
			b.link(id, cur.id, FluxEdgeDependsOn)
			if dobj != nil {
				queue = append(queue, item{id, dobj})
			}
		}
	}
}

// addResources достраивает ресурсы из inventory: рёбра «объект → ресурс» и связи общего построителя. Вершина Flux с
// тем же id выигрывает у вершины ресурса (inventory Kustomization часто содержит другие Kustomization).
func (b *fluxGraphBuilder) addResources(rootID string, entries []fluxInventoryEntry) {
	if len(entries) == 0 {
		return
	}
	if b.kube == nil {
		b.warn("the Kubernetes client is not available: resources were not resolved")
		return
	}
	budget := graphMaxNodes - len(b.nodes)
	if budget <= 0 {
		b.truncated = true
		return
	}
	if len(entries) > budget {
		entries, b.truncated = entries[:budget], true
	}
	refs := make([]graphRef, len(entries))
	for i, e := range entries {
		refs[i] = graphRef{Group: e.Group, Version: e.Version, Kind: e.Kind, Namespace: e.Namespace, Name: e.Name}
	}
	rg := buildResourceGraph(b.ctx, b.kube, refs)
	b.truncated = b.truncated || rg.Truncated
	b.warnings = append(b.warnings, rg.Warnings...)

	// Сначала ресурсы из inventory, затем найденное по связям: при нехватке места обрезается второе.
	for _, managed := range []bool{true, false} {
		for _, n := range rg.Nodes {
			if n.Managed != managed {
				continue
			}
			if _, exists := b.nodes[n.ID]; !exists {
				if len(b.nodes) >= graphMaxNodes {
					b.truncated = true
					continue
				}
				b.nodes[n.ID] = FluxGraphNode{
					ID: n.ID, Kind: n.Kind, Group: n.Group, Version: n.Version, Namespace: n.Namespace, Name: n.Name,
					Role: FluxRoleResource, State: n.Health,
				}
			}
			if managed {
				b.link(rootID, n.ID, FluxEdgeInventory)
			}
		}
	}
	for _, e := range rg.Edges {
		b.link(e.From, e.To, e.Type)
	}
}
