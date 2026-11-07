// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// fluxListTimeout — предел чтения списка одного вида.
const fluxListTimeout = 20 * time.Second

// fluxList — единая точка чтения списка объектов Flux: только Kubernetes API, без кэша (кэш подключает B.7, меняя
// только внутренности). GVR строится из таблицы видов и версии, которую отдаёт fluxServedVersions; область namespace
// проверяется parseScope. Задачи B.2–B.6 вызывают fluxList и сам файл не меняют.
func fluxList[T any](
	m *Manager,
	kind fluxKind,
	namespaces []string,
	parse func(*unstructured.Unstructured) T,
	nsOf func(T) string,
) ([]T, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	versions, err := fluxServedVersions(conn)
	if err != nil {
		return nil, err
	}
	version := versions[kind.Kind]
	if !kind.supports(version) {
		return nil, fmt.Errorf("%w: %s", errFluxVersionUnsupported, kind.Kind)
	}
	gvr := kind.GVR(version)

	return scopedListTimeout(m, fluxListTimeout, "GetFlux"+kind.Kind, kind.Group, kind.Plural, namespaces,
		func(ctx context.Context, conn *ClusterConnection, ns string) ([]T, error) {
			list, err := conn.DynamicClient.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				return nil, fmt.Errorf("list %s.%s: %w", kind.Group, kind.Plural, err)
			}
			items := make([]T, 0, len(list.Items))
			for i := range list.Items {
				items = append(items, parse(&list.Items[i]))
			}
			return items, nil
		}, nsOf)
}
