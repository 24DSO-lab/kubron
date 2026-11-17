// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"context"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (b *builder) expand(ctx context.Context, it item) {
	n, ok := b.g.nodes[it.ref.Key()]
	if !ok || n.Object == nil {
		return
	}
	switch it.ref.Kind {
	case "Pod":
		b.expandPod(ctx, it, n)
	case "ReplicaSet":
		b.expandOwners(ctx, it, n)
		if it.down {
			b.expandReplicaSet(ctx, it, n)
		}
	case "Deployment":
		if it.down {
			b.expandDeployment(ctx, it, n)
		}
	case "StatefulSet", "DaemonSet", "Job", "CronJob":
		b.expandOwners(ctx, it, n)
	case "Service":
		b.expandService(ctx, it, n)
	case "PersistentVolumeClaim":
		b.expandPVC(ctx, it, n)
	case "Node":
		b.expandNode(ctx, it, n)
	}
}

// ownerRefs возвращает ownerReferences объекта в стабильном порядке.
func ownerRefs(obj *unstructured.Unstructured) []metav1.OwnerReference {
	refs := slices.Clone(obj.GetOwnerReferences())
	slices.SortFunc(refs, func(a, b metav1.OwnerReference) int {
		return strings.Compare(a.Kind+"/"+a.Name, b.Kind+"/"+b.Name)
	})
	return refs
}

// expandOwners поднимается по ownerReferences (Pod → ReplicaSet → Deployment, Job → CronJob). Владелец вида вне
// таблицы графа попадает вершиной Unknown/policy без чтения.
func (b *builder) expandOwners(ctx context.Context, it item, n Node) {
	for _, or := range ownerRefs(n.Object) {
		group := ""
		if gv, err := schema.ParseGroupVersion(or.APIVersion); err == nil {
			group = gv.Group
		}
		if !b.reach(it.depth + 1) {
			return
		}
		spec, ok := specFor(group, or.Kind)
		if !ok || group != spec.gvk.Group {
			ref := Ref{Group: group, Kind: or.Kind, Namespace: n.Namespace, Name: or.Name}
			if b.putNode(Node{Ref: ref, UID: string(or.UID), Health: Unknown, Unknown: UnknownPolicy}) {
				b.edge(ref, n.Ref, EdgeOwns)
			}
			continue
		}
		owner, _ := b.get(ctx, spec, n.Namespace, or.Name)
		b.edge(owner.Ref, n.Ref, EdgeOwns)
		b.enqueue(owner.Ref, it.depth+1, false)
	}
}

// selectorOf разбирает metav1.LabelSelector из spec.selector; пустой селектор (совпадает со всем) не используется.
func selectorOf(obj *unstructured.Unstructured) (labels.Selector, bool) {
	raw, found, _ := unstructured.NestedMap(obj.Object, "spec", "selector")
	if !found {
		return nil, false
	}
	var ls metav1.LabelSelector
	if !decode(&unstructured.Unstructured{Object: raw}, &ls) {
		return nil, false
	}
	sel, err := metav1.LabelSelectorAsSelector(&ls)
	if err != nil || sel.Empty() {
		return nil, false
	}
	return sel, true
}

func int64Field(obj *unstructured.Unstructured, path ...string) int64 {
	switch v, _, _ := unstructured.NestedFieldNoCopy(obj.Object, path...); n := v.(type) {
	case int64:
		return n
	case int32:
		return int64(n)
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

func ownedBy(obj *unstructured.Unstructured, kind, name string) bool {
	for _, or := range obj.GetOwnerReferences() {
		if or.Kind == kind && or.Name == name {
			return true
		}
	}
	return false
}

func sortedItems(items []unstructured.Unstructured) []unstructured.Unstructured {
	out := slices.Clone(items)
	slices.SortFunc(out, func(a, b unstructured.Unstructured) int {
		return strings.Compare(a.GetNamespace()+"/"+a.GetName(), b.GetNamespace()+"/"+b.GetName())
	})
	return out
}

// expandDeployment: Deployment → ReplicaSet'ы с репликами (или самый новый, если активных нет).
func (b *builder) expandDeployment(ctx context.Context, it item, n Node) {
	sel, ok := selectorOf(n.Object)
	if !ok || !b.reach(it.depth+1) {
		return
	}
	res, err := b.list(ctx, specReplicaSet, ListOptions{Namespace: n.Namespace, LabelSelector: sel.String()})
	if err != nil {
		return
	}
	var mine []unstructured.Unstructured
	for _, rs := range sortedItems(res.Items) {
		if ownedBy(&rs, "Deployment", n.Name) {
			mine = append(mine, rs)
		}
	}
	var keep []unstructured.Unstructured
	for _, rs := range mine {
		if int64Field(&rs, "spec", "replicas") > 0 || int64Field(&rs, "status", "replicas") > 0 {
			keep = append(keep, rs)
		}
	}
	if len(keep) == 0 && len(mine) > 0 {
		newest := mine[0]
		for _, rs := range mine[1:] {
			if nt, ct := newest.GetCreationTimestamp(), rs.GetCreationTimestamp(); !ct.Before(&nt) {
				newest = rs
			}
		}
		keep = []unstructured.Unstructured{newest}
	}
	for i := range keep {
		rn, ok := b.addObject(specReplicaSet, &keep[i])
		if !ok {
			return
		}
		b.edge(n.Ref, rn.Ref, EdgeOwns)
		b.enqueue(rn.Ref, it.depth+1, true)
	}
}

// expandReplicaSet: ReplicaSet → его Pod'ы.
func (b *builder) expandReplicaSet(ctx context.Context, it item, n Node) {
	sel, ok := selectorOf(n.Object)
	if !ok || !b.reach(it.depth+1) {
		return
	}
	res, err := b.list(ctx, specPod, ListOptions{Namespace: n.Namespace, LabelSelector: sel.String()})
	if err != nil {
		return
	}
	for _, p := range sortedItems(res.Items) {
		if !ownedBy(&p, "ReplicaSet", n.Name) {
			continue
		}
		pn, ok := b.addObject(specPod, &p)
		if !ok {
			return
		}
		b.edge(n.Ref, pn.Ref, EdgeOwns)
		if b.takePod() {
			b.enqueue(pn.Ref, it.depth+1, true)
		}
	}
}

func nestedString(obj map[string]any, path ...string) (string, bool, error) {
	return unstructured.NestedString(obj, path...)
}
