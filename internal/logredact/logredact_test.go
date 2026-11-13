// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package logredact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Формат корпуса: testdata/<правило>.positive.txt и <правило>.negative.txt; случаи разделены строкой «%%%».
// В положительных случаях секреты обведены ⟦…⟧: маркеры снимаются перед проверкой, а сам секрет не должен
// остаться в результате. Отрицательные случаи после редактирования не меняются.
const (
	markOpen  = "⟦"
	markClose = "⟧"
	minCases  = 3 // минимум положительных и отрицательных случаев на правило
)

func loadCases(t testing.TB, name string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name+".txt"))
	if err != nil {
		t.Fatalf("corpus file for %s: %v", name, err)
	}
	var out []string
	for c := range strings.SplitSeq(strings.TrimSuffix(string(raw), "\n"), "\n%%%\n") {
		if strings.TrimSpace(c) != "" {
			out = append(out, c)
		}
	}
	return out
}

// split разбирает положительный случай на вход (без маркеров) и список секретов.
func split(t testing.TB, c string) (input string, secrets []string) {
	t.Helper()
	rest := c
	var b strings.Builder
	for {
		i := strings.Index(rest, markOpen)
		if i < 0 {
			b.WriteString(rest)
			break
		}
		j := strings.Index(rest[i:], markClose)
		if j < 0 {
			t.Fatalf("unterminated marker in corpus case: %q", c)
		}
		secret := rest[i+len(markOpen) : i+j]
		b.WriteString(rest[:i])
		b.WriteString(secret)
		secrets = append(secrets, secret)
		rest = rest[i+j+len(markClose):]
	}
	if len(secrets) == 0 {
		t.Fatalf("positive corpus case without a marked secret: %q", c)
	}
	return b.String(), secrets
}

// Каждое правило покрыто корпусом: есть положительные и отрицательные случаи. Правило без примеров не принимается.
func TestEveryRuleHasCorpus(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, r := range rules {
		if seen[r.name] {
			t.Fatalf("duplicate rule name %q", r.name)
		}
		seen[r.name] = true
		for _, kind := range []string{"positive", "negative"} {
			if n := len(loadCases(t, r.name+"."+kind)); n < minCases {
				t.Errorf("rule %q has %d %s corpus cases, want at least %d", r.name, n, kind, minCases)
			}
		}
	}
	// и наоборот: нет файлов корпуса для несуществующих правил
	files, _ := filepath.Glob(filepath.Join("testdata", "*.txt"))
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".txt")
		name = strings.TrimSuffix(strings.TrimSuffix(name, ".positive"), ".negative")
		if !seen[name] {
			t.Errorf("corpus file %s belongs to no rule", f)
		}
	}
}

// Положительные случаи: секрет скрыт и самим правилом (изолированно), и полным редактором.
func TestCorpusPositive(t *testing.T) {
	t.Parallel()
	for i := range rules {
		r := &rules[i]
		for n, c := range loadCases(t, r.name+".positive") {
			input, secrets := split(t, c)
			t.Run(r.name+"/"+itoa(n), func(t *testing.T) {
				t.Parallel()
				alone, k := r.apply([]byte(input))
				if k == 0 {
					t.Fatalf("rule %s alone did not fire on %q", r.name, input)
				}
				for _, s := range secrets {
					if bytes.Contains(alone, []byte(s)) {
						t.Errorf("rule %s alone left the secret %q in %q", r.name, s, alone)
					}
				}
				res, err := Redact([]byte(input))
				if err != nil {
					t.Fatal(err)
				}
				for _, s := range secrets {
					if strings.Contains(res.Text, s) {
						t.Errorf("secret %q survived: %q", s, res.Text)
					}
				}
				if res.Replacements == 0 || !strings.Contains(res.Text, Placeholder) || res.Version != Version {
					t.Errorf("result = %+v", res)
				}
			})
		}
	}
}

// Отрицательные случаи: обычный текст не портится — ни своим правилом, ни всеми.
func TestCorpusNegative(t *testing.T) {
	t.Parallel()
	for i := range rules {
		name := rules[i].name
		for n, c := range loadCases(t, name+".negative") {
			t.Run(name+"/"+itoa(n), func(t *testing.T) {
				t.Parallel()
				res, err := Redact([]byte(c))
				if err != nil {
					t.Fatal(err)
				}
				if res.Text != c || res.Replacements != 0 {
					t.Errorf("ordinary text was changed:\n in: %q\nout: %q", c, res.Text)
				}
			})
		}
	}
}

