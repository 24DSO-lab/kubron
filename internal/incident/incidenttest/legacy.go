// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incidenttest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/incident"
)

// LegacyIncidentJSON — документ расследования в формате до ADR-0033 и ADR-0034: ни снимка корреляции, ни
// ProblemID, сигнатуры и повторения, ни расширенной отметки результата. Такие документы лежат в хранилищах
// существующих установок и должны читаться и записываться без ошибок (аддитивные поля, contracts.md §5).
const LegacyIncidentJSON = `{
  "id": "legacy1",
  "scope": {"OrgID": "org1", "TeamID": "team1", "ClusterID": "cl1"},
  "target": {"kind": "Deployment", "namespace": "shop", "name": "web"},
  "status": "closed",
  "openedAt": "2026-10-01T10:00:00Z",
  "updatedAt": "2026-10-01T11:00:00Z",
  "openedBy": "alice",
  "report": {
    "ruleSetVersion": 1,
    "target": {"kind": "Deployment", "namespace": "shop", "name": "web"},
    "observedAt": "2026-10-01T10:00:00Z",
    "symptoms": ["pod.crash_loop"],
    "resources": [{"kind": "Pod", "namespace": "shop", "name": "web-1"}],
    "facts": [{"kind": "fact", "id": "f1", "code": "pod.restarts"}],
    "hypotheses": [{"kind": "hypothesis", "id": "h1", "code": "app.crash", "confidence": "medium"}],
    "recommendations": [{"kind": "recommendation", "id": "r1", "code": "check.logs"}],
    "steps": [],
    "missing": []
  },
  "events": [{"reason": "BackOff", "message": "back-off", "count": 2, "lastSeen": "2026-10-01T09:59:00Z"}],
  "eventsUpdatedAt": "2026-10-01T10:00:00Z",
  "saveLogs": true,
  "evidence": [],
  "outcome": {"result": "resolved", "confirmedHypothesisId": "h1", "note": "fixed", "markedBy": "alice", "markedAt": "2026-10-01T11:00:00Z"},
  "version": 3
}`

// newKeys — ключи JSON, появившиеся в расширении ADR-0033/0034. У документа старого формата их нет, и запись
// такого документа без изменений их не добавляет (все поля omitempty).
var newKeys = []string{`"correlation"`, `"problemId"`, `"signature"`, `"recurrenceOf"`, `"rejectedHypothesisIds"`,
	`"helpfulRecommendationIds"`, `"uselessRecommendationIds"`, `"otherAction"`, `"resolvedAt"`}

// AssertNoNewKeys проверяет, что в сериализованном виде нет ключей расширения. Ключ ruleSetVersion сюда не входит:
// он есть и в отчёте старого формата, а Summary выводит из него значение.
func AssertNoNewKeys(t *testing.T, what string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	for _, k := range newKeys {
		if strings.Contains(string(raw), k) {
			t.Errorf("%s: unexpected key %s in %s", what, k, raw)
		}
	}
}

// Legacy возвращает документ старого формата, разобранный в Incident, с обнулённой версией (для создания).
func Legacy(t *testing.T) *incident.Incident {
	t.Helper()
	var in incident.Incident
	if err := json.Unmarshal([]byte(LegacyIncidentJSON), &in); err != nil {
		t.Fatalf("legacy document does not parse: %v", err)
	}
	in.Version = 0
	return &in
}

// legacy — контрактный случай: документ старого формата сохраняется, читается, меняется по версии и попадает
// в историю; новые поля пусты и в JSON не появляются.
func legacy(t *testing.T, b incident.Backend) {
	ctx := t.Context()
	in := Legacy(t)
	AssertNoNewKeys(t, "parsed legacy document", in)
	must(t, b.PutIncident(ctx, in))
	got, err := b.GetIncident(ctx, Scope, "legacy1")
	must(t, err)
	if got.Correlation != nil || got.ProblemID != "" || got.Signature != "" || got.RecurrenceOf != "" ||
		got.Outcome == nil || got.Outcome.ResolvedAt != nil || got.Outcome.Correlation != nil ||
		len(got.Outcome.RejectedHypothesisIDs) != 0 || got.Outcome.Result != incident.ResultResolved {
		t.Errorf("legacy document read back with unexpected fields: %+v", got)
	}
	AssertNoNewKeys(t, "stored legacy document", got)

	// Замена по версии: изменение документа старого формата не добавляет новых ключей.
	got.UpdatedAt = got.UpdatedAt.Add(1)
	must(t, b.PutIncident(ctx, got))
	again, err := b.GetIncident(ctx, Scope, "legacy1")
	must(t, err)
	if again.Version != 2 {
		t.Errorf("version = %d", again.Version)
	}
	AssertNoNewKeys(t, "rewritten legacy document", again)

	page, err := b.ListIncidents(ctx, Scope, incident.ListQuery{})
	must(t, err)
	if len(page.Items) != 1 || page.Items[0].ID != "legacy1" || page.Items[0].Result != incident.ResultResolved ||
		page.Items[0].ProblemID != "" || page.Items[0].ResolvedAt != nil || page.Items[0].Confirmed != nil {
		t.Errorf("history row = %+v", page.Items)
	}
	AssertNoNewKeys(t, "history row of a legacy document", page.Items[0])
}
