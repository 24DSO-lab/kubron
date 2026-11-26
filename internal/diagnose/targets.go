// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"fmt"
	"maps"
	"slices"
)

// TargetCollector читает контекст для цели одного вида: базовый сбор (сама цель, владельцы, события и т. д.).
// Хуки сбора (RegisterCollectHook) выполняет CollectWith уже после него, поэтому сборщик цели их не вызывает.
// Ошибка возвращается, только если не удалось прочитать саму цель; остальное — Context.Missing / Context.Partial.
type TargetCollector func(ctx context.Context, r Reader, t ResourceRef, o CollectOptions) (*Context, error)

// targets — реестр видов цели (ADR-0031 §4): вид, которого здесь нет, — ErrUnsupportedTarget. Заменяет
// фиксированный switch в Collect.
var targets = map[string]TargetCollector{}

// RegisterTarget регистрирует сборщик для вида цели. Вызывать только из init; повторная регистрация вида,
// пустой вид и nil-сборщик — ошибка программиста (panic).
func RegisterTarget(kind string, c TargetCollector) {
	if kind == "" || c == nil {
		panic("diagnose: target needs a kind and a collector")
	}
	if _, dup := targets[kind]; dup {
		panic(fmt.Sprintf("diagnose: target %q is already registered", kind))
	}
	targets[kind] = c
}

// targetKinds — зарегистрированные виды цели по алфавиту (для документации и тестов).
func targetKinds() []string { return slices.Sorted(maps.Keys(targets)) }
