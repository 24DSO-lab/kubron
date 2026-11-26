// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package feedback_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/feedback"
)

var update = flag.Bool("update", false, "rewrite testdata/stats.example.json")

// TestStatsExampleGolden хранит пример JSON feedback.Stats для фикстур фронтенда (D8): те же типы и поля, что
// отдаёт RPC IncidentRuleStats. Обновление: go test ./internal/feedback -run Golden -update.
func TestStatsExampleGolden(t *testing.T) {
	var recs []feedback.Record
	for i := range 6 {
		r := resolvedWith(fmt.Sprint("e", i), hOOM, time.Duration(20+i)*time.Minute, hApp)
		r.Helpful = []feedback.Ref{rBmp}
		r.Features = []string{feedback.FeatureMetrics}
		if i == 0 {
			r.Symptoms = []string{"pod.oom_killed", "pod.crash_loop"}
		}
		recs = append(recs, r)
	}
	recs[1].RecurrenceOf = "e0"
	for i := range 2 {
		recs = append(recs, feedback.Record{ID: fmt.Sprint("u", i), Result: "unresolved", OpenedAt: t0, RuleSetVersion: 2,
			Symptoms: []string{"pod.pending"}, Useless: []feedback.Ref{rBmp}})
	}
	recs = append(recs, feedback.Record{ID: "n", Result: "inconclusive", OpenedAt: t0, RuleSetVersion: 2, Symptoms: []string{"pod.pending"}})
	st := feedback.Compute(recs, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(st); err != nil {
		t.Fatal(err)
	}
	const path = "testdata/stats.example.json"
	if *update {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("stats JSON differs from %s (run with -update):\n%s", path, buf.Bytes())
	}
}
