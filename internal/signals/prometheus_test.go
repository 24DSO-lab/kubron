// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package signals

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var testAt = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

// fakeQuerier — Querier с подставляемым ответом на выражение. Запоминает все выражения.
type fakeQuerier struct {
	mu    sync.Mutex
	calls []string
	ats   []time.Time
	// answer возвращает ответ на выражение; по умолчанию — стек в порядке.
	answer func(expr string) ([]Sample, error)
}

func (q *fakeQuerier) Query(_ context.Context, expr string, at time.Time) ([]Sample, error) {
	q.mu.Lock()
	q.calls = append(q.calls, expr)
	q.ats = append(q.ats, at)
	q.mu.Unlock()
	if q.answer != nil {
		return q.answer(expr)
	}
	return healthy(0.5)(expr)
}

func (q *fakeQuerier) signalCalls() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []string
	for _, c := range q.calls {
		if !isProbe(c) {
			out = append(out, c)
		}
	}
	return out
}

func (q *fakeQuerier) probeCalls() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []string
	for _, c := range q.calls {
		if isProbe(c) {
			out = append(out, c)
		}
	}
	return out
}

func isProbe(expr string) bool {
	return expr == coverageProbe || slices.Contains(probeExprs(), expr)
}

func probeExprs() []string {
	out := make([]string, 0, len(familyProbe))
	for _, e := range familyProbe {
		out = append(out, e)
	}
	return out
}

func one(v float64) []Sample { return []Sample{{Labels: map[string]string{}, Value: v, At: testAt}} }

// healthy — стек в порядке: проверки семейств видят серии, история — два дня, каждый сигнал равен v.
func healthy(v float64) func(string) ([]Sample, error) {
	return func(expr string) ([]Sample, error) {
		if expr == coverageProbe {
			return one(float64(testAt.Add(-48 * time.Hour).Unix())), nil
		}
		if isProbe(expr) {
			return one(10), nil
		}
		return one(v), nil
	}
}

