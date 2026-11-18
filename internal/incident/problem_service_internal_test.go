// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

var (
	tScope = tenant.Scope{OrgID: "org1", TeamID: "team1", ClusterID: "cl1"}
	tBase  = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
)

// testClock — управляемое время сервиса.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// failBackend подставляет сбои в Backend: PutIncident с номером failPutAt (с 1) и все чтения расследований.
type failBackend struct {
	Backend
	puts      atomic.Int32
	failPutAt int32
}

func (f *failBackend) PutIncident(ctx context.Context, in *Incident) error {
	if f.failPutAt > 0 && f.puts.Add(1) == f.failPutAt {
		return errors.New("disk is full")
	}
	return f.Backend.PutIncident(ctx, in)
}

// failProblems подставляет сбои и паузы в ProblemBackend.
type failProblems struct {
	ProblemBackend
	failPut error
	// listBarrier: если задан, каждый ListProblems ждёт, пока его вызовут listWaiters раз (гонка двух открытий).
	listWaiters int
	listMu      sync.Mutex
	listCalls   int
	listCond    *sync.Cond
	puts        atomic.Int32
}

func (f *failProblems) PutProblem(ctx context.Context, p *Problem) error {
	f.puts.Add(1)
	if f.failPut != nil {
		return f.failPut
	}
	return f.ProblemBackend.PutProblem(ctx, p)
}

func (f *failProblems) ListProblems(ctx context.Context, sc tenant.Scope, q ProblemQuery) (ProblemPage, error) {
	page, err := f.ProblemBackend.ListProblems(ctx, sc, q)
	if f.listWaiters > 0 {
		f.listMu.Lock()
		if f.listCond == nil {
			f.listCond = sync.NewCond(&f.listMu)
		}
		f.listCalls++
		if f.listCalls >= f.listWaiters {
			f.listCond.Broadcast()
		}
		for f.listCalls < f.listWaiters {
			f.listCond.Wait()
		}
		f.listMu.Unlock()
	}
	return page, err
}

type fixture struct {
	t     *testing.T
	svc   *Service
	fb    *FileBackend
	clock *testClock
	ids   atomic.Int32
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	fx := &fixture{t: t, fb: NewFileBackend(t.TempDir()), clock: &testClock{t: tBase}}
	fx.svc = NewService(fx.fb, identity(), WithClock(fx.clock.now),
		WithIDs(func() string { return fmt.Sprintf("p%d", fx.ids.Add(1)) })).WithProblems(fx.fb)
	return fx
}

func corr(kind correlation.Kind, node string, conf diagnose.Confidence) *correlation.Result {
	return &correlation.Result{Version: correlation.Version, Candidates: []correlation.Candidate{{
		ID: "c1", Kind: kind, Source: diagnose.ResourceRef{Kind: "Node", Name: node}, Confidence: conf}}}
}

// add сохраняет расследование (как это делает Open до автоприсоединения) и возвращает его.
func (fx *fixture) add(id string, c *correlation.Result) *Incident {
	fx.t.Helper()
	return fx.addIn(tScope, id, c)
}

func (fx *fixture) addIn(sc tenant.Scope, id string, c *correlation.Result) *Incident {
	fx.t.Helper()
	now := fx.clock.now()
	in := &Incident{ID: id, Scope: sc, Status: StatusOpen, OpenedAt: now, UpdatedAt: now, OpenedBy: "alice",
		Target: diagnose.ResourceRef{Kind: "Deployment", Namespace: "shop", Name: id},
		Report: diagnose.Report{RuleSetVersion: 1, Symptoms: []string{"pod.crash_loop"}},
		Events: []diagnose.Event{}, Evidence: []Evidence{}, Correlation: c}
	if err := fx.fb.PutIncident(context.Background(), in); err != nil {
		fx.t.Fatal(err)
	}
	return in
}

func (fx *fixture) incident(id string) *Incident {
	fx.t.Helper()
	in, err := fx.fb.GetIncident(context.Background(), tScope, id)
	if err != nil {
		fx.t.Fatal(err)
	}
	return in
}

func (fx *fixture) problem(id string) *Problem {
	fx.t.Helper()
	p, err := fx.fb.GetProblem(context.Background(), tScope, id)
	if err != nil {
		fx.t.Fatal(err)
	}
	return p
}

func (fx *fixture) problems() []Problem {
	fx.t.Helper()
	page, err := fx.fb.ListProblems(context.Background(), tScope, ProblemQuery{Limit: MaxPageSize})
	if err != nil {
		fx.t.Fatal(err)
	}
	return page.Items
}

func (fx *fixture) attach(in *Incident) []string { return fx.svc.autoAttach(context.Background(), in) }

func (fx *fixture) caller() Caller { return Caller{Scope: tScope, Actor: "bob"} }

