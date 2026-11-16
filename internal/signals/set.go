// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package signals

import (
	"cmp"
	"maps"
	"slices"
)

// partialSource — имя источника, которое передаётся в Op.End при сбое существующего стека (ADR-0032 §7).
const partialSource = "metrics"

// Set — результат Provider.Fetch: значения и пробелы (причины отсутствия значений) по запрошенным сигналам.
// Нулевой *Set безопасен: сигналов нет, стек считается доступным. Set неизменяем после создания.
type Set struct {
	stack  Reason
	values map[setKey]Value
	gaps   map[setKey]Gap
}

type setKey struct {
	signal  Signal
	subject Subject
}

// NewSet собирает Set. stack — причина недоступности стека целиком (not_installed, forbidden, unavailable) либо
// пусто, если стек доступен. Конструктор нужен реализациям Provider и тестам потребителей (правила диагностики
// проверяются подставленным Set). Если один и тот же сигнал по одной цели указан и значением, и пробелом,
// остаётся значение; из повторов одного вида побеждает последний.
func NewSet(stack Reason, values []Value, gaps []Gap) *Set {
	s := &Set{stack: stack, values: make(map[setKey]Value, len(values)), gaps: make(map[setKey]Gap, len(gaps))}
	for _, g := range gaps {
		s.gaps[setKey{g.Signal, g.Subject}] = g
	}
	for _, v := range values {
		k := setKey{v.Signal, v.Subject}
		s.values[k] = v
		delete(s.gaps, k)
	}
	return s
}

// Get возвращает значение сигнала по цели. Нет значения (пробел, не запрашивали, nil-Set) — false: отсутствие данных
// не превращается в 0.
func (s *Set) Get(sig Signal, sub Subject) (Value, bool) {
	if s == nil {
		return Value{}, false
	}
	v, ok := s.values[setKey{sig, sub}]
	return v, ok
}

// GapOf возвращает пробел сигнала по цели (причину отсутствия значения).
func (s *Set) GapOf(sig Signal, sub Subject) (Gap, bool) {
	if s == nil {
		return Gap{}, false
	}
	g, ok := s.gaps[setKey{sig, sub}]
	return g, ok
}

// Values возвращает значения в стабильном порядке (сигнал, затем цель): отчёт детерминирован.
func (s *Set) Values() []Value {
	if s == nil {
		return nil
	}
	out := slices.Collect(maps.Values(s.values))
	slices.SortFunc(out, func(a, b Value) int { return compareKeys(a.Signal, a.Subject, b.Signal, b.Subject) })
	return out
}

// Gaps возвращает пробелы в стабильном порядке (сигнал, цель, причина).
func (s *Set) Gaps() []Gap {
	if s == nil {
		return nil
	}
	out := slices.Collect(maps.Values(s.gaps))
	slices.SortFunc(out, func(a, b Gap) int {
		return cmp.Or(compareKeys(a.Signal, a.Subject, b.Signal, b.Subject), cmp.Compare(a.Reason, b.Reason))
	})
	return out
}

// StackReason — причина недоступности стека целиком: not_installed, forbidden или unavailable. Пусто — стек доступен
// (отдельные сигналы при этом могут иметь пробелы).
func (s *Set) StackReason() Reason {
	if s == nil {
		return ""
	}
	return s.stack
}

// PartialSource возвращает "metrics" только при unavailable у существующего стека (таймаут, ошибка Prometheus): тогда
// операция «частичная». Отсутствие стека и forbidden операцию частичной не делают: метрики опциональны (ADR-0032 §7).
func (s *Set) PartialSource() string {
	if s.StackReason() == ReasonUnavailable {
		return partialSource
	}
	return ""
}

func compareKeys(as Signal, a Subject, bs Signal, b Subject) int {
	return cmp.Or(
		cmp.Compare(as, bs),
		cmp.Compare(a.Namespace, b.Namespace),
		cmp.Compare(a.Pod, b.Pod),
		cmp.Compare(a.Container, b.Container),
		cmp.Compare(a.Node, b.Node),
		cmp.Compare(a.PVC, b.PVC),
	)
}
