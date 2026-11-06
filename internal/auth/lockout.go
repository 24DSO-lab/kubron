// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"
)

// LockedError — вход временно заблокирован после серии неудачных попыток.
type LockedError struct{ RetryAfter time.Duration }

func (e *LockedError) Error() string {
	secs := int((e.RetryAfter + time.Second - 1) / time.Second)
	return fmt.Sprintf("too many failed login attempts, try again in %d s", secs)
}

type failure struct {
	count       int
	lockedUntil time.Time
	last        time.Time // последняя неудача (для забывания счётчика по адресу)
}

// lockouts — политика блокировок входа: счётчики неудач по логину и по адресу клиента, их лимиты и забывание, общее
// хранилище счётчиков для реплик и метрики.
//
// Компонент не имеет своего мьютекса: его состояние защищено shared.mu, и методы *Locked вызываются под ним —
// проверка блокировки и учёт неудачи в Login остаются одной атомарной секцией, как и прежде. Методы shared*
// ходят во внешнее хранилище и вызываются без shared.mu.
type lockouts struct {
	failures map[string]*failure // счётчики по адресу (и по логину, если нет общего хранилища fail)
	fail     FailureBackend      // не nil: блокировки по логину общие для всех реплик
	clock    func() time.Time    // часы сервиса: тесты подменяют их в shared.now

	// Счётчики для метрик (только числа, без имён пользователей и адресов).
	loginBlocked    atomic.Int64 // попытки входа, отклонённые из-за действующей блокировки
	lockoutsStarted atomic.Int64 // сколько раз серия неудач привела к блокировке
}

// initLockouts задаёт часы и общее хранилище; вызывается один раз при создании shared (структуру с atomic-счётчиками не копируют).
func (l *lockouts) initLockouts(clock func() time.Time, fail FailureBackend) {
	l.failures, l.fail, l.clock = map[string]*failure{}, fail, clock
}

func (l *lockouts) lockRemainingLocked(key string) time.Duration {
	f := l.failures[key]
	if f == nil {
		return 0
	}
	if d := f.lockedUntil.Sub(l.clock()); d > 0 {
		return d
	}
	return 0
}

// sharedLockRemaining — остаток блокировки логина в общем хранилище (0 — блокировки нет или хранилища нет).
// Если хранилище недоступно, блокировка не проверяется (ошибка в журнал): сессию без него всё равно не открыть.
// Вызывать без s.mu.
func (l *lockouts) sharedLockRemaining(key string) time.Duration {
	if l.fail == nil {
		return 0
	}
	d, err := l.fail.LockRemaining(key)
	if err != nil {
		log.Printf("auth: login lockout is not checked: %v", err)
		return 0
	}
	return d
}

// sharedRecordFailure учитывает неудачу в общем хранилище (без s.mu; нет хранилища — ничего не делает).
func (l *lockouts) sharedRecordFailure(key string) {
	if l.fail == nil {
		return
	}
	if err := l.fail.RecordFailure(key, freeAttempts, baseLockout, maxLockout); err != nil {
		log.Printf("auth: failed login is not counted: %v", err)
	}
}

// sharedClearFailures забывает счётчик в общем хранилище (без s.mu).
func (l *lockouts) sharedClearFailures(key string) {
	if l.fail == nil {
		return
	}
	if err := l.fail.ClearFailures(key); err != nil {
		log.Printf("auth: failure counter is not cleared: %v", err)
	}
}

// recordSourceFailureLocked считает неудачу по адресу клиента: порог выше, чем по логину
// (за одним адресом может быть целый офис), а счётчик забывается через sourceWindow.
func (l *lockouts) recordSourceFailureLocked(key string) {
	if key == "" {
		return
	}
	if f := l.failures[key]; f != nil && l.clock().Sub(f.last) > sourceWindow && f.lockedUntil.Before(l.clock()) {
		delete(l.failures, key)
	}
	l.recordFailureLocked(key, sourceFreeAttempts)
}

func (l *lockouts) recordFailureLocked(key string, free int) {
	if l.failures[key] == nil && !l.makeRoomLocked(key) {
		return // все места заняты действующими блокировками: новый ключ не отслеживается, чужие блокировки целы
	}
	f := l.failures[key]
	if f == nil {
		f = &failure{}
		l.failures[key] = f
	}
	f.count++
	f.last = l.clock()
	if f.count >= free {
		if !f.lockedUntil.After(l.clock()) {
			l.lockoutsStarted.Add(1) // новый период блокировки (первый или повторный после истечения прежнего)
		}
		exp := f.count - free
		if exp > 6 {
			exp = 6
		}
		d := baseLockout << uint(exp)
		if d > maxLockout {
			d = maxLockout
		}
		f.lockedUntil = l.clock().Add(d)
	}
}

// makeRoomLocked освобождает место под новый ключ, не трогая действующие блокировки. Лимит считается
// отдельно для адресов («ip:») и для логинов, поэтому подбор множества логинов не стирает записи об
// адресах и наоборот. Порядок вытеснения: сначала записи без блокировки, давно не обновлявшиеся
// (sourceWindow), затем самая старая из остальных незаблокированных. Если все записи этого вида —
// действующие блокировки, место не освобождается (false): новый ключ не отслеживается, зато ни одна
// блокировка не сбрасывается. Обход всей карты выполняется только при заполнении.
func (l *lockouts) makeRoomLocked(key string) bool {
	isIP := strings.HasPrefix(key, "ip:")
	now := l.clock()
	n := 0
	var oldestKey string
	var oldest time.Time
	for k, f := range l.failures {
		if strings.HasPrefix(k, "ip:") != isIP {
			continue
		}
		n++
		if f.lockedUntil.After(now) {
			continue
		}
		if now.Sub(f.last) > sourceWindow {
			delete(l.failures, k) // истёкшая запись
			n--
			continue
		}
		if oldestKey == "" || f.last.Before(oldest) {
			oldestKey, oldest = k, f.last
		}
	}
	if n < maxTracked {
		return true
	}
	if oldestKey == "" {
		return false
	}
	delete(l.failures, oldestKey)
	return true
}

// ipKey — ключ блокировки по адресу клиента ("" — адрес неизвестен, блокировки по адресу нет).
func (s *Service) ipKey() string {
	if s.source == "" {
		return ""
	}
	return "ip:" + NormalizeSource(s.source)
}
