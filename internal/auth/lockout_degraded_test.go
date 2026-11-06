// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

var errStoreDown = errors.New("shared store is down")

// flakyLockDB — lockDB, у которого операции со счётчиками входа можно «ронять» по одной (роль недоступной
// PostgreSQL) и у которого считаются обращения.
type flakyLockDB struct {
	*lockDB
	failCheck, failRecord, failClear atomic.Bool
	calls                            atomic.Int64 // обращения к счётчикам входа (любой операцией)
}

func newFlakyLockDB() *flakyLockDB { return &flakyLockDB{lockDB: newLockDB()} }

// setDown включает или выключает отказ всех трёх операций («БД упала» и «БД вернулась»).
func (d *flakyLockDB) setDown(down bool) {
	d.failCheck.Store(down)
	d.failRecord.Store(down)
	d.failClear.Store(down)
}

func (d *flakyLockDB) LockRemaining(key string) (time.Duration, error) {
	d.calls.Add(1)
	if d.failCheck.Load() {
		return 0, errStoreDown
	}
	return d.lockDB.LockRemaining(key)
}

func (d *flakyLockDB) RecordFailure(key string, free int, base, maxLock time.Duration) error {
	d.calls.Add(1)
	if d.failRecord.Load() {
		return errStoreDown
	}
	return d.lockDB.RecordFailure(key, free, base, maxLock)
}

func (d *flakyLockDB) ClearFailures(key string) error {
	d.calls.Add(1)
	if d.failClear.Load() {
		return errStoreDown
	}
	return d.lockDB.ClearFailures(key)
}

var _ FailureBackend = (*flakyLockDB)(nil)

