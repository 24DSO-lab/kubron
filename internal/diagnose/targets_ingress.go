// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// Цель Ingress (ADR-0031 §4, этап 2). Базовый сбор: сам Ingress, поды его backend-Service (чтобы к ним применялись
// правила подов) и события. Остальное — Service, EndpointSlice, IngressClass — приходит из графа (хук сбора 100).
// Secret и ConfigMap не читаются нигде: имя TLS-Secret берётся из spec.tls[].secretName самого Ingress.

const groupNetworking = "networking.k8s.io"

var gvkIngress = schema.GroupVersionKind{Group: groupNetworking, Version: "v1", Kind: "Ingress"}

func init() { RegisterTarget("Ingress", collectIngress) }

func collectIngress(ctx context.Context, r Reader, target ResourceRef, o CollectOptions) (*Context, error) {
	if target.Namespace == "" {
		return nil, fmt.Errorf("target namespace is empty: %w", ErrNotFound)
	}
	target.Group = groupNetworking // граф различает виды по группе; вызывающий мог её не указать
	col := newCollector(r, target, o)
	ing, err := primary[networkingv1.Ingress](ctx, col, gvkIngress, target.Namespace, target.Name)
	if err != nil {
		return nil, err
	}
	col.c.Target.UID = string(ing.UID)
	col.listBackendPods(ctx, target.Namespace, ing)
	if err := col.readCommon(ctx); err != nil {
		return nil, err
	}
	return col.c, nil
}

// listBackendPods читает поды, которые выбирают backend-Service Ingress: один List namespace, отбор по селекторам.
// Service читается без записи в Missing: его отсутствие — находка графа (ingress.backend_service_missing), а не
// пробел сбора. Service без селектора подов не даёт.
func (col *collector) listBackendPods(ctx context.Context, ns string, ing *networkingv1.Ingress) {
	var selectors []labels.Selector
	seen := map[string]bool{}
	for _, be := range depgraph.IngressBackends(ing) {
		if be.Service == "" || seen[be.Service] || len(seen) == maxBackendServices {
			continue
		}
		seen[be.Service] = true
		u, err := col.r.Get(ctx, gvkService, ns, be.Service)
		if err != nil {
			continue
		}
		svc, err := decode[corev1.Service](u)
		if err != nil || len(svc.Spec.Selector) == 0 {
			continue
		}
		selectors = append(selectors, labels.SelectorFromSet(svc.Spec.Selector))
	}
	if len(selectors) == 0 {
		return
	}
	col.listPodsWhere(ctx, ListOptions{Namespace: ns, Limit: maxListObjects}, func(p *corev1.Pod) bool {
		for _, s := range selectors {
			if s.Matches(labels.Set(p.Labels)) {
				return true
			}
		}
		return false
	})
}
