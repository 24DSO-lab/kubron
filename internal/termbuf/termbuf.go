// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package termbuf — серверная сторона веб-терминала: буфер вывода сессии и реестр сессий.
//
// Браузер открывает терминал в два шага: RPC OpenTerminal (сервер создаёт сессию и сам
// выдаёт неугадываемый ID) и WebSocket /api/terminal/{id}. Пока WebSocket не подключён,
// вывод копится в буфере и отдаётся при подключении: первые байты (приглашение shell)
// не теряются, а подписки с подтверждением, как у событий, не нужны.
package termbuf

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

const (
	// MaxBuffered — сколько байт вывода хранится, пока клиент не подключён.
	MaxBuffered = 256 << 10
	// MaxSessions — предел одновременных веб-терминалов в реестре.
	MaxSessions = 64
)

var (
	ErrAttached   = errors.New("terminal is already attached to another connection")
	ErrNotFound   = errors.New("terminal not found")
	ErrTooMany    = errors.New("too many open terminals")
	errNoCapacity = ErrTooMany
)

// NewID возвращает неугадываемый идентификатор сессии (128 бит).
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("termbuf: crypto/rand is unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Sink — получатель вывода (WebSocket). Send не должен сохранять p после возврата.
type Sink interface {
	Send(p []byte) error
	Exit()
}

// Buffer принимает вывод сессии (реализует k8s.TerminalOutput) и доставляет его подключённому Sink,
// а без него копит последние MaxBuffered байт.
type Buffer struct {
	mu      sync.Mutex
	pending []byte
	exited  bool
	sink    Sink
}

// Data вызывается читателем вывода сессии; p принадлежит вызывающему и переиспользуется.
func (b *Buffer) Data(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sink != nil {
		if err := b.sink.Send(p); err == nil {
			return
		}
		b.sink = nil // сокет сломан: копим вывод до повторного подключения
	}
	b.pending = append(b.pending, p...)
	if over := len(b.pending) - MaxBuffered; over > 0 {
		b.pending = append(b.pending[:0], b.pending[over:]...)
	}
}

// Exit вызывается, когда процесс в контейнере завершился или сессия закрыта.
func (b *Buffer) Exit() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.exited = true
	if b.sink != nil {
		b.sink.Exit()
	}
}

// Exited — сессия завершена.
func (b *Buffer) Exited() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.exited
}

// attach отдаёт накопленный вывод и подключает s; если сессия уже завершена — сообщает об этом.
func (b *Buffer) attach(s Sink) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sink != nil {
		return ErrAttached
	}
	if len(b.pending) > 0 {
		if err := s.Send(b.pending); err != nil {
			return err
		}
		b.pending = nil
	}
	b.sink = s
	if b.exited {
		s.Exit()
	}
	return nil
}

func (b *Buffer) detach(s Sink) {
	b.mu.Lock()
	if b.sink == s {
		b.sink = nil
	}
	b.mu.Unlock()
}

// Hooks — действия над сессией, которые реестр вызывает по запросам браузера.
type Hooks struct {
	Input  func(p []byte) error
	Resize func(cols, rows uint16) error
	Close  func() // завершить сессию (процесс и поток)
	// Owner — идентификатор веб-сессии владельца; подключиться к терминалу может только она.
	Owner string
}

type entry struct {
	buf          *Buffer
	hooks        Hooks
	createdAt    time.Time
	detachedAt   time.Time // нулевое значение — подключён
	everAttached bool
	attached     bool
}

// Registry — открытые веб-терминалы. Сессии, к которым не подключились вовремя или от которых
// клиент давно отключился, закрываются (Sweep): иначе вкладка, закрытая без CloseTerminal,
// оставляла бы shell в поде навсегда.
type Registry struct {
	// Сроки: до первого подключения, после отключения клиента, хранение завершённой сессии.
	AttachTimeout time.Duration
	DetachTimeout time.Duration
	ExitedTTL     time.Duration

	mu    sync.Mutex
	items map[string]*entry
	now   func() time.Time
}

func NewRegistry() *Registry {
	return &Registry{
		AttachTimeout: 30 * time.Second,
		DetachTimeout: 30 * time.Second,
		ExitedTTL:     30 * time.Second,
		items:         map[string]*entry{},
		now:           time.Now,
	}
}

// Register создаёт буфер для сессии id (id выдаёт NewID).
func (r *Registry) Register(id string, h Hooks) (*Buffer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.items) >= MaxSessions {
		return nil, errNoCapacity
	}
	b := &Buffer{}
	r.items[id] = &entry{buf: b, hooks: h, createdAt: r.now()}
	return b, nil
}