// twoFlakyReplicas — две реплики с общим хранилищем счётчиков, которое можно ронять.
func twoFlakyReplicas(t *testing.T) (a, b *Service, db *flakyLockDB) {
	t.Helper()
	db = newFlakyLockDB()
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

// failLogins делает n неудачных входов, каждый с нового адреса вида base.N: блокировка по адресу не вмешивается,
// и проверяется именно защита по логину.
func failLogins(t *testing.T, svc *Service, user string, n int, base string) {
	t.Helper()
	for i := 0; i < n; i++ {
		src := fmt.Sprintf("%s.%d", base, i+1)
		if _, err := svc.NewSession(src).Login(user, "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("failed login %d: %v", i+1, err)
		}
	}
}

// mustBeLocked проверяет, что вход admin с правильным паролем отклонён блокировкой (до проверки пароля).
func mustBeLocked(t *testing.T, svc *Service, src string) {
	t.Helper()
	_, err := svc.NewSession(src).Login("admin", pw)
	var locked *LockedError
	if !errors.As(err, &locked) || locked.RetryAfter <= 0 {
		t.Fatalf("the login must be locked out, got: %v", err)
	}
}

// Сбой общего хранилища не отключает защиту от подбора по логину: неудачи считаются в памяти реплики, и атака
// со сменой адресов упирается в блокировку по логину, как и при живом хранилище.
func TestStoreFailureKeepsLoginLockout(t *testing.T) {
	cases := map[string]func(*flakyLockDB){
		"whole store down":     func(d *flakyLockDB) { d.setDown(true) },
		"lock check fails":     func(d *flakyLockDB) { d.failCheck.Store(true) },
		"failure record fails": func(d *flakyLockDB) { d.failRecord.Store(true) },
	}
	for name, breakStore := range cases {
		t.Run(name, func(t *testing.T) {
			a, _, db := twoFlakyReplicas(t)
			breakStore(db)
			failLogins(t, a, "admin", freeAttempts, "10.50.0")
			mustBeLocked(t, a, "10.50.9.9")
		})
	}
}

// Блокировка, которую хранилище успело сообщить, не исчезает вместе с ним: она зеркалируется в память реплики.
func TestSharedLockoutSurvivesStoreOutage(t *testing.T) {
	a, b, db := twoFlakyReplicas(t)
	failLogins(t, a, "admin", freeAttempts-1, "10.51.0")
	failLogins(t, b, "admin", 1, "10.51.1") // последняя неудача на другой реплике: общая блокировка
	mustBeLocked(t, a, "10.51.9.1")         // реплика видит блокировку и запоминает её

	db.setDown(true)
	mustBeLocked(t, a, "10.51.9.2") // хранилище упало, блокировка должна остаться
}

// Пока хранилище отвечает, локального счётчика по логину нет: успешный вход на другой реплике сбрасывает общий
// счётчик, и «остатки» неудач на первой реплике не должны давать ложную блокировку.
func TestHealthyStoreKeepsNoLocalLoginCounter(t *testing.T) {
	a, b, _ := twoFlakyReplicas(t)
	failLogins(t, a, "admin", freeAttempts-2, "10.52.0")
	a.mu.Lock()
	local := a.failures["admin"]
	a.mu.Unlock()
	if local != nil {
		t.Fatalf("with a healthy shared store the login counter must not be kept in memory: %+v", local)
	}

	if _, err := b.NewSession("10.52.1.1").Login("admin", pw); err != nil { // сбрасывает общий счётчик
		t.Fatal(err)
	}
	failLogins(t, a, "admin", freeAttempts-1, "10.52.2") // с локальным счётчиком вышло бы 3+4 и блокировка
	if _, err := a.NewSession("10.52.3.1").Login("admin", pw); err != nil {
		t.Fatalf("false lockout after the shared counter was cleared: %v", err)
	}
}

// Отказ ClearFailures не мешает входу и оставляет счётчик (это строже, а не слабее защиты).
func TestClearFailuresErrorKeepsCounterAndLogin(t *testing.T) {
	a, _, db := twoFlakyReplicas(t)
	failLogins(t, a, "admin", freeAttempts-1, "10.53.0")

	db.failClear.Store(true)
	if _, err := a.NewSession("10.53.1.1").Login("admin", pw); err != nil {
		t.Fatalf("a failed counter clear must not break a valid login: %v", err)
	}
	db.fmu.Lock()
	n := db.counts["admin"]
	db.fmu.Unlock()
	if n != freeAttempts-1 {
		t.Fatalf("the counter must stay when it cannot be cleared, got %d", n)
	}
}

// Смена пароля (ключ «pw:») защищена так же, как вход.
func TestChangePasswordLockoutSurvivesStoreOutage(t *testing.T) {
	a, _, db := twoFlakyReplicas(t)
	v := a.NewSession("10.55.0.1")
	if _, err := v.Login("admin", pw); err != nil {
		t.Fatal(err)
	}

	db.setDown(true)
	const newPw = "battery-staple-2"
	for i := 0; i < freeAttempts; i++ {
		if err := v.ChangePassword("wrong-old-password", newPw); !errors.Is(err, ErrWrongPassword) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	var locked *LockedError
	if err := v.ChangePassword(pw, newPw); !errors.As(err, &locked) || locked.RetryAfter <= 0 {
		t.Fatalf("the password change must be locked out while the store is down: %v", err)
	}
}

func TestChangePasswordSharedLockoutIsMirrored(t *testing.T) {
	a, b, db := twoFlakyReplicas(t)
	va, vb := a.NewSession("10.57.0.1"), b.NewSession("10.57.1.1")
	for _, v := range []*Service{va, vb} {
		if _, err := v.Login("admin", pw); err != nil {
			t.Fatal(err)
		}
	}
	const newPw = "battery-staple-2"
	for i := 0; i < freeAttempts; i++ {
		v := va
		if i%2 == 1 {
			v = vb
		}
		_ = v.ChangePassword("wrong-old-password", newPw)
	}
	var locked *LockedError
	if err := va.ChangePassword(pw, newPw); !errors.As(err, &locked) { // видит общую блокировку и запоминает
		t.Fatalf("expected the shared lockout: %v", err)
	}
	db.setDown(true)
	if err := va.ChangePassword(pw, newPw); !errors.As(err, &locked) {
		t.Fatalf("the mirrored lockout must survive the outage: %v", err)
	}
}

// После восстановления хранилища общая блокировка снова действует на всех репликах.
func TestReplicasRestoreSharedLockoutAfterOutage(t *testing.T) {
	a, b, db := twoFlakyReplicas(t)
	clk := time.Now()
	clock := func() time.Time { return clk }
	a.now, b.now = clock, clock

	db.setDown(true)
	failLogins(t, a, "admin", freeAttempts, "10.56.0")
	failLogins(t, b, "admin", freeAttempts, "10.56.1")
	mustBeLocked(t, a, "10.56.9.1")
	mustBeLocked(t, b, "10.56.9.2")

	db.setDown(false)
	clk = clk.Add(maxLockout + time.Minute) // локальные блокировки и пауза после сбоя истекли

	failLogins(t, a, "admin", 3, "10.56.2")
	failLogins(t, b, "admin", freeAttempts-3, "10.56.3")
	mustBeLocked(t, a, "10.56.9.3") // неудачи двух реплик сложились в общем хранилище
	mustBeLocked(t, b, "10.56.9.4")
}
