// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package feedback_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/feedback"
)

var (
	t0   = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	hOOM = feedback.Ref{RuleID: "pod.oom", Epoch: 1, Code: "memory_limit_too_low"}
	hApp = feedback.Ref{RuleID: "pod.crash", Epoch: 1, Code: "app_exits"}
	rBmp = feedback.Ref{RuleID: "pod.oom", Epoch: 1, Code: "raise_memory_limit"}
)

func at(d time.Duration) *time.Time { v := t0.Add(d); return &v }

func resolvedWith(id string, confirmed feedback.Ref, d time.Duration, rejected ...feedback.Ref) feedback.Record {
	return feedback.Record{ID: id, Result: feedback.ResultResolved, OpenedAt: t0, ResolvedAt: at(d), RuleSetVersion: 2,
		Symptoms: []string{"pod.oom_killed"}, Confirmed: &confirmed, Rejected: rejected}
}

func hyp(st feedback.Stats, ref feedback.Ref) (feedback.HypothesisStat, bool) {
	for _, h := range st.Hypotheses {
		if h.Ref == ref {
			return h, true
		}
	}
	return feedback.HypothesisStat{}, false
}

func TestComputeEmpty(t *testing.T) {
	t.Parallel()
	st := feedback.Compute(nil, t0)
	if st.Records != 0 || !st.ComputedAt.Equal(t0) {
		t.Errorf("stats = %+v", st)
	}
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), "null") {
		t.Errorf("nil slice in empty stats: %s", raw)
	}
}

func TestPrecisionThreshold(t *testing.T) {
	t.Parallel()
	var recs []feedback.Record
	for i := range 3 {
		recs = append(recs, resolvedWith(fmt.Sprint("c", i), hOOM, time.Hour))
	}
	recs = append(recs, resolvedWith("r1", hApp, time.Hour, hOOM)) // отвергнута: 3 подтверждена, 1 отвергнута
	st := feedback.Compute(recs, t0)
	h, ok := hyp(st, hOOM)
	if !ok || h.Confirmed != 3 || h.Rejected != 1 || h.Sample != 4 || h.Trusted || h.Precision != nil {
		t.Fatalf("below threshold: %+v", h)
	}

	recs = append(recs, resolvedWith("c4", hOOM, time.Hour)) // 4 подтверждена, 1 отвергнута: sample 5
	st = feedback.Compute(recs, t0)
	h, _ = hyp(st, hOOM)
	if h.Sample != 5 || !h.Trusted || h.Precision == nil || *h.Precision != 0.8 {
		t.Fatalf("at threshold: %+v", h)
	}
}

func TestUnresolvedAndInconclusiveDoNotEnterPrecision(t *testing.T) {
	t.Parallel()
	recs := []feedback.Record{
		{ID: "u", Result: "unresolved", OpenedAt: t0, Symptoms: []string{"pod.pending"}, Confirmed: &hOOM, Rejected: []feedback.Ref{hApp}},
		{ID: "i", Result: "inconclusive", OpenedAt: t0, Symptoms: []string{"pod.pending"}, Confirmed: &hOOM},
		{ID: "n", Result: "inconclusive", OpenedAt: t0, Symptoms: []string{"pod.pending"}},
	}
	st := feedback.Compute(recs, t0)
	if len(st.Hypotheses) != 0 {
		t.Errorf("hypotheses = %+v", st.Hypotheses)
	}
	if st.Records != 3 || st.Unevaluated != 1 {
		t.Errorf("records = %d unevaluated = %d", st.Records, st.Unevaluated)
	}
	// В покрытии симптома они учитываются.
	if len(st.Symptoms) != 1 || st.Symptoms[0].Closed != 3 || st.Symptoms[0].WithConfirmed != 2 || st.Symptoms[0].Coverage != nil {
		t.Errorf("symptoms = %+v", st.Symptoms)
	}
}

func TestSymptomCoverageAndNone(t *testing.T) {
	t.Parallel()
	var recs []feedback.Record
	for i := range 5 {
		r := feedback.Record{ID: fmt.Sprint(i), Result: "resolved", OpenedAt: t0, Symptoms: []string{"pod.crash_loop", "none", "pod.crash_loop"}}
		if i < 2 {
			r.Confirmed = &hApp
		}
		recs = append(recs, r)
	}
	st := feedback.Compute(recs, t0)
	if len(st.Symptoms) != 1 {
		t.Fatalf("symptoms = %+v", st.Symptoms)
	}
	s := st.Symptoms[0]
	if s.Code != "pod.crash_loop" || s.Closed != 5 || s.WithConfirmed != 2 || s.Coverage == nil || *s.Coverage != 0.4 {
		t.Errorf("symptom = %+v", s)
	}
}

