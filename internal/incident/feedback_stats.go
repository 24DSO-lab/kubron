// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"slices"

	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/feedback"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// StatsWindow — сколько последних закрытых расследований кластера читает RuleStats (две страницы по MaxPageSize).
const StatsWindow = feedback.StatsWindow

// minFeedbackRuleSetVersion — первая версия набора правил с RuleID и RuleEpoch в отчёте (diagnose.RuleSetVersion
// 2, ADR-0034 §3). Строки истории более ранних отчётов (и старые строки PostgreSQL без новых полей) в статистику
// не входят и не мигрируются.
const minFeedbackRuleSetVersion = 2

// RuleStats считает статистику правил по закрытым расследованиям кластера области (ADR-0034 §6): читает до
// StatsWindow последних закрытых, пропускает записи без новых полей, возвращает Stats с Unevaluated и
// Truncated. Вычисляется по запросу, не при Open. Повторение учитывается, когда расследование-повторение уже
// закрыто: открытые в статистику не входят.
func (s *Service) RuleStats(ctx context.Context, scope tenant.Scope) (feedback.Stats, error) {
	if err := CheckScope(scope); err != nil {
		return feedback.Stats{}, err
	}
	var recs []feedback.Record
	cursor := ""
	for range StatsWindow / MaxPageSize {
		page, err := s.b.ListIncidents(ctx, scope, ListQuery{Limit: MaxPageSize, Cursor: cursor, Status: StatusClosed})
		if err != nil {
			return feedback.Stats{}, err
		}
		for i := range page.Items {
			if r, ok := recordOf(&page.Items[i]); ok {
				recs = append(recs, r)
			}
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	st := feedback.Compute(recs, s.clock())
	st.Truncated = cursor != "" // после последней прочитанной страницы есть ещё закрытые расследования
	return st, nil
}

// ExportRuleStats возвращает анонимный JSON статистики правил для ручной отправки мантейнерам (ADR-0034 §8).
func (s *Service) ExportRuleStats(ctx context.Context, scope tenant.Scope) ([]byte, error) {
	st, err := s.RuleStats(ctx, scope)
	if err != nil {
		return nil, err
	}
	return feedback.Export(st, diagnose.RuleSetVersion)
}

// recordOf — адаптер Summary → feedback.Record. ok=false — строка не годится для статистики: расследование не
// закрыто, без результата или записано до появления правил с RuleID.
func recordOf(sm *Summary) (feedback.Record, bool) {
	if sm.Status != StatusClosed || sm.Result == "" || sm.RuleSetVersion < minFeedbackRuleSetVersion {
		return feedback.Record{}, false
	}
	r := feedback.Record{
		ID: sm.ID, Result: string(sm.Result), OpenedAt: sm.OpenedAt, ResolvedAt: sm.ResolvedAt,
		RuleSetVersion: sm.RuleSetVersion, Symptoms: slices.Clone(sm.Symptoms), RecurrenceOf: sm.RecurrenceOf,
		Features: slices.Clone(sm.Features),
		Rejected: feedbackRefs(sm.Rejected), Helpful: feedbackRefs(sm.Helpful), Useless: feedbackRefs(sm.Useless),
	}
	if sm.Confirmed != nil {
		ref := feedback.Ref(*sm.Confirmed)
		r.Confirmed = &ref
	}
	if v := sm.CorrelationVerdict; v != nil {
		r.CorrelationKind, r.CorrelationConfidence, r.CorrelationVerdict = v.Kind, v.Confidence, v.Verdict
	}
	return r, true
}

func feedbackRefs(in []RuleRef) []feedback.Ref {
	out := make([]feedback.Ref, 0, len(in))
	for _, r := range in {
		out = append(out, feedback.Ref(r))
	}
	return out
}
