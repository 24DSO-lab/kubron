// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

func TestCandidateKey(t *testing.T) {
	tests := []struct {
		name string
		c    correlation.Candidate
		want string
	}{
		{"node", correlation.Candidate{Kind: correlation.KindSharedNode, Source: diagnose.ResourceRef{Kind: "Node", Name: "n1"}},
			"shared_node:core/Node//n1"},
		{"storageclass", correlation.Candidate{Kind: correlation.KindSharedStorage, Source: diagnose.ResourceRef{Kind: "StorageClass", Name: "fast"}},
			"shared_storage:storage.k8s.io/StorageClass//fast"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.Key(); got != tt.want {
				t.Fatalf("Key() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResultBest(t *testing.T) {
	var nilRes *correlation.Result
	if nilRes.Best() != nil {
		t.Fatal("Best() у nil-результата должен быть nil")
	}
	if (&correlation.Result{}).Best() != nil {
		t.Fatal("Best() без кандидатов должен быть nil")
	}
	r := &correlation.Result{Candidates: []correlation.Candidate{{ID: "a"}, {ID: "b"}}}
	if got := r.Best(); got == nil || got.ID != "a" {
		t.Fatalf("Best() = %+v, want кандидат a", got)
	}
}

func TestRunSkeleton(t *testing.T) {
	res, err := correlation.Run(context.Background(), nil, nil, time.Time{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res == nil || res.Version != correlation.Version || len(res.Candidates) != 0 {
		t.Fatalf("неожиданный результат скелета: %+v", res)
	}
}

// TestJSONContract фиксирует имена полей JSON (contracts.md §4): camelCase, omitempty там, где уместно.
func TestJSONContract(t *testing.T) {
	since := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	r := correlation.Result{
		Version:    correlation.Version,
		ObservedAt: since,
		Candidates: []correlation.Candidate{{
			ID: "c1", Kind: correlation.KindSharedNode, Confidence: diagnose.ConfidenceMedium,
			Source: diagnose.ResourceRef{Kind: "Node", Name: "n1"}, Since: since, Total: 1,
			Affected: []correlation.WorkloadRef{{Kind: "Deployment", Namespace: "ns", Name: "web", ProblemPods: 2,
				Symptoms: []string{"pod.crash_loop"}, Since: since}},
			Supporting: []string{"f1"},
		}},
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"version", "observedAt", "candidates"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("в JSON Result нет поля %q", k)
		}
	}
	for _, k := range []string{"facts", "missing", "partial"} {
		if _, ok := raw[k]; ok {
			t.Errorf("пустое поле %q должно быть опущено (omitempty)", k)
		}
	}
	cand := raw["candidates"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "kind", "source", "confidence", "since", "affected", "total", "supporting"} {
		if _, ok := cand[k]; !ok {
			t.Errorf("в JSON Candidate нет поля %q", k)
		}
	}
	for _, k := range []string{"truncated", "potential"} {
		if _, ok := cand[k]; ok {
			t.Errorf("пустое поле %q должно быть опущено (omitempty)", k)
		}
	}
	w := cand["affected"].([]any)[0].(map[string]any)
	for _, k := range []string{"kind", "namespace", "name", "problemPods", "symptoms", "since"} {
		if _, ok := w[k]; !ok {
			t.Errorf("в JSON WorkloadRef нет поля %q", k)
		}
	}

	// Обратная совместимость: минимальный документ читается без ошибок.
	var old correlation.Result
	if err := json.Unmarshal([]byte(`{"version":1,"observedAt":"2026-11-17T10:00:00Z"}`), &old); err != nil {
		t.Fatalf("чтение минимального JSON: %v", err)
	}
	if old.Best() != nil {
		t.Fatal("у результата без кандидатов Best() должен быть nil")
	}
}
