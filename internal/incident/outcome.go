// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

// prepareOutcome проверяет расширенные поля отметки результата относительно расследования и подставляет
// умолчания (ADR-0034 §2). Вызывается из MarkOutcome после базовой проверки, внутри чтения-изменения-записи
// расследования (при конфликте версий вызывается повторно, поэтому должна быть идемпотентной). Ошибка —
// ErrInvalidOutcome.
//
// Скелет (Фаза 0, D5): тело передаётся владельцу D6; сигнатура — contracts.md §5.
func (s *Service) prepareOutcome(_ *Incident, _ *Outcome) error {
	return nil
}
