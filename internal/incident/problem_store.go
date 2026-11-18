// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"errors"
	"slices"

	"github.com/24DSO-lab/kubron/internal/tenant"
)

// noChange — служебный признак, не ошибка для пользователя: fn из updateProblem возвращает errNoChange, когда
// менять нечего (запись не пишется, ошибки нет). Наружу не выходит, поэтому текста для перевода у него нет.
type noChange struct{}

func (noChange) Error() string { return "" }

var errNoChange error = noChange{}

// updateProblem читает проблему, применяет fn и записывает с повтором при конфликте версий (как Service.mutate).
// Отсутствующая проблема — ErrProblemNotFound. Если fn вернула errNoChange, возвращается текущая запись без
// записи.
func (s *Service) updateProblem(ctx context.Context, scope tenant.Scope, id string, fn func(*Problem) error) (*Problem, error) {
	if s.problems == nil {
		return nil, ErrProblemsDisabled
	}
	for range maxConflictRetries {
		p, err := s.problems.GetProblem(ctx, scope, id)
		if err != nil {
			return nil, mapProblemErr(err)
		}
		switch err := fn(p); {
		case errors.Is(err, errNoChange):
			return p, nil
		case err != nil:
			return nil, err
		}
		p.UpdatedAt = s.clock()
		switch err := s.problems.PutProblem(ctx, p); {
		case err == nil:
			return p, nil
		case errors.Is(err, ErrConflict):
			continue
		default:
			return nil, err
		}
	}
	return nil, ErrConflict
}

// mapProblemErr превращает ErrNotFound хранилища в ErrProblemNotFound (он совпадает с ErrNotFound по errors.Is).
func mapProblemErr(err error) error {
	if errors.Is(err, ErrNotFound) && !errors.Is(err, ErrProblemNotFound) {
		return ErrProblemNotFound
	}
	return err
}

// setProblemID записывает в расследование только ProblemID (пустой — снимает). Единственное изменение
// расследования, которое делают операции с проблемами: UpdatedAt не трогается, чтобы порядок истории не менялся.
// Запись по версии с повтором при конфликте.
func (s *Service) setProblemID(ctx context.Context, scope tenant.Scope, incidentID, problemID string) (*Incident, error) {
	for range maxConflictRetries {
		in, err := s.b.GetIncident(ctx, scope, incidentID)
		if err != nil {
			return nil, err
		}
		if in.ProblemID == problemID {
			return in, nil
		}
		in.ProblemID = problemID
		switch err := s.b.PutIncident(ctx, in); {
		case err == nil:
			return in, nil
		case errors.Is(err, ErrConflict):
			continue
		default:
			return nil, err
		}
	}
	return nil, ErrConflict
}

// pruneDead возвращает участников без тех, чьё расследование удалено (ErrNotFound); keep остаётся всегда.
// Другие ошибки чтения участника не удаляют: лучше оставить лишнего, чем потерять живого. Вызывается при записи
// проблемы, поэтому чтение участников не нагружает просмотр.
func (s *Service) pruneDead(ctx context.Context, p *Problem, keep string) []ProblemMember {
	out := make([]ProblemMember, 0, len(p.Members))
	for _, m := range p.Members {
		if m.IncidentID != keep {
			if _, err := s.b.GetIncident(ctx, p.Scope, m.IncidentID); errors.Is(err, ErrNotFound) {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}

func hasMember(p *Problem, incidentID string) bool {
	return slices.ContainsFunc(p.Members, func(m ProblemMember) bool { return m.IncidentID == incidentID })
}

func dropMember(p *Problem, incidentID string) {
	p.Members = slices.DeleteFunc(p.Members, func(m ProblemMember) bool { return m.IncidentID == incidentID })
}