func memberIDs(p *Problem) []string {
	out := make([]string, len(p.Members))
	for i, m := range p.Members {
		out[i] = m.IncidentID
	}
	return out
}

// frozen — документ расследования без полей, которые операции с проблемами вправе менять (ProblemID и версия
// записи). Всё остальное, включая UpdatedAt, обязано остаться прежним.
func frozen(t *testing.T, in *Incident) string {
	t.Helper()
	c := *in
	c.ProblemID, c.Version = "", 0
	raw, err := json.Marshal(&c)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestAutoAttachCreatesThenJoins(t *testing.T) {
	fx := newFixture(t)
	c := corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceMedium)
	i1, i2 := fx.add("i1", c), fx.add("i2", c)
	if part := fx.attach(i1); part != nil {
		t.Fatalf("partial = %v", part)
	}
	if i1.ProblemID == "" || fx.incident("i1").ProblemID != i1.ProblemID {
		t.Fatalf("problem id was not stored: %q", i1.ProblemID)
	}
	if i1.Version != fx.incident("i1").Version {
		t.Errorf("returned incident has a stale version %d", i1.Version)
	}
	fx.clock.add(time.Minute)
	fx.attach(i2)
	if i2.ProblemID != i1.ProblemID {
		t.Fatalf("second investigation went to %q, want %q", i2.ProblemID, i1.ProblemID)
	}
	p := fx.problem(i1.ProblemID)
	if fmt.Sprint(memberIDs(p)) != "[i1 i2]" || p.Origin != ProblemAuto || p.Status != ProblemOpen ||
		p.Key != c.Best().Key() || p.Kind != "shared_node" || p.Source.Name != "n1" || p.Scope != tScope {
		t.Errorf("problem = %+v", p)
	}
	for _, m := range p.Members {
		if m.AddedBy != AutoMember {
			t.Errorf("member %+v", m)
		}
	}
	if len(fx.problems()) != 1 {
		t.Errorf("problems = %d", len(fx.problems()))
	}
}

func TestAutoAttachConfidenceThreshold(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    *correlation.Result
		want bool
	}{
		{"low", corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceLow), false},
		{"medium", corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceMedium), true},
		{"high", corr(correlation.KindSharedStorage, "sc1", diagnose.ConfidenceHigh), true},
		{"unknown value", corr(correlation.KindSharedNode, "n1", "certain"), false},
		{"no candidates", &correlation.Result{Version: 1}, false},
		{"no snapshot", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t)
			in := fx.add("i1", tc.c)
			if part := fx.attach(in); part != nil {
				t.Fatalf("partial = %v", part)
			}
			if got := in.ProblemID != ""; got != tc.want || len(fx.problems()) != map[bool]int{true: 1, false: 0}[tc.want] {
				t.Errorf("attached = %v (problems %d), want %v", got, len(fx.problems()), tc.want)
			}
		})
	}
}

func TestAutoAttachWithoutBackendIsNoop(t *testing.T) {
	fx := newFixture(t)
	fx.svc.WithProblems(nil)
	in := fx.add("i1", corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh))
	if part := fx.attach(in); part != nil || in.ProblemID != "" || len(fx.problems()) != 0 {
		t.Errorf("partial %v, problem %q", part, in.ProblemID)
	}
}

func TestAutoAttachKeysAndScopesAreIsolated(t *testing.T) {
	fx := newFixture(t)
	a := fx.add("i1", corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh))
	b := fx.add("i2", corr(correlation.KindSharedNode, "n2", diagnose.ConfidenceHigh))
	c := fx.add("i3", corr(correlation.KindSharedStorage, "n1", diagnose.ConfidenceHigh)) // тот же источник, другой вид
	other := tenant.Scope{OrgID: "org1", TeamID: "team1", ClusterID: "cl2"}
	d := fx.addIn(other, "i4", corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh))
	for _, in := range []*Incident{a, b, c, d} {
		fx.attach(in)
	}
	ids := map[string]bool{a.ProblemID: true, b.ProblemID: true, c.ProblemID: true, d.ProblemID: true}
	if len(ids) != 4 || ids[""] {
		t.Errorf("problem ids = %q %q %q %q", a.ProblemID, b.ProblemID, c.ProblemID, d.ProblemID)
	}
	if n := len(fx.problems()); n != 3 {
		t.Errorf("problems in the first cluster = %d, want 3", n)
	}
}

