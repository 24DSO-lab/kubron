// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Эталон набора правил версии 1 (testdata/legacy.sha256): SHA-256 отчёта каждого сценария, снятый ДО введения
// RuleID/RuleEpoch и графа. Контракт этапа 1 (ADR-0031): по тем же входам отчёт равен прежнему, кроме НОВЫХ полей.
// Тест убирает новые поля, приводит ruleSetVersion к 1 и сверяет хэш, поэтому любое незапланированное изменение
// правил или кодов видно сразу. При расхождении печатается полный отчёт; чтобы увидеть разницу, снимите его же с
// базового коммита. Хэш, а не файл отчёта, выбран ради компактности: эталон — 24 строки, а не 24 документа.
//
// Менять эталон можно только вместе с намеренным изменением правила: тогда поднимается RuleSetVersion (и, при
// изменении смысла, эпоха правила), а отличие описывается в ADR-0034. Пересоздать:
//
//	KUBRON_UPDATE_LEGACY=1 go test ./internal/diagnose -run TestLegacyGolden
const legacySums = "testdata/legacy.sha256"

// legacyNewFields — поля отчёта, появившиеся после версии 1 (на каждом уровне вложенности).
var legacyNewFields = []string{"ruleId", "ruleEpoch", "explains", "group", "signal", "graph", "chains"}

func stripNew(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for _, k := range legacyNewFields {
			delete(x, k)
		}
		for k, e := range x {
			x[k] = stripNew(e)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = stripNew(e)
		}
		return x
	}
	return v
}

func legacyReport(t *testing.T, sc scenario) ([]byte, bool) {
	t.Helper()
	r := newFakeReader(sc.Objects, sc.Logs)
	for k, v := range sc.Expect.Reader {
		r.errs[k] = errFor(v)
	}
	c, err := Collect(t.Context(), r, sc.Expect.Target, CollectOptions{Logs: true, Now: func() time.Time { return fixedNow }})
	if err != nil {
		if sc.Expect.Error != "" && errors.Is(err, errFor(sc.Expect.Error)) {
			return nil, false
		}
		t.Fatalf("collect: %v", err)
	}
	raw, err := json.Marshal(Analyze(c))
	if err != nil {
		t.Fatal(err)
	}
	return raw, true
}

func normalizeJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	m, _ := v.(map[string]any)
	m["ruleSetVersion"] = float64(1)
	out, err := json.Marshal(stripNew(m)) // ключи map отсортированы: вывод детерминирован
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

func readLegacySums(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(legacySums)
	if err != nil {
		t.Fatalf("нет эталона (создаётся через KUBRON_UPDATE_LEGACY=1): %v", err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatalf("плохая строка эталона: %q", line)
		}
		out[name] = sum
	}
	return out
}

func legacySum(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func TestLegacyGolden(t *testing.T) {
	t.Parallel()
	update := os.Getenv("KUBRON_UPDATE_LEGACY") != ""
	var want map[string]string
	if !update {
		want = readLegacySums(t)
	}
	var mu sync.Mutex
	got := map[string]string{}
	t.Run("scenarios", func(t *testing.T) { // группа: родитель ждёт параллельные сценарии
		for _, sc := range loadScenarios(t) {
			t.Run(sc.Name, func(t *testing.T) {
				t.Parallel()
				raw, ok := legacyReport(t, sc)
				if !ok {
					return // сценарий ожидает ошибку сбора: отчёта нет
				}
				norm := normalizeJSON(t, raw)
				sum := legacySum(norm)
				mu.Lock()
				got[sc.Name] = sum
				mu.Unlock()
				if update {
					return
				}
				if w, ok := want[sc.Name]; !ok {
					t.Errorf("для сценария нет эталона (KUBRON_UPDATE_LEGACY=1)")
				} else if sum != w {
					t.Errorf("отчёт %s изменился (кроме новых полей): sha256 %s, эталон %s\n%s", sc.Name, sum, w, norm)
				}
			})
		}
	})
	if update {
		var b strings.Builder
		for _, name := range slices.Sorted(maps.Keys(got)) {
			b.WriteString(got[name] + "  " + name + "\n")
		}
		if err := os.WriteFile(legacySums, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	for name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("в эталоне есть сценарий %s, которого больше нет", name)
		}
	}
	if len(got) == 0 {
		t.Fatal("нет сценариев")
	}
}
