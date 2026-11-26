// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// Подъём RuleSetVersion до 3 не отбрасывает закрытые расследования версии 2: они остаются в выборке статистики вместе
// с версией 3, а записи версии 1 по-прежнему не входят. Статистика ведётся по (RuleID, Epoch), а не по версии набора.
func TestRuleStatsMixesRuleSetVersions(t *testing.T) {
	t.Parallel()
	f := newFbFx(t)
	confirm := Outcome{Result: ResultResolved, ConfirmedHypothesisID: "h1"}

	v1 := fbReport()
	v1.RuleSetVersion = 1
	f.closeAfter("v1", v1, time.Hour, confirm)
	v2 := fbReport() // fbReport — отчёт версии 2
	f.closeAfter("v2", v2, time.Hour, confirm)
	v3 := fbReport()
	v3.RuleSetVersion = diagnose.RuleSetVersion
	f.closeAfter("v3", v3, time.Hour, confirm)

	st, err := f.svc.RuleStats(t.Context(), tScope)
	if err != nil {
		t.Fatal(err)
	}
	if st.Records != 2 {
		t.Errorf("records = %d, want 2 (version 1 is excluded)", st.Records)
	}
	if want := []int{2, diagnose.RuleSetVersion}; !slices.Equal(st.RuleSetVersions, want) {
		t.Errorf("versions = %v, want %v", st.RuleSetVersions, want)
	}
	for _, h := range st.Hypotheses {
		if h.RuleID == "pod.crash" && h.Confirmed != 2 {
			t.Errorf("pod.crash confirmed = %d: versions 2 and 3 share the (RuleID, Epoch) key", h.Confirmed)
		}
	}

	raw, err := f.svc.ExportRuleStats(t.Context(), tScope)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		RuleSetVersion  int   `json:"ruleSetVersion"`
		RuleSetVersions []int `json:"ruleSetVersions"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.RuleSetVersion != diagnose.RuleSetVersion || !slices.Equal(doc.RuleSetVersions, []int{2, diagnose.RuleSetVersion}) {
		t.Errorf("export = %+v", doc)
	}
}

// Снимок корреляции версии 1 (до правил этапа C) читается, остаётся в расследовании со своей версией и принимает
// отметку о кандидате; новая версия подставляется только при пересчёте.
func TestCorrelationSnapshotVersion1StaysReadable(t *testing.T) {
	t.Parallel()
	const v1 = `{"version":1,"observedAt":"2026-10-01T10:00:00Z","candidates":[{"id":"c1","kind":"shared_node","confidence":"high",
	  "source":{"kind":"Node","name":"n1"},"startedAt":"2026-10-01T09:50:00Z","affected":[],"total":2}],"facts":[],"missing":[]}`
	var snap correlation.Result
	if err := json.Unmarshal([]byte(v1), &snap); err != nil {
		t.Fatalf("version 1 snapshot does not decode: %v", err)
	}
	if snap.Version != 1 || snap.Version >= correlation.Version {
		t.Fatalf("snapshot version = %d, current %d", snap.Version, correlation.Version)
	}
	if best := snap.Best(); best == nil || best.ID != "c1" {
		t.Fatalf("best = %+v", best)
	}

	f := newFbFx(t)
	in := f.open("corr-v1", fbReport())
	in.Correlation = &snap
	if err := f.b.PutIncident(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	f.clock.add(time.Hour)
	out, err := f.mark("corr-v1", Outcome{Result: ResultResolved,
		Correlation: &CorrelationFeedback{CandidateID: "c1", Verdict: VerdictConfirmed}})
	if err != nil {
		t.Fatalf("verdict on a version 1 snapshot: %v", err)
	}
	if out.Correlation == nil || out.Correlation.Version != 1 {
		t.Errorf("stored snapshot = %+v: reading must not rewrite its version", out.Correlation)
	}
}
