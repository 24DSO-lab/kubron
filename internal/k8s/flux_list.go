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

// fluxList — единая точка чтения списка объектов Flux. GVR строится из таблицы видов и версии, которую отдаёт
// fluxServedVersions; область namespace проверяется parseScope. Задачи B.2–B.6 вызывают fluxList и сам файл не меняют.
//
// Сначала смотрится informer-кэш вида у подключения (flux_cache.go): после первичной синхронизации список строится из
// памяти без LIST к API-серверу, а об изменениях фронтенд узнаёт по событию flux:changed. Пока informer не
// синхронизирован, нет права list на вид или кэш не создан, список читается через API; результат тот же.
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

	scope, err := parseScope(namespaces)
	if err != nil {
		return nil, err
	}
	// Событие адресовано подключению (ADR-0009): emitCluster рассылает его всем видам, у которых этот кластер активен,
	// и только им. Кэш общий для видов подключения; замыкание создаёт первый вызвавший (как у Argo), но рассылка идёт
	// через общий пул по ключу подключения, поэтому не зависит от того, какой вид его создал.
	if conn.DynamicClient != nil {
		onChange := func() { m.emitCluster(EventFluxChanged, conn) }
		if c := fluxCacheOf(conn, kind, gvr, parse, onChange); c != nil {
			if items, ok := c.list(scope); ok {
				return items, nil
			}
		}
	}

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
