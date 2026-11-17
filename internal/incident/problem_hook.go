// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"time"
)

// autoAttach присоединяет только что сохранённое расследование к проблеме или создаёт новую (ADR-0033 §6) и
// проставляет in.ProblemID. Вызывается из Open после успешного PutIncident. Сбой не делает Open неуспешным:
// возвращает имя частичного источника ("problems"), расследование остаётся без группы.
//
// Скелет (Фаза 0): заглушка. Реализация — Фаза 1.
func (s *Service) autoAttach(_ context.Context, _ *Incident) []string {
	return nil
}

// onOutcomeSaved закрывает проблему, когда закрыт её последний открытый участник (ADR-0033 §6). Вызывается из
// MarkOutcome после сохранения; best-effort, ошибок не возвращает.
//
// Скелет (Фаза 0): заглушка. Реализация — Фаза 1.
func (s *Service) onOutcomeSaved(_ context.Context, _ *Incident) {}

// purgeProblems удаляет проблемы организации, обновлённые раньше before (граница retention расследований).
// Вызывается из Purge; best-effort.
//
// Скелет (Фаза 0): заглушка. Реализация — Фаза 1.
func (s *Service) purgeProblems(_ context.Context, _ string, _ time.Time) {}
