// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package logredact — версионированный редактор фрагментов логов контейнеров (ADR-0024 §5 п.4, ADR-0026).
//
// Фрагмент лога можно сохранить (история расследований) только после Redact. Редактор работает по принципу
// fail-closed: любая неопределённость — не-UTF-8 или бинарный вход, превышение лимита, внутренняя ошибка, правила,
// не дошедшие до неподвижной точки, — это ошибка, и вызывающий НЕ сохраняет фрагмент. Лучше потерять фрагмент,
// чем сохранить секрет.
//
// Редактор намеренно предпочитает избыточное скрытие: «token: expired» в тексте ошибки будет скрыто. Все шаблоны
// линейны по времени (RE2), поэтому вход произвольного вида не вызывает деградации.
//
// Правила и корпус: у каждого правила есть положительные и отрицательные примеры в testdata (тест покрытия не
// принимает правило без них). Любое изменение правил поднимает Version (тест отпечатка правил подскажет).
package logredact

import (
	"bytes"
	"errors"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Version — версия набора правил. Сохранённый фрагмент хранит версию, которой он отредактирован: при смене правил
// можно отличить старые фрагменты от новых. Поднимается при любом изменении правил, порогов или заменителя;
// тест TestRulesFingerprint следит за этим.
const Version = 1

// MaxInputBytes — предельный размер входа. Больший вход отклоняется: вызывающий сам ограничивает лог (tail, байты).
const MaxInputBytes = 1 << 20

// Placeholder — замена скрытого значения.
const Placeholder = "[REDACTED]"

// maxPasses — число проходов до неподвижной точки. Обычно хватает двух; не сошлось — это сбой правил (ErrRedaction).
const maxPasses = 6

// Ошибки. Любая из них означает «фрагмент не сохранять».
var (
	// ErrBinary — вход не является текстом UTF-8 (или содержит NUL).
	ErrBinary = errors.New("log is not valid UTF-8 text")
	// ErrTooLarge — вход больше MaxInputBytes.
	ErrTooLarge = errors.New("log is too large to redact")
	// ErrRedaction — внутренняя ошибка редактирования или проверка результата не пройдена.
	ErrRedaction = errors.New("log redaction failed")
)

// Result — результат редактирования.
type Result struct {
	Text         string // отредактированный текст
	Replacements int    // число скрытых значений
	Version      int    // версия правил
}

// Redact скрывает секреты во фрагменте лога. Вход не изменяется. При ошибке Result пуст, и сохранять нечего.
func Redact(in []byte) (res Result, err error) {
	defer func() {
		if r := recover(); r != nil { // паника правила — тоже отказ, а не пропуск
			res, err = Result{}, ErrRedaction
		}
	}()
	if len(in) > MaxInputBytes {
		return Result{}, ErrTooLarge
	}
	if !utf8.Valid(in) || bytes.IndexByte(in, 0) >= 0 {
		return Result{}, ErrBinary
	}
	cur, total := in, 0
	for range maxPasses {
		n := 0
		for i := range rules {
			var k int
			cur, k = rules[i].apply(cur)
			n += k
		}
		total += n
		if n == 0 {
			// Независимая проверка результата: после неподвижной точки не должно остаться ничего, что правила скрыли бы.
			if scan(cur) != 0 || !utf8.Valid(cur) {
				return Result{}, ErrRedaction
			}
			return Result{Text: string(cur), Replacements: total, Version: Version}, nil
		}
	}
	return Result{}, ErrRedaction
}

// rule — одно правило: шаблон находит кандидата, span выбирает, какую часть совпадения скрыть (и скрывать ли).
type rule struct {
	name   string
	re     *regexp.Regexp
	span   func(src []byte, m []int) (from, to int, ok bool)
	params string // пороги и прочие параметры логики: входят в отпечаток правил
}

// apply заменяет найденные значения заменителем. Уже скрытые значения не трогает (идемпотентность).
func (r *rule) apply(src []byte) ([]byte, int) {
	ms := r.re.FindAllSubmatchIndex(src, -1)
	if len(ms) == 0 {
		return src, 0
	}
	var out []byte
	last, n := 0, 0
	for _, m := range ms {
		from, to, ok := r.span(src, m)
		if !ok || from >= to || bytes.HasPrefix(src[from:to], []byte("[REDACTED")) {
			continue
		}
		out = append(out, src[last:from]...)
		out = append(out, Placeholder...)
		last = to
		n++
	}
	if n == 0 {
		return src, 0
	}
	return append(out, src[last:]...), n
}

// count возвращает число значений, которые правило скрыло бы (без изменения входа).
func (r *rule) count(src []byte) int {
	n := 0
	for _, m := range r.re.FindAllSubmatchIndex(src, -1) {
		if from, to, ok := r.span(src, m); ok && from < to && !bytes.HasPrefix(src[from:to], []byte("[REDACTED")) {
			n++
		}
	}
	return n
}

func scan(src []byte) int {
	n := 0
	for i := range rules {
		n += rules[i].count(src)
	}
	return n
}

func whole(_ []byte, m []int) (int, int, bool) { return m[0], m[1], true }

// group возвращает span для подгруппы g.
func group(g int) func([]byte, []int) (int, int, bool) {
	return func(_ []byte, m []int) (int, int, bool) {
		if m[2*g] < 0 {
			return 0, 0, false
		}
		return m[2*g], m[2*g+1], true
	}
}

// Пороги энтропии (бит на символ) и минимальные длины.
const (
	minHexEntropy = 3.0
	minB64Entropy = 4.0
)

var rules = []rule{
	{
		name: "pem",
		re:   regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----|$)`),
		span: whole,
	},
	{
		name: "auth-header",
		re:   regexp.MustCompile(`(?i)\b(?:proxy-)?authorization["']?\s*[:=]\s*["']?([^"'\r\n]+)`),
		span: group(1),
	},
	{
		name: "bearer",
		re:   regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+([A-Za-z0-9._~+/=\-]{8,})`),
		span: func(src []byte, m []int) (int, int, bool) {
			tok := src[m[2]:m[3]]
			// «Basic authentication» и «bearer tokens» — слова, а не токены: нужен признак токена.
			if !looksLikeToken(tok) {
				return 0, 0, false
			}
			return m[2], m[3], true
		},
		params: "minBearerToken=8;tokenHint=digit|upper+lower|+/=_~",
	},
	{
		name: "jwt",
		re:   regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{5,}\.[A-Za-z0-9_\-]{5,}\.[A-Za-z0-9_\-]*`),
		span: whole,
	},
	{
		name: "url-credentials",
		re:   regexp.MustCompile(`(?i)[a-z][a-z0-9+.\-]*://[^/\s:@"']*:([^/\s@"']+)@`),
		span: group(1),
	},
	{
		name: "cloud-key",
		re: regexp.MustCompile(`\b(?:AKIA|ASIA|AGPA|AIDA|AROA|ANPA|ANVA|AIPA)[A-Z0-9]{16}\b` +
			`|\bAIza[0-9A-Za-z_\-]{35}` +
			`|\bya29\.[0-9A-Za-z_\-]{20,}` +
			`|\bgh[pousr]_[A-Za-z0-9]{36,}` +
			`|\bgithub_pat_[A-Za-z0-9_]{22,}` +
			`|\bglpat-[A-Za-z0-9_\-]{20,}` +
			`|\bxox[baprs]-[A-Za-z0-9\-]{10,}` +
			`|\b[sr]k_(?:live|test)_[A-Za-z0-9]{16,}` +
			`|\bnpm_[A-Za-z0-9]{36}\b` +
			`|\bSG\.[A-Za-z0-9_\-]{16,}\.[A-Za-z0-9_\-]{16,}`),
		span: whole,
	},
	{
		name: "key-value",
		re: regexp.MustCompile(`(?i)["']?([\w.\-]*(?:password|passwd|passphrase|pwd|secret|token|api[_\-]?key|access[_\-]?key|private[_\-]?key|auth[_\-]?key|account[_\-]?key|shared[_\-]?access[_\-]?key|credential|cookie|session[_\-]?id)[\w.\-]*)["']?\s*[:=]\s*` +
			`(?:"([^"\r\n]*)"|'([^'\r\n]*)'|([^\s,;&}\]"']+))`),
		span: func(src []byte, m []int) (int, int, bool) {
			from, to := -1, -1
			for _, g := range []int{2, 3, 4} { // значение в двойных, одинарных кавычках или без них
				if m[2*g] >= 0 {
					from, to = m[2*g], m[2*g+1]
				}
			}
			if from < 0 || from == to {
				return 0, 0, false
			}
			if isNonSecretValue(string(src[from:to])) || isCounter(string(src[m[2]:m[3]]), string(src[from:to])) {
				return 0, 0, false
			}
			return from, to, true
		},
		params: "skip=null|nil|none|true|false;counter=*tokens+digits",
	},
	{
		name: "high-entropy-hex",
		re:   regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`),
		span: func(src []byte, m []int) (int, int, bool) {
			// Дайджест образа (sha256:…) — не секрет и нужен для диагностики.
			if hasDigestPrefix(src[:m[0]]) || entropy(src[m[0]:m[1]]) < minHexEntropy {
				return 0, 0, false
			}
			return m[0], m[1], true
		},
		params: "minHexLen=32;minHexEntropy=3.0;skipDigestPrefix=sha1|sha256|sha512",
	},
	{
		name: "high-entropy-base64",
		re:   regexp.MustCompile(`[A-Za-z0-9+/_\-]{40,}={0,2}`),
		span: func(src []byte, m []int) (int, int, bool) {
			s := src[m[0]:m[1]]
			if !hasAllClasses(s) || entropy(s) < minB64Entropy {
				return 0, 0, false
			}
			return m[0], m[1], true
		},
		params: "minB64Len=40;minB64Entropy=4.0;classes=upper+lower+digit",
	},
}

func looksLikeToken(tok []byte) bool {
	var upper, lower, digit bool
	for _, c := range tok {
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= 'a' && c <= 'z':
			lower = true
		}
	}
	return digit || (upper && lower) || bytes.ContainsAny(tok, "+/=_~")
}

func hasAllClasses(s []byte) bool {
	var upper, lower, digit bool
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= 'a' && c <= 'z':
			lower = true
		}
	}
	return upper && lower && digit
}

func hasDigestPrefix(before []byte) bool {
	for _, p := range []string{"sha256:", "sha512:", "sha1:"} {
		if bytes.HasSuffix(bytes.ToLower(before[max(0, len(before)-len(p)):]), []byte(p)) {
			return true
		}
	}
	return false
}

// isNonSecretValue — значения-пустышки: у них нет секрета.
func isNonSecretValue(v string) bool {
	switch strings.ToLower(v) {
	case "null", "nil", "none", "true", "false":
		return true
	}
	return false
}

// isCounter — счётчики токенов (max_tokens: 4096): ключ во множественном числе и числовое значение.
func isCounter(key, val string) bool {
	if !strings.HasSuffix(strings.ToLower(key), "tokens") || val == "" {
		return false
	}
	for _, c := range val {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// entropy — энтропия Шеннона в битах на байт.
func entropy(s []byte) float64 {
	if len(s) == 0 {
		return 0
	}
	var freq [256]int
	for _, c := range s {
		freq[c]++
	}
	h := 0.0
	for _, n := range freq {
		if n > 0 {
			p := float64(n) / float64(len(s))
			h -= p * math.Log2(p)
		}
	}
	return h
}
