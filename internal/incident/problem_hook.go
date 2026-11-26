// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// partialProblems — имя источника в Report.Partial, когда автоприсоединение не удалось.
const partialProblems = "problems"

// confidenceRank упорядочивает уверенность: low < medium < high; неизвестное значение — ниже low.
func confidenceRank(c diagnose.Confidence) int {
	switch c {
	case diagnose.ConfidenceLow:
		return 1
	case diagnose.ConfidenceMedium:
		return 2
	case diagnose.ConfidenceHigh:
		return 3
	}
	return 0
}

// autoAttach присоединяет только что сохранённое расследование к проблеме или создаёт новую (ADR-0033 §6) и
// проставляет in.ProblemID. Вызывается из Open после успешного PutIncident. Сбой не делает Open неуспешным:
// возвращает имя частичного источника ("problems"), расследование остаётся без группы.
//
// Порядок записи: расследование (уже сохранено) → проблема → ProblemID в расследовании. Если упал последний
// шаг, проблема уже содержит участника; расследование без ProblemID остаётся валидным, а ручное присоединение
// или повторное открытие это исправит.
func (s *Service) autoAttach(ctx context.Context, in *Incident) []string {
	if s.problems == nil {
		return nil
	}
	best := in.Correlation.Best()
	if best == nil || confidenceRank(best.Confidence) < confidenceRank(AutoAttachConfidence) {
		return nil
	}
	pid, err := s.attachAuto(ctx, in, best.Key(), string(best.Kind), best.Source)
	if err != nil {
		if errors.Is(err, ErrProblemFull) {
			return nil // ADR-0033 §6: при полном составе присоединение пропускается, снимок остаётся
		}
		slog.WarnContext(ctx, "incident: automatic problem attach failed", "incident", in.ID, "error", err)
		return []string{partialProblems}
	}
	saved, err := s.setProblemID(ctx, in.Scope, in.ID, pid)
	if err != nil {
		slog.WarnContext(ctx, "incident: problem id was not stored", "incident", in.ID, "problem", pid, "error", err)
		return []string{partialProblems}
	}
	in.ProblemID, in.Version = saved.ProblemID, saved.Version
	return nil
}

// attachAuto атомарно находит открытую проблему по correlation key или создаёт её, затем добавляет участника.
func (s *Service) attachAuto(ctx context.Context, in *Incident, key, kind string, source diagnose.ResourceRef) (string, error) {
	now := s.clock()
	candidate := &Problem{
		ID: s.newID(), Scope: in.Scope, Key: key, Kind: kind, Source: source,
		Status: ProblemOpen, Origin: ProblemAuto, CreatedAt: now, UpdatedAt: now,
		Members: []ProblemMember{{IncidentID: in.ID, AddedAt: now, AddedBy: AutoMember}},
	}
	p, err := s.problems.FindOrCreateOpenProblem(ctx, candidate, now.Add(-ProblemWindow))
	if err != nil { return "", err }
	for range maxConflictRetries {
		if hasMember(p, in.ID) { return p.ID, nil }
		if len(p.Members) >= MaxProblemMembers { return "", ErrProblemFull }
		now = s.clock()
		p.Members = s.pruneDead(ctx, p, in.ID)
		p.Members = append(p.Members, ProblemMember{IncidentID: in.ID, AddedAt: now, AddedBy: AutoMember})
		p.UpdatedAt = now
		switch err := s.problems.PutProblem(ctx, p); {
		case err == nil:
			return p.ID, nil
		case errors.Is(err, ErrConflict):
			p, err = s.problems.GetProblem(ctx, in.Scope, p.ID)
			if err != nil { return "", err }
			if p.Status != ProblemOpen || p.UpdatedAt.Before(s.clock().Add(-ProblemWindow)) || slices.Contains(p.Detached, in.ID) {
				candidate = &Problem{
					ID: s.newID(), Scope: in.Scope, Key: key, Kind: kind, Source: source,
					Status: ProblemOpen, Origin: ProblemAuto, CreatedAt: s.clock(), UpdatedAt: s.clock(),
					Members: []ProblemMember{{IncidentID: in.ID, AddedAt: s.clock(), AddedBy: AutoMember}},
				}
				p, err = s.problems.FindOrCreateOpenProblem(ctx, candidate, s.clock().Add(-ProblemWindow))
				if err != nil { return "", err }
			}
		default:
			return "", err
		}
	}
	return "", ErrConflict
}

// onOutcomeSaved закрывает проблему, когда закрыт её последний открытый участник (ADR-0033 §6). Вызывается из
// MarkOutcome после сохранения; best-effort, ошибок не возвращает.
func (s *Service) onOutcomeSaved(ctx context.Context, in *Incident) {
	if s.problems == nil || in == nil || in.ProblemID == "" {
		return
	}
	_, err := s.updateProblem(ctx, in.Scope, in.ProblemID, func(p *Problem) error {
		if p.Status == ProblemClosed {
			return errNoChange
		}
		for _, m := range p.Members {
			if m.IncidentID == in.ID {
				continue // только что закрыт
			}
			other, err := s.b.GetIncident(ctx, in.Scope, m.IncidentID)
			switch {
			case errors.Is(err, ErrNotFound):
				continue // удалённое расследование не считается
			case err != nil:
				return err
			case other.Status == StatusOpen:
				return errNoChange
			}
		}
		p.Status = ProblemClosed
		return nil
	})
	if err != nil && !errors.Is(err, ErrProblemNotFound) {
		slog.WarnContext(ctx, "incident: problem was not closed", "problem", in.ProblemID, "error", err)
	}
}

// purgeProblems удаляет проблемы организации, обновлённые раньше before (граница retention расследований).
// Вызывается из Purge; best-effort.
func (s *Service) purgeProblems(ctx context.Context, orgID string, before time.Time) {
	if s.problems == nil {
		return
	}
	if _, err := s.problems.PurgeProblems(ctx, orgID, before); err != nil {
		slog.WarnContext(ctx, "incident: problems purge failed", "org", orgID, "error", err)
	}
}
