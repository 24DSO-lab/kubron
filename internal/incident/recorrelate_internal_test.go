// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"errors"
	"testing"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// Этап C корреляции (ADR-0033): автоприсоединение по новым видам кандидата идёт по прежней логике — порог medium.
// shared_symptom всегда low, поэтому проблем сам не создаёт; shared_dependency и shared_root создают, начиная с medium.
func TestAutoAttachStageCKinds(t *testing.T) {
	tests := []struct {
		kind       correlation.Kind
		confidence diagnose.Confidence
		wantAttach bool
	}{
		{correlation.KindSharedSymptom, diagnose.ConfidenceLow, false},
		{correlation.KindSharedDependency, diagnose.ConfidenceLow, false},
		{correlation.KindSharedRoot, diagnose.ConfidenceLow, false},
		{correlation.KindSharedDependency, diagnose.ConfidenceMedium, true},
		{correlation.KindSharedDependency, diagnose.ConfidenceHigh, true},
		{correlation.KindSharedRoot, diagnose.ConfidenceMedium, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind)+"/"+string(tt.confidence), func(t *testing.T) {
			fx := newFixture(t)
			in := fx.add("i1", corr(tt.kind, "n1", tt.confidence))
			if part := fx.attach(in); part != nil {
				t.Fatalf("partial = %v", part)
			}
			if got := len(fx.problems()) == 1 && in.ProblemID != ""; got != tt.wantAttach {
				t.Fatalf("проблема создана=%v, ожидалось %v (problems=%d, id=%q)", got, tt.wantAttach, len(fx.problems()), in.ProblemID)
			}
			if tt.wantAttach {
				if p := fx.problems()[0]; p.Kind != string(tt.kind) {
					t.Fatalf("вид проблемы %q, want %q", p.Kind, tt.kind)
				}
			}
		})
	}
}

// Пересчёт проблему не создаёт и не меняет: автоприсоединение — только при открытии.
func TestRecomputeDoesNotTouchProblems(t *testing.T) {
	fx := newFixture(t)
	in := fx.add("i1", corr(correlation.KindSharedDependency, "n1", diagnose.ConfidenceMedium))
	if fx.attach(in); len(fx.problems()) != 1 {
		t.Fatalf("problems = %d", len(fx.problems()))
	}
	before := fx.problems()[0]
	// Без читателя кластера пересчёт невозможен: ошибка, а не паника, и проблема остаётся прежней.
	if _, err := fx.svc.RecomputeCorrelation(t.Context(), fx.caller(), "i1"); !errors.Is(err, diagnose.ErrUnavailable) {
		t.Fatalf("err = %v, want diagnose.ErrUnavailable", err)
	}
	after := fx.problems()
	if len(after) != 1 || after[0].UpdatedAt != before.UpdatedAt || len(after[0].Members) != len(before.Members) {
		t.Fatalf("проблема изменилась: %+v → %+v", before, after)
	}
}
