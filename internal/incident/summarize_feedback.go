// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

// summarizeFeedback дописывает в строку истории ссылки на правила (подтверждённая и отвергнутые гипотезы,
// помогшие и не помогшие рекомендации), признаки отчёта и оценку корреляции (ADR-0034 §5). Вызывается из
// Summarize; только коды и числа.
//
// Скелет (Фаза 0, D5): тело передаётся владельцу D6; сигнатура — contracts.md §5.
func (in *Incident) summarizeFeedback(_ *Summary) {}
