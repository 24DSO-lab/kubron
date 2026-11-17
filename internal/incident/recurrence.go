// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import "context"

// detectRecurrence ищет недавно устранённое расследование с той же сигнатурой (ADR-0034 §4) и записывает его
// идентификатор в in.RecurrenceOf. Возвращает имена частично прочитанных источников для Report.Partial.
// Вызывается из Open до сохранения; закрытое расследование не изменяется.
//
// Скелет (Фаза 0, D5): тело передаётся владельцу D6; сигнатура — contracts.md §5.
func (s *Service) detectRecurrence(_ context.Context, _ *Incident) []string {
	return nil
}
