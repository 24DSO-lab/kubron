// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"context"

	corev1 "k8s.io/api/core/v1"
)

const (
	annDefaultClass  = "storageclass.kubernetes.io/is-default-class"
	annDefaultBeta   = "storageclass.beta.kubernetes.io/is-default-class"
	annSelectedNode  = "volume.kubernetes.io/selected-node"
	bindingWaitFirst = "WaitForFirstConsumer"
)

// expandPVC: PVC → PV и StorageClass; для цели — ещё и Pod'ы-потребители (mounts).
func (b *builder) expandPVC(ctx context.Context, it item, n Node) {
	var pvc corev1.PersistentVolumeClaim
	if !decode(n.Object, &pvc) {
		return
	}
	d := it.depth + 1
	if pvc.Spec.VolumeName != "" && b.reach(d) {
		pv, _ := b.get(ctx, specPV, "", pvc.Spec.VolumeName)
		b.edge(n.Ref, pv.Ref, EdgeBinds)
	}
	if b.reach(d) {
		b.expandClass(ctx, n, &pvc, d)
	}
	if it.down && b.reach(d) {
		res, err := b.list(ctx, specPod, ListOptions{Namespace: n.Namespace})
		if err != nil {
			return
		}
		for _, p := range sortedItems(res.Items) {
			var pod corev1.Pod
			if !decode(&p, &pod) || !contains(claimNames(&pod), n.Name) {
				continue
			}
			pn, ok := b.addObject(specPod, &p)
			if !ok {
				return
			}
			b.edge(pn.Ref, n.Ref, EdgeMounts)
			if b.takePod() {
				b.enqueue(pn.Ref, d, true)
			}
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (b *builder) expandClass(ctx context.Context, n Node, pvc *corev1.PersistentVolumeClaim, d int) {
	pending := pvc.Status.Phase != corev1.ClaimBound
	var class Node
	var haveClass bool
	switch {
	case pvc.Spec.StorageClassName != nil && *pvc.Spec.StorageClassName != "":
		class, _ = b.get(ctx, specStorageCls, "", *pvc.Spec.StorageClassName)
		b.edge(n.Ref, class.Ref, EdgeBinds)
		haveClass = true
	case pvc.Spec.StorageClassName == nil && pending:
		// Класс не указан: ищем класс по умолчанию (один List кластера).
		res, err := b.list(ctx, specStorageCls, ListOptions{})
		if err != nil {
			return
		}
		for _, sc := range sortedItems(res.Items) {
			if sc.GetAnnotations()[annDefaultClass] == "true" || sc.GetAnnotations()[annDefaultBeta] == "true" {
				cn, ok := b.addObject(specStorageCls, &sc)
				if ok {
					b.edge(n.Ref, cn.Ref, EdgeBinds)
					b.g.edges[edgeKey{n.Key(), cn.Key(), EdgeBinds}] = Edge{From: n.Ref, To: cn.Ref, Type: EdgeBinds, State: EdgeOK, Reason: "default_class"}
					class, haveClass = cn, true
				}
				break
			}
		}
		if !haveClass && !res.Truncated {
			b.setHealth(n.Ref, Degraded, ReasonNoDefaultClass)
			return
		}
	}
	// WaitForFirstConsumer: PVC ждёт Pod'а — это не поломка привязки.
	if haveClass && pending && class.Object != nil && pvc.Status.Phase == corev1.ClaimPending {
		mode, _, _ := nestedString(class.Object.Object, "volumeBindingMode")
		if mode == bindingWaitFirst && pvc.Annotations[annSelectedNode] == "" {
			b.setHealth(n.Ref, Progressing, ReasonWaitingFirstConsumer)
		}
	}
}
