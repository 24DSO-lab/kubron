// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package drift

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/24DSO-lab/kubron/internal/baseline"
	"github.com/24DSO-lab/kubron/internal/logging"
)

// maxDisplayRunes — предел длины значения в показе изменения: длинные значения (скрипты, конфиги) обрезаются.
const maxDisplayRunes = 512

// display готовит значение для показа: редакция по ADR-0022 и обрезка по длине. Вход — JSON-литерал.
func display(lit string) string {
	s := logging.Redact(lit)
	if utf8.RuneCountInString(s) <= maxDisplayRunes {
		return s
	}
	r := []rune(s)
	return string(r[:maxDisplayRunes]) + "…"
}

// diffFields сравнивает сохранённые значения с живыми и возвращает отличия по возрастанию пути.
// Чувствительные значения (hash у любой из сторон) в отличие не попадают: сообщается только операция и имя ключа.
func diffFields(kind string, stored, live map[string]baseline.Field) []baseline.Change {
	paths := make([]string, 0, len(stored)+len(live))
	for p := range stored {
		paths = append(paths, p)
	}
	for p := range live {
		if _, dup := stored[p]; !dup {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)

	var out []baseline.Change
	for _, p := range paths {
		old, hadOld := stored[p]
		cur, hasCur := live[p]
		if hadOld && hasCur && old.Equal(cur) {
			continue
		}
		ch := baseline.Change{Path: p, Priority: priorityFor(kind, p)}
		switch {
		case hadOld && hasCur:
			ch.Op = baseline.OpChanged
		case hadOld:
			ch.Op = baseline.OpRemoved
		default:
			ch.Op = baseline.OpAdded
		}
		if old.Sensitive() || cur.Sensitive() {
			ch.Sensitive = true
			ch.Key = keyName(p)
		} else {
			if hadOld {
				ch.Old = display(old.Plain)
			}
			if hasCur {
				ch.New = display(cur.Plain)
			}
		}
		out = append(out, ch)
	}
	return out
}

// maxPriority возвращает наибольший приоритет среди изменений.
func maxPriority(changes []baseline.Change) baseline.Priority {
	var best baseline.Priority
	for _, c := range changes {
		if c.Priority.Rank() > best.Rank() {
			best = c.Priority
		}
	}
	return best
}

// exceptionIndex применяет исключения к изменениям: действующие скрывают совпавшие изменения, просроченные не
// применяются, но перечисляются в результате.
type exceptionIndex struct {
	active  []baseline.Exception
	hits    map[string]int
	expired []string
}

func newExceptionIndex(list []baseline.Exception, now time.Time) *exceptionIndex {
	x := &exceptionIndex{hits: map[string]int{}}
	for _, e := range list {
		if e.Expired(now) {
			x.expired = append(x.expired, e.ID)
			continue
		}
		x.active = append(x.active, e)
	}
	return x
}

// filter убирает из changes изменения, скрытые исключениями, и возвращает оставшиеся и идентификаторы сработавших
// исключений. Каждое скрытое изменение засчитывается первому подошедшему исключению.
func (x *exceptionIndex) filter(ref baseline.Ref, changes []baseline.Change) (kept []baseline.Change, used []string) {
	for _, c := range changes {
		matched := ""
		for _, e := range x.active {
			if e.Matches(ref) && e.CoversPath(c.Path) {
				matched = e.ID
				break
			}
		}
		if matched == "" {
			kept = append(kept, c)
			continue
		}
		x.hits[matched]++
		if !slices.Contains(used, matched) {
			used = append(used, matched)
		}
	}
	return kept, used
}

// report возвращает сведения об исключениях для результата проверки.
func (x *exceptionIndex) report() baseline.ExceptionReport {
	var rep baseline.ExceptionReport
	for _, e := range x.active {
		if n := x.hits[e.ID]; n > 0 {
			rep.Applied = append(rep.Applied, baseline.AppliedException{
				ID: e.ID, Scope: e.Scope, Ref: e.Ref, Path: e.Path, Comment: e.Comment, ExpiresAt: e.ExpiresAt, Hits: n,
			})
		}
	}
	rep.Expired = x.expired
	return rep
}

// itemOrder задаёт порядок строк результата: сначала то, что требует внимания.
func itemOrder(s baseline.Status) int {
	switch s {
	case baseline.StatusChanged:
		return 0
	case baseline.StatusMissing:
		return 1
	case baseline.StatusUnchecked:
		return 2
	case baseline.StatusNew:
		return 3
	default:
		return 4
	}
}

// sortItems упорядочивает строки: по статусу, затем по приоритету (важные выше), затем по ресурсу.
func sortItems(items []baseline.ItemResult) {
	slices.SortStableFunc(items, func(a, b baseline.ItemResult) int {
		if c := itemOrder(a.Status) - itemOrder(b.Status); c != 0 {
			return c
		}
		if c := b.Priority.Rank() - a.Priority.Rank(); c != 0 {
			return c
		}
		return strings.Compare(a.Ref.Key(), b.Ref.Key())
	})
}

// summarize считает счётчики результата.
func summarize(items []baseline.ItemResult) baseline.Summary {
	s := baseline.Summary{Total: len(items)}
	for _, it := range items {
		s.SuppressedChanges += 0
		switch it.Status {
		case baseline.StatusMatch:
			s.Matched++
		case baseline.StatusChanged:
			s.Changed++
		case baseline.StatusMissing:
			s.Missing++
		case baseline.StatusUnchecked:
			s.Unchecked++
			if s.UncheckedByReason == nil {
				s.UncheckedByReason = map[baseline.Reason]int{}
			}
			s.UncheckedByReason[it.Reason]++
		}
	}
	s.Checked = s.Matched + s.Changed + s.Missing
	return s
}
