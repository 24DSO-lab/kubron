// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// lockDB — sharedDB со счётчиками входа, общими для «реплик» (роль таблицы login_failures в PostgreSQL).
type lockDB struct {
	*sharedDB
	fmu    sync.Mutex
	counts map[string]int
	until  map[string]time.Time
}

func newLockDB() *lockDB {
	return &lockDB{sharedDB: &sharedDB{}, counts: map[string]int{}, until: map[string]time.Time{}}
}

func (d *lockDB) LockRemaining(key string) (time.Duration, error) {
	d.fmu.Lock()
	defer d.fmu.Unlock()
	return max(time.Until(d.until[key]), 0), nil
}

func (d *lockDB) RecordFailure(key string, free int, base, maxLock time.Duration) error {
	d.fmu.Lock()
	defer d.fmu.Unlock()
	d.counts[key]++
	if n := d.counts[key]; n >= free {
		d.until[key] = time.Now().Add(min(base<<uint(min(n-free, 6)), maxLock))
	}
	return nil
}

func (d *lockDB) ClearFailures(key string) error {
	d.fmu.Lock()
	defer d.fmu.Unlock()
	delete(d.counts, key)
	delete(d.until, key)
	return nil
}

var _ FailureBackend = (*lockDB)(nil)

func twoReplicasWithLocks(t *testing.T) (a, b *Service, db *lockDB) {
	t.Helper()
	db = newLockDB()
	a, err := NewServiceWithBackend(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Setup("admin", pw); err != nil {
		t.Fatal(err)
	}
	if b, err = NewServiceWithBackend(db); err != nil {
		t.Fatal(err)
	}
	return a, b, db
}

// Неудачи на разных репликах (и с разных адресов) складываются: блокировка по логину общая.
func TestSharedLoginLockoutAcrossReplicas(t *testing.T) {
	a, b, _ := twoReplicasWithLocks(t)
	for i := 0; i < freeAttempts; i++ {
		replica := a
		if i%2 == 1 {
			replica = b
		}
		src := "10.0.0." + string(rune('1'+i)) // у каждой попытки свой адрес: блокировка по адресу не сработает
		if _, err := replica.NewSession(src).Login("admin", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	for name, replica := range map[string]*Service{"A": a, "B": b} {
		_, err := replica.NewSession("10.9.9.9").Login("admin", pw) // правильный пароль до проверки пароля не доходит
		var locked *LockedError
		if !errors.As(err, &locked) || locked.RetryAfter <= 0 {
			t.Errorf("replica %s must refuse the login while the shared lockout is active: %v", name, err)
		}
	}
}

// Несуществующий логин блокируется так же, как существующий: ответ не выдаёт, есть ли такой пользователь.
func TestSharedLoginLockoutForUnknownUser(t *testing.T) {
	a, b, _ := twoReplicasWithLocks(t)
	for i := 0; i < freeAttempts; i++ {
		replica := a
		if i%2 == 1 {
			replica = b
		}
		_, _ = replica.NewSession("10.1.0."+string(rune('1'+i))).Login("ghost", "whatever-1")
	}
	_, err := b.NewSession("10.2.0.1").Login("ghost", "whatever-1")
	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("an unknown login must be locked out like a real one: %v", err)
	}
}

// Успешный вход сбрасывает общий счётчик.
func TestSharedLoginSuccessClearsFailures(t *testing.T) {
	a, b, db := twoReplicasWithLocks(t)
	for i := 0; i < freeAttempts-1; i++ {
		_, _ = a.NewSession("10.3.0."+string(rune('1'+i))).Login("admin", "wrong-password")
	}
	if _, err := b.NewSession("10.3.1.1").Login("admin", pw); err != nil {
		t.Fatal(err)
	}
	db.fmu.Lock()
	n := db.counts["admin"]
	db.fmu.Unlock()
	if n != 0 {
		t.Fatalf("the shared counter must be cleared after a successful login, got %d", n)
	}
}

// Без общего хранилища счётчиков (файл, десктоп) блокировка прежняя, в памяти процесса.
func TestLoginLockoutInMemoryWithoutSharedCounters(t *testing.T) {
	a, b, _ := twoReplicas(t) // sharedDB не реализует FailureBackend
	for i := 0; i < freeAttempts; i++ {
		_, _ = a.NewSession("10.4.0."+string(rune('1'+i))).Login("admin", "wrong-password")
	}
	var locked *LockedError
	if _, err := a.NewSession("10.4.9.9").Login("admin", pw); !errors.As(err, &locked) {
		t.Fatalf("the local lockout must still work: %v", err)
	}
	if _, err := b.NewSession("10.4.9.8").Login("admin", pw); err != nil {
		t.Fatalf("without shared counters another replica is not affected: %v", err)
	}
}
