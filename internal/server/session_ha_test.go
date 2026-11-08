// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/replicaevents"
	"github.com/24DSO-lab/kubron/internal/websession"
)

// memBackend — общее хранилище сессий в памяти: роль PostgreSQL для двух «реплик» в тесте.
type memBackend struct {
	mu       sync.Mutex
	now      func() time.Time
	rows     map[string]*memRow // по id
	disabled map[string]bool    // отключённые пользователи
	down     bool
	gets     int
}

type memRow struct {
	rec       websession.Record
	prev      websession.Hash
	hasPrev   bool
	prevUntil time.Time
}

func newMemBackend(now func() time.Time) *memBackend {
	return &memBackend{now: now, rows: map[string]*memRow{}, disabled: map[string]bool{}}
}

var errDown = errors.New("database is down")

func (m *memBackend) Create(_ context.Context, r websession.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return errDown
	}
	m.rows[r.ID] = &memRow{rec: r}
	return nil
}

func (m *memBackend) Get(_ context.Context, h websession.Hash) (websession.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
	if m.down {
		return websession.Record{}, errDown
	}
	for _, r := range m.rows {
		cur := r.rec.TokenHash == h
		prev := r.hasPrev && r.prev == h && m.now().Before(r.prevUntil)
		if (cur || prev) && m.now().Before(r.rec.ExpiresAt) && !m.disabled[r.rec.UserID] {
			return r.rec, nil
		}
	}
	return websession.Record{}, websession.ErrNotFound
}

func (m *memBackend) Touch(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.rows[id]; r != nil && at.After(r.rec.LastActive) {
		r.rec.LastActive = at
	}
	return nil
}

func (m *memBackend) SetActiveCluster(_ context.Context, id, c string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.rows[id]; r != nil {
		r.rec.ActiveCluster = c
	}
	return nil
}

func (m *memBackend) Rotate(_ context.Context, id string, nh websession.Hash, until time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rows[id]
	if r == nil {
		return websession.ErrNotFound
	}
	r.prev, r.hasPrev, r.prevUntil = r.rec.TokenHash, true, until
	r.rec.TokenHash = nh
	return nil
}

func (m *memBackend) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return errDown
	}
	delete(m.rows, id)
	return nil
}

func (m *memBackend) DeleteUser(_ context.Context, userID, except string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for id, r := range m.rows {
		if r.rec.UserID == userID && id != except {
			ids = append(ids, id)
			delete(m.rows, id)
		}
	}
	return ids, nil
}

func (m *memBackend) DeleteExpired(context.Context) (int, error) { return 0, nil }

// fakeView — вид ядра в тесте: считает освобождения.
type fakeView struct {
	userID   string
	released int
	active   bool
	synced   time.Time
}

// replica — «процесс»: свой реестр сессий поверх общего хранилища и общих часов.
func replica(t *testing.T, id string, b *memBackend, clock *time.Time) (*Sessions, *[]*fakeView) {
	t.Helper()
	ss := NewSessions(time.Hour, false)
	ss.now = func() time.Time { return *clock }
	ss.Backend, ss.ReplicaID = b, id
	var views []*fakeView
	ss.Restore = func(rec websession.Record) (any, error) {
		v := &fakeView{userID: rec.UserID, active: true}
		views = append(views, v)
		return v, nil
	}
	ss.Release = func(v any) { v.(*fakeView).released++ }
	ss.Sync = func(v any, rec websession.Record) { v.(*fakeView).synced = rec.LastActive }
	return ss, &views
}

