// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package clusterhealth — фоновая проверка соединения с кластерами (задача 3.6).
//
// Пакет не знает про Kubernetes: что значит «проверить кластер», подсказывает Prober, а где хранить и
// кто проверяет — Sink и Leader. Так логика (параллельность, таймауты, смена лидера, уведомление об
// изменениях) проверяется тестами без кластера и без БД.
package clusterhealth

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// State — итог проверки.
type State string

const (
	// Unknown — кластер ещё не проверяли или проверка невозможна (например, учётные данные берутся командой).
	Unknown State = "unknown"
	// OK — кластер отвечает, учётные данные приняты.
	OK State = "ok"
	// Unreachable — кластер не отвечает: сеть, адрес, сертификат, таймаут.
	Unreachable State = "unreachable"
	// Unauthorized — кластер ответил, но не принял учётные данные (истёкший токен, отозванный сертификат).
	Unauthorized State = "unauthorized"
	// Misconfigured — kubeconfig не удалось разобрать или не хватает данных для подключения.
	Misconfigured State = "misconfigured"
)

// Health — результат последней проверки.
type Health struct {
	State     State     `json:"state"`
	Message   string    `json:"message,omitempty"` // причина на английском, как в ошибке клиента; без секретов
	LatencyMs int64     `json:"latencyMs,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}

// Healthy — кластером можно пользоваться.
func (h Health) Healthy() bool { return h.State == OK }

// same — проверка не изменила того, что видит пользователь.
func (h Health) same(o Health) bool { return h.State == o.State && h.Message == o.Message }

// MaxMessage — предел длины причины: ошибки клиента бывают многострочными и длинными.
const MaxMessage = 200

// Trim оставляет первую строку сообщения и укорачивает её до MaxMessage символов.
func Trim(msg string) string {
	msg = strings.TrimSpace(msg)
	if i := strings.IndexAny(msg, "\r\n"); i >= 0 {
		msg = strings.TrimSpace(msg[:i])
	}
	if utf8.RuneCountInString(msg) <= MaxMessage {
		return msg
	}
	r := []rune(msg)
	return string(r[:MaxMessage-1]) + "…"
}

// Board — текущие состояния кластеров, общие для всех сессий процесса.
type Board struct {
	mu sync.RWMutex
	m  map[string]Health
}

// NewBoard создаёт пустую доску.
func NewBoard() *Board { return &Board{m: map[string]Health{}} }

// Get возвращает состояние кластера; false — ещё не проверялся.
func (b *Board) Get(id string) (Health, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	h, ok := b.m[id]
	return h, ok
}

// Counts возвращает число кластеров по состояниям (для метрик: без имён и идентификаторов кластеров).
func (b *Board) Counts() map[State]int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(map[State]int, len(b.m))
	for _, h := range b.m {
		out[h.State]++
	}
	return out
}

// Set записывает результат; возвращает true, если состояние или причина изменились.
func (b *Board) Set(id string, h Health) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	old, ok := b.m[id]
	b.m[id] = h
	return !ok || !old.same(h)
}

// Replace заменяет содержимое целиком (реплика-наблюдатель читает результаты из общего хранилища).
func (b *Board) Replace(m map[string]Health) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	changed := len(m) != len(b.m)
	for id, h := range m {
		if old, ok := b.m[id]; !ok || !old.same(h) {
			changed = true
		}
	}
	b.m = make(map[string]Health, len(m))
	for id, h := range m {
		b.m[id] = h
	}
	return changed
}

// Retain забывает кластеры, которых нет в ids (удалены); возвращает true, если что-то забыто.
func (b *Board) Retain(ids []string) bool {
	keep := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		keep[id] = struct{}{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	changed := false
	for id := range b.m {
		if _, ok := keep[id]; !ok {
			delete(b.m, id)
			changed = true
		}
	}
	return changed
}

// Sink — общее хранилище результатов (PostgreSQL): реплики-наблюдатели показывают то же, что видит лидер.
type Sink interface {
	Save(ctx context.Context, id string, h Health) error
	Load(ctx context.Context) (map[string]Health, error)
}

// Leader — выбор одной реплики, которая ходит в кластеры (pg_advisory_lock).
type Leader interface {
	// Acquire возвращает true, если эта реплика лидер сейчас.
	Acquire(ctx context.Context) bool
	// Release отдаёт лидерство (при остановке).
	Release()
}

// Prober проверяет один кластер. Таймаут задаёт ctx; паника превращается в Unknown.
type Prober func(ctx context.Context, id string) Health

// Monitor периодически проверяет все кластеры.
type Monitor struct {
	Board *Board
	// List возвращает идентификаторы кластеров.
	List  func() ([]string, error)
	Probe Prober
	// Sink и Leader необязательны: без них монитор работает один и хранит результаты только в памяти.
	Sink   Sink
	Leader Leader
	// Interval — пауза между кругами проверок (по умолчанию 30 с); Timeout — предел одной проверки (8 с);
	// Parallel — сколько кластеров проверяются одновременно (8).
	Interval time.Duration
	Timeout  time.Duration
	Parallel int
	// OnChange вызывается после круга, если у какого-либо кластера изменилось состояние.
	OnChange func()
	// OnPublish вызывается только у лидера после круга, в котором что-то изменилось и записано в Sink:
	// остальные реплики должны перечитать состояния (событие health.changed). У читающих реплик не вызывается,
	// иначе сообщение ходило бы по кругу.
	OnPublish func()
	// Logf — журнал процесса; nil — молча.
	Logf func(format string, args ...any)
	// Now — часы (тесты).
	Now func() time.Time

	follower atomic.Bool // true: на последнем круге эта реплика читала результаты лидера
	ticked   atomic.Bool // был хотя бы один круг
}

// IsLeader — эта реплика сама проверяет кластеры (без Leader — единственная, значит лидер). До первого
// круга false: состояние ещё неизвестно.
func (m *Monitor) IsLeader() bool { return m.ticked.Load() && !m.follower.Load() }

func (m *Monitor) interval() time.Duration {
	if m.Interval > 0 {
		return m.Interval
	}
	return 30 * time.Second
}

func (m *Monitor) timeout() time.Duration {
	if m.Timeout > 0 {
		return m.Timeout
	}
	return 8 * time.Second
}

func (m *Monitor) parallel() int {
	if m.Parallel > 0 {
		return m.Parallel
	}
	return 8
}

func (m *Monitor) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Monitor) logf(format string, args ...any) {
	if m.Logf != nil {
		m.Logf(format, args...)
	}
}

// Run проверяет кластеры сразу и затем каждые Interval, пока ctx не отменён.
func (m *Monitor) Run(ctx context.Context) {
	if m.Leader != nil {
		defer m.Leader.Release()
	}
	m.Tick(ctx)
	t := time.NewTicker(m.interval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Tick(ctx)
		}
	}
}

// Tick выполняет один круг: лидер проверяет кластеры, остальные читают результаты из Sink.
func (m *Monitor) Tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	m.ticked.Store(true)
	if m.Leader != nil && !m.Leader.Acquire(ctx) {
		m.follower.Store(true)
		m.follow(ctx)
		return
	}
	m.follower.Store(false)
	ids, err := m.List()
	if err != nil {
		m.logf("cluster health: list clusters: %v", err)
		return
	}
	var (
		mu      sync.Mutex
		changed bool
		wg      sync.WaitGroup
		sem     = make(chan struct{}, m.parallel())
	)
	for _, id := range ids {
		sem <- struct{}{}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			h := m.probe(ctx, id)
			if ctx.Err() != nil {
				return // остановка: оборванная проверка не результат
			}
			c := m.Board.Set(id, h)
			if m.Sink != nil {
				if err := m.Sink.Save(ctx, id, h); err != nil && ctx.Err() == nil {
					m.logf("cluster health: save %s: %v", id, err)
				}
			}
			if c {
				mu.Lock()
				changed = true
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}
	if m.Board.Retain(ids) {
		changed = true
	}
	if changed && m.OnChange != nil {
		m.OnChange()
	}
	if changed && m.OnPublish != nil {
		m.OnPublish()
	}
}

// Refresh немедленно читает результаты из Sink (по событию health.changed от лидера), не дожидаясь очередного
// круга. Без Sink ничего не делает. Сообщение приходит только от лидера, поэтому сама лидирующая реплика его не
// получает (свои сообщения отбрасываются).
func (m *Monitor) Refresh(ctx context.Context) { m.follow(ctx) }

// follow обновляет доску из общего хранилища (эта реплика не лидер).
func (m *Monitor) follow(ctx context.Context) {
	if m.Sink == nil {
		return
	}
	all, err := m.Sink.Load(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.logf("cluster health: load: %v", err)
		}
		return
	}
	if m.Board.Replace(all) && m.OnChange != nil {
		m.OnChange()
	}
}

func (m *Monitor) probe(ctx context.Context, id string) (h Health) {
	ctx, cancel := context.WithTimeout(ctx, m.timeout())
	defer cancel()
	start := m.now()
	defer func() {
		if r := recover(); r != nil {
			h = Health{State: Unknown, Message: Trim(fmt.Sprintf("check failed: %v", r))}
		}
		if h.CheckedAt.IsZero() {
			h.CheckedAt = start
		}
		h.Message = Trim(h.Message)
	}()
	h = m.Probe(ctx, id)
	if h.LatencyMs == 0 {
		h.LatencyMs = m.now().Sub(start).Milliseconds()
	}
	return h
}
