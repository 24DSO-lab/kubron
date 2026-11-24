// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Цель StatefulSet (ADR-0031 §4, этап 3). Базовый сбор минимален: сам StatefulSet, его Pod'ы и события. Остальное
// (PVC шаблонов, Node, Service) приходит из графа (хук сбора 100), а правило chain.statefulset читает граф.
func init() { RegisterTarget("StatefulSet", collectStatefulSet) }

var gvkStatefulSet = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"}

func collectStatefulSet(ctx context.Context, r Reader, target ResourceRef, o CollectOptions) (*Context, error) {
	if target.Namespace == "" {
		return nil, fmt.Errorf("target namespace is empty: %w", ErrNotFound)
	}
	col := newCollector(r, target, o)
	sts, err := primary[appsv1.StatefulSet](ctx, col, gvkStatefulSet, target.Namespace, target.Name)
	if err != nil {
		return nil, err
	}
	col.c.Target.UID = string(sts.UID)
	sel := selectorString(sts.Spec.Selector)
	if sel == "" {
		col.c.addMissing("statefulset.selector_missing", col.c.Target, nil)
	} else {
		col.listPodsWhere(ctx, ListOptions{Namespace: target.Namespace, LabelSelector: sel, Limit: maxListObjects},
			func(p *corev1.Pod) bool {
				o := controllerOf(p.OwnerReferences, "StatefulSet")
				return o != nil && o.Name == sts.Name
			})
	}
	if err := col.readCommon(ctx); err != nil {
		return nil, err
	}
	return col.c, nil
}
