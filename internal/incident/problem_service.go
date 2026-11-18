// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Ручное управление проблемами (ADR-0033 §7). Все операции работают только с сохранёнными данными и возвращают
// изменённую Problem, чтобы слой связки мог отправить событие problem.changed. Расследования никогда не
// удаляются, не скрываются и не сливаются: меняется только Incident.ProblemID.
//
// Порядок записи тот же, что у автоприсоединения: сначала проблема, затем ProblemID в расследованиях. Сбой на
// втором шаге оставляет проблему корректной, расследование — без группы; повтор операции идемпотентен.

// AttachToProblem присоединяет расследование к проблеме вручную; присоединение к закрытой проблеме открывает её.
// Если расследование было в другой проблеме, оно выходит из неё. Ручное присоединение снимает отметку
// «отсоединено». Ошибки: ErrProblemNotFound, ErrNotFound (расследование), ErrProblemFull, ErrProblemsDisabled.
func (s *Service) AttachToProblem(ctx context.Context, c Caller, incidentID, problemID string) (*Problem, error) {
	if s.problems == nil {
		return nil, ErrProblemsDisabled
	}
	in, err := s.b.GetIncident(ctx, c.Scope, incidentID)
	if err != nil {
		return nil, err
	}
	prev := in.ProblemID
	p, err := s.updateProblem(ctx, c.Scope, problemID, func(p *Problem) error {
		if hasMember(p, incidentID) && p.Status == ProblemOpen && !slices.Contains(p.Detached, incidentID) {
			return errNoChange
		}
		p.Members = s.pruneDead(ctx, p, incidentID)
		if !hasMember(p, incidentID) {
			if len(p.Members) >= MaxProblemMembers {
				return ErrProblemFull
			}
			p.Members = append(p.Members, ProblemMember{IncidentID: incidentID, AddedAt: s.clock(), AddedBy: c.Actor})
		}
		p.Detached = slices.DeleteFunc(p.Detached, func(id string) bool { return id == incidentID })
		p.Status = ProblemOpen
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, err := s.setProblemID(ctx, c.Scope, incidentID, p.ID); err != nil {
		return nil, fmt.Errorf("problem updated, investigation was not linked: %w", err)
	}
	if prev != "" && prev != p.ID {
		s.leaveProblem(ctx, c.Scope, prev, incidentID)
	}
	return p, nil
}

// DetachFromProblem отсоединяет расследование от его проблемы и запоминает это в Problem.Detached: автоматически
// оно в эту проблему не вернётся. Проблема без участников закрывается. Возвращает проблему, из которой
// отсоединено. Ошибки: ErrNotFound (расследование), ErrProblemNotFound (у расследования нет проблемы или она
// исчезла: ссылка при этом снимается), ErrProblemsDisabled.
func (s *Service) DetachFromProblem(ctx context.Context, c Caller, incidentID string) (*Problem, error) {
	if s.problems == nil {
		return nil, ErrProblemsDisabled
	}
	in, err := s.b.GetIncident(ctx, c.Scope, incidentID)
	if err != nil {
		return nil, err
	}
	if in.ProblemID == "" {
		return nil, ErrProblemNotFound
	}
	p, err := s.updateProblem(ctx, c.Scope, in.ProblemID, func(p *Problem) error {
		dropMember(p, incidentID)
		if !slices.Contains(p.Detached, incidentID) {
			p.Detached = append(p.Detached, incidentID)
			if over := len(p.Detached) - MaxProblemMembers; over > 0 {
				p.Detached = p.Detached[over:] // старейшие забываются
			}
		}
		if len(p.Members) == 0 {
			p.Status = ProblemClosed
		}
		return nil
	})
	if err != nil && !errors.Is(err, ErrProblemNotFound) {
		return nil, err
	}
	if _, serr := s.setProblemID(ctx, c.Scope, incidentID, ""); serr != nil {
		return nil, fmt.Errorf("problem updated, investigation was not unlinked: %w", serr)
	}
	if err != nil {
		return nil, err // проблемы уже нет: ссылка снята
	}
	return p, nil
}

// MergeProblems переносит участников проблемы fromID в toID; исходная закрывается с MergedInto. Возвращает toID.
// Ошибки: ErrInvalidProblem (одна и та же проблема, исходная уже объединена), ErrProblemNotFound, ErrProblemFull
// (в сумме больше MaxProblemMembers), ErrProblemsDisabled. Повтор после сбоя безопасен.
func (s *Service) MergeProblems(ctx context.Context, c Caller, fromID, toID string) (*Problem, error) {
	if s.problems == nil {
		return nil, ErrProblemsDisabled
	}
	if fromID == toID {
		return nil, fmt.Errorf("%w: a problem cannot be merged into itself", ErrInvalidProblem)
	}
	from, err := s.problems.GetProblem(ctx, c.Scope, fromID)
	if err != nil {
		return nil, mapProblemErr(err)
	}
	if from.MergedInto != "" && from.MergedInto != toID {
		return nil, fmt.Errorf("%w: the source problem is already merged", ErrInvalidProblem)
	}
	moved := s.pruneDead(ctx, from, "")
	to, err := s.updateProblem(ctx, c.Scope, toID, func(to *Problem) error {
		to.Members = s.pruneDead(ctx, to, "")
		for _, m := range moved {
			if !hasMember(to, m.IncidentID) {
				to.Members = append(to.Members, m)
			}
			to.Detached = slices.DeleteFunc(to.Detached, func(id string) bool { return id == m.IncidentID })
		}
		if len(to.Members) > MaxProblemMembers {
			return ErrProblemFull
		}
		if from.Status == ProblemOpen {
			to.Status = ProblemOpen
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var firstErr error
	for _, m := range moved {
		if _, err := s.setProblemID(ctx, c.Scope, m.IncidentID, to.ID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return nil, fmt.Errorf("problems merged, some investigations were not relinked: %w", firstErr)
	}
	if _, err := s.updateProblem(ctx, c.Scope, fromID, func(f *Problem) error {
		f.Status, f.MergedInto, f.Members = ProblemClosed, to.ID, []ProblemMember{}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("problems merged, source was not closed: %w", err)
	}
	return to, nil
}

// CreateProblem создаёт проблему вручную из выбранных расследований (в том числе чтобы выделить часть участников
// другой проблемы: выбранные выходят из прежних). Ключ «manual:<id>» не совпадает с ключами корреляции, поэтому
// автоприсоединение сюда не добавляет. Ошибки: ErrInvalidProblem (пусто, повторы), ErrProblemFull, ErrNotFound
// (расследование), ErrProblemsDisabled.
func (s *Service) CreateProblem(ctx context.Context, c Caller, incidentIDs []string) (*Problem, error) {
	if s.problems == nil {
		return nil, ErrProblemsDisabled
	}
	switch {
	case len(incidentIDs) == 0:
		return nil, fmt.Errorf("%w: no investigations selected", ErrInvalidProblem)
	case len(incidentIDs) > MaxProblemMembers:
		return nil, ErrProblemFull
	}
	seen := make(map[string]bool, len(incidentIDs))
	ins := make([]*Incident, 0, len(incidentIDs))
	for _, id := range incidentIDs {
		if !ValidID(id) || seen[id] {
			return nil, fmt.Errorf("%w: invalid or repeated investigation id", ErrInvalidProblem)
		}
		seen[id] = true
		in, err := s.b.GetIncident(ctx, c.Scope, id)
		if err != nil {
			return nil, err
		}
		ins = append(ins, in)
	}
	now := s.clock()
	id := s.newID()
	p := &Problem{ID: id, Scope: c.Scope, Key: "manual:" + id, Kind: "manual", Source: ins[0].Target,
		Status: ProblemOpen, Origin: ProblemManual, CreatedAt: now, UpdatedAt: now}
	for _, in := range ins {
		p.Members = append(p.Members, ProblemMember{IncidentID: in.ID, AddedAt: now, AddedBy: c.Actor})
	}
	if err := s.problems.PutProblem(ctx, p); err != nil {
		return nil, err
	}
	var firstErr error
	for _, in := range ins {
		if _, err := s.setProblemID(ctx, c.Scope, in.ID, p.ID); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if in.ProblemID != "" && in.ProblemID != p.ID {
			s.leaveProblem(ctx, c.Scope, in.ProblemID, in.ID)
		}
	}
	if firstErr != nil {
		return nil, fmt.Errorf("problem created, some investigations were not linked: %w", firstErr)
	}
	return p, nil
}

// leaveProblem убирает расследование из состава прежней проблемы (best-effort: сбой не отменяет операцию, а
// лишний участник отфильтруется при чтении, когда ProblemID расследования указывает на другую проблему).
func (s *Service) leaveProblem(ctx context.Context, scope tenant.Scope, problemID, incidentID string) {
	_, _ = s.updateProblem(ctx, scope, problemID, func(p *Problem) error {
		if !hasMember(p, incidentID) {
			return errNoChange
		}
		dropMember(p, incidentID)
		if len(p.Members) == 0 {
			p.Status = ProblemClosed
		}
		return nil
	})
}

// GetProblem возвращает проблему области. Участники — идентификаторы расследований; тех, что удалены, очищает
// следующая запись проблемы, а потребитель пропускает не найденных.
func (s *Service) GetProblem(ctx context.Context, scope tenant.Scope, id string) (*Problem, error) {
	if s.problems == nil {
		return nil, ErrProblemsDisabled
	}
	p, err := s.problems.GetProblem(ctx, scope, id)
	if err != nil {
		return nil, mapProblemErr(err)
	}
	return p, nil
}

// ListProblems возвращает страницу проблем кластера, новые первыми.
func (s *Service) ListProblems(ctx context.Context, scope tenant.Scope, q ProblemQuery) (ProblemPage, error) {
	if s.problems == nil {
		return ProblemPage{}, ErrProblemsDisabled
	}
	return s.problems.ListProblems(ctx, scope, q)
}
