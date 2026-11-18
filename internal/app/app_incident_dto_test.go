// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/incident"
)

func jsonKeys(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// Фронтенд (D8) зеркалит эти ключи в frontend/src/types/incident.ts: тест не даёт им разойтись с контрактом.
func TestProblemDetailDTOJSONShape(t *testing.T) {
	d := ProblemDetailDTO{
		ProblemSummaryDTO: ProblemSummaryDTO{ID: "p1", Key: "shared_node:core/Node//n1", Kind: "shared_node",
			Source: diagnose.ResourceRef{Kind: "Node", Name: "n1"}, Status: incident.ProblemStatus("open"),
			Origin: incident.ProblemOrigin("auto"), MemberCount: 2, AffectedTotal: 3, UpdatedAt: time.Unix(0, 0).UTC()},
		Members:  []ProblemMemberDTO{},
		Affected: []correlation.WorkloadRef{},
		Detached: []string{},
	}
	m := jsonKeys(t, d)
	for _, k := range []string{"id", "key", "kind", "source", "status", "origin", "memberCount", "affectedTotal",
		"updatedAt", "members", "affected", "affectedTruncated", "detached"} {
		if _, ok := m[k]; !ok {
			t.Errorf("ProblemDetailDTO has no %q key", k)
		}
	}
	for _, k := range []string{"members", "affected", "detached"} {
		if string(m[k]) != "[]" {
			t.Errorf("%s must be an empty array, got %s", k, m[k])
		}
	}
}

func TestExportFileDTOJSONShape(t *testing.T) {
	m := jsonKeys(t, ExportFileDTO{Filename: "rule-stats.json", MIME: "application/json", DataBase64: "e30="})
	for _, k := range []string{"filename", "mime", "dataBase64"} {
		if _, ok := m[k]; !ok {
			t.Errorf("ExportFileDTO has no %q key", k)
		}
	}
}

func TestOutcomeRequestCarriesFeedbackFields(t *testing.T) {
	at := time.Unix(100, 0).UTC()
	cf := &incident.CorrelationFeedback{CandidateID: "c1", Verdict: incident.VerdictConfirmed}
	out := IncidentOutcomeRequest{
		Result: incident.ResultResolved, ConfirmedHypothesisID: "h1", Note: "n",
		RejectedHypothesisIDs: []string{"h2"}, HelpfulRecommendationIDs: []string{"r1"}, UselessRecommendationIDs: []string{"r2"},
		OtherAction: true, ResolvedAt: &at, Correlation: cf,
	}.outcome()
	if out.Result != incident.ResultResolved || out.ConfirmedHypothesisID != "h1" || out.Note != "n" ||
		len(out.RejectedHypothesisIDs) != 1 || len(out.HelpfulRecommendationIDs) != 1 || len(out.UselessRecommendationIDs) != 1 ||
		!out.OtherAction || out.ResolvedAt != &at || out.Correlation != cf {
		t.Fatalf("outcome lost fields: %+v", out)
	}
}
