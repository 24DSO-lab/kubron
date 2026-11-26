// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package signals

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// scanLiteral читает строковый литерал PromQL в двойных кавычках с начала s и возвращает его значение и длину в
// байтах. Это проверочный разбор: литерал должен закрываться ровно там, где его закрыл построитель запроса, и
// давать исходное значение. Экранирование PromQL — то же, что у Go (\\, \", \n, \xNN, \uNNNN).
func scanLiteral(s string) (val string, n int, ok bool) {
	if s == "" || s[0] != '"' {
		return "", 0, false
	}
	var b []byte
	for i := 1; i < len(s); {
		if s[i] == '"' {
			return string(b), i + 1, true
		}
		if s[i] == '\n' {
			return "", 0, false // в литерале PromQL перевод строки недопустим
		}
		r, multibyte, tail, err := strconv.UnquoteChar(s[i:], '"')
		if err != nil {
			return "", 0, false
		}
		if multibyte {
			b = utf8.AppendRune(b, r)
		} else {
			b = append(b, byte(r))
		}
		i = len(s) - len(tail)
	}
	return "", 0, false
}

// literals возвращает значения всех строковых литералов запроса; ok=false, если какой-то литерал не закрыт.
func literals(q string) (out []string, ok bool) {
	for i := 0; i < len(q); i++ {
		if q[i] != '"' {
			continue
		}
		v, n, good := scanLiteral(q[i:])
		if !good {
			return nil, false
		}
		out = append(out, v)
		i += n - 1
	}
	return out, true
}

func FuzzPromQuote(f *testing.F) {
	for _, s := range []string{"", "a", `a"b`, `a\b`, "a\nb", `"} or vector(1) #`, "\x00", "\xff\xfe", "я", "a b", `\"`, `\\"`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		lit := promQuote(s)
		v, n, ok := scanLiteral(lit)
		if !ok || n != len(lit) {
			t.Fatalf("the literal %q does not close at its end (n=%d, ok=%v)", lit, n, ok)
		}
		if v != s {
			t.Fatalf("literal %q decodes to %q, want %q", lit, v, s)
		}
		if strings.ContainsAny(lit, "\n\r") {
			t.Fatalf("the literal contains a raw line break: %q", lit)
		}
	})
}

func TestPromQuoteBlocksBreakout(t *testing.T) {
	for _, s := range []string{
		`x"} or vector(1) #`, `x\"} or vector(1) #`, "x\"}\nor vector(1)", `x"}[5m]) or (up`, `\`, `\"`, `"`,
	} {
		q := `up{pod=` + promQuote(s) + `}`
		lits, ok := literals(q)
		if !ok || len(lits) != 1 || lits[0] != s {
			t.Errorf("%q: the value escaped its literal: %q", s, lits)
		}
		if strings.Count(q, "{") != 1 || !strings.HasSuffix(q, "}") {
			t.Errorf("%q: the selector structure changed: %s", s, q)
		}
	}
}

func FuzzMatchAny(f *testing.F) {
	f.Add("a", "b")
	f.Add("a.b", ".*")
	f.Add(`a|b`, `c)`)
	f.Add(`(`, `[`)
	f.Add(`x"y`, "z\nw")
	f.Add("", `\`)
	f.Fuzz(func(t *testing.T, a, b string) {
		m, err := matchAny("pod", []string{a, b})
		if !utf8.ValidString(a) || !utf8.ValidString(b) {
			if err == nil {
				t.Fatalf("a value that is not UTF-8 must be rejected, got %s", m)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(m, "pod=~") {
			t.Fatalf("two values must give a regex matcher: %s", m)
		}
		lit, n, ok := scanLiteral(strings.TrimPrefix(m, "pod=~"))
		if !ok || n != len(strings.TrimPrefix(m, "pod=~")) {
			t.Fatalf("the matcher literal is broken: %s", m)
		}
		// PromQL привязывает регулярное выражение к началу и концу значения.
		re, err := regexp.Compile(`^(?:` + lit + `)$`)
		if err != nil {
			t.Fatalf("the pattern is not valid RE2: %v", err)
		}
		for _, v := range []string{a, b} {
			if !re.MatchString(v) {
				t.Errorf("value %q does not match its own matcher %q", v, lit)
			}
		}
		// Метасимволы значений обезврежены: совпадают только сами значения, а не их расширения.
		for _, probe := range []string{a + "x", "x" + b, a + b, "", "x"} {
			if re.MatchString(probe) != (probe == a || probe == b) {
				t.Errorf("pattern %q matches %q, which is neither %q nor %q", lit, probe, a, b)
			}
		}
	})
}

func TestMatchAny(t *testing.T) {
	if got, _ := matchAny("pod", []string{"a-1"}); got != `pod="a-1"` {
		t.Errorf("one value must be an equality matcher, got %s", got)
	}
	if got, _ := matchAny("pod", []string{"a.b", "c"}); got != `pod=~"a\\.b|c"` {
		t.Errorf("got %s", got)
	}
	if _, err := matchAny("pod", nil); err == nil {
		t.Error("no values must be an error")
	}
	many := make([]string, MaxPodsPerMatcher+1)
	for i := range many {
		many[i] = "p" + strconv.Itoa(i)
	}
	if _, err := matchAny("pod", many); err == nil {
		t.Error("more than MaxPodsPerMatcher values must be an error")
	}
	if _, err := matchAny("pod", many[:MaxPodsPerMatcher]); err != nil {
		t.Errorf("exactly MaxPodsPerMatcher values: %v", err)
	}
}

func TestPromDuration(t *testing.T) {
	for in, want := range map[string]string{"15m": "15m", "1h": "1h", "6h": "6h", "5m": "5m", "90s": "90s", "1m": "1m"} {
		d, err := time.ParseDuration(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := promDuration(d); got != want {
			t.Errorf("promDuration(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestValidRefs(t *testing.T) {
	ok := Subject{Namespace: "kube-system", Pod: "coredns-5d78c9869d-abcde", Container: "coredns", Node: "node-1.example.com", PVC: "data-0"}
	if !validRefs(ok, fNamespace, fPod, fContainer, fNode, fPVC) {
		t.Error("valid names were rejected")
	}
	for name, s := range map[string]Subject{
		"empty namespace":      {Pod: "p", Container: "c"},
		"uppercase namespace":  {Namespace: "NS", Pod: "p", Container: "c"},
		"namespace with a dot": {Namespace: "a.b", Pod: "p", Container: "c"},
		"quote in pod":         {Namespace: "ns", Pod: `p"`, Container: "c"},
		"space in container":   {Namespace: "ns", Pod: "p", Container: "c c"},
		"long container":       {Namespace: "ns", Pod: "p", Container: strings.Repeat("a", 64)},
		"newline in pod":       {Namespace: "ns", Pod: "p\n", Container: "c"},
		"regex in pod":         {Namespace: "ns", Pod: "p.*", Container: "c"},
	} {
		if validRefs(s, fNamespace, fPod, fContainer) {
			t.Errorf("%s: accepted", name)
		}
	}
	// Поля, которые сигналу не нужны, не проверяются.
	if !validRefs(Subject{Node: "n1", Pod: `bad"`}, fNode) {
		t.Error("an unused invalid field must not matter")
	}
}
