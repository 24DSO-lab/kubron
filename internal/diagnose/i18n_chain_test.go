// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Каждый код, который выдают цепочечные правила и сбор этапа 1, обязан иметь перевод в en, ru и cn. Коды ищутся в
// исходниках этапа 1; перевод — в incidentsChain.json или (для кодов, которые правила этапа 1 делят с прежними) в
// incidents.json. Так непереведённый код не превратится в «сырую» строку в интерфейсе.
func TestEveryChainCodeIsTranslated(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("rules_chain_*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "targets_chain.go", "graph_hook.go")
	var src strings.Builder
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(raw)
	}
	codes := map[string]map[string]bool{"facts": {}, "hypotheses": {}, "recommendations": {}, "steps": {}, "missing": {}}
	add := func(section, re string) {
		for _, m := range regexp.MustCompile(re).FindAllStringSubmatch(src.String(), -1) {
			codes[section][m[1]] = true
		}
	}
	add("facts", `b\.fact(?:Log)?\(\s*[^,]+,\s*"([^"]+)"`)
	add("recommendations", `"(rec\.[a-z_]+)"`)
	add("steps", `"(step\.[a-z_]+)"`)
	add("missing", `(?:b\.missing|c\.addMissing|col\.c\.addMissing)\(\s*"([a-z_.]+)"`)
	add("hypotheses", `"((?:pvc|storage|service|endpoints|node|volume|chain|oom)\.[a-z_]+)"`)
	for c := range codes["missing"] {
		delete(codes["hypotheses"], c)
	}
	for _, id := range RuleIDs() { // идентификаторы правил — не коды отчёта
		delete(codes["hypotheses"], id)
	}
	if len(codes["missing"]) == 0 {
		t.Fatal("code extraction found no missing-context codes")
	}
	for _, lang := range []string{"en", "ru", "cn"} {
		msgs := map[string]map[string]any{}
		for _, file := range []string{"incidents.json", "incidentsChain.json"} {
			raw, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "i18n", "locales", lang, file))
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("%s/%s: %v", lang, file, err)
			}
			for section, v := range m {
				if sec, ok := v.(map[string]any); ok {
					if msgs[section] == nil {
						msgs[section] = map[string]any{}
					}
					for k, x := range sec {
						msgs[section][k] = x
					}
				}
			}
		}
		for section, set := range codes {
			for code := range set {
				key := strings.ReplaceAll(code, ".", "_")
				if v, ok := msgs[section][key].(string); !ok || v == "" {
					t.Errorf("%s: no translation for %s code %q (incidentsChain.%s.%s)", lang, section, code, section, key)
				}
			}
		}
	}
}

// Наборы ключей и плейсхолдеров incidentsChain.json совпадают во всех языках: тест дублирует i18n-check для Go-стороны
// и ловит расхождение до сборки фронтенда.
func TestChainLocalesHaveTheSameKeys(t *testing.T) {
	t.Parallel()
	load := func(lang string) map[string]string {
		raw, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "i18n", "locales", lang, "incidentsChain.json"))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v (values must be plain strings)", lang, err)
		}
		out := map[string]string{}
		for section, kv := range m {
			for k, v := range kv {
				out[section+"."+k] = v
			}
		}
		return out
	}
	placeholders := func(s string) string {
		var ph []string
		for _, m := range regexp.MustCompile(`\{[a-zA-Z0-9_]+\}`).FindAllString(s, -1) {
			ph = append(ph, m)
		}
		slices.Sort(ph)
		return strings.Join(ph, ",")
	}
	en := load("en")
	for _, lang := range []string{"ru", "cn"} {
		other := load(lang)
		for k, v := range en {
			o, ok := other[k]
			if !ok {
				t.Errorf("%s: key %s is missing", lang, k)
				continue
			}
			if placeholders(v) != placeholders(o) {
				t.Errorf("%s: placeholders of %s differ: en %q, %s %q", lang, k, v, lang, o)
			}
		}
		for k := range other {
			if _, ok := en[k]; !ok {
				t.Errorf("%s: extra key %s", lang, k)
			}
		}
	}
}