func TestSessionLoginOnAServedByB(t *testing.T) {
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newMemBackend(func() time.Time { return clock })
	a, _ := replica(t, "a", b, &clock)
	bb, bViews := replica(t, "b", b, &clock)

	tok, sa, err := a.Open("u1", &fakeView{}, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := bb.Lookup(tok)
	if err != nil {
		t.Fatalf("a session opened on replica A must work on B: %v", err)
	}
	if got.ID != sa.ID || got.UserID != "u1" || got.CSRF != sa.CSRF {
		t.Errorf("session on B: %+v, on A: %+v", got, sa)
	}
	if got.View == nil || len(*bViews) != 1 {
		t.Fatal("B must build its own view lazily")
	}
	// повторный запрос в пределах кэша не строит вид заново и не ходит в хранилище
	gets := b.gets
	if _, err := bb.Lookup(tok); err != nil || len(*bViews) != 1 || b.gets != gets {
		t.Errorf("cache: err=%v views=%d store reads=%d→%d", err, len(*bViews), gets, b.gets)
	}
}

func TestSessionStoreReadsAreCached(t *testing.T) {
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newMemBackend(func() time.Time { return clock })
	a, _ := replica(t, "a", b, &clock)
	tok, _, _ := a.Open("u1", nil, "")
	for i := 0; i < 50; i++ {
		if _, err := a.Lookup(tok); err != nil {
			t.Fatal(err)
		}
	}
	if b.gets != 0 {
		t.Errorf("the session was opened here: no reads expected within the cache TTL, got %d", b.gets)
	}
	clock = clock.Add(sessionCacheTTL + time.Millisecond)
	for i := 0; i < 50; i++ {
		_, _ = a.Lookup(tok)
	}
	if b.gets != 1 {
		t.Errorf("one read per cache TTL expected, got %d", b.gets)
	}
}

func TestSessionLogoutOnAEndsOnB(t *testing.T) {
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newMemBackend(func() time.Time { return clock })
	a, _ := replica(t, "a", b, &clock)
	bb, bViews := replica(t, "b", b, &clock)
	var ended []string
	bb.OnEnd = func(id string) { ended = append(ended, id) }

	tok, sa, _ := a.Open("u1", &fakeView{}, "")
	if _, err := bb.Lookup(tok); err != nil {
		t.Fatal(err)
	}
	if err := a.Delete(tok); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(sessionCacheTTL + time.Millisecond)
	if _, err := bb.Lookup(tok); !errors.Is(err, websession.ErrNotFound) {
		t.Fatalf("after logout on A the session must end on B: %v", err)
	}
	if (*bViews)[0].released != 1 || len(ended) != 1 || ended[0] != sa.ID {
		t.Errorf("B must release the view and close the connections of the session: released=%d ended=%v", (*bViews)[0].released, ended)
	}
}

func TestSessionLogoutOnBWithoutLocalView(t *testing.T) {
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newMemBackend(func() time.Time { return clock })
	a, _ := replica(t, "a", b, &clock)
	bb, _ := replica(t, "b", b, &clock)
	tok, _, _ := a.Open("u1", nil, "")
	if err := bb.Delete(tok); err != nil { // B ни разу не видела эту сессию
		t.Fatal(err)
	}
	clock = clock.Add(sessionCacheTTL + time.Millisecond)
	if _, err := a.Lookup(tok); !errors.Is(err, websession.ErrNotFound) {
		t.Errorf("logout on B must end the session everywhere: %v", err)
	}
}

func TestSessionDeleteReportsStoreError(t *testing.T) {
	clock := time.Now()
	b := newMemBackend(func() time.Time { return clock })
	a, _ := replica(t, "a", b, &clock)
	tok, _, _ := a.Open("u1", nil, "")
	b.down = true
	if err := a.Delete(tok); err == nil {
		t.Error("a logout that could not be stored must not look successful")
	}
}

func TestSessionEndUserEverywhere(t *testing.T) {
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newMemBackend(func() time.Time { return clock })
	a, _ := replica(t, "a", b, &clock)
	bb, _ := replica(t, "b", b, &clock)
	t1, s1, _ := a.Open("u1", &fakeView{}, "")
	t2, _, _ := a.Open("u1", &fakeView{}, "")
	t3, _, _ := a.Open("u2", &fakeView{}, "")
	for _, tk := range []string{t1, t2, t3} {
		if _, err := bb.Lookup(tk); err != nil {
			t.Fatal(err)
		}
	}
	// смена пароля на A из сессии t1: остальные сессии u1 завершаются на обеих репликах
	if err := a.EndUser("u1", s1.ID); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(sessionCacheTTL + time.Millisecond)
	for tk, want := range map[string]bool{t1: true, t2: false, t3: true} {
		for name, r := range map[string]*Sessions{"A": a, "B": bb} {
			if _, err := r.Lookup(tk); (err == nil) != want {
				t.Errorf("replica %s, token valid=%v, want %v (%v)", name, err == nil, want, err)
			}
		}
	}
}

func TestSessionDisabledUserRejectedByStore(t *testing.T) {
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newMemBackend(func() time.Time { return clock })
	a, _ := replica(t, "a", b, &clock)
	bb, _ := replica(t, "b", b, &clock)
	tok, _, _ := a.Open("u1", &fakeView{}, "")
	if _, err := bb.Lookup(tok); err != nil {
		t.Fatal(err)
	}
	b.disabled["u1"] = true
	clock = clock.Add(sessionCacheTTL + time.Millisecond)
	if _, err := bb.Lookup(tok); !errors.Is(err, websession.ErrNotFound) {
		t.Errorf("a disabled user's session must end: %v", err)
	}
}

func TestSessionRotateKeepsOldTokenForGrace(t *testing.T) {
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newMemBackend(func() time.Time { return clock })
	a, _ := replica(t, "a", b, &clock)
	bb, bViews := replica(t, "b", b, &clock)
	old, sa, _ := a.Open("u1", &fakeView{}, "")
	nt, err := a.Rotate(old)
	if err != nil || nt == old {
		t.Fatalf("rotate: %q %v", nt, err)
	}
	// новый токен действует на обеих репликах и это та же сессия
	for _, r := range []*Sessions{a, bb} {
		got, err := r.Lookup(nt)
		if err != nil || got.ID != sa.ID {
			t.Fatalf("new token: %+v %v", got, err)
		}
	}
	// старый — ещё 30 с (параллельные запросы), на обеих репликах и без второго вида
	clock = clock.Add(5 * time.Second)
	for _, r := range []*Sessions{a, bb} {
		if got, err := r.Lookup(old); err != nil || got.ID != sa.ID {
			t.Errorf("old token within the grace period: %+v %v", got, err)
		}
	}
	if len(*bViews) != 1 {
		t.Errorf("one session must have one view on a replica: %d", len(*bViews))
	}
	clock = clock.Add(websession.PrevTokenGrace + time.Second)
	for name, r := range map[string]*Sessions{"A": a, "B": bb} {
		if _, err := r.Lookup(old); !errors.Is(err, websession.ErrNotFound) {
			t.Errorf("replica %s: the old token must stop working: %v", name, err)
		}
		if _, err := r.Lookup(nt); err != nil {
			t.Errorf("replica %s: the session itself must live on under the new token: %v", name, err)
		}
	}
}

func TestSessionStoreOutage(t *testing.T) {
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newMemBackend(func() time.Time { return clock })
	a, _ := replica(t, "a", b, &clock)
	bb, _ := replica(t, "b", b, &clock)
	tok, _, _ := a.Open("u1", &fakeView{}, "")
	if _, err := bb.Lookup(tok); err != nil {
		t.Fatal(err)
	}
	b.down = true
	clock = clock.Add(10 * time.Second) // кэш устарел, но льгота ещё действует
	if _, err := bb.Lookup(tok); err != nil {
		t.Errorf("within the grace period the cached session must be served: %v", err)
	}
	clock = clock.Add(sessionStaleGrace + time.Second)
	if _, err := bb.Lookup(tok); !errors.Is(err, ErrSessionStoreUnavailable) {
		t.Fatalf("after the grace period: 503, not logout: %v", err)
	}
	// БД вернулась: повторный вход не нужен
	b.down = false
	if _, err := bb.Lookup(tok); err != nil {
		t.Errorf("the session must survive an outage: %v", err)
	}
	// неизвестная этой реплике сессия при недоступной БД — тоже 503, а не «не вошёл»
	b.down = true
	if _, err := replicaLookupFresh(t, b, &clock, tok); !errors.Is(err, ErrSessionStoreUnavailable) {
		t.Errorf("unknown session during an outage: %v", err)
	}
}

func replicaLookupFresh(t *testing.T, b *memBackend, clock *time.Time, tok string) (Session, error) {
	t.Helper()
	c, _ := replica(t, "c", b, clock)
	return c.Lookup(tok)
}

func TestSessionOpenFailsWhenStoreIsDown(t *testing.T) {
	clock := time.Now()
	b := newMemBackend(func() time.Time { return clock })
	b.down = true
	a, _ := replica(t, "a", b, &clock)
	if _, _, err := a.Open("u1", &fakeView{}, ""); err == nil {
		t.Fatal("a login that could not be stored must fail")
	}
	if a.Len() != 0 {
		t.Error("no local session must be left")
	}
}

func TestSessionRestoreFailureDropsRow(t *testing.T) {
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newMemBackend(func() time.Time { return clock })
	a, _ := replica(t, "a", b, &clock)
	bb, _ := replica(t, "b", b, &clock)
	bb.Restore = func(websession.Record) (any, error) { return nil, errors.New("the user is gone") }
	tok, _, _ := a.Open("u1", &fakeView{}, "")
	if _, err := bb.Lookup(tok); !errors.Is(err, websession.ErrNotFound) {
		t.Fatalf("a session whose user cannot be resumed is invalid: %v", err)
	}
	if len(b.rows) != 0 {
		t.Error("the unusable row must be removed")
	}
}

func TestSessionReapKeepsViewActiveOnAnotherReplica(t *testing.T) {
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newMemBackend(func() time.Time { return clock })
	a, _ := replica(t, "a", b, &clock)
	bb, bViews := replica(t, "b", b, &clock)
	tok, sa, _ := a.Open("u1", &fakeView{}, "")
	if _, err := bb.Lookup(tok); err != nil {
		t.Fatal(err)
	}
	bv := (*bViews)[0]
	bv.active = false // на B вид простоял и считает сессию завершённой простоем…
	// …но пользователь работает на A: активность записана в хранилище
	clock = clock.Add(time.Minute)
	_ = b.Touch(context.Background(), sa.ID, clock)
	bb.Sync = func(v any, rec websession.Record) {
		fv := v.(*fakeView)
		fv.synced = rec.LastActive
		fv.active = true
	}
	if n := bb.Reap(func(v any) bool { return v.(*fakeView).active }); n != 0 {
		t.Fatalf("a session active on another replica must not be reaped: %d", n)
	}
	if bv.released != 0 || len(b.rows) != 1 {
		t.Error("neither the view nor the row may be removed")
	}
	// действительно простаивает везде: закрывается, строка удаляется
	bb.Sync = func(any, websession.Record) {}
	bv.active = false
	if n := bb.Reap(func(v any) bool { return v.(*fakeView).active }); n != 1 {
		t.Fatalf("a really idle session must be reaped: %d", n)
	}
	if bv.released != 1 || len(b.rows) != 0 {
		t.Errorf("released=%d rows=%d", bv.released, len(b.rows))
	}
}

func TestSessionActivityWriteThrottle(t *testing.T) {
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newMemBackend(func() time.Time { return clock })
	a, _ := replica(t, "a", b, &clock)
	_, sa, _ := a.Open("u1", nil, "")
	t0 := b.rows[sa.ID].rec.LastActive
	clock = clock.Add(5 * time.Second)
	a.Activity(sa.ID)
	if !b.rows[sa.ID].rec.LastActive.Equal(t0) {
		t.Error("activity must not be written more often than once per activityWriteEvery")
	}
	clock = clock.Add(activityWriteEvery)
	a.Activity(sa.ID)
	if !b.rows[sa.ID].rec.LastActive.Equal(clock) {
		t.Errorf("activity must be written after the interval: %v", b.rows[sa.ID].rec.LastActive)
	}
}

func TestSessionActiveClusterTravelsToAnotherReplica(t *testing.T) {
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newMemBackend(func() time.Time { return clock })
	a, _ := replica(t, "a", b, &clock)
	bb, _ := replica(t, "b", b, &clock)
	tok, sa, _ := a.Open("u1", &fakeView{}, "")
	a.SetActiveCluster(sa.ID, "prod")
	if _, err := bb.Lookup(tok); err != nil {
		t.Fatal(err)
	}
	if c := bb.TakePendingCluster(sa.ID); c != "prod" {
		t.Errorf("B must connect the active cluster on the first request: %q", c)
	}
	if c := bb.TakePendingCluster(sa.ID); c != "" {
		t.Errorf("the cluster is taken once: %q", c)
	}
	if c := a.TakePendingCluster(sa.ID); c != "" {
		t.Errorf("the replica that owns the view has nothing to restore: %q", c)
	}
}

func TestSessionsWithoutBackendUnchanged(t *testing.T) {
	ss := NewSessions(time.Hour, false)
	tok, s := ss.Create("u1", "view")
	got, err := ss.Lookup(tok)
	if err != nil || got.ID != s.ID || got.View != "view" {
		t.Fatalf("memory mode: %+v %v", got, err)
	}
	if nt, err := ss.Rotate(tok); err != nil || nt == tok {
		t.Fatalf("rotate in memory mode: %v", err)
	}
	if err := ss.Delete(tok); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.Lookup(tok); !errors.Is(err, websession.ErrNotFound) {
		t.Errorf("after Delete: %v", err)
	}
	ss.Revalidate() // без хранилища — пустая операция
}

func TestSessionPublishesRevocationsAndAppliesRemoteOnes(t *testing.T) {
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newMemBackend(func() time.Time { return clock })
	a, _ := replica(t, "a", b, &clock)
	bb, bViews := replica(t, "b", b, &clock)
	var sent []replicaevents.Message
	a.Publish = func(m replicaevents.Message) { sent = append(sent, m) }
	var ended []string
	bb.OnEnd = func(id string) { ended = append(ended, id) }

	t1, s1, _ := a.Open("u1", &fakeView{}, "")
	t2, s2, _ := a.Open("u1", &fakeView{}, "")
	t3, s3, _ := a.Open("u2", &fakeView{}, "")
	for _, tk := range []string{t1, t2, t3} {
		if _, err := bb.Lookup(tk); err != nil {
			t.Fatal(err)
		}
	}

	// выход на A → session.ended
	if err := a.Delete(t1); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].Kind != replicaevents.SessionEnded || sent[0].Session != s1.ID {
		t.Fatalf("logout must be published: %+v", sent)
	}
	// B применяет сообщение сразу, не дожидаясь кэша: вид освобождён, соединения сессии закрыты
	bb.EndRemote(s1.ID)
	if len(ended) != 1 || ended[0] != s1.ID || (*bViews)[0].released != 1 {
		t.Errorf("remote end: ended=%v released=%d", ended, (*bViews)[0].released)
	}
	bb.EndRemote(s1.ID) // повторное сообщение безвредно
	if len(ended) != 1 {
		t.Errorf("a repeated message must change nothing: %v", ended)
	}

	// смена пароля на A из сессии s2 → user.sessions_ended с исключением
	sent = nil
	if err := a.EndUser("u1", s2.ID); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].Kind != replicaevents.UserSessionsEnded || sent[0].User != "u1" || sent[0].Session != s2.ID {
		t.Fatalf("user sessions end must be published with the exception: %+v", sent)
	}
	bb.EndUserRemote("u1", s2.ID)
	for _, tk := range []string{t2, t3} {
		if _, err := bb.Lookup(tk); err != nil { // текущая сессия пользователя и чужая остались
			t.Errorf("session must survive: %v", err)
		}
	}
	if len(ended) != 1 { // s1 уже была закрыта, остальные не затронуты
		t.Errorf("unexpected ends: %v", ended)
	}
	bb.EndUserRemote("u2", "")
	if len(ended) != 2 || ended[1] != s3.ID {
		t.Errorf("all sessions of the user must end: %v", ended)
	}
	// отказ хранилища: событие не публикуется (сессия не отозвана)
	sent = nil
	b.down = true
	_ = a.Delete(t2)
	if len(sent) != 0 {
		t.Errorf("nothing must be published if the revocation failed: %+v", sent)
	}
}

func TestSessionReapPublishesWhenRemovingRows(t *testing.T) {
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newMemBackend(func() time.Time { return clock })
	a, _ := replica(t, "a", b, &clock)
	var sent []replicaevents.Message
	a.Publish = func(m replicaevents.Message) { sent = append(sent, m) }
	_, s, _ := a.Open("u1", &fakeView{active: false}, "")
	if n := a.Reap(func(v any) bool { return v.(*fakeView).active }); n != 1 {
		t.Fatalf("reaped %d", n)
	}
	if len(sent) != 1 || sent[0].Session != s.ID {
		t.Errorf("a reaped session must be announced: %+v", sent)
	}
}
