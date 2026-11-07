// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestPoolManager создаёт Manager с пулом, в котором уже лежат кластеры ids, и счётчиком
// закрытий (вызовов Close, т.е. остановок informers) по каждому кластеру.
func newTestPoolManager(t *testing.T, ids ...string) (*Manager, map[string]*atomic.Int32) {
	t.Helper()
	m := NewManager()
	closes := map[string]*atomic.Int32{}
	for _, id := range ids {
		closes[id] = &atomic.Int32{}
		m.pool.put(id, &ClusterConnection{Name: id})
	}
	m.pool.closeFn = func(c *ClusterConnection) { closes[c.Name].Add(1) }
	return m, closes
}

func mustActivate(t *testing.T, m *Manager, id string) {
	t.Helper()
	m.viewMu.Lock()
	defer m.viewMu.Unlock()
	if err := m.activateLocked(id, id); err != nil {
		t.Fatalf("activate %s: %v", id, err)
	}
}

func activeName(t *testing.T, m *Manager) string {
	t.Helper()
	c, err := m.GetActiveConnection()
	if err != nil {
		return ""
	}
	return c.Name
}

func TestViewsHaveIndependentActiveCluster(t *testing.T) {
	root, closes := newTestPoolManager(t, "a", "b")
	v1, v2 := root.ForSession(), root.ForSession()

	mustActivate(t, v1, "a")
	mustActivate(t, v2, "b")

	if got := activeName(t, v1); got != "a" {
		t.Errorf("v1 active is %q, expected a", got)
	}
	if got := activeName(t, v2); got != "b" {
		t.Errorf("v2 active is %q, expected b", got)
	}
	if v1.terminalSessions == v2.terminalSessions || v1.terminalSessions == root.terminalSessions {
		t.Error("each view must have its own terminals")
	}
	for id, n := range closes {
		if n.Load() != 0 {
			t.Errorf("cluster %s was closed for no reason", id)
		}
	}
}

func TestSharedClusterStaysOpenUntilLastViewLeaves(t *testing.T) {
	root, closes := newTestPoolManager(t, "a", "b")
	v1, v2 := root.ForSession(), root.ForSession()

	mustActivate(t, v1, "a")
	mustActivate(t, v2, "a")

	mustActivate(t, v1, "b") // v2 ещё на a: informers a не трогаем
	if n := closes["a"].Load(); n != 0 {
		t.Fatalf("a was closed when one of two views left (closes=%d)", n)
	}
	mustActivate(t, v2, "b") // последний вид ушёл с a
	if n := closes["a"].Load(); n != 1 {
		t.Fatalf("a must be closed exactly once, closes=%d", n)
	}
	if !root.pool.has("a") {
		t.Error("connection a must stay in the pool for reuse")
	}
}

func TestDetachReleasesOnlyOwnReference(t *testing.T) {
	root, closes := newTestPoolManager(t, "a")
	v1, v2 := root.ForSession(), root.ForSession()
	mustActivate(t, v1, "a")
	mustActivate(t, v2, "a")

	v1.Detach()
	if closes["a"].Load() != 0 {
		t.Fatal("Detach of one view closed a cluster held by another")
	}
	if activeName(t, v2) != "a" {
		t.Fatal("v2 lost its active cluster")
	}
	v2.Detach()
	if closes["a"].Load() != 1 {
		t.Fatal("the cluster must be closed after all views leave")
	}
}

func TestRemoveConnectionAffectsEveryView(t *testing.T) {
	root, _ := newTestPoolManager(t, "a")
	v1, v2 := root.ForSession(), root.ForSession()
	mustActivate(t, v1, "a")
	mustActivate(t, v2, "a")

	if !v1.RemoveConnection("a") {
		t.Error("wasActive must be true for the initiating view")
	}
	if _, err := v2.GetActiveConnection(); err == nil {
		t.Error("the second view must lose its connection (\"no active connection\" error)")
	}
}

func TestActivateUnknownClusterKeepsPreviousActive(t *testing.T) {
	root, _ := newTestPoolManager(t, "a")
	v := root.ForSession()
	mustActivate(t, v, "a")

	v.viewMu.Lock()
	err := v.activateLocked("missing", "missing")
	v.viewMu.Unlock()
	if err == nil {
		t.Fatal("expected an error for a cluster that is not connected")
	}
	if activeName(t, v) != "a" {
		t.Error("a failed activation must not reset the previous cluster")
	}
}

