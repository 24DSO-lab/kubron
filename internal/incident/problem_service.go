// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"

	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Ручное управление проблемами (ADR-0033 §7). Все операции работают только с сохранёнными данными и возвращают
// изменённую Problem, чтобы слой связки мог отправить событие problem.changed. Расследования никогда не
// удаляются, не скрываются и не сливаются: меняется только Incident.ProblemID.
//
// Скелет (Фаза 0): сигнатуры для слоя связки, тела — заглушки. Реализация — Фаза 1.

// AttachToProblem присоединяет расследование к проблеме вручную; присоединение к закрытой проблеме открывает её.
// Ошибки: ErrProblemNotFound, ErrNotFound (расследование), ErrProblemFull, ErrProblemsDisabled.
func (s *Service) AttachToProblem(_ context.Context, _ Caller, _, _ string) (*Problem, error) {
	return nil, ErrProblemsDisabled
}

// DetachFromProblem отсоединяет расследование от его проблемы и запоминает это в Problem.Detached: автоматически
// оно в эту проблему не вернётся. Возвращает проблему, из которой отсоединено.
func (s *Service) DetachFromProblem(_ context.Context, _ Caller, _ string) (*Problem, error) {
	return nil, ErrProblemsDisabled
}

// MergeProblems переносит участников проблемы fromID в toID; исходная закрывается с MergedInto. Возвращает toID.
func (s *Service) MergeProblems(_ context.Context, _ Caller, _, _ string) (*Problem, error) {
	return nil, ErrProblemsDisabled
}

// CreateProblem создаёт проблему вручную из выбранных расследований.
func (s *Service) CreateProblem(_ context.Context, _ Caller, _ []string) (*Problem, error) {
	return nil, ErrProblemsDisabled
}

// GetProblem возвращает проблему области.
func (s *Service) GetProblem(_ context.Context, _ tenant.Scope, _ string) (*Problem, error) {
	return nil, ErrProblemsDisabled
}

// ListProblems возвращает страницу проблем кластера, новые первыми.
func (s *Service) ListProblems(_ context.Context, _ tenant.Scope, _ ProblemQuery) (ProblemPage, error) {
	return ProblemPage{}, ErrProblemsDisabled
}
