// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/24DSO-lab/kubron/internal/signals"
)

var t0 = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

// metricsPod — Pod с контейнером app: oom=true — убит по памяти (лимит 256Mi), иначе цикл перезапусков.
func metricsPod(name string, restarts int32, oom bool) *corev1.Pod {
	st := corev1.ContainerStatus{Name: "app", RestartCount: restarts}
	if oom {
		st.LastTerminationState.Terminated = &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137, FinishedAt: metav1.NewTime(t0.Add(-10 * time.Minute))}
	} else {
		st.State.Waiting = &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}
		st.LastTerminationState.Terminated = &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1, FinishedAt: metav1.NewTime(t0.Add(-time.Minute))}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: name, UID: "u-" + "x"},
		Spec: corev1.PodSpec{NodeName: "n1", Containers: []corev1.Container{{Name: "app", Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Mi")}}}}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{st}},
	}
}

func metricsCtx(pods ...*corev1.Pod) *Context {
	return &Context{Target: ResourceRef{Kind: "Pod", Namespace: "ns", Name: pods[0].Name}, ObservedAt: t0, Pods: pods,
		Nodes: map[string]*corev1.Node{}}
}

// analyzeWith повторяет Analyze с пост-хуком метрик: хуки ядра подключаются после его влития.
func analyzeWith(c *Context, o *signalOutcome) Report {
	b := newBuilder(c)
	b.touch(c.Target)
	for _, r := range rules {
		runRule(b, r)
	}
	applyMetrics(b, o)
	return *b.rep
}

var (
	appSub  = signals.Subject{Namespace: "ns", Pod: "oom", Container: "app"}
	nodeSub = signals.Subject{Node: "n1"}
)

func val(sig signals.Signal, s signals.Subject, v float64, unit string) signals.Value {
	return signals.Value{Signal: sig, Subject: s, Value: v, Unit: unit, Agg: "max", Window: time.Hour, ObservedAt: t0}
}

func outcome(c *Context, set *signals.Set) *signalOutcome {
	return &signalOutcome{Wants: collectWants(c), Set: set}
}

func hyp(rep Report, code string) *Hypothesis {
	for i := range rep.Hypotheses {
		if rep.Hypotheses[i].Code == code {
			return &rep.Hypotheses[i]
		}
	}
	return nil
}

func TestNoMetricsLeavesReportIdentical(t *testing.T) {
	c := metricsCtx(metricsPod("oom", 4, true))
	if !reflect.DeepEqual(Analyze(c), analyzeWith(c, nil)) {
		t.Fatal("report changed without metrics")
	}
}

func TestMemoryNearLimitConfirmsOnceAndCapsAtMedium(t *testing.T) {
	c := metricsCtx(metricsPod("oom", 4, true))
	base := Analyze(c)
	if h := hyp(base, "oom.limit_too_low"); h == nil || h.Confidence != ConfidenceMedium {
		t.Fatalf("fixture changed: %+v", h)
	}
	set := signals.NewSet("", []signals.Value{
		val(signals.MemoryUsageVsLimit, appSub, 0.97, "ratio"),
		val(signals.OOMEvents, appSub, 2, "count"),
	}, nil)
	rep := analyzeWith(c, outcome(c, set))
	h := hyp(rep, "oom.limit_too_low")
	if h.Confidence != ConfidenceMedium {
		t.Errorf("confidence = %s, metrics must not exceed medium", h.Confidence)
	}
	if len(h.Supporting) != len(hyp(base, "oom.limit_too_low").Supporting)+2 {
		t.Errorf("supporting facts not extended: %v", h.Supporting)
	}
	for _, f := range rep.Facts {
		if f.Code == "metric.memory_usage_vs_limit" && f.Params["percent"] != "97" {
			t.Errorf("percent = %q", f.Params["percent"])
		}
	}
	// Подтверждение не рождает дубль «память у лимита».
	if hyp(rep, hypMemoryNearLimit) != nil {
		t.Error("duplicate hypothesis next to oom.limit_too_low")
	}
}

func TestFarFromLimitLowersOnlyWithinWindow(t *testing.T) {
	c := metricsCtx(metricsPod("oom", 4, true))
	set := signals.NewSet("", []signals.Value{val(signals.MemoryUsageVsLimit, appSub, 0.30, "ratio")}, nil)
	rep := analyzeWith(c, outcome(c, set))
	if h := hyp(rep, "oom.limit_too_low"); h.Confidence != ConfidenceLow || len(h.Contradicting) != 1 {
		t.Errorf("not lowered by one level: %+v", h)
	}
	// Убийство вне окна метрики: метрика о нём ничего не знает.
	v := val(signals.MemoryUsageVsLimit, appSub, 0.30, "ratio")
	v.Window = time.Minute
	rep = analyzeWith(c, outcome(c, signals.NewSet("", []signals.Value{v}, nil)))
	if h := hyp(rep, "oom.limit_too_low"); h.Confidence != ConfidenceMedium || len(h.Contradicting) != 0 {
		t.Errorf("outside the window must not contradict: %+v", h)
	}
}

func TestConflictingMetricsKeepConfidence(t *testing.T) {
	c := metricsCtx(metricsPod("oom", 4, true))
	set := signals.NewSet("", []signals.Value{
		val(signals.MemoryUsageVsLimit, appSub, 0.30, "ratio"),
		val(signals.OOMEvents, appSub, 3, "count"),
	}, nil)
	h := hyp(analyzeWith(c, outcome(c, set)), "oom.limit_too_low")
	if h.Confidence != ConfidenceMedium || len(h.Contradicting) != 1 {
		t.Errorf("conflict must keep the level and record both sides: %+v", h)
	}
}

func TestCrashLoopNewHypothesesAreCappedAndNeedSymptom(t *testing.T) {
	p := metricsPod("crash", 8, false)
	c := metricsCtx(p)
	s := signals.Subject{Namespace: "ns", Pod: "crash", Container: "app"}
	set := signals.NewSet("", []signals.Value{
		val(signals.CPUThrottlingRatio, s, 0.6, "ratio"),
		val(signals.CPUUsageVsLimit, s, 0.95, "ratio"),
		val(signals.ContainerRestarts, s, 8, "count"),
		val(signals.NodePressureHistory, nodeSub, 1, "flag"),
	}, nil)
	rep := analyzeWith(c, outcome(c, set))
	for _, code := range []string{hypCPULimited, hypRestartsGrowing, hypNodePressure} {
		h := hyp(rep, code)
		if h == nil {
			t.Fatalf("%s missing", code)
		}
		if h.Confidence == ConfidenceHigh {
			t.Errorf("%s reached high", code)
		}
	}
	// Без симптома метрики гипотез не создают.
	healthy := metricsCtx(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "ok"}})
	rep = analyzeWith(healthy, &signalOutcome{Wants: wantsOf(set), Set: set})
	if len(rep.Hypotheses) != 0 {
		t.Errorf("hypotheses without a symptom: %+v", rep.Hypotheses)
	}
}