// Remove забывает сессию (например, если её не удалось запустить).
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	delete(r.items, id)
	r.mu.Unlock()
}

// OwnedBy — сессия зарегистрирована и принадлежит owner.
func (r *Registry) OwnedBy(id, owner string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.items[id]
	return ok && e.hooks.Owner == owner
}

// Exists — сессия зарегистрирована.
func (r *Registry) Exists(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.items[id]
	return ok
}

// Len — число сессий.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.items)
}

// Attachment — подключение браузера к сессии.
type Attachment struct {
	r     *Registry
	id    string
	e     *entry
	s     Sink
	owner string // пусто — без проверки владельца (Attach)
}

// AttachAs подключает s к сессии id от имени owner: чужая сессия неотличима от несуществующей, а ввод и размер
// проверяют владельца на каждый вызов (сессию могли закрыть или заменить после подключения).
func (r *Registry) AttachAs(id, owner string, s Sink) (*Attachment, error) {
	if owner == "" || !r.OwnedBy(id, owner) {
		return nil, ErrNotFound
	}
	a, err := r.Attach(id, s)
	if err != nil {
		return nil, err
	}
	a.owner = owner
	return a, nil
}

func (a *Attachment) allowed() error {
	if a.owner != "" && !a.r.OwnedBy(a.id, a.owner) {
		return ErrNotFound
	}
	return nil
}

// Attach подключает s к сессии id.
func (r *Registry) Attach(id string, s Sink) (*Attachment, error) {
	r.mu.Lock()
	e, ok := r.items[id]
	if !ok {
		r.mu.Unlock()
		return nil, ErrNotFound
	}
	if e.attached {
		r.mu.Unlock()
		return nil, ErrAttached
	}
	e.attached, e.everAttached = true, true
	r.mu.Unlock()

	if err := e.buf.attach(s); err != nil {
		r.mu.Lock()
		e.attached, e.detachedAt = false, r.now()
		r.mu.Unlock()
		return nil, err
	}
	return &Attachment{r: r, id: id, e: e, s: s}, nil
}

// Input передаёт ввод пользователя в сессию.
func (a *Attachment) Input(p []byte) error {
	if err := a.allowed(); err != nil {
		return err
	}
	return a.e.hooks.Input(p)
}

// Resize меняет размер терминала.
func (a *Attachment) Resize(cols, rows uint16) error {
	if err := a.allowed(); err != nil {
		return err
	}
	return a.e.hooks.Resize(cols, rows)
}

// Detach отключает браузер. Завершённая сессия удаляется сразу, живая ждёт DetachTimeout.
func (a *Attachment) Detach() {
	a.e.buf.detach(a.s)
	exited := a.e.buf.Exited()
	a.r.mu.Lock()
	defer a.r.mu.Unlock()
	a.e.attached = false
	a.e.detachedAt = a.r.now()
	if exited {
		delete(a.r.items, a.id)
	}
}

// Sweep закрывает просроченные сессии; возвращает число закрытых.
func (r *Registry) Sweep() int {
	now := r.now()
	var closers []func()
	r.mu.Lock()
	for id, e := range r.items {
		if e.attached {
			continue
		}
		exited := e.buf.Exited()
		switch {
		case exited && e.detachedAt.IsZero() && now.Sub(e.createdAt) > r.ExitedTTL,
			exited && !e.detachedAt.IsZero() && now.Sub(e.detachedAt) > r.ExitedTTL:
			delete(r.items, id)
		case !e.everAttached && now.Sub(e.createdAt) > r.AttachTimeout,
			e.everAttached && now.Sub(e.detachedAt) > r.DetachTimeout:
			delete(r.items, id)
			if e.hooks.Close != nil {
				closers = append(closers, e.hooks.Close)
			}
		}
	}
	r.mu.Unlock()
	for _, c := range closers {
		c()
	}
	return len(closers)
}

// CloseAll закрывает все сессии (остановка сервера).
func (r *Registry) CloseAll() {
	r.mu.Lock()
	var closers []func()
	for id, e := range r.items {
		if e.hooks.Close != nil {
			closers = append(closers, e.hooks.Close)
		}
		delete(r.items, id)
	}
	r.mu.Unlock()
	for _, c := range closers {
		c()
	}
}

// Run периодически вызывает Sweep до отмены done.
func (r *Registry) Run(done <-chan struct{}, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			r.Sweep()
		}
	}
}
