// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"log/slog"

	"github.com/24DSO-lab/kubron/internal/feedback"
)

// partialRecurrence — имя источника в Report.Partial, когда поиск повторения не удался.
const partialRecurrence = "recurrence"

// detectRecurrence ищет недавно устранённое расследование с той же сигнатурой (ADR-0034 §4) и записывает его
// идентификатор в in.RecurrenceOf. Подходит закрытое расследование этой же области с результатом resolved,
// устранённое не раньше feedback.RecurrenceWindow до открытия текущего; из нескольких берётся устранённое
// последним (при равенстве — с меньшим ID). Возвращает имена частично прочитанных источников для
// Report.Partial: сбой чтения истории Open не роняет. Закрытое расследование не изменяется.
func (s *Service) detectRecurrence(ctx context.Context, in *Incident) []string {
	if in.Signature == "" {
		return nil
	}
	cursor := ""
	var best *Summary
	for range StatsWindow / MaxPageSize {
		page, err := s.b.ListIncidents(ctx, in.Scope, ListQuery{Limit: MaxPageSize, Cursor: cursor, Status: StatusClosed})
		if err != nil {
			slog.WarnContext(ctx, "incident: recurrence lookup failed", "incident", in.ID, "error", err)
			return []string{partialRecurrence}
		}
		for i := range page.Items {
			if sm := &page.Items[i]; isRecurrenceOf(in, sm) && (best == nil || newerResolved(sm, best)) {
				best = sm
			}
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	if best != nil {
		in.RecurrenceOf = best.ID
	}
	return nil
}

func isRecurrenceOf(in *Incident, sm *Summary) bool {
	if sm.ID == in.ID || sm.Signature != in.Signature || sm.Result != ResultResolved || sm.ResolvedAt == nil {
		return false
	}
	age := in.OpenedAt.Sub(*sm.ResolvedAt)
	return age >= 0 && age <= feedback.RecurrenceWindow
}

func newerResolved(a, b *Summary) bool {
	if !a.ResolvedAt.Equal(*b.ResolvedAt) {
		return a.ResolvedAt.After(*b.ResolvedAt)
	}
	return a.ID < b.ID
}