func fetch(t *testing.T, q *fakeQuerier, wants ...Want) *Set {
	t.Helper()
	set, err := NewPrometheus(q).Fetch(t.Context(), Request{At: testAt, Want: wants})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func podWant(sig Signal) Want  { return Want{sig, cSub} }
func nodeWant(sig Signal) Want { return Want{sig, nSub} }

func TestFetchReturnsValuesWithMetadata(t *testing.T) {
	q := &fakeQuerier{answer: healthy(0.25)}
	set := fetch(t, q, podWant(CPUThrottlingRatio), podWant(ContainerRestarts), nodeWant(NodeDiskAvailableRatio))

	if set.StackReason() != "" || set.PartialSource() != "" || len(set.Gaps()) != 0 {
		t.Fatalf("a healthy stack: stack=%q partial=%q gaps=%v", set.StackReason(), set.PartialSource(), set.Gaps())
	}
	v, ok := set.Get(CPUThrottlingRatio, cSub)
	if !ok || v.Value != 0.25 || v.Unit != "ratio" || v.Agg != "ratio" || v.Window != 15*time.Minute || !v.ObservedAt.Equal(testAt) || v.Limit != nil {
		t.Errorf("throttling = %+v, %v", v, ok)
	}
	if v, _ := set.Get(ContainerRestarts, cSub); v.Unit != "count" || v.Agg != "increase" || v.Window != 6*time.Hour {
		t.Errorf("restarts = %+v", v)
	}
	if v, _ := set.Get(NodeDiskAvailableRatio, nSub); v.Window != 0 || v.Agg != "last" {
		t.Errorf("an instant signal must have no window: %+v", v)
	}
	// Все запросы вычислены на момент операции.
	for _, at := range q.ats {
		if !at.Equal(testAt) {
			t.Errorf("query evaluated at %v, want the operation moment %v", at, testAt)
		}
	}
}

// Нет данных — не ноль: настоящий ноль возвращается значением, пустое и нечисловое — пробелом no_series.
func TestFetchDistinguishesZeroFromNoData(t *testing.T) {
	for name, tc := range map[string]struct {
		samples []Sample
		err     error
		value   bool
		gap     Reason
	}{
		"real zero":    {samples: one(0), value: true},
		"empty vector": {samples: nil, gap: ReasonNoSeries},
		"NaN":          {samples: one(math.NaN()), gap: ReasonNoSeries},
		"+Inf":         {samples: one(math.Inf(1)), gap: ReasonNoSeries},
		"-Inf":         {samples: one(math.Inf(-1)), gap: ReasonNoSeries},
		"NaN then 7":   {samples: []Sample{{Value: math.NaN()}, {Value: 7}}, value: true},
	} {
		t.Run(name, func(t *testing.T) {
			q := &fakeQuerier{answer: func(e string) ([]Sample, error) {
				if isProbe(e) {
					return healthy(0)(e)
				}
				return tc.samples, tc.err
			}}
			set := fetch(t, q, podWant(OOMEvents))
			_, ok := set.Get(OOMEvents, cSub)
			if ok != tc.value {
				t.Fatalf("value present = %v, want %v", ok, tc.value)
			}
			if !tc.value {
				if g, _ := set.GapOf(OOMEvents, cSub); g.Reason != tc.gap {
					t.Errorf("gap = %q, want %q", g.Reason, tc.gap)
				}
				if set.PartialSource() != "" {
					t.Error("empty data must not make the operation partial")
				}
			}
		})
	}
}

func TestFetchStackLevelReasons(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		reason  Reason
		partial string
	}{
		{"not installed", ErrNotFound, ReasonNotInstalled, ""},
		{"wrapped not installed", fmt.Errorf("proxy: %w", ErrNotFound), ReasonNotInstalled, ""},
		{"forbidden", ErrForbidden, ReasonForbidden, ""},
		{"unavailable", ErrUnavailable, ReasonUnavailable, "metrics"},
		{"timeout", context.DeadlineExceeded, ReasonUnavailable, "metrics"},
		{"unknown error", errors.New("boom"), ReasonUnavailable, "metrics"},
		{"too large", ErrTooLarge, ReasonUnavailable, "metrics"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &fakeQuerier{answer: func(string) ([]Sample, error) { return nil, tc.err }}
			set := fetch(t, q, podWant(CPUThrottlingRatio), nodeWant(NodeCPUUtilization), podWant(OOMEvents))
			if set.StackReason() != tc.reason || set.PartialSource() != tc.partial {
				t.Errorf("stack=%q partial=%q, want %q %q", set.StackReason(), set.PartialSource(), tc.reason, tc.partial)
			}
			if len(set.Values()) != 0 || len(set.Gaps()) != 3 {
				t.Fatalf("values=%d gaps=%d", len(set.Values()), len(set.Gaps()))
			}
			for _, g := range set.Gaps() {
				if g.Reason != tc.reason {
					t.Errorf("gap %v: reason %q, want %q", g.Signal, g.Reason, tc.reason)
				}
			}
			if n := len(q.signalCalls()); n != 0 {
				t.Errorf("no signal query may run when the stack is down, ran %d", n)
			}
			if n := len(q.probeCalls()); n > MaxProbeQueries {
				t.Errorf("%d probes exceed the budget", n)
			}
		})
	}
}

