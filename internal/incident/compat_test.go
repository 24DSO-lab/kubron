// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/incident/incidenttest"
)

// Документ, записанный версией до ADR-0033/0034, лежит в файловом хранилище как есть: читается, попадает в
// историю, получает отметку результата через сервис и переписывается без новых ключей.
func TestLegacyDocumentOnDisk(t *testing.T) {
	root := t.TempDir()
	sc := incidenttest.Scope
	dir := filepath.Join(root, sc.OrgID, sc.TeamID, sc.ClusterID, "incidents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "legacy1.json"), []byte(incidenttest.LegacyIncidentJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	b := incident.NewFileBackend(root)

	got, err := b.GetIncident(t.Context(), sc, "legacy1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 3 || got.Status != incident.StatusClosed || got.Correlation != nil || got.ProblemID != "" {
		t.Errorf("legacy document = %+v", got)
	}
	page, err := b.ListIncidents(t.Context(), sc, incident.ListQuery{})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "legacy1" || page.Items[0].RuleSetVersion != 1 {
		t.Fatalf("history = %+v, %v", page, err)
	}
	incidenttest.AssertNoNewKeys(t, "history row", page.Items[0])

	// Запись по версии: на диске по-прежнему нет ключей расширения.
	if err := b.PutIncident(t.Context(), got); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "legacy1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var back incident.Incident
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	incidenttest.AssertNoNewKeys(t, "rewritten file", back)
	if back.Version != 4 {
		t.Errorf("version = %d", back.Version)
	}
}

// Summarize старого документа не падает и не заполняет поля расширения, кроме выводимых из отчёта.
func TestSummarizeLegacyDocument(t *testing.T) {
	in := incidenttest.Legacy(t)
	s := in.Summarize()
	if s.ProblemID != "" || s.Signature != "" || s.RecurrenceOf != "" || s.ResolvedAt != nil || s.Confirmed != nil ||
		len(s.Rejected) != 0 || len(s.Helpful) != 0 || len(s.Useless) != 0 || s.CorrelationVerdict != nil {
		t.Errorf("summary of a legacy document has extension fields: %+v", s)
	}
	if s.RuleSetVersion != 1 || s.Result != incident.ResultResolved || len(s.Symptoms) != 1 {
		t.Errorf("summary = %+v", s)
	}
}

// Расширенные поля проходят круг «запись — чтение» без потерь (файловое хранилище, JSON camelCase).
func TestExtensionFieldsRoundTrip(t *testing.T) {
	b := incident.NewFileBackend(t.TempDir())
	in := incidenttest.NewIncident(incidenttest.Scope, "ext1", testBase)
	resolved := testBase.Add(time.Hour)
	in.ProblemID, in.Signature, in.RecurrenceOf = "p1", "sig", "old1"
	in.Outcome = &incident.Outcome{Result: incident.ResultResolved, RejectedHypothesisIDs: []string{"h2"},
		HelpfulRecommendationIDs: []string{"r1"}, UselessRecommendationIDs: []string{"r2"}, OtherAction: true,
		ResolvedAt: &resolved, Correlation: &incident.CorrelationFeedback{CandidateID: "c1", Verdict: incident.VerdictConfirmed}}
	if err := b.PutIncident(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	got, err := b.GetIncident(t.Context(), incidenttest.Scope, "ext1")
	if err != nil {
		t.Fatal(err)
	}
	o := got.Outcome
	if got.ProblemID != "p1" || got.Signature != "sig" || got.RecurrenceOf != "old1" || o.ResolvedAt == nil ||
		!o.ResolvedAt.Equal(resolved) || !o.OtherAction || o.Correlation == nil || o.Correlation.CandidateID != "c1" ||
		len(o.RejectedHypothesisIDs) != 1 || len(o.HelpfulRecommendationIDs) != 1 || len(o.UselessRecommendationIDs) != 1 {
		t.Errorf("extension fields lost: %+v %+v", got, o)
	}
	s := got.Summarize()
	if s.ProblemID != "p1" || s.Signature != "sig" || s.RecurrenceOf != "old1" || s.ResolvedAt == nil || !s.ResolvedAt.Equal(resolved) {
		t.Errorf("summary = %+v", s)
	}
}
