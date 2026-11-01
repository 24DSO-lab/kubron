// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pfproxy

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// Причины остановки потока (попадают в аудит как есть).
const (
	ReasonUser          = "user"           // пользователь остановил поток
	ReasonIdle          = "idle"           // простой дольше idle-таймаута
	ReasonMaxDuration   = "max_duration"   // истёк абсолютный срок
	ReasonSessionClosed = "session_closed" // веб-сессия закрыта
	ReasonError         = "error"          // сбой туннеля
)

// ErrDuplicate — поток с таким идентификатором уже есть.
var ErrDuplicate = errors.New("port-forward stream already exists")

// DialFunc открывает соединение с портом пода. Вызывается на каждое соединение прокси; ctx ограничен
// таймаутом соединения.
type DialFunc func(ctx context.Context) (net.Conn, error)

// Target — что открыл пользователь: куда прокси отправляет запросы потока.
type Target struct {
	ID        string // идентификатор потока (NewID)
	Owner     string // идентификатор веб-сессии владельца
	TeamID    string
	ClusterID string
	Namespace string
	Pod       string
	Service   string // пусто, если проброс запущен на под
	Port      int    // порт пода
	Scheme    string // "http" или "https" (TLS внутри пода; сертификат не проверяется)
	Dial      DialFunc

	Created time.Time // заполняет реестр
}

type stream struct {
	t        Target
	lastSeen time.Time
	stop     func(reason string)
}

// Registry — потоки процесса с владельцами. Реализует Resolver. Потокобезопасен.
type Registry struct {
	mu      sync.Mutex
	streams map[string]*stream
	now     func() time.Time
	idle    time.Duration // 0 — без таймаута простоя
	maxDur  time.Duration // 0 — без абсолютного срока
	hooks   []func(id string)
}

// NewRegistry создаёт реестр с idle-таймаутом и абсолютным сроком потока (ноль отключает проверку).
func NewRegistry(idle, maxDuration time.Duration) *Registry {
	return &Registry{streams: map[string]*stream{}, now: time.Now, idle: idle, maxDur: maxDuration}
}

// OnRemove регистрирует обработчик, который вызывается после удаления потока (освобождение соединений).
func (r *Registry) OnRemove(f func(id string)) {
	r.mu.Lock()
	r.hooks = append(r.hooks, f)
	r.mu.Unlock()
}

// Add регистрирует поток. stop вызывается один раз, когда реестр удаляет поток по любой причине
// (в том числе по явному Remove), уже без блокировки реестра.
func (r *Registry) Add(t Target, stop func(reason string)) error {
	if !ValidID(t.ID) || t.Owner == "" || t.Dial == nil {
		return errors.New("invalid port-forward stream")
	}
	if t.Scheme == "" {
		t.Scheme = "http"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.streams[t.ID]; ok {
		return ErrDuplicate
	}
	now := r.now()
	t.Created = now
	r.streams[t.ID] = &stream{t: t, lastSeen: now, stop: stop}
	return nil
}

func (r *Registry) expiredLocked(s *stream, now time.Time) string {
	if r.maxDur > 0 && now.Sub(s.t.Created) >= r.maxDur {
		return ReasonMaxDuration
	}
	if r.idle > 0 && now.Sub(s.lastSeen) >= r.idle {
		return ReasonIdle
	}
	return ""
}

// Resolve возвращает цель потока, если он существует, принадлежит owner и не истёк. Чужой поток неотличим
// от несуществующего. Успешный вызов считается активностью.
func (r *Registry) Resolve(owner, id string) (Target, bool) {
	r.mu.Lock()
	s, ok := r.streams[id]
	if !ok || s.t.Owner != owner {
		r.mu.Unlock()
		return Target{}, false
	}
	now := r.now()
	if reason := r.expiredLocked(s, now); reason != "" {
		r.mu.Unlock()
		r.Remove(id, reason)
		return Target{}, false
	}
	s.lastSeen = now
	t := s.t
	r.mu.Unlock()
	return t, true
}

// Touch отмечает активность потока (данные по уже открытому соединению, WebSocket, SSE).
func (r *Registry) Touch(id string) {
	r.mu.Lock()
	if s, ok := r.streams[id]; ok {
		s.lastSeen = r.now()
	}
	r.mu.Unlock()
}

// Get возвращает цель потока без проверки владельца и без отметки активности (для служебного кода).
func (r *Registry) Get(id string) (Target, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.streams[id]
	if !ok {
		return Target{}, false
	}
	return s.t, true
}

// Remove удаляет поток; возвращает false, если его уже нет.
func (r *Registry) Remove(id, reason string) bool {
	r.mu.Lock()
	s, ok := r.streams[id]
	if ok {
		delete(r.streams, id)
	}
	hooks := append([]func(string){}, r.hooks...)
	r.mu.Unlock()
	if !ok {
		return false
	}
	for _, h := range hooks {
		h(id)
	}
	if s.stop != nil {
		s.stop(reason)
	}
	return true
}

// CloseOwner удаляет все потоки владельца (закрытие веб-сессии).
func (r *Registry) CloseOwner(owner, reason string) int {
	r.mu.Lock()
	var ids []string
	for id, s := range r.streams {
		if s.t.Owner == owner {
			ids = append(ids, id)
		}
	}
	r.mu.Unlock()
	n := 0
	for _, id := range ids {
		if r.Remove(id, reason) {
			n++
		}
	}
	return n
}

// Len — число потоков.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.streams)
}

// Sweep удаляет истёкшие потоки и возвращает их число.
func (r *Registry) Sweep() int {
	type victim struct{ id, reason string }
	r.mu.Lock()
	now := r.now()
	var vs []victim
	for id, s := range r.streams {
		if reason := r.expiredLocked(s, now); reason != "" {
			vs = append(vs, victim{id, reason})
		}
	}
	r.mu.Unlock()
	n := 0
	for _, v := range vs {
		if r.Remove(v.id, v.reason) {
			n++
		}
	}
	return n
}

// Run периодически вызывает Sweep до отмены ctx.
func (r *Registry) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 15 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Sweep()
		}
	}
}
