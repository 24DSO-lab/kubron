// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package signals

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"
)

// Option настраивает Provider поверх Prometheus. Пределы ADR-0032 §6 заданы кодом и опциями не меняются.
type Option func(*prometheus)

// WithClock подменяет часы: нужны, когда Request.At не задан (по умолчанию — time.Now). Для тестов.
func WithClock(now func() time.Time) Option {
	return func(p *prometheus) {
		if now != nil {
			p.now = now
		}
	}
}

// prometheus — реализация Provider поверх встроенного стека мониторинга Kubron.
type prometheus struct {
	q   Querier
	now func() time.Time
}

// NewPrometheus создаёт Provider поверх встроенного Prometheus. Выражения строит каталог сигналов; Querier только
// выполняет их (слой связки поверх opsplatform.MetricsReader).
func NewPrometheus(q Querier, opts ...Option) Provider {
	p := &prometheus{q: q, now: time.Now}
	for _, o := range opts {
		o(p)
	}
	return p
}

// item — один запрошенный сигнал в ходе Fetch.
type item struct {
	want Want
	sp   *spec
	expr string
	gap  Reason // не пусто — значения не будет, запрос не нужен
	fail bool   // пробел — следствие отказа существующего стека
}

// probeResult — итог одной проверки.
type probeResult struct {
	samples []Sample
	err     error
}

// Fetch выдаёт сигналы. Недоступность метрик выражается причинами в Set и ошибкой не становится; ошибка — только
// программная: нет Querier или сигнал вне каталога.
func (p *prometheus) Fetch(ctx context.Context, req Request) (*Set, error) {
	if p.q == nil {
		return nil, errors.New("signals: no querier")
	}
	at := req.At
	if at.IsZero() {
		at = p.now().UTC()
	}

	items, err := plan(req.Want)
	if err != nil {
		return nil, err
	}

	// Бюджет запросов сигналов: первые MaxSignalQueries допустимых в порядке запроса; остальные — без запроса.
	kept := 0
	for i := range items {
		if items[i].gap != "" {
			continue
		}
		if kept++; kept > MaxSignalQueries {
			items[i].gap = ReasonUnavailable // нет бюджета; это не отказ стека
		}
	}

	families, needHistory := needs(items)
	if len(families) == 0 {
		return finish("", items, nil, false), nil
	}

	// Фаза 1: проверка наличия семейств и покрытия истории (не более MaxProbeQueries запросов, параллельно).
	exprs := make([]string, 0, len(families)+1)
	for _, f := range families {
		exprs = append(exprs, familyProbe[f])
	}
	if needHistory {
		exprs = append(exprs, coverageProbe)
	}
	probes := p.runAll(ctx, exprs, at)

	if r := stackReason(probes); r != "" {
		return finish(r, items, nil, r == ReasonUnavailable), nil
	}

	state := make(map[Family]Reason, len(families)) // пусто — семейство есть
	for i, f := range families {
		switch pr := probes[i]; {
		case pr.err != nil:
			state[f] = ReasonUnavailable
		case !positive(pr.samples):
			state[f] = ReasonFamilyMissing
		}
	}
	var lowest time.Time // нулевое — покрытие неизвестно
	if needHistory {
		if pr := probes[len(probes)-1]; pr.err == nil {
			lowest = coverageStart(pr.samples, at)
		}
	}

	// Фаза 2: запросы сигналов, у которых есть все нужные семейства и хватает истории.
	var run []int
	for i := range items {
		it := &items[i]
		if it.gap != "" {
			continue
		}
		if r := familiesGap(it.sp, state); r != "" {
			it.gap, it.fail = r, r == ReasonUnavailable
			continue
		}
		if !lowest.IsZero() && it.sp.need() > 0 && lowest.After(at.Add(-it.sp.need()).Add(coverageSlack)) {
			it.gap = ReasonHistoryShort
			continue
		}
		run = append(run, i)
	}
	sig := make([]string, len(run))
	for j, i := range run {
		sig[j] = items[i].expr
	}
	results := p.runAll(ctx, sig, at)

	values := make(map[int]Value, len(run))
	for j, i := range run {
		it := &items[i]
		pr := results[j]
		if pr.err != nil {
			it.gap, it.fail = errReason(pr.err), !errors.Is(pr.err, ErrNotFound) && !errors.Is(pr.err, ErrForbidden)
			continue
		}
		v, ok := firstFinite(pr.samples)
		if !ok {
			it.gap = ReasonNoSeries // пусто, NaN или бесконечность: данных нет, а не ноль
			continue
		}
		values[i] = Value{
			Signal: it.want.Signal, Subject: it.want.Subject, Value: v, Unit: it.sp.unit,
			Window: it.sp.window, Agg: it.sp.agg, ObservedAt: at,
		}
	}
	return finish("", items, values, false), nil
}