func TestAutoAttachWindow(t *testing.T) {
	fx := newFixture(t)
	c := corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceMedium)
	first := fx.add("i1", c)
	fx.attach(first)

	fx.clock.add(ProblemWindow - time.Minute)
	in := fx.add("i2", c)
	fx.attach(in)
	if in.ProblemID != first.ProblemID {
		t.Fatalf("inside the window: %q, want %q", in.ProblemID, first.ProblemID)
	}
	// Окно считается от последнего обновления: присоединение его продлило.
	fx.clock.add(ProblemWindow - time.Minute)
	in = fx.add("i3", c)
	fx.attach(in)
	if in.ProblemID != first.ProblemID {
		t.Fatalf("the window did not slide: %q", in.ProblemID)
	}
	fx.clock.add(ProblemWindow + time.Minute)
	in = fx.add("i4", c)
	fx.attach(in)
	if in.ProblemID == "" || in.ProblemID == first.ProblemID {
		t.Fatalf("after the window: %q, want a new problem", in.ProblemID)
	}
	if n := len(fx.problems()); n != 2 {
		t.Errorf("problems = %d", n)
	}
}

func TestAutoAttachRespectsDetachedAndClosed(t *testing.T) {
	fx := newFixture(t)
	c := corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceMedium)
	i1, i2, i3 := fx.add("i1", c), fx.add("i2", c), fx.add("i3", c)
	fx.attach(i1)
	fx.attach(i2)
	old := i1.ProblemID

	if _, err := fx.svc.DetachFromProblem(context.Background(), fx.caller(), "i2"); err != nil {
		t.Fatal(err)
	}
	again := fx.incident("i2") // повторное автоприсоединение отсоединённого
	fx.attach(again)
	if again.ProblemID == "" || again.ProblemID == old {
		t.Fatalf("a detached investigation returned to %q", again.ProblemID)
	}
	if hasMember(fx.problem(old), "i2") {
		t.Error("detached member is back in the old problem")
	}
	// Остальные по-прежнему присоединяются к старой, если она свежее.
	fx.clock.add(time.Minute)
	fx.attach(i3)
	if i3.ProblemID == "" {
		t.Fatal("i3 not attached")
	}

	// Закрытая проблема автоматически не принимает.
	fx2 := newFixture(t)
	a, b := fx2.add("a", c), fx2.add("b", c)
	fx2.attach(a)
	p := fx2.problem(a.ProblemID)
	p.Status = ProblemClosed
	if err := fx2.fb.PutProblem(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	fx2.attach(b)
	if b.ProblemID == "" || b.ProblemID == a.ProblemID {
		t.Errorf("closed problem accepted an automatic member: %q", b.ProblemID)
	}
}

func TestAutoAttachFullProblemIsSkipped(t *testing.T) {
	fx := newFixture(t)
	c := corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh)
	first := fx.add("m0", c)
	fx.attach(first)
	p := fx.problem(first.ProblemID)
	for i := 1; i < MaxProblemMembers; i++ {
		p.Members = append(p.Members, ProblemMember{IncidentID: fmt.Sprintf("m%d", i), AddedAt: tBase, AddedBy: AutoMember})
	}
	if err := fx.fb.PutProblem(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	in := fx.add("late", c)
	if part := fx.attach(in); part != nil {
		t.Errorf("a full problem must not be reported as a failure: %v", part)
	}
	if in.ProblemID != "" || len(fx.problems()) != 1 || len(fx.problem(first.ProblemID).Members) != MaxProblemMembers {
		t.Errorf("problem %q, problems %d", in.ProblemID, len(fx.problems()))
	}
	if in.Correlation == nil {
		t.Error("the correlation snapshot must stay")
	}
}

func TestAutoAttachScansAtMostMaxProblemScan(t *testing.T) {
	fx := newFixture(t)
	c := corr(correlation.KindSharedNode, "target", diagnose.ConfidenceHigh)
	first := fx.add("t0", c)
	fx.attach(first)
	// MaxProblemScan более свежих проблем с другими ключами вытесняют искомую за предел просмотра.
	for i := 0; i < MaxProblemScan; i++ {
		fx.clock.add(time.Second)
		in := fx.add(fmt.Sprintf("o%d", i), corr(correlation.KindSharedNode, fmt.Sprintf("other%d", i), diagnose.ConfidenceHigh))
		fx.attach(in)
	}
	fx.clock.add(time.Second)
	in := fx.add("t1", c)
	fx.attach(in)
	if in.ProblemID == first.ProblemID || in.ProblemID == "" {
		t.Errorf("a problem beyond the scan limit was found: %q", in.ProblemID)
	}
}

