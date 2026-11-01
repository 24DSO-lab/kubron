// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/audit"
)

type memRecorder struct {
	mu sync.Mutex
	ev []audit.Event
}

func (m *memRecorder) Record(e audit.Event) {
	m.mu.Lock()
	m.ev = append(m.ev, e)
	m.mu.Unlock()
}

func (m *memRecorder) count(action string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, e := range m.ev {
		if e.Action == action {
			n++
		}
	}
	return n
}

// idleFixture: администратор вошёл в момент t0 по управляемым часам.
type idleFixture struct {
	s     *Service
	dir   string
	clock *time.Time
	rec   *memRecorder
	hook  chan struct{}
	t0    time.Time
}

func newIdleFixture(t *testing.T) *idleFixture {
	t.Helper()
	s, dir := newTestService(t)
	t0 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	clock := t0
	s.now = func() time.Time { return clock }
	f := &idleFixture{s: s, dir: dir, clock: &clock, rec: &memRecorder{}, hook: make(chan struct{}, 4), t0: t0}
	s.SetAuditor(f.rec)
	s.SetOnLogout(func() { f.hook <- struct{}{} })
	if _, err := s.Setup("admin", goodPass); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *idleFixture) at(d time.Duration) { *f.clock = f.t0.Add(d) }

func (f *idleFixture) hookFired(t *testing.T) bool {
	t.Helper()
	select {
	case <-f.hook:
		return true
	case <-time.After(2 * time.Second):
		return false
	}
}

func TestIdleDefaultIsFifteenMinutes(t *testing.T) {
	f := newIdleFixture(t)
	if got := f.s.Status().IdleTimeoutMinutes; got != DefaultIdleTimeoutMinutes {
		t.Fatalf("default is %d min, expected %d", got, DefaultIdleTimeoutMinutes)
	}
}

func TestSessionExpiresAfterIdleTimeoutPlusGrace(t *testing.T) {
	f := newIdleFixture(t)

	// В пределах настроенного времени и запаса — сессия жива.
	f.at(15*time.Minute + idleGrace)
	if err := f.s.Authorize(PermRead); err != nil {
		t.Fatalf("within the margin: %v", err)
	}
	// Дольше — сессия завершена, очистка запущена, событие записано.
	f.at(15*time.Minute + idleGrace + time.Second)
	if err := f.s.Authorize(PermRead); !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("after inactivity: %v", err)
	}
	if !f.hookFired(t) {
		t.Error("cleanup (terminals, port-forward) must run on auto-logout")
	}
	if _, err := f.s.Current(); !errors.Is(err, ErrNotAuthenticated) {
		t.Errorf("Current after auto-logout: %v", err)
	}
	if n := f.rec.count("SessionTimeout"); n != 1 {
		t.Errorf("SessionTimeout events: %d, expected 1", n)
	}
}

func TestAPICallsDoNotExtendSession(t *testing.T) {
	f := newIdleFixture(t)
	// Интерфейс опрашивает кластер, но пользователь ничего не делает.
	for _, m := range []time.Duration{5, 10, 14} {
		f.at(m * time.Minute)
		if err := f.s.Authorize(PermRead); err != nil {
			t.Fatalf("at minute %d: %v", m, err)
		}
	}
	f.at(15*time.Minute + idleGrace + time.Second)
	if err := f.s.Authorize(PermRead); !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("API polling must not extend the session: %v", err)
	}
}

func TestReportActivityExtendsSession(t *testing.T) {
	f := newIdleFixture(t)
	f.at(10 * time.Minute)
	if err := f.s.ReportActivity(); err != nil {
		t.Fatal(err)
	}
	f.at(20 * time.Minute) // 10 минут после активности
	if err := f.s.Authorize(PermRead); err != nil {
		t.Fatalf("the session must stay alive after activity: %v", err)
	}
	f.at(10*time.Minute + 15*time.Minute + idleGrace + time.Second)
	if err := f.s.Authorize(PermRead); !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("the session must expire after a new pause: %v", err)
	}
}

