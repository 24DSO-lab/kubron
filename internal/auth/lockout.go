// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
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

const (
	// sharedRetryAfter — пауза в обращениях к общему хранилищу после сбоя (размыкатель): пока она идёт, вход
	// не ждёт таймаута БД на каждой попытке и работает на локальной защите.
	sharedRetryAfter = 5 * time.Second
	// sharedLogEvery — не чаще одной записи в журнал о недоступности общего хранилища за этот интервал.
	sharedLogEvery = 30 * time.Second
)

// lockouts — политика блокировок входа: счётчики неудач по логину и по адресу клиента, их лимиты и забывание, общее
// хранилище счётчиков для реплик и метрики.
//
// Компонент не имеет своего мьютекса: его состояние защищено shared.mu, и методы *Locked вызываются под ним —
// проверка блокировки и учёт неудачи в Login остаются одной атомарной секцией, как и прежде. Методы shared*
// ходят во внешнее хранилище и вызываются без shared.mu.
//
// Режим деградации. Блокировка по логину общая, пока отвечает хранилище fail. Если оно недоступно, защита от подбора
// по логину не отключается, а переходит на память процесса: неудачи этой реплики считаются локально (с лимитами
// того же размера), а блокировка, которую хранилище успело сообщить, зеркалируется в память и не пропадает вместе
// с ним. Без сбоев локальный счётчик по логину не ведётся — иначе вход на другой реплике не снимал бы его и
// возникали бы ложные блокировки. Цена деградации: до freeAttempts неудач на каждую реплику за окно вместо общих
// freeAttempts; после восстановления хранилища общая блокировка снова действует, накопленное локально остаётся
// только до истечения собственных блокировок.
type lockouts struct {
	failures map[string]*failure // счётчики по адресу, а по логину — без общего хранилища, в деградации и как зеркало блокировки
	fail     FailureBackend      // не nil: блокировки по логину общие для всех реплик
	clock    func() time.Time    // часы сервиса: тесты подменяют их в shared.now

	sharedDownUntil atomic.Int64 // UnixNano: до этого момента хранилище не опрашивается (после сбоя)
	sharedLoggedAt  atomic.Int64 // UnixNano последней записи в журнал о сбое хранилища

	// Счётчики для метрик (только числа, без имён пользователей и адресов).
	loginBlocked    atomic.Int64 // попытки входа, отклонённые из-за действующей блокировки
	lockoutsStarted atomic.Int64 // сколько раз серия неудач привела к блокировке
	sharedDegraded  atomic.Int64 // проверки и записи неудач, выполненные без общего хранилища (сбой или пауза после него)
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

// sharedReady — можно ли обращаться к общему хранилищу: оно есть и не в паузе после сбоя.
func (l *lockouts) sharedReady() bool {
	return l.fail != nil && l.clock().UnixNano() >= l.sharedDownUntil.Load()
}

// sharedFailed фиксирует сбой хранилища: открывает размыкатель, считает метрику и пишет в журнал не чаще
// sharedLogEvery (иначе при падении БД журнал заполнится по строке на каждую попытку входа).
func (l *lockouts) sharedFailed(op string, err error) {
	now := l.clock().UnixNano()
	l.sharedDegraded.Add(1)
	l.sharedDownUntil.Store(now + int64(sharedRetryAfter))
	if last := l.sharedLoggedAt.Load(); now-last >= int64(sharedLogEvery) && l.sharedLoggedAt.CompareAndSwap(last, now) {
		log.Printf("auth: shared login lockout store is unavailable, falling back to local lockouts (%s): %v", op, err)
	}
}

// sharedLockRemaining — остаток блокировки логина в общем хранилище. ok=false: проверить не удалось (сбой или
// пауза после него), и вызывающий обязан перейти на локальную защиту; нет хранилища вообще — ok=true, 0.
// Если проверку оборвала отмена ctx, это не сбой хранилища: размыкатель не открывается, а вызывающий видит
// ctx.Err() и прекращает операцию. Вызывать без s.mu.
func (l *lockouts) sharedLockRemaining(ctx context.Context, key string) (d time.Duration, ok bool) {
	if l.fail == nil {
		return 0, true
	}
	if !l.sharedReady() {
		l.sharedDegraded.Add(1)
		return 0, false
	}
	d, err := l.fail.LockRemaining(ctx, key)
	if err != nil {
		l.sharedError(ctx, "lock check", err)
		return 0, false
	}
	return d, true
}

// sharedRecordFailure учитывает неудачу в общем хранилище. false — записать не удалось (сбой или пауза после
// него): неудача должна быть учтена локально. Нет хранилища — true, ничего не делает. Вызывать без s.mu.
//
// Запись не зависит от отмены ctx: пароль уже проверен и оказался неверным, и клиент не должен уметь «потерять»
// неудачу, оборвав запрос до записи (иначе блокировку можно обойти). Значения контекста сохраняются, а предел
// по времени задаёт хранилище.
func (l *lockouts) sharedRecordFailure(ctx context.Context, key string) bool {
	if l.fail == nil {
		return true
	}
	if !l.sharedReady() {
		l.sharedDegraded.Add(1)
		return false
	}
	if err := l.fail.RecordFailure(context.WithoutCancel(ctx), key, freeAttempts, baseLockout, maxLockout); err != nil {
		l.sharedFailed("failure record", err)
		return false
	}
	return true
}

// sharedClearFailures забывает счётчик в общем хранилище (без s.mu). Не удалось — счётчик остаётся: это
// строже, но не слабее защиты. Во время паузы после сбоя хранилище не опрашивается. Как и запись неудачи,
// не прерывается отменой ctx: итог входа уже определён, и счётчик должен соответствовать ему.
func (l *lockouts) sharedClearFailures(ctx context.Context, key string) {
	if !l.sharedReady() {
		return
	}
	if err := l.fail.ClearFailures(context.WithoutCancel(ctx), key); err != nil {
		l.sharedFailed("counter clear", err)
	}
}

// sharedError разбирает ошибку чтения из общего хранилища: отмена или срок вызывающего ctx — не сбой хранилища,
// и размыкатель не трогается (иначе клиент, обрывающий запросы, отключал бы общую защиту всей реплики).
func (l *lockouts) sharedError(ctx context.Context, op string, err error) {
	if ctx.Err() != nil {
		return
	}
	l.sharedFailed(op, err)
}

// mirrorSharedLockLocked запоминает в памяти блокировку, которую сообщило общее хранилище (d > 0): если оно
// выйдет из строя, эта блокировка не должна исчезнуть. Блокировка не укорачивается, а счётчик поднимается до
// порога, как в хранилище, где после истечения блокировки счёт остаётся и следующая неудача блокирует снова.
// Если места нет (все записи — действующие блокировки), ничего не запоминается: текущая попытка всё равно
// отклоняется по ответу хранилища.
func (l *lockouts) mirrorSharedLockLocked(key string, d time.Duration) {
	if d <= 0 {
		return
	}
	if l.failures[key] == nil && !l.makeRoomLocked(key) {
		return
	}
	f := l.failures[key]
	if f == nil {
		f = &failure{}
		l.failures[key] = f
	}
	now := l.clock()
	f.count = max(f.count, freeAttempts)
	f.last = now
	if until := now.Add(d); until.After(f.lockedUntil) {
		f.lockedUntil = until
	}
}

// recordKeyFailureLocked учитывает неудачу по логину (ключу «pw:…» для смены пароля) в памяти процесса, когда
// общей защиты нет: хранилища нет вовсе либо оно не ответило (degraded). Пока хранилище отвечает, его счётчика
// достаточно, а локальный не ведётся (см. описание lockouts).
func (l *lockouts) recordKeyFailureLocked(key string, degraded bool) {
	if l.fail != nil {
		if !degraded {
			return
		}
		l.forgetStaleLocked(key) // давняя серия неудач до сбоя не должна ускорять блокировку
	}
	l.recordFailureLocked(key, freeAttempts)
}

// forgetStaleLocked забывает счётчик ключа, если последняя неудача старше sourceWindow и блокировки уже нет.
func (l *lockouts) forgetStaleLocked(key string) {
	if f := l.failures[key]; f != nil && l.clock().Sub(f.last) > sourceWindow && f.lockedUntil.Before(l.clock()) {
		delete(l.failures, key)
	}
}

// recordSourceFailureLocked считает неудачу по адресу клиента: порог выше, чем по логину
// (за одним адресом может быть целый офис), а счётчик забывается через sourceWindow.
func (l *lockouts) recordSourceFailureLocked(key string) {
	if key == "" {
		return
	}
	l.forgetStaleLocked(key)
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