func TestAutoAttachFailures(t *testing.T) {
	c := corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh)
	t.Run("problem write fails", func(t *testing.T) {
		fx := newFixture(t)
		fx.svc.WithProblems(&failProblems{ProblemBackend: fx.fb, failPut: errors.New("boom")})
		in := fx.add("i1", c)
		part := fx.attach(in)
		if len(part) != 1 || part[0] != "problems" || in.ProblemID != "" || fx.incident("i1").ProblemID != "" || len(fx.problems()) != 0 {
			t.Errorf("partial %v, problem %q", part, in.ProblemID)
		}
	})
	t.Run("investigation link write fails", func(t *testing.T) {
		fx := newFixture(t)
		fb := &failBackend{Backend: fx.fb, failPutAt: 1}
		fx.svc = NewService(fb, identity(), WithClock(fx.clock.now), WithIDs(func() string { return "p1" })).WithProblems(fx.fb)
		in := fx.add("i1", c)
		part := fx.attach(in)
		if len(part) != 1 || part[0] != "problems" || in.ProblemID != "" || fx.incident("i1").ProblemID != "" {
			t.Fatalf("partial %v, problem %q", part, in.ProblemID)
		}
		// Расследование сохранено и валидно; проблема содержит участника и не мешает повтору.
		if got := fx.problems(); len(got) != 1 || !hasMember(&got[0], "i1") {
			t.Errorf("problems = %+v", got)
		}
		if part := fx.attach(in); part != nil || fx.incident("i1").ProblemID != "p1" {
			t.Errorf("retry: partial %v, problem %q", part, fx.incident("i1").ProblemID)
		}
		if got := fx.problems(); len(got) != 1 || len(got[0].Members) != 1 {
			t.Errorf("retry duplicated the member: %+v", got)
		}
	})
	t.Run("missing problem is no group", func(t *testing.T) {
		fx := newFixture(t)
		in := fx.add("i1", c)
		fx.attach(in)
		if err := fx.fb.DeleteProblem(context.Background(), tScope, in.ProblemID); err != nil {
			t.Fatal(err)
		}
		if _, err := fx.svc.GetProblem(context.Background(), tScope, in.ProblemID); !errors.Is(err, ErrProblemNotFound) || !errors.Is(err, ErrNotFound) {
			t.Errorf("GetProblem of a vanished problem: %v", err)
		}
		if _, err := fx.svc.DetachFromProblem(context.Background(), fx.caller(), "i1"); !errors.Is(err, ErrProblemNotFound) {
			t.Errorf("detach: %v", err)
		}
		if fx.incident("i1").ProblemID != "" {
			t.Error("a dangling reference was not cleared")
		}
	})
}

// Два одновременных присоединения к существующей проблеме: оба участника в составе (CAS с повтором).
func TestAutoAttachConcurrentJoin(t *testing.T) {
	fx := newFixture(t)
	c := corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh)
	first := fx.add("i0", c)
	fx.attach(first)
	fx.clock.add(time.Minute)
	ins := []*Incident{fx.add("i1", c), fx.add("i2", c)}
	var wg sync.WaitGroup
	for _, in := range ins {
		wg.Add(1)
		go func() { defer wg.Done(); fx.attach(in) }()
	}
	wg.Wait()
	p := fx.problem(first.ProblemID)
	if len(p.Members) != 3 || !hasMember(p, "i1") || !hasMember(p, "i2") {
		t.Errorf("members = %v", memberIDs(p))
	}
	for _, in := range ins {
		if in.ProblemID != first.ProblemID {
			t.Errorf("%s: problem %q", in.ID, in.ProblemID)
		}
	}
}

// Известное ограничение (ADR-0033 §6): два одновременных открытия по одному ключу без существующей проблемы
// создают две проблемы. Исправляется ручным объединением.
func TestKnownLimitationDuplicateProblemOnRace(t *testing.T) {
	fx := newFixture(t)
	fx.svc.WithProblems(&failProblems{ProblemBackend: fx.fb, listWaiters: 2})
	c := corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh)
	a, b := fx.add("i1", c), fx.add("i2", c)
	var wg sync.WaitGroup
	for _, in := range []*Incident{a, b} {
		wg.Add(1)
		go func() { defer wg.Done(); fx.attach(in) }()
	}
	wg.Wait()
	if a.ProblemID == "" || b.ProblemID == "" || a.ProblemID == b.ProblemID || len(fx.problems()) != 2 {
		t.Fatalf("expected two problems from the race, got %q and %q (%d)", a.ProblemID, b.ProblemID, len(fx.problems()))
	}
	fx.svc.WithProblems(fx.fb)
	to, err := fx.svc.MergeProblems(context.Background(), fx.caller(), a.ProblemID, b.ProblemID)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(memberIDs(to)) != "[i2 i1]" || fx.incident("i1").ProblemID != b.ProblemID {
		t.Errorf("merged = %v, i1 problem %q", memberIDs(to), fx.incident("i1").ProblemID)
	}
	// При поиске выбирается последняя обновлённая: новое открытие идёт в объединённую.
	fx.clock.add(time.Minute)
	c3 := fx.add("i3", c)
	fx.attach(c3)
	if c3.ProblemID != b.ProblemID {
		t.Errorf("new investigation went to %q, want %q", c3.ProblemID, b.ProblemID)
	}
}

