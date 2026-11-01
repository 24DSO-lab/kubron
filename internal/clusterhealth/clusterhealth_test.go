// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package clusterhealth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memSink struct {
	mu   sync.Mutex
	data map[string]Health
	fail bool
}

func (s *memSink) Save(_ context.Context, id string, h Health) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("db down")
	}
	if s.data == nil {
		s.data = map[string]Health{}
	}
	s.data[id] = h
	return nil
}

func (s *memSink) Load(context.Context) (map[string]Health, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]Health{}
	for k, v := range s.data {
		out[k] = v
	}
	return out, nil
}

type fakeLeader struct {
	lead     atomic.Bool
	released atomic.Int32
}

func (l *fakeLeader) Acquire(context.Context) bool { return l.lead.Load() }
func (l *fakeLeader) Release()                     { l.released.Add(1) }

func TestTickProbesAllClustersAndReportsChangeOnce(t *testing.T) {
	var state atomic.Value
	state.Store(OK)
	changes := 0
	m := &Monitor{
		Board: NewBoard(),
		List:  func() ([]string, error) { return []string{"a", "b"}, nil },
		Probe: func(_ context.Context, id string) Health {
			if id == "b" {
				return Health{State: state.Load().(State), Message: "dial tcp: i/o timeout"}
			}
			return Health{State: OK}
		},
		OnChange: func() { changes++ },
	}
	ctx := context.Background()
	m.Tick(ctx)
	if changes != 1 {
		t.Fatalf("first tick must report a change, got %d", changes)
	}
	m.Tick(ctx)
	if changes != 1 {
		t.Fatalf("unchanged tick must stay silent, got %d", changes)
	}
	state.Store(Unreachable)
	m.Tick(ctx)
	if changes != 2 {
		t.Fatalf("state change must be reported, got %d", changes)
	}
	if h, _ := m.Board.Get("b"); h.State != Unreachable || h.Message == "" || h.CheckedAt.IsZero() {
		t.Fatalf("unexpected health %+v", h)
	}
}

