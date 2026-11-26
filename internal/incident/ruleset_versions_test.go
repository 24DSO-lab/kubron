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

// Подъём diagnose.RuleSetVersion до 3 и correlation.Version до 2 не ломает ранее сохранённые данные: отчёты версий
// 1 и 2 и снимок корреляции версии 1 читаются как записаны (версия записи не переписывается), статистика и
// экспорт обратной связи различают версии, а выгрузка называет версию текущей сборки.
func TestStoredOlderVersionsStayReadableAndCounted(t *testing.T) {
	t.Parallel()
	if diagnose.RuleSetVersion < 3 || correlation.Version < 2 {
		t.Fatalf("versions = %d/%d, want at least 3/2 after the second iteration", diagnose.RuleSetVersion, correlation.Version)
	}
	f := newFbFx(t)
	reportOf := func(v int) diagnose.Report {
		r := fbReport()
		r.RuleSetVersion = v
		return r
	}

	f.closeAfter("v1", reportOf(1), time.Hour, Outcome{Result: ResultResolved})

	// Версия 2 со снимком корреляции версии 1 (до правил shared_dependency/shared_root/shared_symptom).
	old := f.open("v2", reportOf(2))
	old.Correlation = &correlation.Result{Version: 1, ObservedAt: tBase}
	if err := f.b.PutIncident(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	f.clock.add(time.Hour)
	if _, err := f.mark("v2", Outcome{Result: ResultResolved}); err != nil {
		t.Fatal(err)
	}
	f.closeAfter("v3", reportOf(diagnose.RuleSetVersion), time.Hour, Outcome{Result: ResultResolved})

	// Чтение: версии отчёта и снимка остаются теми, что записаны.
	for id, want := range map[string]int{"v1": 1, "v2": 2, "v3": diagnose.RuleSetVersion} {
		in, err := f.b.GetIncident(t.Context(), tScope, id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if in.Report.RuleSetVersion != want {
			t.Errorf("%s: report ruleSetVersion = %d, want %d", id, in.Report.RuleSetVersion, want)
		}
	}
	in, err := f.b.GetIncident(t.Context(), tScope, "v2")
	if err != nil {
		t.Fatal(err)
	}
	if in.Correlation == nil || in.Correlation.Version != 1 {
		t.Errorf("stored correlation snapshot = %+v, want it untouched at version 1", in.Correlation)
	}

	// Статистика: версия 1 без RuleID в выборку не входит, версии 2 и 3 — входят и различимы.
	st, err := f.svc.RuleStats(t.Context(), tScope)
	if err != nil {
		t.Fatal(err)
	}
	if st.Records != 2 {
		t.Errorf("records = %d, want 2 (v2 and v3; v1 has no rule ids)", st.Records)
	}
	if want := []int{2, diagnose.RuleSetVersion}; !slices.Equal(st.RuleSetVersions, want) {
		t.Errorf("ruleSetVersions = %v, want %v", st.RuleSetVersions, want)
	}

	// Экспорт: версия сборки — текущая, версии выборки — все, что в ней есть.
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