func wantsOf(s *signals.Set) []signals.Want {
	var w []signals.Want
	for _, v := range s.Values() {
		w = append(w, signals.Want{Signal: v.Signal, Subject: v.Subject})
	}
	return w
}

func TestMissingReasons(t *testing.T) {
	c := metricsCtx(metricsPod("oom", 4, true))
	for _, tc := range []struct {
		name string
		o    *signalOutcome
		code string
		rsn  string
	}{
		{"not installed", outcome(c, signals.NewSet(signals.ReasonNotInstalled, nil, nil)), missingMetricsUnavailable, "not_installed"},
		{"forbidden", outcome(c, signals.NewSet(signals.ReasonForbidden, nil, nil)), missingMetricsUnavailable, "forbidden"},
		{"skipped", &signalOutcome{Wants: collectWants(c), Skipped: true}, missingMetricsUnavailable, "unavailable"},
		{"no series", outcome(c, signals.NewSet("", nil, []signals.Gap{{Signal: signals.OOMEvents, Subject: appSub, Reason: signals.ReasonNoSeries}})), missingMetricsSignal, "no_series"},
		{"history short", outcome(c, signals.NewSet("", nil, []signals.Gap{{Signal: signals.OOMEvents, Subject: appSub, Reason: signals.ReasonHistoryShort}})), missingMetricsSignal, "history_short"},
	} {
		rep := analyzeWith(c, tc.o)
		i := slices.IndexFunc(rep.Missing, func(m MissingContext) bool { return m.Code == tc.code })
		if i < 0 || rep.Missing[i].Params["reason"] != tc.rsn {
			t.Errorf("%s: missing = %+v", tc.name, rep.Missing)
		}
		for _, f := range rep.Facts {
			if len(f.Code) > 7 && f.Code[:7] == "metric." {
				t.Errorf("%s: fact from absent data: %s", tc.name, f.Code)
			}
		}
	}
	// Один пробел у двух целей — одна подсказка.
	gaps := []signals.Gap{
		{Signal: signals.OOMEvents, Subject: appSub, Reason: signals.ReasonNoSeries},
		{Signal: signals.OOMEvents, Subject: signals.Subject{Namespace: "ns", Pod: "oom", Container: "side"}, Reason: signals.ReasonNoSeries},
	}
	b := newBuilder(c)
	addMetricsMissing(b, &signalOutcome{Wants: []signals.Want{{Signal: signals.OOMEvents, Subject: appSub}, {Signal: signals.OOMEvents, Subject: gaps[1].Subject}}, Set: signals.NewSet("", nil, gaps)})
	if len(b.rep.Missing) != 1 {
		t.Errorf("duplicate hints: %+v", b.rep.Missing)
	}
}