func TestProbeIsBoundedByTimeoutAndParallelism(t *testing.T) {
	var running, peak atomic.Int32
	m := &Monitor{
		Board:    NewBoard(),
		Parallel: 2,
		Timeout:  50 * time.Millisecond,
		List:     func() ([]string, error) { return []string{"1", "2", "3", "4", "5"}, nil },
		Probe: func(ctx context.Context, _ string) Health {
			n := running.Add(1)
			defer running.Add(-1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			<-ctx.Done() // зависший кластер: проверка обрывается по таймауту
			return Health{State: Unreachable, Message: ctx.Err().Error()}
		},
	}
	start := time.Now()
	m.Tick(context.Background())
	if peak.Load() > 2 {
		t.Fatalf("parallelism exceeded: %d", peak.Load())
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("hung clusters must not stall the tick")
	}
	if h, _ := m.Board.Get("5"); h.State != Unreachable {
		t.Fatalf("got %+v", h)
	}
}

func TestPanicInProbeBecomesUnknown(t *testing.T) {
	m := &Monitor{
		Board: NewBoard(),
		List:  func() ([]string, error) { return []string{"x"}, nil },
		Probe: func(context.Context, string) Health { panic("boom") },
	}
	m.Tick(context.Background())
	h, ok := m.Board.Get("x")
	if !ok || h.State != Unknown || !strings.Contains(h.Message, "boom") {
		t.Fatalf("got %+v ok=%v", h, ok)
	}
}

func TestRemovedClustersAreForgotten(t *testing.T) {
	ids := []string{"a", "b"}
	changes := 0
	m := &Monitor{
		Board:    NewBoard(),
		List:     func() ([]string, error) { return ids, nil },
		Probe:    func(context.Context, string) Health { return Health{State: OK} },
		OnChange: func() { changes++ },
	}
	m.Tick(context.Background())
	ids = []string{"a"}
	m.Tick(context.Background())
	if _, ok := m.Board.Get("b"); ok {
		t.Fatal("deleted cluster must disappear from the board")
	}
	if changes != 2 {
		t.Fatalf("deletion is a change, got %d", changes)
	}
}

func TestFollowerReadsLeaderResultsAndTakesOver(t *testing.T) {
	sink := &memSink{}
	lead := &fakeLeader{}
	lead.lead.Store(true)
	probes := atomic.Int32{}
	mk := func(l Leader) *Monitor {
		return &Monitor{
			Board:  NewBoard(),
			Sink:   sink,
			Leader: l,
			List:   func() ([]string, error) { return []string{"c"}, nil },
			Probe: func(context.Context, string) Health {
				probes.Add(1)
				return Health{State: Unauthorized, Message: "token expired"}
			},
		}
	}
	a := mk(lead)
	follower := &fakeLeader{}
	b := mk(follower)
	ctx := context.Background()

	a.Tick(ctx)
	b.Tick(ctx)
	if probes.Load() != 1 {
		t.Fatalf("only the leader probes, got %d", probes.Load())
	}
	if h, _ := b.Board.Get("c"); h.State != Unauthorized {
		t.Fatalf("follower must show the leader's result, got %+v", h)
	}

	lead.lead.Store(false) // лидер потерян, подхватывает вторая реплика
	follower.lead.Store(true)
	b.Tick(ctx)
	if probes.Load() != 2 {
		t.Fatalf("new leader must probe, got %d", probes.Load())
	}
}

func TestSinkFailureDoesNotBreakTick(t *testing.T) {
	var logged []string
	m := &Monitor{
		Board: NewBoard(),
		Sink:  &memSink{fail: true},
		List:  func() ([]string, error) { return []string{"a"}, nil },
		Probe: func(context.Context, string) Health { return Health{State: OK} },
		Logf:  func(f string, a ...any) { logged = append(logged, f) },
	}
	m.Tick(context.Background())
	if h, ok := m.Board.Get("a"); !ok || h.State != OK {
		t.Fatalf("local board must still be updated: %+v", h)
	}
	if len(logged) == 0 {
		t.Fatal("storage error must be logged")
	}
}

func TestRunStopsAndReleasesLeadership(t *testing.T) {
	l := &fakeLeader{}
	l.lead.Store(true)
	m := &Monitor{
		Board: NewBoard(), Leader: l, Interval: 10 * time.Millisecond,
		List:  func() ([]string, error) { return nil, nil },
		Probe: func(context.Context, string) Health { return Health{State: OK} },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
	if l.released.Load() != 1 {
		t.Fatalf("leadership must be released once, got %d", l.released.Load())
	}
}

func TestTrim(t *testing.T) {
	if got := Trim("  first line\nsecond  "); got != "first line" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("я", 500)
	got := Trim(long)
	if n := len([]rune(got)); n != MaxMessage {
		t.Fatalf("got %d runes", n)
	}
}

// Лидер сообщает остальным репликам об изменениях (OnPublish), читающая реплика — нет (иначе сообщения ходили бы
// по кругу); Refresh подтягивает результаты лидера немедленно, не дожидаясь круга.
func TestLeaderPublishesAndFollowerRefreshes(t *testing.T) {
	sink := &memSink{}
	var published, leaderChanged, followerChanged atomic.Int32
	leader := &Monitor{
		Board: NewBoard(), Sink: sink, Interval: time.Hour,
		List:     func() ([]string, error) { return []string{"c1"}, nil },
		Probe:    func(context.Context, string) Health { return Health{State: OK} },
		OnChange: func() { leaderChanged.Add(1) }, OnPublish: func() { published.Add(1) },
	}
	follower := &Monitor{
		Board: NewBoard(), Sink: sink, Interval: time.Hour, Leader: &fakeLeader{},
		List:     func() ([]string, error) { return nil, nil },
		OnChange: func() { followerChanged.Add(1) }, OnPublish: func() { published.Add(100) },
	}
	leader.Tick(context.Background())
	if published.Load() != 1 || leaderChanged.Load() != 1 {
		t.Fatalf("the leader must publish once: published=%d changed=%d", published.Load(), leaderChanged.Load())
	}
	follower.Refresh(context.Background())
	if followerChanged.Load() != 1 || published.Load() != 1 {
		t.Errorf("the follower must pick up the result without publishing: changed=%d published=%d", followerChanged.Load(), published.Load())
	}
	if h, ok := follower.Board.Get("c1"); !ok || h.State != OK {
		t.Errorf("follower board: %+v %v", h, ok)
	}
	// второй круг без изменений ничего не публикует
	leader.Tick(context.Background())
	if published.Load() != 1 {
		t.Errorf("an unchanged round must not publish: %d", published.Load())
	}
}