func TestRecordsWithoutRuleIDAreSkipped(t *testing.T) {
	t.Parallel()
	legacy := feedback.Ref{Code: "memory_limit_too_low"}
	recs := []feedback.Record{
		{ID: "a", Result: "resolved", OpenedAt: t0, ResolvedAt: at(time.Hour), Confirmed: &legacy, Rejected: []feedback.Ref{legacy}, Helpful: []feedback.Ref{legacy}},
		{ID: "b", Result: "resolved", OpenedAt: t0},
	}
	st := feedback.Compute(recs, t0)
	if len(st.Hypotheses) != 0 || len(st.Recommendations) != 0 || st.Unevaluated != 2 {
		t.Errorf("stats = %+v", st)
	}
}

func TestEpochsAreSeparate(t *testing.T) {
	t.Parallel()
	e2 := hOOM
	e2.Epoch = 2
	st := feedback.Compute([]feedback.Record{
		resolvedWith("a", hOOM, time.Hour), resolvedWith("b", e2, time.Hour), resolvedWith("c", e2, time.Hour),
	}, t0)
	if len(st.Hypotheses) != 2 {
		t.Fatalf("hypotheses = %+v", st.Hypotheses)
	}
	if st.Hypotheses[0].Epoch != 1 || st.Hypotheses[0].Confirmed != 1 || st.Hypotheses[1].Epoch != 2 || st.Hypotheses[1].Confirmed != 2 {
		t.Errorf("epoch split: %+v", st.Hypotheses)
	}
}

func TestMedianResolveSeconds(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		d    []time.Duration
		want int64
	}{
		"odd":  {[]time.Duration{10 * time.Minute, time.Hour, 20 * time.Minute}, 1200},
		"even": {[]time.Duration{10 * time.Minute, 20 * time.Minute}, 900},
		"one":  {[]time.Duration{90 * time.Second}, 90},
	}
	for name, c := range cases {
		var recs []feedback.Record
		for i, d := range c.d {
			recs = append(recs, resolvedWith(fmt.Sprint(i), hOOM, d))
		}
		h, _ := hyp(feedback.Compute(recs, t0), hOOM)
		if h.MedianResolveSeconds == nil || *h.MedianResolveSeconds != c.want {
			t.Errorf("%s: median = %v, want %d", name, h.MedianResolveSeconds, c.want)
		}
	}
	// ResolvedAt раньше OpenedAt и отсутствующий ResolvedAt — не данные.
	bad := resolvedWith("x", hOOM, -time.Hour)
	none := resolvedWith("y", hOOM, 0)
	none.ResolvedAt = nil
	h, _ := hyp(feedback.Compute([]feedback.Record{bad, none}, t0), hOOM)
	if h.MedianResolveSeconds != nil || h.Resolved != 2 {
		t.Errorf("invalid durations: %+v", h)
	}
}

func TestRecurrence(t *testing.T) {
	t.Parallel()
	first := resolvedWith("first", hOOM, time.Hour)
	second := resolvedWith("second", hOOM, time.Hour)
	second.RecurrenceOf = "first"
	third := resolvedWith("third", hApp, time.Hour)
	st := feedback.Compute([]feedback.Record{first, second, third}, t0)
	h, _ := hyp(st, hOOM)
	if h.Resolved != 2 || h.Recurred != 1 {
		t.Errorf("recurrence: %+v", h)
	}
	if h2, _ := hyp(st, hApp); h2.Recurred != 0 {
		t.Errorf("unrelated recurrence: %+v", h2)
	}
}

func TestRecommendationUsefulness(t *testing.T) {
	t.Parallel()
	mk := func(helpful, useless int) feedback.RecommendationStat {
		var recs []feedback.Record
		for i := range helpful {
			recs = append(recs, feedback.Record{ID: fmt.Sprint("h", i), Result: "resolved", Helpful: []feedback.Ref{rBmp, rBmp}})
		}
		for i := range useless {
			recs = append(recs, feedback.Record{ID: fmt.Sprint("u", i), Result: "unresolved", Useless: []feedback.Ref{rBmp}})
		}
		st := feedback.Compute(recs, t0)
		if len(st.Recommendations) != 1 {
			t.Fatalf("recommendations = %+v", st.Recommendations)
		}
		return st.Recommendations[0]
	}
	cases := map[string]struct {
		helpful, useless int
		trusted, flagged bool
	}{
		"small sample never flagged": {0, 4, false, false},
		"exactly 70% of 10":          {3, 7, true, true},
		"just below 70%":             {4, 6, true, false},
		"all useless":                {0, 5, true, true},
		"all helpful":                {5, 0, true, false},
	}
	for name, c := range cases {
		s := mk(c.helpful, c.useless)
		if s.Sample != c.helpful+c.useless || s.Trusted != c.trusted || s.PreviouslyUnhelpful != c.flagged {
			t.Errorf("%s: %+v", name, s)
		}
		if (s.Ratio != nil) != c.trusted {
			t.Errorf("%s: ratio presence %v", name, s.Ratio)
		}
	}
	if s := mk(3, 7); *s.Ratio != 0.3 {
		t.Errorf("ratio = %v", *s.Ratio)
	}
}

