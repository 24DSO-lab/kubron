// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package feedback_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/feedback"
)

func TestExportAnonymousAndDeterministic(t *testing.T) {
	t.Parallel()
	var recs []feedback.Record
	for _, id := range []string{"MARK-INCIDENT-1", "MARK-INCIDENT-2"} {
		r := resolvedWith(id, hOOM, time.Hour)
		r.RecurrenceOf = "MARK-PREVIOUS"
		recs = append(recs, r)
	}
	st := feedback.Compute(recs, time.Date(2026, 11, 18, 12, 34, 56, 0, time.UTC))
	st.Truncated = true

	out, err := feedback.Export(st, 2)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, forbidden := range []string{"MARK-", "computedAt", "2026-11-18", "12:34"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("export leaks %q:\n%s", forbidden, text)
		}
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc["format"] != feedback.ExportFormat || doc["formatVersion"] != float64(feedback.ExportFormatVersion) ||
		doc["ruleSetVersion"] != float64(2) || doc["truncated"] != true || doc["records"] != float64(2) {
		t.Errorf("header = %v", doc)
	}

	// Порядок во входе не влияет на байты выгрузки.
	rev := st
	rev.Hypotheses = append([]feedback.HypothesisStat{{Ref: hApp, Confirmed: 1}}, st.Hypotheses...)
	rev2 := st
	rev2.Hypotheses = append(append([]feedback.HypothesisStat(nil), st.Hypotheses...), feedback.HypothesisStat{Ref: hApp, Confirmed: 1})
	a, _ := feedback.Export(rev, 2)
	b, _ := feedback.Export(rev2, 2)
	if string(a) != string(b) {
		t.Errorf("export depends on input order:\n%s\n%s", a, b)
	}
}

func TestExportKeepsNumbersBelowThreshold(t *testing.T) {
	t.Parallel()
	st := feedback.Compute([]feedback.Record{resolvedWith("a", hOOM, time.Hour)}, t0)
	out, err := feedback.Export(st, 2)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hypotheses []feedback.HypothesisStat `json:"hypotheses"`
	}
	if err := json.Unmarshal(out, &doc); err != nil || len(doc.Hypotheses) != 1 {
		t.Fatalf("%v %s", err, out)
	}
	if h := doc.Hypotheses[0]; h.Confirmed != 1 || h.Trusted || h.Precision != nil {
		t.Errorf("hypothesis = %+v", h)
	}
}

func TestExportEmptyHasNoNulls(t *testing.T) {
	t.Parallel()
	out, err := feedback.Export(feedback.Stats{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "null") {
		t.Errorf("null in empty export:\n%s", out)
	}
}
