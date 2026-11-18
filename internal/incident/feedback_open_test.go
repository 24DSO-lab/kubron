// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident_test

import (
	"testing"

	"github.com/24DSO-lab/kubron/internal/incident"
)

// E2E-4: открыть → устранить с подтверждённой гипотезой → снова открыть то же → повторение найдено,
// статистика видит оба расследования, повторение учтено у первого.
func TestRecurrenceAndStatsThroughOpen(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	first, err := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if first.Signature == "" || first.RecurrenceOf != "" {
		t.Fatalf("first: signature=%q recurrenceOf=%q", first.Signature, first.RecurrenceOf)
	}
	if len(first.Report.Hypotheses) == 0 || first.Report.Hypotheses[0].RuleID == "" {
		t.Fatalf("report carries no rule ids: %+v", first.Report.Hypotheses)
	}
	hyp := first.Report.Hypotheses[0].ID
	afterMark, err := e.svc.MarkOutcome(t.Context(), e.call, first.ID, incident.Outcome{
		Result: incident.ResultResolved, ConfirmedHypothesisID: hyp})
	if err != nil {
		t.Fatal(err)
	}

	second, err := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if second.Signature != first.Signature || second.RecurrenceOf != first.ID {
		t.Fatalf("second: signature=%q recurrenceOf=%q, want %q / %q", second.Signature, second.RecurrenceOf, first.Signature, first.ID)
	}
	// Закрытое предыдущее расследование не изменено повторным открытием.
	prev, err := e.b.GetIncident(t.Context(), e.call.Scope, first.ID)
	if err != nil || prev.RecurrenceOf != "" || prev.Version != afterMark.Version || !prev.UpdatedAt.Equal(afterMark.UpdatedAt) {
		t.Errorf("closed investigation changed: %+v %v", prev, err)
	}
	if _, err := e.svc.MarkOutcome(t.Context(), e.call, second.ID, incident.Outcome{Result: incident.ResultUnresolved}); err != nil {
		t.Fatal(err)
	}

	st, err := e.svc.RuleStats(t.Context(), e.call.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if st.Records != 2 || st.Unevaluated != 1 {
		t.Fatalf("records=%d unevaluated=%d", st.Records, st.Unevaluated)
	}
	h := st.Hypotheses[0]
	if h.Confirmed != 1 || h.Resolved != 1 || h.Recurred != 1 || h.Trusted || h.Precision != nil {
		t.Errorf("hypothesis stat = %+v", h)
	}
}