func TestGetOrCreateBuildsOnce(t *testing.T) {
	p := newConnPool()
	var builds atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.getOrCreate("a", func() (*ClusterConnection, error) {
				builds.Add(1)
				return &ClusterConnection{Name: "a"}, nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if builds.Load() != 1 {
		t.Fatalf("connection created %d times, expected 1", builds.Load())
	}
}

// Запускать с -race: 50 видов параллельно переключают кластеры; в конце все отсоединяются,
// и каждый кластер закрыт, а счётчики не ушли в минус.
func TestManyViewsConcurrentSwitching(t *testing.T) {
	ids := []string{"a", "b", "c", "d"}
	root, _ := newTestPoolManager(t, ids...)
	root.pool.closeFn = func(*ClusterConnection) {}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := root.ForSession()
			for j := 0; j < 200; j++ {
				id := ids[(i+j)%len(ids)]
				v.viewMu.Lock()
				_ = v.activateLocked(id, id)
				v.viewMu.Unlock()
				if _, err := v.GetActiveConnection(); err != nil {
					t.Errorf("view %d: %v", i, err)
					return
				}
			}
			v.Detach()
		}(i)
	}
	wg.Wait()

	root.pool.mu.Lock()
	defer root.pool.mu.Unlock()
	for id, e := range root.pool.entries {
		if e.refs != 0 {
			t.Errorf("%s: refs=%d after all views left", id, e.refs)
		}
	}
}

func TestClusterEventsGoToEveryViewHoldingCluster(t *testing.T) {
	root, _ := newTestPoolManager(t, "a", "b")
	got := map[string][]string{}
	var mu sync.Mutex
	mk := func(name string) *Manager {
		v := root.ForSession()
		v.SetNotifier(func(ev string, data ...any) {
			mu.Lock()
			got[name] = append(got[name], ev)
			mu.Unlock()
		})
		return v
	}
	v1, v2, v3 := mk("v1"), mk("v2"), mk("v3")
	mustActivate(t, v1, "a")
	mustActivate(t, v2, "a")
	mustActivate(t, v3, "b")

	// Событие инициирует любой вид, получают все, у кого кластер активен, и только они.
	v1.emitCluster(EventClusterChanged, &ClusterConnection{Name: "a"})
	mu.Lock()
	if len(got["v1"]) != 1 || len(got["v2"]) != 1 || len(got["v3"]) != 0 {
		t.Errorf("receivers: %v", got)
	}
	mu.Unlock()

	// После выхода вида из кластера события ему больше не идут.
	v2.Detach()
	v1.emitCluster(EventClusterChanged, &ClusterConnection{Name: "a"})
	mu.Lock()
	defer mu.Unlock()
	if len(got["v1"]) != 2 || len(got["v2"]) != 1 {
		t.Errorf("after Detach: %v", got)
	}
}

func TestSlowBuildDoesNotBlockOtherClusters(t *testing.T) {
	p := newConnPool()
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_, _ = p.getOrCreate("slow", func() (*ClusterConnection, error) {
			close(started)
			<-release
			return &ClusterConnection{Name: "slow"}, nil
		})
	}()
	<-started
	done := make(chan struct{})
	go func() {
		_, _ = p.getOrCreate("fast", func() (*ClusterConnection, error) { return &ClusterConnection{Name: "fast"}, nil })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("building a slow cluster blocked another one")
	}
	close(release)
}

func TestSingleflightSharesErrorAndAllowsRetry(t *testing.T) {
	p := newConnPool()
	var builds atomic.Int32
	gate := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = p.getOrCreate("a", func() (*ClusterConnection, error) {
				builds.Add(1)
				<-gate
				return nil, errors.New("oidc unavailable")
			})
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	for _, err := range errs {
		if err == nil {
			t.Fatal("all waiters must receive the build error")
		}
	}
	if builds.Load() > 3 { // допускаем запоздавших, но не лавину
		t.Errorf("%d builds", builds.Load())
	}
	// Ошибка не кешируется: следующая попытка строит заново.
	if _, err := p.getOrCreate("a", func() (*ClusterConnection, error) { return &ClusterConnection{Name: "a"}, nil }); err != nil {
		t.Fatalf("retry after an error: %v", err)
	}
}

func TestBuildPanicBecomesError(t *testing.T) {
	p := newConnPool()
	if _, err := p.getOrCreate("a", func() (*ClusterConnection, error) { panic("boom") }); err == nil {
		t.Fatal("a build panic must become an error")
	}
	if len(p.flights) != 0 {
		t.Error("an unfinished build stayed in the registry")
	}
}

func TestIdleTTLDefersInformersShutdown(t *testing.T) {
	root, closes := newTestPoolManager(t, "a")
	root.SetIdleTTL(80 * time.Millisecond)
	v := root.ForSession()
	mustActivate(t, v, "a")
	v.Detach()
	if closes["a"].Load() != 0 {
		t.Fatal("informers were stopped before the TTL expired")
	}
	// Возврат в пределах TTL отменяет остановку.
	mustActivate(t, v, "a")
	time.Sleep(200 * time.Millisecond)
	if closes["a"].Load() != 0 {
		t.Fatal("the cluster is active again, there must be no stop")
	}
	v.Detach()
	time.Sleep(250 * time.Millisecond)
	if closes["a"].Load() != 1 {
		t.Fatalf("after the TTL exactly one stop was expected, got %d", closes["a"].Load())
	}
}
