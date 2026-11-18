// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/feedback"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

var errListFailed = errors.New("list failed")

// fbReport — отчёт версии 2: три гипотезы (high, medium, low) и три рекомендации.
func fbReport() diagnose.Report {
	return diagnose.Report{
		RuleSetVersion: 2, Symptoms: []string{"pod.crash_loop"},
		Hypotheses: []diagnose.Hypothesis{
			{ID: "h1", Code: "app_exits", Confidence: diagnose.ConfidenceHigh, RuleID: "pod.crash", RuleEpoch: 1},
			{ID: "h2", Code: "oom", Confidence: diagnose.ConfidenceMedium, RuleID: "pod.oom", RuleEpoch: 1},
			{ID: "h3", Code: "weak", Confidence: diagnose.ConfidenceLow, RuleID: "pod.weak", RuleEpoch: 1},
		},
		Recommendations: []diagnose.Recommendation{
			{ID: "r1", Code: "read_logs", RuleID: "pod.crash", RuleEpoch: 1},
			{ID: "r2", Code: "raise_limit", RuleID: "pod.oom", RuleEpoch: 1},
			{ID: "r3", Code: "check_probe", RuleID: "pod.crash", RuleEpoch: 2},
		},
	}
}

type fbFx struct {
	t     *testing.T
	b     Backend
	svc   *Service
	clock *testClock
	n     int
}

func newFbFx(t *testing.T) *fbFx {
	t.Helper()
	f := &fbFx{t: t, b: NewFileBackend(t.TempDir()), clock: &testClock{t: tBase}}
	f.svc = NewService(f.b, identity(), WithClock(f.clock.now), WithIDs(func() string { f.n++; return fmt.Sprintf("n%03d", f.n) }))
	return f
}

// open кладёт открытое расследование с отчётом rep в хранилище на текущий момент часов.
func (f *fbFx) open(id string, rep diagnose.Report) *Incident {
	f.t.Helper()
	now := f.clock.now()
	in := &Incident{ID: id, Scope: tScope, Status: StatusOpen, OpenedAt: now, UpdatedAt: now, OpenedBy: "alice",
		Target: diagnose.ResourceRef{Kind: "Pod", Namespace: "shop", Name: "api-" + id}, Report: rep,
		Events: []diagnose.Event{}, Evidence: []Evidence{}}
	in.Signature = signatureOf(in, &in.Report)
	if err := f.b.PutIncident(context.Background(), in); err != nil {
		f.t.Fatal(err)
	}
	return in
}

func (f *fbFx) mark(id string, o Outcome) (*Incident, error) {
	return f.svc.MarkOutcome(context.Background(), Caller{Scope: tScope, Actor: "alice"}, id, o)
}

// closeAfter открывает расследование, через d помечает его результатом o и возвращает закрытую запись.
func (f *fbFx) closeAfter(id string, rep diagnose.Report, d time.Duration, o Outcome) *Incident {
	f.t.Helper()
	f.open(id, rep)
	f.clock.add(d)
	in, err := f.mark(id, o)
	if err != nil {
		f.t.Fatalf("mark %s: %v", id, err)
	}
	return in
}