func TestDeterministicAndStepsDropped(t *testing.T) {
	c := metricsCtx(metricsPod("oom", 4, true))
	set := signals.NewSet("", []signals.Value{val(signals.MemoryUsageVsLimit, appSub, 0.95, "ratio")}, nil)
	a, b := analyzeWith(c, outcome(c, set)), analyzeWith(c, outcome(c, set))
	if !reflect.DeepEqual(a, b) {
		t.Fatal("non-deterministic report")
	}
	if slices.ContainsFunc(a.Steps, func(s Step) bool { return s.Code == "step.check_memory_metrics" }) {
		t.Error("manual step stayed although the signal was obtained")
	}
	if !slices.ContainsFunc(analyzeWith(c, outcome(c, signals.NewSet("", nil, nil))).Steps, func(s Step) bool { return s.Code == "step.check_memory_metrics" }) {
		t.Error("manual step dropped without the signal")
	}
}

type fakeProvider struct {
	set   *signals.Set
	err   error
	panic bool
	got   signals.Request
}

func (f *fakeProvider) Fetch(_ context.Context, r signals.Request) (*signals.Set, error) {
	f.got = r
	if f.panic {
		panic("boom")
	}
	return f.set, f.err
}

func TestCollectSignals(t *testing.T) {
	c := metricsCtx(metricsPod("oom", 4, true))
	if collectSignals(t.Context(), c, nil) != nil {
		t.Error("no provider must mean no outcome")
	}
	if collectSignals(t.Context(), metricsCtx(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "ok"}}), &fakeProvider{}) != nil {
		t.Error("no symptom must mean no outcome")
	}
	for _, p := range []*fakeProvider{{err: errors.New("x")}, {panic: true}, {}} {
		o := collectSignals(t.Context(), c, p)
		if o == nil || !o.Skipped || len(o.Wants) == 0 {
			t.Errorf("provider failure must skip metrics: %+v", o)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	p := &fakeProvider{set: signals.NewSet(signals.ReasonUnavailable, nil, nil)}
	if o := collectSignals(ctx, c, p); !o.Skipped || len(p.got.Want) != 0 {
		t.Error("short budget must not query")
	}
	if o := collectSignals(t.Context(), c, p); o.Skipped || !slices.Contains(c.Partial, "metrics") {
		t.Errorf("unavailable stack must make the report partial: %+v %v", o, c.Partial)
	}
}

// Сигналы запрашиваются ровно тогда, когда правила нашли симптом: расхождение условий ловит этот тест.
func TestSignalWantsFollowSymptoms(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pod     *corev1.Pod
		symptom string
	}{
		{"crash", metricsPod("crash", 4, false), SymptomCrashLoop},
		{"oom", metricsPod("oom", 1, true), SymptomOOMKilled},
		{"healthy", &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "ok"}}, ""},
	} {
		c := metricsCtx(tc.pod)
		has := slices.Contains(Analyze(c).Symptoms, tc.symptom)
		if want := len(collectWants(c)) > 0; tc.symptom != "" && has != want || tc.symptom == "" && want {
			t.Errorf("%s: symptom %v but wants %v", tc.name, has, want)
		}
	}
}

func TestWantsBudgetAndOrder(t *testing.T) {
	var pods []*corev1.Pod
	for _, n := range []string{"d", "c", "b", "a"} {
		pods = append(pods, metricsPod(n, 5, false))
	}
	w1 := collectWants(metricsCtx(pods...))
	slices.Reverse(pods)
	w2 := collectWants(metricsCtx(pods...))
	if !reflect.DeepEqual(w1, w2) || len(w1) > signals.MaxSignalQueries {
		t.Errorf("wants depend on input order or exceed the budget: %d", len(w1))
	}
}
