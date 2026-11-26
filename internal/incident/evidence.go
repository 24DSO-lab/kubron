// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// RedactResult — результат редактора. Совпадает по смыслу с logredact.Result; пакет работает через интерфейс,
// а адаптер к logredact лежит в слое связки (в тестах — двойник).
type RedactResult struct {
	Text         string
	Replacements int
	Version      int
}

// Ошибки редактора, которые адаптер сообщает домену. Любая другая ошибка — сбой редактирования.
var (
	ErrRedactNotText  = errors.New("log is not valid UTF-8 text")
	ErrRedactTooLarge = errors.New("log is too large to redact")
)

// Redactor — версионированный fail-closed редактор (ADR-0024 §5 п.4). Любая ошибка означает «не сохранять».
type Redactor interface {
	Redact(in []byte) (RedactResult, error)
}

// RedactorFunc — функция как Redactor.
type RedactorFunc func(in []byte) (RedactResult, error)

// Redact implements Redactor.
func (f RedactorFunc) Redact(in []byte) (RedactResult, error) { return f(in) }

// notSaved — отказ сохранить фрагмент с кодом причины (одним из Reason*). Это не ошибка процесса: finding получает
// пометку «доказательство не сохранено».
type notSaved struct{ reason string }

func (e *notSaved) Error() string { return "log evidence was not saved: " + e.reason }

// NotSavedReason возвращает код причины, если err — отказ сохранить фрагмент.
func NotSavedReason(err error) (string, bool) {
	var ns *notSaved
	if errors.As(err, &ns) {
		return ns.reason, true
	}
	return "", false
}

// window — выбранное окно строк лога.
type window struct {
	lines      []string
	start, end int // индексы в исходных строках: [start, end)
	anchor     int // индекс якоря в исходных строках
	truncated  bool
}

// splitLines режет лог на строки (CRLF → LF), отбрасывая хвостовой пустой элемент, и усекает строки до
// MaxLineBytes по границе руны. Возвращает признак усечения строк.
func splitLines(raw string) ([]string, bool) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	cut := false
	for i, l := range lines {
		if len(l) > MaxLineBytes {
			lines[i] = truncateUTF8(l, MaxLineBytes)
			cut = true
		}
	}
	return lines, cut
}

// truncateUTF8 усекает строку до n байт, не разрезая руну.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// pickWindow выбирает окно вокруг якоря: до MaxFragmentLines строк и MaxFragmentBytes байт. Окно растёт от
// якоря в обе стороны поровну; когда одна сторона упёрлась в границу лога, добирает с другой.
func pickWindow(lines []string, anchor int) window {
	n := len(lines)
	w := window{anchor: anchor}
	start, end := anchor, anchor+1
	size := len(lines[anchor]) + 1
	if size > MaxFragmentBytes {
		// одна строка уже больше лимита (не должно быть: строка усечена до MaxLineBytes)
		size = MaxFragmentBytes
	}
	growBefore := true
	for end-start < MaxFragmentLines {
		var progressed bool
		for range 2 {
			if end-start >= MaxFragmentLines {
				break
			}
			if growBefore && start > 0 && size+len(lines[start-1])+1 <= MaxFragmentBytes {
				start--
				size += len(lines[start]) + 1
				progressed = true
			} else if !growBefore && end < n && size+len(lines[end])+1 <= MaxFragmentBytes {
				size += len(lines[end]) + 1
				end++
				progressed = true
			}
			growBefore = !growBefore
		}
		if !progressed {
			break
		}
	}
	w.start, w.end = start, end
	w.lines = lines[start:end]
	w.truncated = start > 0 || end < n
	return w
}

// buildFragment готовит фрагмент из сырого лога: окно → редакция → проверки → метаданные. Любой отказ — *notSaved
// с кодом причины; сырой текст дальше этой функции не уходит. anchor1 — якорь пользователя (с 1), 0 — выбрать
// автоматически.
func buildFragment(raw []byte, anchor1 int, red Redactor, now time.Time) (Fragment, error) {
	if !utf8.Valid(raw) || strings.IndexByte(string(raw), 0) >= 0 {
		return Fragment{}, &notSaved{ReasonNotText}
	}
	lines, lineCut := splitLines(string(raw))
	anchor := diagnose.AnchorLine(lines)
	if anchor1 > 0 {
		anchor = anchor1 - 1
	}
	if anchor < 0 || anchor >= len(lines) || (len(lines) == 1 && strings.TrimSpace(lines[0]) == "") {
		return Fragment{}, &notSaved{ReasonEmptyLog}
	}
	w := pickWindow(lines, anchor)
	text := strings.Join(w.lines, "\n")
	res, err := red.Redact([]byte(text))
	switch {
	case errors.Is(err, ErrRedactNotText):
		return Fragment{}, &notSaved{ReasonNotText}
	case errors.Is(err, ErrRedactTooLarge):
		return Fragment{}, &notSaved{ReasonLimitExceeded}
	case err != nil:
		return Fragment{}, &notSaved{ReasonRedactionFailed}
	}
	// Проверка результата: редактор не должен вернуть невалидный текст или превысить лимиты.
	switch {
	case !utf8.ValidString(res.Text):
		return Fragment{}, &notSaved{ReasonRedactionFailed}
	case len(res.Text) > MaxFragmentBytes || strings.Count(res.Text, "\n")+1 > MaxFragmentLines:
		return Fragment{}, &notSaved{ReasonLimitExceeded}
	}
	sum := sha256.Sum256([]byte(res.Text))
	f := Fragment{Text: res.Text}
	f.CapturedAt = Micro(now)
	f.StartLine, f.EndLine, f.AnchorLine = w.start+1, w.end, anchor+1
	f.Lines, f.Bytes = len(w.lines), len(res.Text)
	f.Truncated = w.truncated || lineCut
	f.RedactorVersion, f.Replacements = res.Version, res.Replacements
	f.SHA256 = hex.EncodeToString(sum[:])
	f.FromTime, f.ToTime = lineTimes(w.lines)
	return f, nil
}

// lineTimes возвращает время первой и последней строки окна, если строки начинаются с метки RFC 3339
// (kubectl logs --timestamps). Нет меток — nil.
func lineTimes(lines []string) (from, to *time.Time) {
	parse := func(l string) *time.Time {
		tok, _, _ := strings.Cut(l, " ")
		if t, err := time.Parse(time.RFC3339Nano, tok); err == nil {
			t = t.UTC()
			return &t
		}
		return nil
	}
	for _, l := range lines {
		if t := parse(l); t != nil {
			from = t
			break
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if t := parse(lines[i]); t != nil {
			to = t
			break
		}
	}
	return from, to
}

// quotaCheck проверяет лимиты инцидента (10 фрагментов, 256 КиБ) с учётом заменяемого фрагмента.
func quotaCheck(existing []FragmentMeta, replacing string, addBytes int) error {
	count, total := 0, 0
	for _, m := range existing {
		if m.ID == replacing {
			continue
		}
		count++
		total += m.Bytes
	}
	if count+1 > MaxFragmentsPerIncident || total+addBytes > MaxIncidentFragmentBytes {
		return &notSaved{ReasonIncidentQuota}
	}
	return nil
}

func (e Evidence) key() string {
	return fmt.Sprintf("%s/%s/%t", e.Pod, e.Container, e.Previous)
}