func TestReportActivityAfterExpiryFails(t *testing.T) {
	f := newIdleFixture(t)
	f.at(time.Hour)
	if err := f.s.ReportActivity(); !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("an expired session cannot be revived by activity: %v", err)
	}
	// И без входа тоже нельзя.
	s2, _ := newTestService(t)
	if err := s2.ReportActivity(); !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("without a session: %v", err)
	}
}

func TestExpireIdleWatcher(t *testing.T) {
	f := newIdleFixture(t)
	if f.s.ExpireIdle() {
		t.Error("a fresh session must not expire")
	}
	f.at(time.Hour)
	if !f.s.ExpireIdle() {
		t.Fatal("an idle session must be ended by the background check")
	}
	if !f.hookFired(t) {
		t.Error("cleanup was not run")
	}
	if f.s.ExpireIdle() {
		t.Error("a repeated check without a session does nothing")
	}
	if f.rec.count("SessionTimeout") != 1 {
		t.Error("the event must be recorded exactly once")
	}
}

func TestIdleTimeoutCanBeDisabled(t *testing.T) {
	f := newIdleFixture(t)
	got, err := f.s.SetIdleTimeout(0)
	if err != nil || got != 0 {
		t.Fatalf("SetIdleTimeout(0): %d, %v", got, err)
	}
	f.at(72 * time.Hour)
	if err := f.s.Authorize(PermRead); err != nil {
		t.Fatalf("with auto-logout disabled the session does not expire: %v", err)
	}
	if f.s.Status().IdleTimeoutMinutes != 0 {
		t.Error("Status must show 0")
	}
}

func TestSetIdleTimeoutValidationPermissionAndPersistence(t *testing.T) {
	f := newIdleFixture(t)
	for _, bad := range []int{-1, MaxIdleTimeoutMinutes + 1} {
		if _, err := f.s.SetIdleTimeout(bad); !errors.Is(err, ErrInvalidIdleTimeout) {
			t.Errorf("SetIdleTimeout(%d): %v", bad, err)
		}
	}
	if got, err := f.s.SetIdleTimeout(5); err != nil || got != 5 {
		t.Fatalf("SetIdleTimeout(5): %d, %v", got, err)
	}
	if f.rec.count("SetIdleTimeout") != 1 {
		t.Error("a setting change must be written to the journal")
	}

	// Значение переживает перезапуск, явный 0 тоже (не превращается в значение по умолчанию).
	reload := func() int {
		s2, err := NewService(f.dir)
		if err != nil {
			t.Fatal(err)
		}
		return s2.Status().IdleTimeoutMinutes
	}
	if got := reload(); got != 5 {
		t.Errorf("after restart %d, expected 5", got)
	}
	if _, err := f.s.SetIdleTimeout(0); err != nil {
		t.Fatal(err)
	}
	if got := reload(); got != 0 {
		t.Errorf("explicit 0 became %d after restart", got)
	}

	// Обычный пользователь настройку менять не может.
	if _, err := f.s.CreateUser("bob", "temp-pass-1", RoleUser); err != nil {
		t.Fatal(err)
	}
	f.s.Logout()
	if _, err := f.s.Login("bob", "temp-pass-1"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ChangePassword("temp-pass-1", "bobs-own-pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SetIdleTimeout(1); !errors.Is(err, ErrForbidden) {
		t.Errorf("user must not change auto-logout: %v", err)
	}
}

func TestLoginStartsFreshIdleWindow(t *testing.T) {
	f := newIdleFixture(t)
	f.s.Logout()
	f.at(5 * time.Hour)
	if _, err := f.s.Login("admin", goodPass); err != nil {
		t.Fatal(err)
	}
	f.at(5*time.Hour + 10*time.Minute)
	if err := f.s.Authorize(PermRead); err != nil {
		t.Fatalf("a new session counts from the login time: %v", err)
	}
}