func TestCorrelationPrecision(t *testing.T) {
	t.Parallel()
	mk := func(id, verdict, result string) feedback.Record {
		return feedback.Record{ID: id, Result: result, CorrelationKind: "shared_node", CorrelationConfidence: "high", CorrelationVerdict: verdict}
	}
	recs := []feedback.Record{
		mk("1", "confirmed", "resolved"), mk("2", "confirmed", "resolved"), mk("3", "confirmed", "resolved"),
		mk("4", "rejected", "resolved"), mk("5", "unknown", "resolved"), mk("6", "rejected", "unresolved"),
	}
	st := feedback.Compute(recs, t0)
	if len(st.Correlations) != 1 || st.Correlations[0].Confirmed != 3 || st.Correlations[0].Rejected != 1 || st.Correlations[0].Precision != nil {
		t.Fatalf("below threshold: %+v", st.Correlations)
	}
	if st.Unevaluated != 1 { // только «unknown»
		t.Errorf("unevaluated = %d", st.Unevaluated)
	}
	recs = append(recs, mk("7", "confirmed", "resolved"))
	st = feedback.Compute(recs, t0)
	if p := st.Correlations[0].Precision; p == nil || *p != 0.8 {
		t.Errorf("precision = %v", p)
	}
}

func TestFeatureSplit(t *testing.T) {
	t.Parallel()
	var recs []feedback.Record
	for i := range 5 {
		r := resolvedWith(fmt.Sprint("m", i), hOOM, time.Hour)
		r.Features = []string{feedback.FeatureMetrics, feedback.FeatureGraph}
		recs = append(recs, r)
	}
	recs = append(recs, resolvedWith("plain", hApp, time.Hour, hOOM))
	st := feedback.Compute(recs, t0)
	byName := map[string]feedback.FeatureStat{}
	for _, f := range st.Features {
		byName[f.Feature] = f
	}
	m := byName[feedback.FeatureMetrics]
	if m.With.Records != 5 || m.With.Confirmed != 5 || m.With.Precision == nil || *m.With.Precision != 1 ||
		m.Without.Records != 1 || m.Without.Confirmed != 1 || m.Without.Rejected != 1 || m.Without.Precision != nil {
		t.Errorf("metrics = %+v", m)
	}
	if c := byName[feedback.FeatureCorrelation]; c.With.Records != 0 || c.Without.Records != 6 {
		t.Errorf("correlation = %+v", c)
	}
	if len(st.Features) != 3 {
		t.Errorf("features = %+v", st.Features)
	}
}

func TestPerRecordDedupAndVersions(t *testing.T) {
	t.Parallel()
	r := resolvedWith("a", hOOM, time.Hour, hApp, hApp)
	r.Helpful = []feedback.Ref{rBmp, rBmp}
	r2 := resolvedWith("b", hOOM, time.Hour)
	r2.RuleSetVersion = 3
	st := feedback.Compute([]feedback.Record{r, r2}, t0)
	if h, _ := hyp(st, hApp); h.Rejected != 1 {
		t.Errorf("duplicate rejection counted twice: %+v", h)
	}
	if st.Recommendations[0].Helpful != 1 {
		t.Errorf("duplicate helpful: %+v", st.Recommendations[0])
	}
	if !reflect.DeepEqual(st.RuleSetVersions, []int{2, 3}) {
		t.Errorf("versions = %v", st.RuleSetVersions)
	}
}

func TestComputeIsDeterministic(t *testing.T) {
	t.Parallel()
	var recs []feedback.Record
	for i := range 40 {
		ref := []feedback.Ref{hOOM, hApp}[i%2]
		r := resolvedWith(fmt.Sprint(i), ref, time.Duration(i+1)*time.Minute, []feedback.Ref{hApp, hOOM}[i%2])
		r.Helpful = []feedback.Ref{rBmp}
		r.Symptoms = []string{"pod.oom_killed", "pod.crash_loop"}
		recs = append(recs, r)
	}
	want, _ := json.Marshal(feedback.Compute(recs, t0))
	rng := rand.New(rand.NewPCG(1, 2))
	for range 10 {
		shuffled := append([]feedback.Record(nil), recs...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		if got, _ := json.Marshal(feedback.Compute(shuffled, t0)); string(got) != string(want) {
			t.Fatalf("order dependent result:\n%s\n%s", got, want)
		}
	}
}