// Ключи платёжного провайдера собираются из частей: цельная строка такого вида в репозитории блокируется push protection
// даже будучи заведомо вымышленной, поэтому в текстовый корпус этот случай не вынесен.
func TestCloudKeyPaymentProviderStyle(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{"sk_" + "live_", "rk_" + "test_"} {
		secret := prefix + "0123456789abcdefghijklmn"
		res, err := Redact([]byte("billing " + secret + " charged"))
		if err != nil || strings.Contains(res.Text, secret) || res.Replacements == 0 {
			t.Errorf("%s…: %+v, %v", prefix, res, err)
		}
	}
	// короткий префикс без тела — обычный текст
	if res, err := Redact([]byte("the sk_live_ prefix")); err != nil || res.Replacements != 0 {
		t.Errorf("prefix only: %+v, %v", res, err)
	}
}

func itoa(n int) string { return string(rune('0' + n)) } // случаев в корпусе меньше десяти на файл

func TestRedactErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []byte
		want error
	}{
		{"invalid utf-8", []byte("ok \xff\xfe broken"), ErrBinary},
		{"NUL byte", []byte("text\x00more"), ErrBinary},
		{"binary blob", bytes.Repeat([]byte{0x89, 0x50, 0x4e, 0x47, 0x00}, 10), ErrBinary},
		{"too large", bytes.Repeat([]byte("a "), MaxInputBytes), ErrTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := Redact(tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if res != (Result{}) {
				t.Fatalf("an error must come with an empty result (nothing to save): %+v", res)
			}
		})
	}
}

func TestRedactBoundaries(t *testing.T) {
	t.Parallel()
	if res, err := Redact(nil); err != nil || res.Text != "" || res.Version != Version {
		t.Fatalf("empty input: %+v, %v", res, err)
	}
	exact := bytes.Repeat([]byte("ab "), MaxInputBytes/3+1)[:MaxInputBytes]
	if _, err := Redact(exact); err != nil {
		t.Fatalf("input of exactly MaxInputBytes: %v", err)
	}
	if _, err := Redact(append(bytes.Clone(exact), 'x')); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("MaxInputBytes+1: %v", err)
	}
	// Юникод и ANSI-последовательности — обычный текст.
	txt := "\x1b[31mошибка\x1b[0m 错误 ✓"
	if res, err := Redact([]byte(txt)); err != nil || res.Text != txt {
		t.Fatalf("unicode text: %+v, %v", res, err)
	}
}

func TestRedactDoesNotMutateInput(t *testing.T) {
	t.Parallel()
	in := []byte("password=hunter2 and Authorization: Bearer AbCdEf0123456789")
	orig := bytes.Clone(in)
	if _, err := Redact(in); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(in, orig) {
		t.Fatal("Redact modified its input")
	}
}

func TestRedactIsIdempotentAndComplete(t *testing.T) {
	t.Parallel()
	for i := range rules {
		for _, c := range loadCases(t, rules[i].name+".positive") {
			input, _ := split(t, c)
			once, err := Redact([]byte(input))
			if err != nil {
				t.Fatal(err)
			}
			twice, err := Redact([]byte(once.Text))
			if err != nil || twice.Text != once.Text || twice.Replacements != 0 {
				t.Errorf("not idempotent for %q: %q -> %q (%d more), %v", input, once.Text, twice.Text, twice.Replacements, err)
			}
			if scan([]byte(once.Text)) != 0 {
				t.Errorf("a secret is still detectable in %q", once.Text)
			}
		}
	}
}