func TestFetchFamilyMissingIsolatesFamilies(t *testing.T) {
	q := &fakeQuerier{answer: func(e string) ([]Sample, error) {
		if e == familyProbe[FamilyContainer] {
			return nil, nil // cAdvisor нет
		}
		return healthy(0.5)(e)
	}}
	set := fetch(t, q, podWant(OOMEvents), podWant(ContainerRestarts), podWant(MemoryUsageVsLimit), nodeWant(NodeCPUUtilization))
	if g, _ := set.GapOf(OOMEvents, cSub); g.Reason != ReasonFamilyMissing {
		t.Errorf("oom: %+v", g)
	}
	if g, _ := set.GapOf(MemoryUsageVsLimit, cSub); g.Reason != ReasonFamilyMissing {
		t.Errorf("a signal that needs a missing family must be a gap: %+v", g)
	}
	if _, ok := set.Get(ContainerRestarts, cSub); !ok {
		t.Error("kube-state signals must survive a missing cAdvisor")
	}
	if _, ok := set.Get(NodeCPUUtilization, nSub); !ok {
		t.Error("node signals must survive a missing cAdvisor")
	}
	if set.PartialSource() != "" || set.StackReason() != "" {
		t.Errorf("a missing family is not a stack failure: %q %q", set.StackReason(), set.PartialSource())
	}
	for _, c := range q.signalCalls() {
		if strings.Contains(c, "container_oom_events_total") || strings.Contains(c, "container_memory_working_set_bytes") {
			t.Errorf("a query for the missing family was sent: %s", c)
		}
	}
}

// Одна неудавшаяся проверка при живых остальных: сигналы этого семейства — unavailable, операция частичная.
func TestFetchOneFamilyUnavailable(t *testing.T) {
	q := &fakeQuerier{answer: func(e string) ([]Sample, error) {
		if e == familyProbe[FamilyNode] {
			return nil, ErrUnavailable
		}
		return healthy(0.5)(e)
	}}
	set := fetch(t, q, podWant(OOMEvents), nodeWant(NodeCPUUtilization))
	if g, _ := set.GapOf(NodeCPUUtilization, nSub); g.Reason != ReasonUnavailable {
		t.Errorf("node gap = %+v", g)
	}
	if _, ok := set.Get(OOMEvents, cSub); !ok {
		t.Error("an unrelated family must keep working")
	}
	if set.StackReason() != "" || set.PartialSource() != "metrics" {
		t.Errorf("stack=%q partial=%q", set.StackReason(), set.PartialSource())
	}
}

func TestFetchLazyProbes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		wants      []Want
		wantProbes int
	}{
		{"instant node signal: one family, no history", []Want{nodeWant(NodeDiskAvailableRatio)}, 1},
		{"node signal with a window: family + history", []Want{nodeWant(NodeCPUUtilization)}, 2},
		{"kube-state only", []Want{podWant(ContainerRestarts)}, 2},
		{"all three families", []Want{podWant(OOMEvents), podWant(ContainerRestarts), nodeWant(NodeCPUUtilization)}, 4},
		{"two signals of one family share one probe", []Want{podWant(OOMEvents), podWant(CPUThrottlingRatio)}, 2},
		{"nothing valid, nothing probed", []Want{{OOMEvents, Subject{Namespace: "BAD", Pod: "p", Container: "c"}}}, 0},
		{"no wants", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &fakeQuerier{}
			fetch(t, q, tc.wants...)
			if got := len(q.probeCalls()); got != tc.wantProbes {
				t.Errorf("%d probes, want %d: %v", got, tc.wantProbes, q.probeCalls())
			}
		})
	}
}

