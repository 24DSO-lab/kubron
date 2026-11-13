// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/opsplatform"
)

// Действие аудита операционного модуля называется по правилу «модуль + операция» (opsplatform.ActionName) и в записи
// об успехе, и в записи об отказе по праву. Эти тесты не дают именам разойтись: имена не пишут строкой, а для каждого
// значимого действия есть перевод во всех языках.

func TestAuditActionNamesOfOperationalModules(t *testing.T) {
	t.Parallel()
	cases := []struct{ module, operation, want string }{
		{moduleIncident, incOpOpen, "IncidentOpen"},
		{moduleIncident, incOpMarkOutcome, "IncidentMarkOutcome"},
		{moduleIncident, incOpSaveFragment, "IncidentSaveFragment"},
		{moduleIncident, incOpDeleteFragment, "IncidentDeleteFragment"},
		{moduleIncident, incOpSetLogCapture, "IncidentSetLogCapture"},
		{moduleIncident, incOpReadFragment, "IncidentReadFragment"},
		{moduleIncident, incOpExport, "IncidentExport"},
		{moduleIncident, incOpDelete, "IncidentDelete"},
		{moduleIncident, incOpSaveSettings, "IncidentSaveSettings"},
		{baselineModule, bsOpPreview, "BaselinePreview"},
		{baselineModule, bsOpCreate, "BaselineCreate"},
		{baselineModule, bsOpVersionAdd, "BaselineVersionAdd"},
		{baselineModule, bsOpActivate, "BaselineActivate"},
		{baselineModule, bsOpExceptionAdd, "BaselineExceptionAdd"},
		{baselineModule, bsOpExceptionRemove, "BaselineExceptionRemove"},
		{baselineModule, bsOpDelete, "BaselineDelete"},
		{baselineModule, bsOpCheck, "BaselineCheck"},
		{readinessModule, rdOpRun, "ReadinessRun"},
		{readinessModule, rdOpExport, "ReadinessExport"},
	}
	langs := []string{"en", "ru", "cn"}
	translations := map[string]map[string]json.RawMessage{}
	for _, lang := range langs {
		raw, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "i18n", "locales", lang, "audit.json"))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Actions map[string]json.RawMessage `json:"actions"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s/audit.json: %v", lang, err)
		}
		translations[lang] = doc.Actions
	}
	for _, c := range cases {
		if got := opsplatform.ActionName(c.module, c.operation); got != c.want {
			t.Errorf("ActionName(%q, %q) = %q, want %q", c.module, c.operation, got, c.want)
		}
		for _, lang := range langs {
			var text string // вложенные группы (portforward.start и т. п.) не строка — это не перевод действия
			if json.Unmarshal(translations[lang][c.want], &text) != nil || strings.TrimSpace(text) == "" {
				t.Errorf("audit.actions.%s has no %s translation", c.want, lang)
			}
		}
	}
}

// Имя действия в Op.Audit не пишут строкой: его строят из модуля и операции, иначе запись об успехе и запись об
// отказе по праву получат разные имена.
func TestOpAuditActionIsNotAStringLiteral(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	literal := regexp.MustCompile(`\bop\.Audit\(\s*"`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if literal.Match(raw) {
			t.Errorf("%s: op.Audit with a string literal; use opsplatform.ActionName(module, operation)", f)
		}
	}
}
