// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
)

func nestedSelector(svc *unstructured.Unstructured) (map[string]string, bool, error) {
	m, found, err := unstructured.NestedStringMap(svc.Object, "spec", "selector")
	return m, found, err
}

// expandService: Service → EndpointSlice (backed_by); для цели — ещё и Pod'ы по селектору (selects).
func (b *builder) expandService(ctx context.Context, it item, n Node) {
	sel, _, _ := nestedSelector(n.Object)
	extName := func() bool {
		t, _, _ := unstructured.NestedString(n.Object.Object, "spec", "type")
		return t == "ExternalName"
	}()
	if len(sel) == 0 || extName {
		return // нет селектора (ручные endpoints) или ExternalName: проверять нечего
	}
	d := it.depth + 1
	matched := 0
	if it.down && b.reach(d) {
		res, err := b.list(ctx, specPod, ListOptions{Namespace: n.Namespace, LabelSelector: labels.SelectorFromSet(sel).String()})
		if err == nil {
			for _, p := range sortedItems(res.Items) {
				pn, ok := b.addObject(specPod, &p)
				if !ok {
					break
				}
				matched++
				b.edge(n.Ref, pn.Ref, EdgeSelects)
				if b.takePod() {
					b.enqueue(pn.Ref, d, true)
				}
			}
			if matched == 0 && !res.Truncated {
				b.setHealth(n.Ref, Degraded, ReasonSelectorMatchesNothing)
				return
			}
		}
	}
	if !b.reach(d) {
		return
	}
	res, err := b.list(ctx, specSlice, ListOptions{Namespace: n.Namespace, LabelSelector: "kubernetes.io/service-name=" + n.Name, Limit: maxSlices})
	if err != nil {
		return
	}
	ready := false
	for i, s := range sortedItems(res.Items) {
		sn, ok := b.addObject(specSlice, &s)
		if !ok || i >= maxSlices {
			break
		}
		b.edge(n.Ref, sn.Ref, EdgeBackedBy)
		ready = ready || sn.Health == Healthy
	}
	if !ready && !res.Truncated {
		reason := ReasonNoEndpoints
		if len(res.Items) > 0 {
			reason = ReasonNoReadyEndpoints
		}
		if cur := b.g.nodes[n.Key()]; cur.Reason != ReasonSelectorMatchesNothing {
			b.setHealth(n.Ref, Degraded, reason)
		}
	}
}

// expandNode: Node → Pod'ы на ней (leaf-вершины, без дальнейшего раскрытия).
func (b *builder) expandNode(ctx context.Context, it item, n Node) {
	d := it.depth + 1
	if !b.reach(d) {
		return
	}
	res, err := b.list(ctx, specPod, ListOptions{FieldSelector: "spec.nodeName=" + n.Name})
	if err != nil {
		return
	}
	for _, p := range sortedItems(res.Items) {
		pn, ok := b.addObject(specPod, &p)
		if !ok {
			return
		}
		b.edge(pn.Ref, n.Ref, EdgeRunsOn)
	}
}