func TestFetchHistoryCoverage(t *testing.T) {
	at := func(d time.Duration) float64 { return float64(testAt.Add(-d).Unix()) }
	for _, tc := range []struct {
		name     string
		coverage []Sample
		covErr   error
		oom      Reason // 6 ч
		cpu      Reason // 15 мин + 5 мин rate
		disk     bool   // мгновенный сигнал всегда получает значение
	}{
		{"two days: everything fits", one(at(48 * time.Hour)), nil, "", "", true},
		{"10 minutes: both windows too long", one(at(10 * time.Minute)), nil, ReasonHistoryShort, ReasonHistoryShort, true},
		{"1 hour: 6h window too long, 20m fits", one(at(time.Hour)), nil, ReasonHistoryShort, "", true},
		{"exactly the window", one(at(6 * time.Hour)), nil, "", "", true},
		{"within the slack", one(at(6*time.Hour - 30*time.Second)), nil, "", "", true},
		{"beyond the slack", one(at(6*time.Hour - 2*time.Minute)), nil, ReasonHistoryShort, "", true},
		{"coverage unknown (no series)", nil, nil, "", "", true},
		{"coverage probe failed", nil, ErrUnavailable, "", "", true},
		{"coverage NaN", one(math.NaN()), nil, "", "", true},
		{"coverage in the future", one(float64(testAt.Add(time.Hour).Unix())), nil, "", "", true},
		{"coverage zero", one(0), nil, "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &fakeQuerier{answer: func(e string) ([]Sample, error) {
				if e == coverageProbe {
					return tc.coverage, tc.covErr
				}
				return healthy(0.5)(e)
			}}
			set := fetch(t, q, podWant(OOMEvents), Want{CPUUsageVsLimit, cSub}, nodeWant(NodeDiskAvailableRatio))
			check := func(sig Signal, sub Subject, want Reason) {
				t.Helper()
				_, has := set.Get(sig, sub)
				g, gap := set.GapOf(sig, sub)
				if want == "" && (!has || gap) || want != "" && (has || g.Reason != want) {
					t.Errorf("%s: value=%v gap=%+v, want reason %q", sig, has, g, want)
				}
			}
			check(OOMEvents, cSub, tc.oom)
			check(CPUUsageVsLimit, cSub, tc.cpu)
			check(NodeDiskAvailableRatio, nSub, "")
			if set.PartialSource() != "" || set.StackReason() != "" {
				t.Errorf("a short history is not a failure: stack=%q partial=%q", set.StackReason(), set.PartialSource())
			}
			// Сигнал с недостаточной историей не запрашивается: обманчиво спокойного значения не будет.
			for _, c := range q.signalCalls() {
				if tc.oom == ReasonHistoryShort && strings.Contains(c, "container_oom_events_total") {
					t.Errorf("a signal with a short history was queried: %s", c)
				}
			}
		})
	}
}

func TestFetchInvalidRefsAreGapsNotQueries(t *testing.T) {
	bad := Subject{Namespace: "shop", Pod: `p"} or vector(1) #`, Container: "app"}
	q := &fakeQuerier{}
	set := fetch(t, q, Want{ContainerRestarts, bad}, podWant(ContainerRestarts))
	if g, _ := set.GapOf(ContainerRestarts, bad); g.Reason != ReasonInvalidRef {
		t.Errorf("gap = %+v", g)
	}
	if _, ok := set.Get(ContainerRestarts, cSub); !ok {
		t.Error("a valid target next to an invalid one must still be answered")
	}
	for _, c := range q.calls {
		if strings.Contains(c, "vector(1)") {
			t.Errorf("an unvalidated value reached the query: %s", c)
		}
	}
	// Недоступный стек не затирает invalid_ref: параметр неверен независимо от стека.
	down := &fakeQuerier{answer: func(string) ([]Sample, error) { return nil, ErrNotFound }}
	set = fetch(t, down, Want{ContainerRestarts, bad}, podWant(ContainerRestarts))
	if g, _ := set.GapOf(ContainerRestarts, bad); g.Reason != ReasonInvalidRef {
		t.Errorf("invalid_ref was overwritten by the stack reason: %+v", g)
	}
	if g, _ := set.GapOf(ContainerRestarts, cSub); g.Reason != ReasonNotInstalled {
		t.Errorf("valid target: %+v", g)
	}
}