// Несколько категорий в одном фрагменте скрываются все.
func TestRedactMixedFragment(t *testing.T) {
	t.Parallel()
	in := strings.Join([]string{
		"2026-01-01T00:00:00Z INFO starting",
		"db=postgres://app:s3cr3tP4ss@db:5432/app",
		"Authorization: Bearer AbCdEf0123456789",
		"aws=AKIAIOSFODNN7EXAMPLE password=hunter2",
		"2026-01-01T00:00:01Z INFO ready",
	}, "\n")
	res, err := Redact([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"s3cr3tP4ss", "AbCdEf0123456789", "AKIAIOSFODNN7EXAMPLE", "hunter2"} {
		if strings.Contains(res.Text, s) {
			t.Errorf("%q survived:\n%s", s, res.Text)
		}
	}
	if !strings.Contains(res.Text, "INFO starting") || !strings.Contains(res.Text, "INFO ready") {
		t.Errorf("ordinary lines were damaged:\n%s", res.Text)
	}
}

// Время обработки не зависит от «неудобного» входа: шаблоны линейны.
func TestRedactAdversarialInputIsFast(t *testing.T) {
	t.Parallel()
	inputs := map[string][]byte{
		"one long token":       bytes.Repeat([]byte("aB3"), MaxInputBytes/3),
		"many key fragments":   bytes.Repeat([]byte("token"), MaxInputBytes/5),
		"many separators":      bytes.Repeat([]byte("a=b;"), MaxInputBytes/4),
		"unterminated pem":     append([]byte("-----BEGIN PRIVATE KEY-----"), bytes.Repeat([]byte("x\n"), MaxInputBytes/2-20)...),
		"password repetitions": bytes.Repeat([]byte("password="), MaxInputBytes/9),
	}
	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			start := time.Now()
			_, err := Redact(in)
			if d := time.Since(start); d > adversarialBudget {
				t.Fatalf("took %v", d)
			}
			if err != nil && !errors.Is(err, ErrRedaction) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// rulesFingerprint — отпечаток правил текущей Version. Любое изменение шаблонов, порогов или заменителя меняет
// отпечаток: тест требует поднять Version и обновить константу. Изменения логики внутри функций правил отпечаток
// не видит — их фиксируют параметры (params) и корпус; при таком изменении поднимайте Version вручную.
const rulesFingerprint = "5324308739b3c7e8"

func fingerprint() string {
	h := sha256.New()
	h.Write([]byte(Placeholder))
	for _, r := range rules {
		h.Write([]byte("\x00" + r.name + "\x00" + r.re.String() + "\x00" + r.params))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func TestRulesFingerprint(t *testing.T) {
	t.Parallel()
	if got := fingerprint(); got != rulesFingerprint {
		t.Fatalf("the redaction rules changed (fingerprint %s, recorded %s for Version %d): bump Version and update rulesFingerprint",
			got, rulesFingerprint, Version)
	}
}

func FuzzRedact(f *testing.F) {
	for i := range rules {
		for _, kind := range []string{"positive", "negative"} {
			for _, c := range loadCases(f, rules[i].name+"."+kind) {
				in := c
				if kind == "positive" {
					in, _ = split(f, c)
				}
				f.Add([]byte(in))
			}
		}
	}
	f.Add([]byte(""))
	f.Add([]byte("password=\"[REDACTED]\" token: [REDACTED"))
	f.Add([]byte("\xff\x00binary"))
	f.Fuzz(func(t *testing.T, in []byte) {
		before := scan(in)
		res, err := Redact(in)
		if err != nil {
			if res != (Result{}) {
				t.Fatalf("an error with a non-empty result: %+v", res)
			}
			if !errors.Is(err, ErrBinary) && !errors.Is(err, ErrRedaction) && !errors.Is(err, ErrTooLarge) {
				t.Fatalf("unexpected error %v", err)
			}
			if !utf8.Valid(in) && !errors.Is(err, ErrBinary) {
				t.Fatalf("invalid UTF-8 must be ErrBinary, got %v", err)
			}
			return
		}
		if !utf8.Valid(in) {
			t.Fatal("invalid UTF-8 input was accepted")
		}
		out := []byte(res.Text)
		if !utf8.Valid(out) {
			t.Fatal("the output is not valid UTF-8")
		}
		// не раскрывает больше, чем было: в результате не остаётся ничего, что правила скрыли бы
		if after := scan(out); after > before || after != 0 {
			t.Fatalf("secrets detectable: before=%d after=%d\nin=%q\nout=%q", before, after, in, out)
		}
		// идемпотентность
		again, err := Redact(out)
		if err != nil || again.Text != res.Text || again.Replacements != 0 {
			t.Fatalf("not idempotent: %q -> %q (%v)", res.Text, again.Text, err)
		}
		if res.Version != Version {
			t.Fatalf("version = %d", res.Version)
		}
	})
}