// plan убирает повторы, проверяет сигналы и параметры и строит выражения. Повтор — тот же сигнал по той же цели:
// выполняется один раз, порядок первых вхождений сохраняется.
func plan(wants []Want) ([]item, error) {
	items := make([]item, 0, len(wants))
	seen := make(map[setKey]bool, len(wants))
	for _, w := range wants {
		k := setKey{w.Signal, w.Subject}
		if seen[k] {
			continue
		}
		seen[k] = true
		sp, ok := catalog[w.Signal]
		if !ok {
			return nil, fmt.Errorf("signals: unknown signal %q", string(w.Signal))
		}
		it := item{want: w, sp: sp}
		if !validRefs(w.Subject, sp.fields...) {
			it.gap = ReasonInvalidRef
		} else {
			it.expr = sp.build(w.Subject, sp.window)
		}
		items = append(items, it)
	}
	return items, nil
}

// needs возвращает семейства, нужные сигналам с запросом (в фиксированном порядке), и нужна ли история.
func needs(items []item) (families []Family, history bool) {
	for _, f := range probeFamilies {
		for i := range items {
			if items[i].gap == "" && slices.Contains(items[i].sp.families, f) {
				families = append(families, f)
				break
			}
		}
	}
	for i := range items {
		if items[i].gap == "" && items[i].sp.need() > 0 {
			history = true
		}
	}
	return families, history
}

// stackReason определяет недоступность стека целиком: not_installed или forbidden по любой проверке; unavailable —
// если не удалась каждая проверка семейств (Prometheus не отвечает). Иначе пусто.
func stackReason(probes []probeResult) Reason {
	var notFound, forbidden, failed int
	for _, pr := range probes {
		switch {
		case pr.err == nil:
		case errors.Is(pr.err, ErrNotFound):
			notFound++
		case errors.Is(pr.err, ErrForbidden):
			forbidden++
		default:
			failed++
		}
	}
	switch {
	case notFound > 0:
		return ReasonNotInstalled
	case forbidden > 0:
		return ReasonForbidden
	case failed == len(probes):
		return ReasonUnavailable
	}
	return ""
}

// familiesGap возвращает причину, если не хватает семейства: недоступность важнее отсутствия.
func familiesGap(sp *spec, state map[Family]Reason) Reason {
	var r Reason
	for _, f := range sp.families {
		switch state[f] {
		case ReasonUnavailable:
			return ReasonUnavailable
		case ReasonFamilyMissing:
			r = ReasonFamilyMissing
		}
	}
	return r
}

// coverageStart читает начало истории; значение вне разумного (нет, не конечное, позже момента операции) — неизвестно.
func coverageStart(samples []Sample, at time.Time) time.Time {
	v, ok := firstFinite(samples)
	if !ok || v <= 0 {
		return time.Time{}
	}
	t := time.Unix(int64(v), 0).UTC()
	if t.After(at) {
		return time.Time{}
	}
	return t
}

// positive — проверка count вернула хотя бы одну серию.
func positive(samples []Sample) bool {
	v, ok := firstFinite(samples)
	return ok && v > 0
}

// firstFinite возвращает значение первой серии с конечным значением. Запросы каталога агрегируют до одной серии,
// поэтому серия одна; NaN и бесконечности — отсутствие данных.
func firstFinite(samples []Sample) (float64, bool) {
	for _, s := range samples {
		if !math.IsNaN(s.Value) && !math.IsInf(s.Value, 0) {
			return s.Value, true
		}
	}
	return 0, false
}

// errReason приводит ошибку Querier к причине; неизвестная ошибка — unavailable.
func errReason(err error) Reason {
	switch {
	case errors.Is(err, ErrNotFound):
		return ReasonNotInstalled
	case errors.Is(err, ErrForbidden):
		return ReasonForbidden
	}
	return ReasonUnavailable
}

// runAll выполняет выражения параллельно (число одновременных запросов ограничивает лимитер платформы) и возвращает
// результаты в порядке выражений.
func (p *prometheus) runAll(ctx context.Context, exprs []string, at time.Time) []probeResult {
	out := make([]probeResult, len(exprs))
	var wg sync.WaitGroup
	for i, e := range exprs {
		wg.Go(func() { out[i].samples, out[i].err = p.query(ctx, e, at) })
	}
	wg.Wait()
	return out
}

// query выполняет один запрос; паника Querier становится отказом этого запроса, а не расследования. Значение паники
// в ошибку не попадает: оно может содержать данные запроса.
func (p *prometheus) query(ctx context.Context, expr string, at time.Time) (s []Sample, err error) {
	defer func() {
		if recover() != nil {
			s, err = nil, fmt.Errorf("%w: query failed unexpectedly", ErrUnavailable)
		}
	}()
	return p.q.Query(ctx, expr, at)
}

// finish собирает Set: значения, пробелы и причину стека. Если стек недоступен целиком, все допустимые сигналы получают
// его причину (invalid_ref остаётся: параметр неверен независимо от стека).
func finish(stack Reason, items []item, values map[int]Value, failed bool) *Set {
	var vs []Value
	var gs []Gap
	for i := range items {
		it := &items[i]
		switch v, ok := values[i]; {
		case ok:
			vs = append(vs, v)
		case stack != "" && it.gap != ReasonInvalidRef:
			gs = append(gs, Gap{Signal: it.want.Signal, Subject: it.want.Subject, Reason: stack})
		case it.gap != "":
			gs = append(gs, Gap{Signal: it.want.Signal, Subject: it.want.Subject, Reason: it.gap})
			failed = failed || it.fail
		}
	}
	return newSet(stack, vs, gs, failed)
}