func TestFetchBudget(t *testing.T) {
	wants := make([]Want, 0, MaxSignalQueries+3)
	for i := range MaxSignalQueries + 3 {
		wants = append(wants, Want{OOMEvents, Subject{Namespace: "shop", Pod: fmt.Sprintf("p-%d", i), Container: "app"}})
	}
	q := &fakeQuerier{}
	set := fetch(t, q, wants...)
	if n := len(q.signalCalls()); n != MaxSignalQueries {
		t.Errorf("%d signal queries, want the budget of %d", n, MaxSignalQueries)
	}
	if len(set.Values()) != MaxSignalQueries || len(set.Gaps()) != 3 {
		t.Fatalf("values=%d gaps=%d", len(set.Values()), len(set.Gaps()))
	}
	for i, w := range wants {
		_, has := set.Get(w.Signal, w.Subject)
		g, gap := set.GapOf(w.Signal, w.Subject)
		if i < MaxSignalQueries && !has || i >= MaxSignalQueries && (!gap || g.Reason != ReasonUnavailable) {
			t.Errorf("want %d: value=%v gap=%+v (the first wants get the budget)", i, has, g)
		}
	}
	if set.PartialSource() != "" {
		t.Error("running out of budget is not a failure of the stack")
	}
	// Невалидные запросы бюджет не занимают.
	mixed := []Want{{OOMEvents, Subject{Namespace: "BAD", Pod: "p", Container: "c"}}}
	mixed = append(mixed, wants[:MaxSignalQueries]...)
	q = &fakeQuerier{}
	set = fetch(t, q, mixed...)
	if len(set.Values()) != MaxSignalQueries {
		t.Errorf("an invalid want consumed the budget: %d values", len(set.Values()))
	}
}

func TestFetchDedupesRepeatedWants(t *testing.T) {
	q := &fakeQuerier{}
	set := fetch(t, q, podWant(OOMEvents), podWant(OOMEvents), podWant(OOMEvents))
	if n := len(q.signalCalls()); n != 1 || len(set.Values()) != 1 {
		t.Errorf("%d queries, %d values", n, len(set.Values()))
	}
}

func TestFetchSignalErrorsAreIsolated(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		reason  Reason
		partial string
	}{
		{"unavailable", ErrUnavailable, ReasonUnavailable, "metrics"},
		{"too large", ErrTooLarge, ReasonUnavailable, "metrics"},
		{"timeout", context.DeadlineExceeded, ReasonUnavailable, "metrics"},
		{"forbidden", ErrForbidden, ReasonForbidden, ""},
		{"not found", ErrNotFound, ReasonNotInstalled, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &fakeQuerier{answer: func(e string) ([]Sample, error) {
				if strings.Contains(e, "container_oom_events_total") {
					return nil, tc.err
				}
				return healthy(0.5)(e)
			}}
			set := fetch(t, q, podWant(OOMEvents), podWant(ContainerRestarts))
			if g, _ := set.GapOf(OOMEvents, cSub); g.Reason != tc.reason {
				t.Errorf("gap = %+v, want %q", g, tc.reason)
			}
			if _, ok := set.Get(ContainerRestarts, cSub); !ok {
				t.Error("one failing signal must not affect the others")
			}
			if set.PartialSource() != tc.partial || set.StackReason() != "" {
				t.Errorf("stack=%q partial=%q, want partial %q", set.StackReason(), set.PartialSource(), tc.partial)
			}
		})
	}
}

func TestFetchSurvivesQuerierPanic(t *testing.T) {
	const secret = "TOP-SECRET-PANIC"
	q := &fakeQuerier{answer: func(e string) ([]Sample, error) {
		if strings.Contains(e, "container_oom_events_total") {
			panic(secret)
		}
		return healthy(0.5)(e)
	}}
	set := fetch(t, q, podWant(OOMEvents), podWant(ContainerRestarts))
	if g, _ := set.GapOf(OOMEvents, cSub); g.Reason != ReasonUnavailable {
		t.Errorf("gap = %+v", g)
	}
	if _, ok := set.Get(ContainerRestarts, cSub); !ok {
		t.Error("a panic in one query must not affect the others")
	}
}

func TestFetchCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	q := &fakeQuerier{answer: func(string) ([]Sample, error) { return nil, ctx.Err() }}
	set, err := NewPrometheus(q).Fetch(ctx, Request{At: testAt, Want: []Want{podWant(OOMEvents)}})
	if err != nil {
		t.Fatalf("a cancelled context is not a programming error: %v", err)
	}
	if set.StackReason() != ReasonUnavailable || set.PartialSource() != "metrics" {
		t.Errorf("stack=%q partial=%q", set.StackReason(), set.PartialSource())
	}
}

func TestFetchProgrammingErrors(t *testing.T) {
	if _, err := NewPrometheus(nil).Fetch(t.Context(), Request{Want: []Want{podWant(OOMEvents)}}); err == nil {
		t.Error("a nil querier must be an error")
	}
	_, err := NewPrometheus(&fakeQuerier{}).Fetch(t.Context(), Request{Want: []Want{{Signal("pvc_usage"), cSub}}})
	if err == nil || !strings.Contains(err.Error(), "unknown signal") {
		t.Errorf("an unknown signal: %v", err)
	}
	if _, err := NewPrometheus(&fakeQuerier{}).Fetch(t.Context(), Request{}); err != nil {
		t.Errorf("an empty request: %v", err)
	}
}

func TestFetchZeroAtUsesClock(t *testing.T) {
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	q := &fakeQuerier{}
	set, err := NewPrometheus(q, WithClock(func() time.Time { return clock })).Fetch(t.Context(), Request{Want: []Want{podWant(OOMEvents)}})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := set.Get(OOMEvents, cSub); !v.ObservedAt.Equal(clock) {
		t.Errorf("ObservedAt = %v, want %v", v.ObservedAt, clock)
	}
	for _, at := range q.ats {
		if !at.Equal(clock) {
			t.Errorf("query at %v, want %v", at, clock)
		}
	}
	WithClock(nil)(&prometheus{}) // nil не ломает часы
}

// Результат не зависит от порядка завершения параллельных запросов.
func TestFetchIsDeterministic(t *testing.T) {
	wants := []Want{
		podWant(OOMEvents), podWant(ContainerRestarts), podWant(CPUThrottlingRatio), nodeWant(NodeCPUUtilization),
		nodeWant(NodeMemoryAvailableRatio), nodeWant(NodePressureHistory),
	}
	var first []Value
	for range 20 {
		q := &fakeQuerier{answer: func(e string) ([]Sample, error) {
			if isProbe(e) {
				return healthy(0)(e)
			}
			return one(float64(len(e))), nil // значение зависит от выражения
		}}
		got := fetch(t, q, wants...).Values()
		if first == nil {
			first = got
			continue
		}
		if !slices.EqualFunc(first, got, func(a, b Value) bool { return a == b }) {
			t.Fatalf("results differ between runs:\n%v\n%v", first, got)
		}
	}
}

// Запросы идут параллельно: проверка по лимитеру платформы, а не по последовательной очереди.
func TestFetchRunsQueriesConcurrently(t *testing.T) {
	var mu sync.Mutex
	inflight, peak := 0, 0
	release := make(chan struct{})
	var once sync.Once
	q := &fakeQuerier{answer: func(e string) ([]Sample, error) {
		if isProbe(e) {
			return healthy(0)(e)
		}
		mu.Lock()
		inflight++
		peak = max(peak, inflight)
		if inflight >= 3 {
			once.Do(func() { close(release) })
		}
		mu.Unlock()
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		mu.Lock()
		inflight--
		mu.Unlock()
		return one(1), nil
	}}
	fetch(t, q, podWant(OOMEvents), podWant(ContainerRestarts), nodeWant(NodeCPUUtilization), nodeWant(NodeDiskAvailableRatio))
	if peak < 3 {
		t.Errorf("peak concurrency %d: signal queries ran one by one", peak)
	}
}
