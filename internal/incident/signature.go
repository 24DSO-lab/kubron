// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import "github.com/24DSO-lab/kubron/internal/diagnose"

// signatureOf возвращает сигнатуру расследования для поиска повторений (ADR-0034 §4); пустая строка — сигнатуру
// построить нельзя. Вызывается из Open до сохранения, результат кладётся в Incident.Signature.
//
// Скелет (Фаза 0, D5): тело передаётся владельцу D6; сигнатура — contracts.md §5.
func signatureOf(_ *Incident, _ *diagnose.Report) string {
	return ""
}
