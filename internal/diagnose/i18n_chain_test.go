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

var chainLangs = []string{"en", "ru", "cn"}

// chainMessages — переводы языка из incidents.json и incidentsChain.json, слитые по разделам: цепочечные правила
// делят часть кодов с прежними (source.unavailable, event, rec.fix_volume_claim), остальное лежит в incidentsChain.json.
func chainMessages(t *testing.T, lang string) map[string]map[string]any {
	t.Helper()
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
			sec, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if msgs[section] == nil {
				msgs[section] = map[string]any{}
			}
			for k, x := range sec {
				msgs[section][k] = x
			}
		}
	}
	return msgs
}

func translated(msgs map[string]map[string]any, section, code string) bool {
	v, ok := msgs[section][strings.ReplaceAll(code, ".", "_")].(string)
	return ok && v != ""
}

// Каждый код из исходников этапа 1 переведён на en, ru и cn. Исходники не различают разделы надёжно (код факта
// может прийти в помощник строкой), поэтому здесь код должен быть хотя бы в одном из разделов «факт, гипотеза,
// симптом, пропуск»; рекомендации и шаги проверяются по своим разделам строго, а точное соответствие разделам
// проверяет TestChainFixtureCodesAreTranslated по настоящим отчётам.
func TestEveryChainCodeIsTranslated(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("rules_chain_*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "targets_chain.go", "graph_hook.go", "chains.go")
	var src strings.Builder
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(raw)
	}
	find := func(re string) map[string]bool {
		out := map[string]bool{}
		for _, m := range regexp.MustCompile(re).FindAllStringSubmatch(src.String(), -1) {
			out[m[1]] = true
		}
		return out
	}
	recs := find(`"(rec\.[a-z_]+)"`)
	steps := find(`"(step\.[a-z_]+)"`)
	missing := find(`(?:b\.missing|c\.addMissing|col\.c\.addMissing)\(\s*"([a-z_.]+)"`)
	dotted := find(`"((?:pvc|storage|service|endpoints|node|volume|chain|oom)\.[a-z_]+)"`)
	symptoms := find(`Symptom[A-Za-z]+\s*=\s*"([a-z_.]+)"`)
	for _, id := range RuleIDs() { // идентификаторы правил — не коды отчёта
		delete(dotted, id)
	}
	if len(missing) == 0 || len(dotted) == 0 || len(symptoms) == 0 {
		t.Fatalf("code extraction found nothing: missing=%v dotted=%v symptoms=%v", missing, dotted, symptoms)
	}
	for _, lang := range chainLangs {
		msgs := chainMessages(t, lang)
		for code := range recs {
			if !translated(msgs, "recommendations", code) {
				t.Errorf("%s: no translation for recommendation %q", lang, code)
			}
		}
		for code := range steps {
			if !translated(msgs, "steps", code) {
				t.Errorf("%s: no translation for step %q", lang, code)
			}
		}
		for code := range missing {
			if !translated(msgs, "missing", code) {
				t.Errorf("%s: no translation for missing-context code %q", lang, code)
			}
		}
		for code := range symptoms {
			if !translated(msgs, "symptoms", code) {
				t.Errorf("%s: no translation for symptom %q", lang, code)
			}
		}
		for code := range dotted {
			if !slices.ContainsFunc([]string{"facts", "hypotheses", "symptoms", "missing"}, func(s string) bool { return translated(msgs, s, code) }) {
				t.Errorf("%s: code %q has no translation in facts, hypotheses, symptoms or missing", lang, code)
			}
		}
	}
}

// Точная проверка по разделам: каждый код настоящих отчётов фикстур переведён в своём разделе.
func TestChainFixtureCodesAreTranslated(t *testing.T) {
	t.Parallel()
	reports := map[string]Report{}
	for name, c := range loadChainCases(t) {
		reports[name] = Analyze(chainContext(t, name, c))
	}
	for _, lang := range chainLangs {
		msgs := chainMessages(t, lang)
		for name, rep := range reports {
			check := func(section, code string) {
				if !translated(msgs, section, code) {
					t.Errorf("%s: %s: no translation for %s code %q", lang, name, section, code)
				}
			}
			for _, s := range rep.Symptoms {
				check("symptoms", s)
			}
			for _, f := range rep.Facts {
				check("facts", f.Code)
			}
			for _, h := range rep.Hypotheses {
				check("hypotheses", h.Code)
			}
			for _, r := range rep.Recommendations {
				check("recommendations", r.Code)
			}
			for _, s := range rep.Steps {
				check("steps", s.Code)
			}
			for _, m := range rep.Missing {
				check("missing", m.Code)
			}
			// Состояния и причины звеньев цепочки показываются словарями incidentsChain.
			for _, ch := range rep.Chains {
				for _, st := range ch.Path {
					if v, _ := msgs["graphHealth"][st.Health].(string); v == "" {
						t.Errorf("%s: %s: no translation for graph health %q", lang, name, st.Health)
					}
					if st.Reason != "" {
						if v, _ := msgs["graphReasons"][st.Reason].(string); v == "" {
							t.Errorf("%s: %s: no translation for graph reason %q", lang, name, st.Reason)
						}
					}
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
		ph := regexp.MustCompile(`\{[a-zA-Z0-9_]+\}`).FindAllString(s, -1)
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

// Состояния и причины вершин графа берутся из исходников depgraph: новое значение без перевода не доходит до
// пользователя пустым словом в цепочке (так был найден пропуск «Suspended»).
func TestEveryGraphHealthAndReasonIsTranslated(t *testing.T) {
	t.Parallel()
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join("..", "depgraph", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	var healths, reasons []string
	for _, m := range regexp.MustCompile(`\bHealth\s*=\s*"([A-Za-z]+)"`).FindAllStringSubmatch(read("model.go"), -1) {
		healths = append(healths, m[1])
	}
	for _, m := range regexp.MustCompile(`\bReason[A-Za-z]+\s*=\s*"([a-z_]+)"`).FindAllStringSubmatch(read("health.go"), -1) {
		reasons = append(reasons, m[1])
	}
	if len(healths) < 6 || len(reasons) < 20 {
		t.Fatalf("extraction found too little: %d healths, %d reasons", len(healths), len(reasons))
	}
	for _, lang := range chainLangs {
		msgs := chainMessages(t, lang)
		for _, h := range healths {
			if v, _ := msgs["graphHealth"][h].(string); v == "" {
				t.Errorf("%s: no translation for graph health %q", lang, h)
			}
		}
		for _, r := range reasons {
			if v, _ := msgs["graphReasons"][r].(string); v == "" {
				t.Errorf("%s: no translation for graph reason %q", lang, r)
			}
		}
	}
}
