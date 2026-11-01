// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/audit"
)

func TestNormalizeSource(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.9":                      "203.0.113.9",
		"2001:db8:1:2:aaaa:bbbb:cccc:dddd": "2001:db8:1:2::/64",
		"2001:db8:1:2::1":                  "2001:db8:1:2::/64",
		"2001:db8:1:3::1":                  "2001:db8:1:3::/64", // соседняя /64 — другой ключ
		"::ffff:198.51.100.7":              "198.51.100.7",      // IPv4-mapped считается IPv4
		"fe80::1%eth0":                     "fe80::/64",         // зона отбрасывается
		"not-an-ip":                        "not-an-ip",
		"":                                 "",
	} {
		if got := NormalizeSource(in); got != want {
			t.Errorf("NormalizeSource(%q) = %q, want %q", in, got, want)
		}
	}
}

// count — сколько записей о неудачах хранится: по адресам и по остальным ключам (логины).
func count(s *Service) (ips, users int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.failures {
		if strings.HasPrefix(k, "ip:") {
			ips++
		} else {
			users++
		}
	}
	return
}

func lockedFor(err error) bool {
	var l *LockedError
	return errors.As(err, &l)
}

func fixedClock(s *Service) *time.Time {
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }
	return &clock
}

// Вся сеть /64 — один источник: перебор с разных адресов одного префикса упирается в общий лимит.
func TestIPv6PrefixSharesTheSourceLimit(t *testing.T) {
	s, _ := setupWithUser(t)
	fixedClock(s)
	for i := 0; i < sourceFreeAttempts; i++ {
		v := s.NewSession(fmt.Sprintf("2001:db8:aa:bb::%x", i+1))
		if _, err := v.Login(fmt.Sprintf("nobody%d", i), "x"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	// новый адрес той же /64 с верным паролем всё равно заблокирован
	if _, err := s.NewSession("2001:db8:aa:bb:ffff::1").Login("bob", "bobs-own-pass"); !lockedFor(err) {
		t.Fatalf("the same /64 must be locked, got %v", err)
	}
	// другая /64 не затронута
	if _, err := s.NewSession("2001:db8:aa:cc::1").Login("bob", "bobs-own-pass"); err != nil {
		t.Fatalf("another /64 must not be locked: %v", err)
	}
}

// Регрессия: при переполнении карта обнулялась целиком, и подбор множества уникальных логинов/адресов
// стирал действующие блокировки. Теперь вытесняются только незаблокированные записи.
func TestFloodOfUniqueKeysDoesNotEraseActiveLocks(t *testing.T) {
	s, _ := setupWithUser(t)
	fixedClock(s)

	// Блокировка по логину: bob с адреса A
	a := s.NewSession("198.51.100.1")
	for i := 0; i < freeAttempts; i++ {
		_, _ = a.Login("bob", "wrong")
	}
	// Блокировка по адресу B (разные логины, чтобы не блокировать конкретного пользователя)
	b := s.NewSession("198.51.100.2")
	for i := 0; i < sourceFreeAttempts; i++ {
		_, _ = b.Login(fmt.Sprintf("probe%d", i), "wrong")
	}
	if _, err := s.NewSession("192.0.2.50").Login("bob", goodPass); !lockedFor(err) {
		t.Fatalf("bob must be locked before the flood: %v", err)
	}
	if _, err := b.Login("admin", goodPass); !lockedFor(err) {
		t.Fatalf("source B must be locked before the flood: %v", err)
	}

	// Затопление: втрое больше уникальных логинов и адресов, чем помещается в карту
	for i := 0; i < 3*maxTracked; i++ {
		src := fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255)
		_, _ = s.NewSession(src).Login(fmt.Sprintf("flood%d", i), "x")
	}

	if _, err := s.NewSession("192.0.2.51").Login("bob", goodPass); !lockedFor(err) {
		t.Errorf("the lock on bob was erased by the flood: %v", err)
	}
	if _, err := b.Login("admin", goodPass); !lockedFor(err) {
		t.Errorf("the lock on source B was erased by the flood: %v", err)
	}
	ips, users := count(s)
	if ips > maxTracked || users > maxTracked {
		t.Errorf("limits are per key type: %d addresses, %d logins, max %d", ips, users, maxTracked)
	}
}

// Когда все места заняты действующими блокировками, новые ключи не отслеживаются, а блокировки целы.
func TestFullOfActiveLocksKeepsThemAll(t *testing.T) {
	s, _ := setupWithUser(t)
	clock := fixedClock(s)
	s.mu.Lock()
	for i := 0; i < maxTracked; i++ {
		s.failures[fmt.Sprintf("victim%d", i)] = &failure{count: freeAttempts, lockedUntil: clock.Add(time.Minute), last: *clock}
	}
	s.mu.Unlock()

	_, _ = s.NewSession("192.0.2.9").Login("newcomer", "x")

	ips, users := count(s)
	if users != maxTracked {
		t.Errorf("logins tracked: %d, want exactly %d (no eviction of locks, no growth)", users, maxTracked)
	}
	if ips != 1 {
		t.Errorf("the address bucket is separate and must still track the new source: %d", ips)
	}
	for i := 0; i < maxTracked; i++ {
		if s.lockRemainingLocked(fmt.Sprintf("victim%d", i)) <= 0 {
			t.Fatalf("lock victim%d was dropped", i)
		}
	}
}

// Истёкшие и устаревшие записи вытесняются первыми, затем самые старые незаблокированные.
func TestEvictionOrder(t *testing.T) {
	s, _ := setupWithUser(t)
	clock := fixedClock(s)
	now := *clock
	s.mu.Lock()
	for i := 0; i < maxTracked-2; i++ {
		s.failures[fmt.Sprintf("fresh%d", i)] = &failure{count: 1, last: now}
	}
	s.failures["old"] = &failure{count: 1, last: now.Add(-time.Minute)}        // старейшая из свежих
	s.failures["stale"] = &failure{count: 2, last: now.Add(-2 * sourceWindow)} // устаревшая
	s.mu.Unlock()

	s.mu.Lock()
	s.recordFailureLocked("brandnew", freeAttempts)
	_, hasStale := s.failures["stale"]
	_, hasOld := s.failures["old"]
	s.mu.Unlock()
	if hasStale {
		t.Error("the stale record must be evicted first")
	}
	if !hasOld {
		t.Error("the oldest record must stay while there is room after dropping stale ones")
	}

	// теперь карта снова полна: вытесняется самая старая незаблокированная
	s.mu.Lock()
	s.recordFailureLocked("another", freeAttempts)
	_, hasOld = s.failures["old"]
	s.mu.Unlock()
	if hasOld {
		t.Error("the oldest unlocked record must be evicted when the map is full")
	}
}

// Подбор старого пароля через ChangePassword (украденная сессия) ограничен так же, как вход,
// а блокировка попадает в журнал вместе с адресом.
func TestChangePasswordGuessingIsLimitedAndAudited(t *testing.T) {
	root, _ := newTestService(t)
	rec := &memRecorder{}
	root.SetAuditor(rec)
	fixedClock(root)
	v := root.NewSession("192.0.2.7")
	if _, err := v.Setup("admin", goodPass); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < freeAttempts; i++ {
		if err := v.ChangePassword("wrong-old", "brand-new-pass-1"); !errors.Is(err, ErrWrongPassword) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	// даже верный старый пароль во время блокировки не принимается
	if err := v.ChangePassword(goodPass, "brand-new-pass-1"); !lockedFor(err) {
		t.Fatalf("expected a lockout, got %v", err)
	}
	var denied *audit.Event
	rec.mu.Lock()
	for i := range rec.ev {
		if rec.ev[i].Action == "ChangePassword" && rec.ev[i].Result == audit.ResultDenied {
			denied = &rec.ev[i]
		}
	}
	rec.mu.Unlock()
	if denied == nil {
		t.Fatal("the lockout is not in the audit log")
	}
	if denied.Source != "192.0.2.7" || denied.User != "admin" {
		t.Errorf("audit event: source=%q user=%q", denied.Source, denied.User)
	}
}

// Блокировка входа тоже записывается с адресом клиента.
func TestLoginLockoutAuditHasSource(t *testing.T) {
	root, _ := newTestService(t)
	rec := &memRecorder{}
	root.SetAuditor(rec)
	fixedClock(root)
	if _, err := root.NewSession("192.0.2.1").Setup("admin", goodPass); err != nil {
		t.Fatal(err)
	}
	v := root.NewSession("203.0.113.77")
	for i := 0; i < freeAttempts; i++ {
		_, _ = v.Login("admin", "wrong")
	}
	if _, err := v.Login("admin", goodPass); !lockedFor(err) {
		t.Fatalf("expected a lockout, got %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, e := range rec.ev {
		if e.Action == "Login" && e.Result == audit.ResultDenied {
			if e.Source != "203.0.113.77" {
				t.Errorf("source of the lockout event: %q", e.Source)
			}
			return
		}
	}
	t.Fatal("no lockout event in the audit log")
}