func TestOnOutcomeSavedClosesProblemWithLastOpenMember(t *testing.T) {
	fx := newFixture(t)
	c := corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh)
	i1, i2 := fx.add("i1", c), fx.add("i2", c)
	fx.attach(i1)
	fx.attach(i2)
	pid := i1.ProblemID
	closeInc := func(id string) *Incident {
		in := fx.incident(id)
		in.Status = StatusClosed
		in.Outcome = &Outcome{Result: ResultResolved, MarkedBy: "alice", MarkedAt: fx.clock.now()}
		if err := fx.fb.PutIncident(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		return fx.incident(id)
	}
	fx.svc.onOutcomeSaved(context.Background(), closeInc("i1"))
	if fx.problem(pid).Status != ProblemOpen {
		t.Fatal("the problem closed while a member is open")
	}
	fx.svc.onOutcomeSaved(context.Background(), closeInc("i2"))
	if fx.problem(pid).Status != ProblemClosed {
		t.Error("the problem stayed open after the last member was closed")
	}
	// Участник без проблемы и сервис без хранилища — без ошибок и паники.
	fx.svc.onOutcomeSaved(context.Background(), fx.add("solo", nil))
	fx.svc.WithProblems(nil)
	fx.svc.onOutcomeSaved(context.Background(), fx.incident("i1"))
}

func TestMarkOutcomeClosesProblem(t *testing.T) {
	fx := newFixture(t)
	c := corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh)
	in := fx.add("i1", c)
	fx.attach(in)
	if _, err := fx.svc.MarkOutcome(context.Background(), fx.caller(), "i1", Outcome{Result: ResultResolved}); err != nil {
		t.Fatal(err)
	}
	if fx.problem(in.ProblemID).Status != ProblemClosed {
		t.Error("MarkOutcome did not close the problem")
	}
}

func TestPurgeProblemsRemovesStale(t *testing.T) {
	fx := newFixture(t)
	c := corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh)
	old := fx.add("i1", c)
	fx.attach(old)
	fx.clock.add(48 * time.Hour)
	fresh := fx.add("i2", corr(correlation.KindSharedNode, "n2", diagnose.ConfidenceHigh))
	fx.attach(fresh)
	fx.svc.purgeProblems(context.Background(), "org1", fx.clock.now().Add(-24*time.Hour))
	if _, err := fx.fb.GetProblem(context.Background(), tScope, old.ProblemID); !errors.Is(err, ErrNotFound) {
		t.Errorf("stale problem survived: %v", err)
	}
	if _, err := fx.fb.GetProblem(context.Background(), tScope, fresh.ProblemID); err != nil {
		t.Errorf("fresh problem lost: %v", err)
	}
	// Сбой хранилища не падает.
	fx.svc.WithProblems(&purgeFails{fx.fb})
	fx.svc.purgeProblems(context.Background(), "org1", fx.clock.now())
}

type purgeFails struct{ ProblemBackend }

func (purgeFails) PurgeProblems(context.Context, string, time.Time) (int, error) {
	return 0, errors.New("boom")
}

func TestPurgeThroughServiceRemovesProblems(t *testing.T) {
	fx := newFixture(t)
	in := fx.add("i1", corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh))
	fx.attach(in)
	fx.clock.add(400 * 24 * time.Hour) // дальше любого срока хранения по умолчанию
	if _, err := fx.svc.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fx.problems(); len(got) != 0 {
		t.Errorf("problems after Purge = %d", len(got))
	}
}

func TestDeadMembersArePrunedOnNextWrite(t *testing.T) {
	fx := newFixture(t)
	c := corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh)
	i1, i2 := fx.add("i1", c), fx.add("i2", c)
	fx.attach(i1)
	fx.attach(i2)
	if err := fx.fb.DeleteIncident(context.Background(), tScope, "i1"); err != nil {
		t.Fatal(err)
	}
	fx.clock.add(time.Minute)
	i3 := fx.add("i3", c)
	fx.attach(i3)
	if got := memberIDs(fx.problem(i3.ProblemID)); fmt.Sprint(got) != "[i2 i3]" {
		t.Errorf("members = %v, want the deleted one pruned", got)
	}
}