func TestPrepareOutcomeRejectsInvalid(t *testing.T) {
	t.Parallel()
	opened := tBase
	marked := tBase.Add(time.Hour)
	early, late := tBase.Add(-time.Minute), marked.Add(time.Minute)
	many := make([]string, MaxOutcomeRefs+1)
	for i := range many {
		many[i] = "h1"
	}
	cases := map[string]Outcome{
		"unknown rejected":            {Result: ResultResolved, RejectedHypothesisIDs: []string{"h9"}},
		"empty rejected id":           {Result: ResultResolved, RejectedHypothesisIDs: []string{""}},
		"too many rejected":           {Result: ResultResolved, RejectedHypothesisIDs: many},
		"rejected equals confirmed":   {Result: ResultResolved, ConfirmedHypothesisID: "h1", RejectedHypothesisIDs: []string{"h2", "h1"}},
		"unknown helpful":             {Result: ResultResolved, HelpfulRecommendationIDs: []string{"r9"}},
		"unknown useless":             {Result: ResultResolved, UselessRecommendationIDs: []string{"h1"}},
		"helpful and useless overlap": {Result: ResultResolved, HelpfulRecommendationIDs: []string{"r1"}, UselessRecommendationIDs: []string{"r2", "r1"}},
		"resolvedAt for unresolved":   {Result: ResultUnresolved, ResolvedAt: &marked},
		"resolvedAt before opened":    {Result: ResultResolved, ResolvedAt: &early},
		"resolvedAt after marked":     {Result: ResultResolved, ResolvedAt: &late},
		"correlation without snapshot": {Result: ResultResolved,
			Correlation: &CorrelationFeedback{CandidateID: "c1", Verdict: VerdictConfirmed}},
	}
	for name, o := range cases {
		in := &Incident{Status: StatusOpen, OpenedAt: opened, Report: fbReport()}
		o.MarkedAt = marked
		if err := (&Service{}).prepareOutcome(in, &o); !errors.Is(err, ErrInvalidOutcome) {
			t.Errorf("%s: err = %v", name, err)
		}
	}

	snap := &correlation.Result{Candidates: []correlation.Candidate{{ID: "c1", Kind: correlation.KindSharedNode}}}
	for name, c := range map[string]*CorrelationFeedback{
		"unknown candidate": {CandidateID: "c9", Verdict: VerdictConfirmed},
		"empty candidate":   {Verdict: VerdictConfirmed},
		"unknown verdict":   {CandidateID: "c1", Verdict: "maybe"},
	} {
		in := &Incident{Status: StatusOpen, OpenedAt: opened, Report: fbReport(), Correlation: snap}
		o := Outcome{Result: ResultResolved, MarkedAt: marked, Correlation: c}
		if err := (&Service{}).prepareOutcome(in, &o); !errors.Is(err, ErrInvalidOutcome) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestPrepareOutcomeClosedIsRejected(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]*Incident{
		"closed status": {Status: StatusClosed},
		"has outcome":   {Status: StatusOpen, Outcome: &Outcome{Result: ResultResolved}},
	} {
		if err := (&Service{}).prepareOutcome(in, &Outcome{Result: ResultResolved}); !errors.Is(err, ErrClosed) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestPrepareOutcomeDefaultsDedupAndIdempotence(t *testing.T) {
	t.Parallel()
	marked := tBase.Add(time.Hour)
	snap := &correlation.Result{Candidates: []correlation.Candidate{{ID: "c1", Kind: correlation.KindSharedNode}}}
	in := &Incident{Status: StatusOpen, OpenedAt: tBase, Report: fbReport(), Correlation: snap}
	orig := Outcome{Result: ResultResolved, MarkedAt: marked,
		RejectedHypothesisIDs: []string{"h2", "h2", "h3"}, HelpfulRecommendationIDs: []string{"r1", "r1"},
		Correlation: &CorrelationFeedback{CandidateID: "c1", Verdict: VerdictUnknown}}

	first, second := orig, orig
	if err := (&Service{}).prepareOutcome(in, &first); err != nil {
		t.Fatal(err)
	}
	if err := (&Service{}).prepareOutcome(in, &second); err != nil {
		t.Fatal(err)
	}
	if got, _ := json.Marshal(first); string(got) != mustJSON(second) {
		t.Errorf("not idempotent:\n%s\n%s", got, mustJSON(second))
	}
	if first.ResolvedAt == nil || !first.ResolvedAt.Equal(marked) {
		t.Errorf("default resolvedAt = %v, want %v", first.ResolvedAt, marked)
	}
	if strings.Join(first.RejectedHypothesisIDs, ",") != "h2,h3" || strings.Join(first.HelpfulRecommendationIDs, ",") != "r1" {
		t.Errorf("not deduplicated: %+v", first)
	}
	// Данные вызывающего не изменены: срезы остались с повторами, указатели не подменены.
	if len(orig.RejectedHypothesisIDs) != 3 || orig.ResolvedAt != nil || first.Correlation == orig.Correlation {
		t.Errorf("caller data mutated: %+v", orig)
	}

	// Явный момент устранения сохраняется (с округлением до микросекунд); не-resolved остаётся без него.
	at := tBase.Add(30*time.Minute + 123*time.Nanosecond)
	o := Outcome{Result: ResultResolved, MarkedAt: marked, ResolvedAt: &at}
	if err := (&Service{}).prepareOutcome(in, &o); err != nil || !o.ResolvedAt.Equal(Micro(at)) {
		t.Errorf("explicit resolvedAt: %v %v", o.ResolvedAt, err)
	}
	u := Outcome{Result: ResultInconclusive, MarkedAt: marked}
	if err := (&Service{}).prepareOutcome(in, &u); err != nil || u.ResolvedAt != nil {
		t.Errorf("inconclusive: %v %v", u.ResolvedAt, err)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestMarkOutcomeIsSingleAndStoresExtension(t *testing.T) {
	t.Parallel()
	f := newFbFx(t)
	in := f.closeAfter("a", fbReport(), time.Hour, Outcome{Result: ResultResolved, ConfirmedHypothesisID: "h1",
		RejectedHypothesisIDs: []string{"h2"}, HelpfulRecommendationIDs: []string{"r1"}, UselessRecommendationIDs: []string{"r2"}})
	if in.Status != StatusClosed || in.Outcome.ResolvedAt == nil || !in.Outcome.ResolvedAt.Equal(in.Outcome.MarkedAt) {
		t.Errorf("outcome = %+v", in.Outcome)
	}
	if _, err := f.mark("a", Outcome{Result: ResultUnresolved}); !errors.Is(err, ErrClosed) {
		t.Errorf("second mark: %v", err)
	}
	got, _ := f.b.GetIncident(context.Background(), tScope, "a")
	if got.Outcome.Result != ResultResolved {
		t.Errorf("second mark overwrote the outcome: %+v", got.Outcome)
	}
}

func TestSummarizeFeedback(t *testing.T) {
	t.Parallel()
	rep := fbReport()
	rep.Graph = &depgraph.Snapshot{}
	rep.Facts = []diagnose.Fact{{ID: "f1", Source: diagnose.Source{Kind: diagnose.SourceMetric, Signal: "cpu"}}}
	in := &Incident{ID: "i1", Status: StatusClosed, Report: rep,
		Correlation: &correlation.Result{Candidates: []correlation.Candidate{
			{ID: "c1", Kind: correlation.KindSharedNode, Confidence: diagnose.ConfidenceHigh}}},
		Outcome: &Outcome{Result: ResultResolved, ConfirmedHypothesisID: "h1",
			RejectedHypothesisIDs: []string{"h3", "h2", "h2"}, HelpfulRecommendationIDs: []string{"r3", "r1"},
			UselessRecommendationIDs: []string{"r2"}, Note: "NOTE-TEXT",
			Correlation: &CorrelationFeedback{CandidateID: "c1", Verdict: VerdictRejected}}}
	s := in.Summarize()

	if s.Confirmed == nil || *s.Confirmed != (RuleRef{"pod.crash", 1, "app_exits"}) {
		t.Errorf("confirmed = %+v", s.Confirmed)
	}
	wantRej := []RuleRef{{"pod.oom", 1, "oom"}, {"pod.weak", 1, "weak"}}
	wantHelp := []RuleRef{{"pod.crash", 1, "read_logs"}, {"pod.crash", 2, "check_probe"}}
	if fmt.Sprint(s.Rejected) != fmt.Sprint(wantRej) || fmt.Sprint(s.Helpful) != fmt.Sprint(wantHelp) ||
		len(s.Useless) != 1 || s.Useless[0].Code != "raise_limit" {
		t.Errorf("refs: rejected=%v helpful=%v useless=%v", s.Rejected, s.Helpful, s.Useless)
	}
	if strings.Join(s.Features, ",") != "graph,metrics,correlation" {
		t.Errorf("features = %v", s.Features)
	}
	if v := s.CorrelationVerdict; v == nil || *v != (CorrelationVerdict{Kind: "shared_node", Confidence: "high", Verdict: VerdictRejected}) {
		t.Errorf("correlation verdict = %+v", v)
	}
	if raw := mustJSON(s); strings.Contains(raw, "NOTE-TEXT") {
		t.Errorf("note leaked into the summary: %s", raw)
	}
}

func TestSummarizeFeedbackIsDefensive(t *testing.T) {
	t.Parallel()
	// Пустой отчёт и отметка без расширения.
	if s := (&Incident{Status: StatusClosed, Outcome: &Outcome{Result: ResultResolved, ConfirmedHypothesisID: "h1"}}).Summarize(); s.Confirmed != nil || s.Features != nil {
		t.Errorf("empty report: %+v", s)
	}
	// Открытое расследование: только признаки.
	open := &Incident{Report: fbReport(), Correlation: &correlation.Result{}}
	if s := open.Summarize(); s.Confirmed != nil || strings.Join(s.Features, ",") != "correlation" {
		t.Errorf("open: %+v", s)
	}
	// Отчёт версии 1: RuleID нет — ссылок нет, без догадок по Code.
	legacy := fbReport()
	for i := range legacy.Hypotheses {
		legacy.Hypotheses[i].RuleID, legacy.Hypotheses[i].RuleEpoch = "", 0
	}
	in := &Incident{Report: legacy, Outcome: &Outcome{Result: ResultResolved, ConfirmedHypothesisID: "h1", RejectedHypothesisIDs: []string{"h2"}}}
	if s := in.Summarize(); s.Confirmed != nil || s.Rejected != nil {
		t.Errorf("legacy: %+v", s)
	}
	// Оценка корреляции на кандидата, которого нет, пропускается.
	in = &Incident{Report: fbReport(), Correlation: &correlation.Result{},
		Outcome: &Outcome{Result: ResultResolved, Correlation: &CorrelationFeedback{CandidateID: "x", Verdict: VerdictConfirmed}}}
	if s := in.Summarize(); s.CorrelationVerdict != nil {
		t.Errorf("phantom candidate: %+v", s.CorrelationVerdict)
	}
	// Предел списка.
	byID := map[string]RuleRef{}
	var ids []string
	for i := range MaxOutcomeRefs + 5 {
		id := fmt.Sprint("x", i)
		byID[id] = RuleRef{RuleID: "r", Epoch: 1, Code: id}
		ids = append(ids, id)
	}
	if got := refsOf(byID, ids); len(got) != MaxOutcomeRefs {
		t.Errorf("refs not capped: %d", len(got))
	}
}

func TestSignatureOf(t *testing.T) {
	t.Parallel()
	graph := func(pod, rs, dep string) *depgraph.Snapshot {
		return &depgraph.Snapshot{
			Nodes: []depgraph.SnapshotNode{
				{Kind: "Pod", Namespace: "shop", Name: pod},
				{Kind: "ReplicaSet", Group: "apps", Namespace: "shop", Name: rs},
				{Kind: "Deployment", Group: "apps", Namespace: "shop", Name: dep},
			},
			Edges: []depgraph.SnapshotEdge{
				{From: "apps/ReplicaSet/shop/" + rs, To: "core/Pod/shop/" + pod, Type: "owns"},
				{From: "apps/Deployment/shop/" + dep, To: "apps/ReplicaSet/shop/" + rs, Type: "owns"},
			}}
	}
	sig := func(pod string, rep diagnose.Report) string {
		in := &Incident{Target: diagnose.ResourceRef{Kind: "Pod", Namespace: "shop", Name: pod}}
		return signatureOf(in, &rep)
	}

	a, b := fbReport(), fbReport()
	a.Graph, b.Graph = graph("api-aaa-1", "api-aaa", "api"), graph("api-bbb-2", "api-bbb", "api")
	if sa, sb := sig("api-aaa-1", a), sig("api-bbb-2", b); sa == "" || sa != sb {
		t.Errorf("owner via graph is not stable across pod names: %q %q", sa, sb)
	}
	b.Graph = graph("api-bbb-2", "api-bbb", "web")
	if sig("api-aaa-1", a) == sig("api-bbb-2", b) {
		t.Error("different owners share a signature")
	}

	// Без графа: единственная нагрузка среди ресурсов отчёта.
	nog := fbReport()
	nog.Resources = []diagnose.ResourceRef{{Kind: "Pod", Namespace: "shop", Name: "api-aaa-1"}, {Kind: "Deployment", Namespace: "shop", Name: "api"}}
	other := nog
	other.Resources = []diagnose.ResourceRef{{Kind: "Pod", Namespace: "shop", Name: "api-bbb-2"}, {Kind: "Deployment", Namespace: "shop", Name: "api"}}
	if sig("api-aaa-1", nog) != sig("api-bbb-2", other) {
		t.Error("fallback by report resources is not stable")
	}
	// Неоднозначно (две нагрузки) — сама цель, имя пода различает расследования.
	nog.Resources = append(nog.Resources, diagnose.ResourceRef{Kind: "StatefulSet", Namespace: "shop", Name: "db"})
	if sig("api-aaa-1", nog) == sig("api-bbb-2", nog) {
		t.Error("ambiguous owner must fall back to the target")
	}

	// Только гипотезы medium и выше входят; эпоха не влияет; «none» без гипотез даёт пустую сигнатуру.
	base := sig("p", fbReport())
	noLow := fbReport()
	noLow.Hypotheses = noLow.Hypotheses[:2]
	if sig("p", noLow) != base {
		t.Error("low-confidence hypothesis changed the signature")
	}
	epoch := fbReport()
	epoch.Hypotheses[0].RuleEpoch = 9
	if sig("p", epoch) != base {
		t.Error("rule epoch changed the signature")
	}
	fewer := fbReport()
	fewer.Hypotheses = fewer.Hypotheses[:1]
	if sig("p", fewer) == base {
		t.Error("dropping a medium hypothesis kept the signature")
	}
	none := diagnose.Report{Symptoms: []string{diagnose.SymptomNone}}
	if got := sig("p", none); got != "" {
		t.Errorf("none-only signature = %q", got)
	}
	if got := signatureOf(&Incident{}, nil); got != "" {
		t.Errorf("nil report: %q", got)
	}
	if got := signatureOf(&Incident{}, &diagnose.Report{Symptoms: []string{"pod.pending"}}); got != "" {
		t.Errorf("no target name: %q", got)
	}
}

func TestTopOwnerSurvivesCyclesAndGaps(t *testing.T) {
	t.Parallel()
	cyc := &depgraph.Snapshot{
		Nodes: []depgraph.SnapshotNode{{Kind: "Pod", Namespace: "n", Name: "p"}, {Kind: "Foo", Group: "x", Namespace: "n", Name: "a"}},
		Edges: []depgraph.SnapshotEdge{
			{From: "x/Foo/n/a", To: "core/Pod/n/p", Type: "owns"},
			{From: "core/Pod/n/p", To: "x/Foo/n/a", Type: "owns"},
		}}
	got, ok := topOwner(cyc, diagnose.ResourceRef{Kind: "Pod", Namespace: "n", Name: "p"})
	if !ok || got.Kind != "Foo" {
		t.Errorf("cycle: %+v %v", got, ok)
	}
	if _, ok := topOwner(cyc, diagnose.ResourceRef{Kind: "Pod", Namespace: "n", Name: "absent"}); ok {
		t.Error("target outside the graph must not resolve")
	}
	gap := &depgraph.Snapshot{
		Nodes: []depgraph.SnapshotNode{{Kind: "Pod", Namespace: "n", Name: "p"}},
		Edges: []depgraph.SnapshotEdge{{From: "apps/Deployment/n/missing", To: "core/Pod/n/p", Type: "owns"}}}
	if _, ok := topOwner(gap, diagnose.ResourceRef{Kind: "Pod", Namespace: "n", Name: "p"}); ok {
		t.Error("owner missing from nodes must not resolve")
	}
}

// previous кладёт закрытое расследование с заданной сигнатурой и результатом.
func (f *fbFx) previous(id, sig string, result Result, resolvedAt *time.Time) {
	f.t.Helper()
	in := &Incident{ID: id, Scope: tScope, Status: StatusClosed, OpenedAt: tBase.Add(-60 * 24 * time.Hour), UpdatedAt: tBase,
		Target: diagnose.ResourceRef{Kind: "Deployment", Namespace: "shop", Name: "api"}, Report: fbReport(),
		Events: []diagnose.Event{}, Evidence: []Evidence{}, Signature: sig,
		Outcome: &Outcome{Result: result, ResolvedAt: resolvedAt, MarkedBy: "alice", MarkedAt: tBase}}
	if err := f.b.PutIncident(context.Background(), in); err != nil {
		f.t.Fatal(err)
	}
}

func TestDetectRecurrence(t *testing.T) {
	t.Parallel()
	day := 24 * time.Hour
	at := func(d time.Duration) *time.Time { v := tBase.Add(d); return &v }
	newIn := func(sig string) *Incident {
		return &Incident{ID: "new", Scope: tScope, Signature: sig, OpenedAt: tBase.Add(10 * day)}
	}

	t.Run("recent resolved match", func(t *testing.T) {
		f := newFbFx(t)
		f.previous("old", "sig", ResultResolved, at(-day))
		in := newIn("sig")
		if p := f.svc.detectRecurrence(t.Context(), in); p != nil || in.RecurrenceOf != "old" {
			t.Errorf("recurrenceOf = %q partial = %v", in.RecurrenceOf, p)
		}
	})
	t.Run("boundary of the window", func(t *testing.T) {
		f := newFbFx(t)
		f.previous("edge", "sig", ResultResolved, at(10*day-feedback.RecurrenceWindow))
		in := newIn("sig")
		f.svc.detectRecurrence(t.Context(), in)
		if in.RecurrenceOf != "edge" {
			t.Errorf("exactly 30 days must match, got %q", in.RecurrenceOf)
		}
	})
	t.Run("not a recurrence", func(t *testing.T) {
		cases := map[string]func(f *fbFx){
			"too old": func(f *fbFx) {
				f.previous("p", "sig", ResultResolved, at(10*day-feedback.RecurrenceWindow-time.Second))
			},
			"resolved later":   func(f *fbFx) { f.previous("p", "sig", ResultResolved, at(11*day)) },
			"unresolved":       func(f *fbFx) { f.previous("p", "sig", ResultUnresolved, nil) },
			"inconclusive":     func(f *fbFx) { f.previous("p", "sig", ResultInconclusive, nil) },
			"different sig":    func(f *fbFx) { f.previous("p", "other", ResultResolved, at(day)) },
			"no resolved time": func(f *fbFx) { f.previous("p", "sig", ResultResolved, nil) },
			"other scope": func(f *fbFx) {
				in := &Incident{ID: "p", Scope: tenant.Scope{OrgID: "org1", TeamID: "team1", ClusterID: "other"}, Status: StatusClosed,
					OpenedAt: tBase, UpdatedAt: tBase, Report: fbReport(), Events: []diagnose.Event{}, Evidence: []Evidence{}, Signature: "sig",
					Outcome: &Outcome{Result: ResultResolved, ResolvedAt: at(day)}}
				if err := f.b.PutIncident(t.Context(), in); err != nil {
					t.Fatal(err)
				}
			},
		}
		for name, setup := range cases {
			f := newFbFx(t)
			setup(f)
			in := newIn("sig")
			if p := f.svc.detectRecurrence(t.Context(), in); p != nil || in.RecurrenceOf != "" {
				t.Errorf("%s: recurrenceOf = %q partial = %v", name, in.RecurrenceOf, p)
			}
		}
	})
	t.Run("latest of several", func(t *testing.T) {
		f := newFbFx(t)
		f.previous("a", "sig", ResultResolved, at(2*day))
		f.previous("c", "sig", ResultResolved, at(5*day))
		f.previous("b", "sig", ResultResolved, at(5*day))
		in := newIn("sig")
		f.svc.detectRecurrence(t.Context(), in)
		if in.RecurrenceOf != "b" { // позже всех устранены b и c одновременно: при равенстве меньший ID
			t.Errorf("recurrenceOf = %q", in.RecurrenceOf)
		}
	})
	t.Run("empty signature skips the search", func(t *testing.T) {
		f := newFbFx(t)
		f.previous("p", "", ResultResolved, at(day))
		in := newIn("")
		if p := f.svc.detectRecurrence(t.Context(), in); p != nil || in.RecurrenceOf != "" {
			t.Errorf("recurrenceOf = %q partial = %v", in.RecurrenceOf, p)
		}
	})
	t.Run("backend failure is partial, not fatal", func(t *testing.T) {
		f := newFbFx(t)
		f.svc.b = listFailBackend{f.b}
		in := newIn("sig")
		if p := f.svc.detectRecurrence(t.Context(), in); len(p) != 1 || p[0] != partialRecurrence || in.RecurrenceOf != "" {
			t.Errorf("recurrenceOf = %q partial = %v", in.RecurrenceOf, p)
		}
	})
}

type listFailBackend struct{ Backend }

func (listFailBackend) ListIncidents(context.Context, tenant.Scope, ListQuery) (Page, error) {
	return Page{}, errListFailed
}

func TestRuleStatsEndToEnd(t *testing.T) {
	t.Parallel()
	f := newFbFx(t)
	hour := time.Hour
	// Шесть устранённых расследований: h1 подтверждена 5 раз (одно из них — повторение), h2 отвергнута 1 раз.
	var first string
	for i := range 5 {
		id := fmt.Sprint("r", i)
		in := f.closeAfter(id, fbReport(), hour, Outcome{Result: ResultResolved, ConfirmedHypothesisID: "h1",
			RejectedHypothesisIDs: []string{"h2"}, HelpfulRecommendationIDs: []string{"r1"}})
		if i == 0 {
			first = in.ID
		}
		f.clock.add(time.Minute)
	}
	// Повторение первого расследования: закрывается с результатом, RecurrenceOf проставлен при открытии.
	rec := f.open("again", fbReport())
	rec.RecurrenceOf = first
	if err := f.b.PutIncident(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	f.clock.add(hour)
	if _, err := f.mark("again", Outcome{Result: ResultUnresolved}); err != nil {
		t.Fatal(err)
	}
	// Не входят: открытое, старая запись версии 1, закрытое без оценок (входит как Unevaluated).
	f.open("still-open", fbReport())
	v1 := fbReport()
	v1.RuleSetVersion = 1
	f.closeAfter("legacy", v1, hour, Outcome{Result: ResultResolved})
	f.closeAfter("plain", fbReport(), hour, Outcome{Result: ResultInconclusive})

	st, err := f.svc.RuleStats(t.Context(), tScope)
	if err != nil {
		t.Fatal(err)
	}
	if st.Records != 7 || st.Unevaluated != 2 || st.Truncated {
		t.Fatalf("records=%d unevaluated=%d truncated=%v", st.Records, st.Unevaluated, st.Truncated)
	}
	var h1 *feedback.HypothesisStat
	for i := range st.Hypotheses {
		if st.Hypotheses[i].RuleID == "pod.crash" {
			h1 = &st.Hypotheses[i]
		}
	}
	if h1 == nil || h1.Confirmed != 5 || h1.Resolved != 5 || h1.Recurred != 1 || h1.Rejected != 0 {
		t.Fatalf("pod.crash = %+v", h1)
	}
	if h1.MedianResolveSeconds == nil || *h1.MedianResolveSeconds != 3600 {
		t.Errorf("median = %v", h1.MedianResolveSeconds)
	}
	for _, h := range st.Hypotheses {
		if h.RuleID == "pod.oom" && (h.Rejected != 5 || h.Precision == nil || *h.Precision != 0 || !h.Trusted) {
			t.Errorf("pod.oom = %+v", h)
		}
	}
	if len(st.Recommendations) != 1 || st.Recommendations[0].Helpful != 5 || st.Recommendations[0].Ratio == nil {
		t.Errorf("recommendations = %+v", st.Recommendations)
	}
	if len(st.RuleSetVersions) != 1 || st.RuleSetVersions[0] != 2 {
		t.Errorf("versions = %v", st.RuleSetVersions)
	}
}

func TestRuleStatsTruncatesAtWindow(t *testing.T) {
	t.Parallel()
	f := newFbFx(t)
	for i := range StatsWindow + 1 {
		in := &Incident{ID: fmt.Sprintf("c%04d", i), Scope: tScope, Status: StatusClosed, OpenedAt: tBase.Add(time.Duration(i) * time.Second),
			UpdatedAt: tBase, Report: fbReport(), Events: []diagnose.Event{}, Evidence: []Evidence{},
			Outcome: &Outcome{Result: ResultInconclusive}}
		if err := f.b.PutIncident(t.Context(), in); err != nil {
			t.Fatal(err)
		}
	}
	st, err := f.svc.RuleStats(t.Context(), tScope)
	if err != nil {
		t.Fatal(err)
	}
	if st.Records != StatsWindow || !st.Truncated {
		t.Errorf("records=%d truncated=%v", st.Records, st.Truncated)
	}
}

func TestRuleStatsErrors(t *testing.T) {
	t.Parallel()
	f := newFbFx(t)
	if _, err := f.svc.RuleStats(t.Context(), tenant.Scope{}); !errors.Is(err, ErrInvalidScope) {
		t.Errorf("empty scope: %v", err)
	}
	if _, err := f.svc.ExportRuleStats(t.Context(), tenant.Scope{}); !errors.Is(err, ErrInvalidScope) {
		t.Errorf("export empty scope: %v", err)
	}
	f.svc.b = listFailBackend{f.b}
	if _, err := f.svc.RuleStats(t.Context(), tScope); !errors.Is(err, errListFailed) {
		t.Errorf("backend failure: %v", err)
	}
}

// E2E-5: в выгрузке нет ни идентификаторов, ни имён, ни комментариев, ни времени.
func TestExportRuleStatsIsAnonymous(t *testing.T) {
	t.Parallel()
	scope := tenant.Scope{OrgID: "MARK-ORG", TeamID: "MARK-TEAM", ClusterID: "MARK-CLUSTER"}
	b := NewFileBackend(t.TempDir())
	clock := &testClock{t: tBase}
	svc := NewService(b, identity(), WithClock(clock.now))
	for i := range 6 {
		id := fmt.Sprint("MARK-ID-", i)
		in := &Incident{ID: id, Scope: scope, Status: StatusOpen, OpenedAt: clock.now(), UpdatedAt: clock.now(), OpenedBy: "MARK-USER",
			Target: diagnose.ResourceRef{Kind: "Pod", Namespace: "MARK-NS", Name: "MARK-POD"}, Report: fbReport(),
			Events: []diagnose.Event{}, Evidence: []Evidence{}}
		in.Signature = signatureOf(in, &in.Report)
		if err := b.PutIncident(t.Context(), in); err != nil {
			t.Fatal(err)
		}
		clock.add(time.Hour)
		if _, err := svc.MarkOutcome(t.Context(), Caller{Scope: scope, Actor: "MARK-ACTOR"}, id, Outcome{Result: ResultResolved,
			ConfirmedHypothesisID: "h1", Note: "MARK-NOTE", HelpfulRecommendationIDs: []string{"r1"}}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := svc.ExportRuleStats(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, bad := range []string{"MARK-", "2026-11-01", "signature", "computedAt", "shop"} {
		if strings.Contains(text, bad) {
			t.Errorf("export contains %q:\n%s", bad, text)
		}
	}
	for _, want := range []string{`"format": "kubron.rule-stats"`, `"ruleId": "pod.crash"`, `"code": "app_exits"`, `"confirmed": 6`} {
		if !strings.Contains(text, want) {
			t.Errorf("export lacks %s:\n%s", want, text)
		}
	}
	if !json.Valid(out) {
		t.Errorf("invalid JSON:\n%s", out)
	}
}
