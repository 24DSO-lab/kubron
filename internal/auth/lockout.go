// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"fmt"
	"log"
	"strings"
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

func (s *Service) lockRemainingLocked(key string) time.Duration {
	f := s.failures[key]
	if f == nil {
		return 0
	}
	if d := f.lockedUntil.Sub(s.now()); d > 0 {
		return d
	}
	return 0
}

// sharedLockRemaining — остаток блокировки логина в общем хранилище (0 — блокировки нет или хранилища нет).
// Если хранилище недоступно, блокировка не проверяется (ошибка в журнал): сессию без него всё равно не открыть.
// Вызывать без s.mu.
func (s *Service) sharedLockRemaining(key string) time.Duration {
	if s.fail == nil {
		return 0
	}
	d, err := s.fail.LockRemaining(key)
	if err != nil {
		log.Printf("auth: login lockout is not checked: %v", err)
		return 0
	}
	return d
}

// sharedRecordFailure учитывает неудачу в общем хранилище (без s.mu; нет хранилища — ничего не делает).
func (s *Service) sharedRecordFailure(key string) {
	if s.fail == nil {
		return
	}
	if err := s.fail.RecordFailure(key, freeAttempts, baseLockout, maxLockout); err != nil {
		log.Printf("auth: failed login is not counted: %v", err)
	}
}

// sharedClearFailures забывает счётчик в общем хранилище (без s.mu).
func (s *Service) sharedClearFailures(key string) {
	if s.fail == nil {
		return
	}
	if err := s.fail.ClearFailures(key); err != nil {
		log.Printf("auth: failure counter is not cleared: %v", err)
	}
}

// ipKey — ключ блокировки по адресу клиента ("" — адрес неизвестен, блокировки по адресу нет).
func (s *Service) ipKey() string {
	if s.source == "" {
		return ""
	}
	return "ip:" + NormalizeSource(s.source)
}

// recordSourceFailureLocked считает неудачу по адресу клиента: порог выше, чем по логину
// (за одним адресом может быть целый офис), а счётчик забывается через sourceWindow.
func (s *Service) recordSourceFailureLocked(key string) {
	if key == "" {
		return
	}
	if f := s.failures[key]; f != nil && s.now().Sub(f.last) > sourceWindow && f.lockedUntil.Before(s.now()) {
		delete(s.failures, key)
	}
	s.recordFailureLocked(key, sourceFreeAttempts)
}

func (s *Service) recordFailureLocked(key string, free int) {
	if s.failures[key] == nil && !s.makeRoomLocked(key) {
		return // все места заняты действующими блокировками: новый ключ не отслеживается, чужие блокировки целы
	}
	f := s.failures[key]
	if f == nil {
		f = &failure{}
		s.failures[key] = f
	}
	f.count++
	f.last = s.now()
	if f.count >= free {
		if !f.lockedUntil.After(s.now()) {
			s.lockoutsStarted.Add(1) // новый период блокировки (первый или повторный после истечения прежнего)
		}
		exp := f.count - free
		if exp > 6 {
			exp = 6
		}
		d := baseLockout << uint(exp)
		if d > maxLockout {
			d = maxLockout
		}
		f.lockedUntil = s.now().Add(d)
	}
}

// makeRoomLocked освобождает место под новый ключ, не трогая действующие блокировки. Лимит считается
// отдельно для адресов («ip:») и для логинов, поэтому подбор множества логинов не стирает записи об
// адресах и наоборот. Порядок вытеснения: сначала записи без блокировки, давно не обновлявшиеся
// (sourceWindow), затем самая старая из остальных незаблокированных. Если все записи этого вида —
// действующие блокировки, место не освобождается (false): новый ключ не отслеживается, зато ни одна
// блокировка не сбрасывается. Обход всей карты выполняется только при заполнении.
func (s *Service) makeRoomLocked(key string) bool {
	isIP := strings.HasPrefix(key, "ip:")
	now := s.now()
	n := 0
	var oldestKey string
	var oldest time.Time
	for k, f := range s.failures {
		if strings.HasPrefix(k, "ip:") != isIP {
			continue
		}
		n++
		if f.lockedUntil.After(now) {
			continue
		}
		if now.Sub(f.last) > sourceWindow {
			delete(s.failures, k) // истёкшая запись
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
	delete(s.failures, oldestKey)
	return true
}