func TestManualOperationsRequireBackend(t *testing.T) {
	fx := newFixture(t)
	fx.svc.WithProblems(nil)
	ctx, c := context.Background(), fx.caller()
	for name, err := range map[string]error{
		"attach": func() error { _, e := fx.svc.AttachToProblem(ctx, c, "i1", "p1"); return e }(),
		"detach": func() error { _, e := fx.svc.DetachFromProblem(ctx, c, "i1"); return e }(),
		"merge":  func() error { _, e := fx.svc.MergeProblems(ctx, c, "p1", "p2"); return e }(),
		"create": func() error { _, e := fx.svc.CreateProblem(ctx, c, []string{"i1"}); return e }(),
		"get":    func() error { _, e := fx.svc.GetProblem(ctx, tScope, "p1"); return e }(),
		"list":   func() error { _, e := fx.svc.ListProblems(ctx, tScope, ProblemQuery{}); return e }(),
	} {
		if !errors.Is(err, ErrProblemsDisabled) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestManualAttachDetach(t *testing.T) {
	fx := newFixture(t)
	ctx, c := context.Background(), fx.caller()
	k1 := corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh)
	k2 := corr(correlation.KindSharedNode, "n2", diagnose.ConfidenceHigh)
	a, b, loose := fx.add("a", k1), fx.add("b", k2), fx.add("loose", nil)
	fx.attach(a)
	fx.attach(b)
	pa, pb := a.ProblemID, b.ProblemID

	// Присоединить расследование без группы.
	fx.clock.add(time.Minute)
	p, err := fx.svc.AttachToProblem(ctx, c, "loose", pa)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != pa || fmt.Sprint(memberIDs(p)) != "[a loose]" || p.Members[1].AddedBy != "bob" || fx.incident("loose").ProblemID != pa {
		t.Errorf("attach: %+v", p)
	}
	// Повтор — без изменений.
	ver := fx.problem(pa).Version
	if _, err := fx.svc.AttachToProblem(ctx, c, "loose", pa); err != nil || fx.problem(pa).Version != ver {
		t.Errorf("repeated attach changed the problem: %v", err)
	}
	// Перенос из одной проблемы в другую: из прежней выходит.
	if _, err := fx.svc.AttachToProblem(ctx, c, "loose", pb); err != nil {
		t.Fatal(err)
	}
	if hasMember(fx.problem(pa), "loose") || !hasMember(fx.problem(pb), "loose") || fx.incident("loose").ProblemID != pb {
		t.Error("the investigation did not move between problems")
	}
	// Отсоединить: в Detached, ссылка снята, остальные на месте.
	got, err := fx.svc.DetachFromProblem(ctx, c, "loose")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != pb || hasMember(got, "loose") || fmt.Sprint(got.Detached) != "[loose]" || fx.incident("loose").ProblemID != "" || !hasMember(got, "b") {
		t.Errorf("detach: %+v", got)
	}
	if _, err := fx.svc.DetachFromProblem(ctx, c, "loose"); !errors.Is(err, ErrProblemNotFound) {
		t.Errorf("detach without a problem: %v", err)
	}
	// Ручное присоединение снимает отметку «отсоединено» и открывает закрытую проблему.
	pr := fx.problem(pb)
	pr.Status = ProblemClosed
	if err := fx.fb.PutProblem(ctx, pr); err != nil {
		t.Fatal(err)
	}
	if p, err := fx.svc.AttachToProblem(ctx, c, "loose", pb); err != nil || p.Status != ProblemOpen || len(p.Detached) != 0 {
		t.Errorf("reattach: %+v, %v", p, err)
	}
	_ = loose
}

func TestManualDetachLastMemberClosesProblem(t *testing.T) {
	fx := newFixture(t)
	in := fx.add("only", corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh))
	fx.attach(in)
	p, err := fx.svc.DetachFromProblem(context.Background(), fx.caller(), "only")
	if err != nil || p.Status != ProblemClosed || len(p.Members) != 0 {
		t.Errorf("%+v, %v", p, err)
	}
}

func TestManualAttachErrors(t *testing.T) {
	fx := newFixture(t)
	ctx, c := context.Background(), fx.caller()
	k := corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh)
	a := fx.add("a", k)
	fx.attach(a)
	fx.add("x", nil)
	if _, err := fx.svc.AttachToProblem(ctx, c, "x", "nope"); !errors.Is(err, ErrProblemNotFound) || !errors.Is(err, ErrNotFound) {
		t.Errorf("missing problem: %v", err)
	}
	if _, err := fx.svc.AttachToProblem(ctx, c, "ghost", a.ProblemID); !errors.Is(err, ErrNotFound) || errors.Is(err, ErrProblemNotFound) {
		t.Errorf("missing investigation: %v", err)
	}
	foreign := Caller{Scope: tenant.Scope{OrgID: "org2", TeamID: "team1", ClusterID: "cl1"}, Actor: "eve"}
	if _, err := fx.svc.AttachToProblem(ctx, foreign, "x", a.ProblemID); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign scope: %v", err)
	}
	// Полная проблема.
	p := fx.problem(a.ProblemID)
	for i := len(p.Members); i < MaxProblemMembers; i++ {
		p.Members = append(p.Members, ProblemMember{IncidentID: fmt.Sprintf("m%d", i), AddedAt: tBase, AddedBy: "u"})
	}
	if err := fx.fb.PutProblem(ctx, p); err != nil {
		t.Fatal(err)
	}
	// Участников m… нет как расследований, но pruneDead убирает их только при записи; проверяем границу на живых.
	for i := 0; i < MaxProblemMembers; i++ {
		fx.add(fmt.Sprintf("m%d", i), nil)
	}
	if _, err := fx.svc.AttachToProblem(ctx, c, "x", a.ProblemID); !errors.Is(err, ErrProblemFull) {
		t.Errorf("full problem: %v", err)
	}
	if fx.incident("x").ProblemID != "" {
		t.Error("a rejected attach linked the investigation")
	}
}

