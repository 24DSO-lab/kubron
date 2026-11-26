// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Сохранённые отчёты прежних версий набора правил (testdata/compat) читаются как есть: версия, с которой построен
// отчёт, не подменяется текущей, отсутствующие новые поля пусты, повторная запись не теряет данных. Отчёт версии 2
// снят с набора до подъёма до 3, версии 1 — до введения RuleID и графа.
func TestStoredReportsOfOlderRuleSetsStayReadable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		file        string
		version     int
		wantRuleIDs bool
		wantGraph   bool
	}{{"report_v1.json", 1, false, false}, {"report_v2.json", 2, true, true}} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(filepath.Join("testdata", "compat", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			var rep Report
			if err := json.Unmarshal(raw, &rep); err != nil {
				t.Fatalf("old report does not decode: %v", err)
			}
			if rep.RuleSetVersion != tc.version || rep.RuleSetVersion >= RuleSetVersion {
				t.Errorf("version = %d, want %d (current %d)", rep.RuleSetVersion, tc.version, RuleSetVersion)
			}
			if len(rep.Hypotheses) == 0 {
				t.Fatal("hypotheses were lost")
			}
			for _, h := range rep.Hypotheses {
				if (h.RuleID != "") != tc.wantRuleIDs {
					t.Errorf("hypothesis %s: ruleId = %q, want present=%v", h.Code, h.RuleID, tc.wantRuleIDs)
				}
			}
			if (rep.Graph != nil) != tc.wantGraph {
				t.Errorf("graph present = %v, want %v", rep.Graph != nil, tc.wantGraph)
			}
			_ = rep.RootCandidates() // не падает на отчёте без Explains
			back, err := json.Marshal(rep)
			if err != nil {
				t.Fatal(err)
			}
			var again Report
			if err := json.Unmarshal(back, &again); err != nil || again.RuleSetVersion != tc.version {
				t.Errorf("rewrite changed the version: %d, %v", again.RuleSetVersion, err)
			}
		})
	}
}
