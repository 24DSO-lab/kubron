// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation

import (
	"context"
	"time"

	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// Run ищет по живым данным общий источник и затронутое множество нагрузок (ADR-0033 §2–§4).
// Вход — уже собранный diagnose.Context и Reader для дополнительных чтений; now передаётся параметром.
// Ошибка возвращается только программная: недоступные данные дают Missing и Partial в результате.
//
// Скелет (Фаза 0): правил нет, результат пуст. Реализация — Фаза 1.
func Run(_ context.Context, _ diagnose.Reader, _ *diagnose.Context, _ time.Time) (*Result, error) {
	return &Result{Version: Version}, nil
}