func TestManualMerge(t *testing.T) {
	fx := newFixture(t)
	ctx, c := context.Background(), fx.caller()
	k1 := corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh)
	k2 := corr(correlation.KindSharedNode, "n2", diagnose.ConfidenceHigh)
	a1, a2, b1 := fx.add("a1", k1), fx.add("a2", k1), fx.add("b1", k2)
	fx.attach(a1)
	fx.attach(a2)
	fx.attach(b1)
	pa, pb := a1.ProblemID, b1.ProblemID
	// a2 был отсоединён от b: после слияния отметка пропадает, если он переносится в b.
	pbDoc := fx.problem(pb)
	pbDoc.Detached = []string{"a2"}
	if err := fx.fb.PutProblem(ctx, pbDoc); err != nil {
		t.Fatal(err)
	}

	if _, err := fx.svc.MergeProblems(ctx, c, pa, pa); !errors.Is(err, ErrInvalidProblem) {
		t.Errorf("self-merge: %v", err)
	}
	if _, err := fx.svc.MergeProblems(ctx, c, pa, "nope"); !errors.Is(err, ErrProblemNotFound) {
		t.Errorf("missing target: %v", err)
	}
	if _, err := fx.svc.MergeProblems(ctx, c, "nope", pb); !errors.Is(err, ErrProblemNotFound) {
		t.Errorf("missing source: %v", err)
	}
	to, err := fx.svc.MergeProblems(ctx, c, pa, pb)
	if err != nil {
		t.Fatal(err)
	}
	if to.ID != pb || fmt.Sprint(memberIDs(to)) != "[b1 a1 a2]" || len(to.Detached) != 0 || to.Status != ProblemOpen {
		t.Errorf("target = %+v", to)
	}
	src := fx.problem(pa)
	if src.Status != ProblemClosed || src.MergedInto != pb || len(src.Members) != 0 {
		t.Errorf("source = %+v", src)
	}
	for _, id := range []string{"a1", "a2", "b1"} {
		if got := fx.incident(id).ProblemID; got != pb {
			t.Errorf("%s: problem %q, want %q", id, got, pb)
		}
	}
	// Повтор слияния безопасен; слияние уже объединённой в другую — нет.
	if _, err := fx.svc.MergeProblems(ctx, c, pa, pb); err != nil {
		t.Errorf("repeated merge: %v", err)
	}
	other := fx.add("o1", corr(correlation.KindSharedNode, "n3", diagnose.ConfidenceHigh))
	fx.attach(other)
	if _, err := fx.svc.MergeProblems(ctx, c, pa, other.ProblemID); !errors.Is(err, ErrInvalidProblem) {
		t.Errorf("merging an already merged problem: %v", err)
	}
}

