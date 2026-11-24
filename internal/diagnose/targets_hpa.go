// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"fmt"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Цель HorizontalPodAutoscaler (ADR-0031 §6, этап 2). Базовый сбор минимален: сам HPA и его события. Целевой workload
// и его Pod'ы приходят из графа (хук сбора 100): правила Pod'ов в отчёт HPA намеренно не подключаются, иначе
// гипотезы нездоровой цели копировались бы в отчёт HPA. Проблемы цели видны в графе и открываются отдельным
// расследованием цели (правило chain.hpa даёт ссылку-цепочку на неё).

// hpaGroup — группа API HorizontalPodAutoscaler. Она нужна, чтобы ссылка цели совпадала с вершиной графа.
const hpaGroup = "autoscaling"

var gvkHPA = schema.GroupVersionKind{Group: hpaGroup, Version: "v2", Kind: "HorizontalPodAutoscaler"}

func init() { RegisterTarget("HorizontalPodAutoscaler", collectHPA) }

// collectHPA читает HPA и события namespace. Недоступность самого HPA — ошибка сбора (как у остальных целей);
// всё остальное — пробелы в Context.Missing и Context.Partial.
func collectHPA(ctx context.Context, r Reader, target ResourceRef, o CollectOptions) (*Context, error) {
	if target.Namespace == "" {
		return nil, fmt.Errorf("target namespace is empty: %w", ErrNotFound)
	}
	// Группа в ссылке цели нужна цепочкам: вершина графа ищется по ключу «группа/вид/namespace/имя».
	target.Group = hpaGroup
	col := newCollector(r, target, o)
	hpa, err := primary[autoscalingv2.HorizontalPodAutoscaler](ctx, col, gvkHPA, target.Namespace, target.Name)
	if err != nil {
		return nil, err
	}
	col.c.Target.UID = string(hpa.UID)
	if err := col.readCommon(ctx); err != nil {
		return nil, err
	}
	return col.c, nil
}
