// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Каждый код, который могут выдать правила, обязан иметь перевод в en, ru и cn: интерфейс показывает текст по
// коду, и непереведённый код превратился бы в «сырую» строку. Тест читает исходники правил и locales фронтенда.
func TestEveryCodeIsTranslated(t *testing.T) {
	t.Parallel()
	var src strings.Builder
	for _, f := range []string{"rules.go", "rollout.go", "engine.go", "collect.go"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(raw)
	}
	codes := map[string]map[string]bool{"facts": {"event": true}, "hypotheses": {}, "recommendations": {}, "steps": {}, "missing": {}, "symptoms": {}}
	add := func(section string, re string, group int) {
		for _, m := range regexp.MustCompile(re).FindAllStringSubmatch(src.String(), -1) {
			codes[section][m[group]] = true
		}
	}
	add("facts", `b\.fact(?:Log)?\(\s*[^,]+,\s*"([^"]+)"`, 1)
	add("recommendations", `"(rec\.[a-z_]+)"`, 1)
	add("steps", `"(step\.[a-z_]+)"`, 1)
	add("missing", `(?:b\.missing|c\.addMissing|col\.c\.addMissing)\(\s*"([a-z_.]+)"`, 1)
	add("hypotheses", `"((?:crash|oom|startup|config|image|pending|rollout)\.[a-z_]+)"`, 1)
	// Коды, которые есть только среди «недостающего контекста», не гипотезы.
	for c := range codes["missing"] {
		delete(codes["hypotheses"], c)
	}
	for _, s := range []string{SymptomPending, SymptomCrashLoop, SymptomOOMKilled, SymptomStartupFailure, SymptomRollout, SymptomNone} {
		codes["symptoms"][s] = true
	}
	for _, id := range RuleIDs() { // идентификаторы правил — не коды отчёта
		delete(codes["hypotheses"], id)
	}
	if len(codes["hypotheses"]) < 20 || len(codes["facts"]) < 15 {
		t.Fatalf("code extraction found too little: %d hypotheses, %d facts", len(codes["hypotheses"]), len(codes["facts"]))
	}
	for _, lang := range []string{"en", "ru", "cn"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "i18n", "locales", lang, "incidents.json"))
		if err != nil {
			t.Fatal(err)
		}
		var msgs map[string]any
		if err := json.Unmarshal(raw, &msgs); err != nil {
			t.Fatal(err)
		}
		for section, set := range codes {
			for code := range set {
				key := strings.ReplaceAll(code, ".", "_")
				sec, _ := msgs[section].(map[string]any)
				if v, ok := sec[key].(string); !ok || v == "" {
					t.Errorf("%s: no translation incidents.%s.%s for code %q", lang, section, key, code)
				}
			}
		}
	}
}
