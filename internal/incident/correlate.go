// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"

	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// correlate строит снимок корреляции (ADR-0033 §5) на уже собранном контексте и кладёт его в in.Correlation.
// Возвращает имена частично прочитанных источников для Report.Partial. Вызывается из Open после Analyze и до
// сохранения; сбой корреляции расследование не роняет.
//
// Скелет (Фаза 0, D5): тело передаётся владельцу D4 (ADR-0033, этап A); сигнатура — contracts.md §5.
func (s *Service) correlate(_ context.Context, _ *diagnose.Context, _ *Incident) []string {
	return nil
}