func TestManualMergeOverflow(t *testing.T) {
	fx := newFixture(t)
	ctx, c := context.Background(), fx.caller()
	mk := func(prefix string, n int) string {
		id := fx.ids.Add(1)
		p := NewProblemForTest(fmt.Sprintf("q%d", id), prefix, n)
		for i := 0; i < n; i++ {
			fx.add(fmt.Sprintf("%s%d", prefix, i), nil)
		}
		if err := fx.fb.PutProblem(ctx, p); err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	a, b := mk("a", 60), mk("b", 60)
	if _, err := fx.svc.MergeProblems(ctx, c, a, b); !errors.Is(err, ErrProblemFull) {
		t.Errorf("overflow: %v", err)
	}
	if fx.problem(a).Status != ProblemOpen || len(fx.problem(b).Members) != 60 {
		t.Error("a rejected merge changed the problems")
	}
}

func NewProblemForTest(id, prefix string, n int) *Problem {
	p := &Problem{ID: id, Scope: tScope, Key: "manual:" + id, Kind: "manual", Status: ProblemOpen, Origin: ProblemManual,
		CreatedAt: tBase, UpdatedAt: tBase}
	for i := 0; i < n; i++ {
		p.Members = append(p.Members, ProblemMember{IncidentID: fmt.Sprintf("%s%d", prefix, i), AddedAt: tBase, AddedBy: "u"})
	}
	return p
}

func TestManualCreate(t *testing.T) {
	fx := newFixture(t)
	ctx, c := context.Background(), fx.caller()
	k := corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh)
	a, b, loose := fx.add("a", k), fx.add("b", k), fx.add("loose", nil)
	fx.attach(a)
	fx.attach(b)
	old := a.ProblemID

	// Выделить часть участников в новую проблему.
	p, err := fx.svc.CreateProblem(ctx, c, []string{"b", "loose"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Origin != ProblemManual || p.Status != ProblemOpen || fmt.Sprint(memberIDs(p)) != "[b loose]" || p.Members[0].AddedBy != "bob" ||
		p.Key == k.Best().Key() || p.Kind != "manual" {
		t.Errorf("problem = %+v", p)
	}
	if fx.incident("b").ProblemID != p.ID || fx.incident("loose").ProblemID != p.ID || fx.incident("a").ProblemID != old {
		t.Error("ProblemID was not updated")
	}
	if got := fx.problem(old); fmt.Sprint(memberIDs(got)) != "[a]" {
		t.Errorf("the old problem members = %v", memberIDs(got))
	}
	// Ручная проблема не принимает автоматически: ключ корреляции ей не соответствует.
	fx.clock.add(time.Minute)
	n := fx.add("n", k)
	fx.attach(n)
	if n.ProblemID != old {
		t.Errorf("automatic member went to %q, want %q", n.ProblemID, old)
	}
	_ = loose

	for name, ids := range map[string][]string{"empty": nil, "repeat": {"a", "a"}, "bad id": {"../x"}} {
		if _, err := fx.svc.CreateProblem(ctx, c, ids); !errors.Is(err, ErrInvalidProblem) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := fx.svc.CreateProblem(ctx, c, []string{"a", "ghost"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing investigation: %v", err)
	}
	many := make([]string, MaxProblemMembers+1)
	for i := range many {
		many[i] = fmt.Sprintf("m%d", i)
	}
	if _, err := fx.svc.CreateProblem(ctx, c, many); !errors.Is(err, ErrProblemFull) {
		t.Errorf("too many: %v", err)
	}
	before := len(fx.problems())
	if _, err := fx.svc.CreateProblem(ctx, c, []string{"a", "ghost"}); err == nil || len(fx.problems()) != before {
		t.Error("a failed create left a problem behind")
	}
}

// Инвариант ADR-0033 §6: ни одна операция с проблемами не меняет документ расследования, кроме ProblemID
// (UpdatedAt и содержимое — прежние; расследования не удаляются).
func TestInvestigationsAreNeverModifiedExceptProblemID(t *testing.T) {
	fx := newFixture(t)
	ctx, c := context.Background(), fx.caller()
	k1 := corr(correlation.KindSharedNode, "n1", diagnose.ConfidenceHigh)
	k2 := corr(correlation.KindSharedNode, "n2", diagnose.ConfidenceMedium)
	ids := []string{"a", "b", "c", "d", "e"}
	for i, id := range ids {
		kk := k1
		if i >= 3 {
			kk = k2
		}
		fx.add(id, kk)
	}
	snap := map[string]string{}
	for _, id := range ids {
		snap[id] = frozen(t, fx.incident(id))
	}
	check := func(step string) {
		t.Helper()
		for _, id := range ids {
			in, err := fx.fb.GetIncident(ctx, tScope, id)
			if err != nil {
				t.Fatalf("%s: investigation %s is gone: %v", step, id, err)
			}
			if got := frozen(t, in); got != snap[id] {
				t.Errorf("%s: investigation %s was modified:\n got %s\nwant %s", step, id, got, snap[id])
			}
		}
	}
	for _, id := range ids {
		fx.clock.add(time.Minute)
		fx.attach(fx.incident(id))
	}
	check("auto attach")
	steps := []struct {
		name string
		fn   func() error
	}{
		{"manual attach", func() error { _, e := fx.svc.AttachToProblem(ctx, c, "e", fx.incident("a").ProblemID); return e }},
		{"detach", func() error { _, e := fx.svc.DetachFromProblem(ctx, c, "b"); return e }},
		{"create", func() error { _, e := fx.svc.CreateProblem(ctx, c, []string{"b", "c"}); return e }},
		{"merge", func() error {
			_, e := fx.svc.MergeProblems(ctx, c, fx.incident("a").ProblemID, fx.incident("d").ProblemID)
			return e
		}},
		{"outcome hook", func() error { fx.svc.onOutcomeSaved(ctx, fx.incident("a")); return nil }},
		{"purge hook", func() error { fx.svc.purgeProblems(ctx, "org1", fx.clock.now().Add(time.Hour)); return nil }},
	}
	for _, s := range steps {
		if err := s.fn(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		check(s.name)
	}
}
