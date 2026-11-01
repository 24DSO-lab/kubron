// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// Состояние реплики при остановке (ADR-0016, раздел 7).
//
//	работает → draining → closing → остановка HTTP-сервера
//
//   - draining: SIGTERM получен. Реплика не принимает новые WebSocket и терминалы (503), а проверка
//     готовности `/readyz` обязана отвечать «не готов», чтобы балансировщик перестал направлять сюда
//     новые соединения. Сам `/readyz` — не здесь: он читает Lifecycle.Draining() (интерфейс Drainer).
//   - closing: после паузы DrainDelay (время на снятие реплики с балансировщика) открытые WebSocket
//     закрываются кадром 1012 «Service Restart»; клиент переподключается к живой реплике.
//
// Сессии при этом не затрагиваются: они лежат в хранилище, а не в процессе (PR 3).

// CloseServiceRestart — код закрытия WebSocket «перезапуск сервиса» (RFC 6455, 1012).
const CloseServiceRestart = 1012

const closeReasonRestart = "service restart"

// Drainer — то, что нужно проверке готовности: «реплика останавливается, новые соединения не слать».
type Drainer interface{ Draining() bool }

// Lifecycle — идентичность и состояние остановки реплики. Методы безопасны для nil (реплика без
// жизненного цикла в тестах никогда не «останавливается»).
type Lifecycle struct {
	replicaID string
	draining  atomic.Bool
	closing   atomic.Bool
}

// NewLifecycle создаёт состояние реплики с идентификатором replicaID (см. ResolveReplicaID).
func NewLifecycle(replicaID string) *Lifecycle { return &Lifecycle{replicaID: replicaID} }

// ReplicaID — идентификатор этой реплики; у nil пусто.
func (l *Lifecycle) ReplicaID() string {
	if l == nil {
		return ""
	}
	return l.replicaID
}

// Draining — true после BeginDrain. Реализует Drainer.
func (l *Lifecycle) Draining() bool { return l != nil && l.draining.Load() }

// Closing — true, когда открытые соединения уже закрываются (или закрыты) кадром 1012.
func (l *Lifecycle) Closing() bool { return l != nil && l.closing.Load() }

// BeginDrain переводит реплику в draining (идемпотентно).
func (l *Lifecycle) BeginDrain() {
	if l != nil {
		l.draining.Store(true)
	}
}

// markClosing отмечает, что соединения закрываются: позднее подключение обнаруживает это и уходит само.
func (l *Lifecycle) markClosing() {
	if l != nil {
		l.draining.Store(true)
		l.closing.Store(true)
	}
}

// refuseIfDraining отвечает 503 и true, если реплика останавливается: клиент повторит запрос позже и
// попадёт на другую реплику.
func (g *Guard) refuseIfDraining(w http.ResponseWriter) bool {
	if g == nil || !g.Lifecycle.Draining() {
		return false
	}
	w.Header().Set("Retry-After", "1")
	writeError(w, http.StatusServiceUnavailable, "service_unavailable", "service is shutting down")
	return true
}

// BeginDrain — первый шаг остановки: см. Lifecycle.BeginDrain.
func (g *Guard) BeginDrain() { g.Lifecycle.BeginDrain() }

// CloseConnections — второй шаг остановки: закрывает все WebSocket событий и терминалов кадром 1012.
// Вид сессии, терминалы (процессы) и сама сессия не закрываются: их судьбу решает завершение процесса.
func (g *Guard) CloseConnections() {
	g.Lifecycle.markClosing()
	if g.Hub != nil {
		g.Hub.CloseAll(CloseServiceRestart, closeReasonRestart)
	}
	g.termConns.shutdown(CloseServiceRestart, closeReasonRestart)
}

// ShutdownPlan — порядок остановки сервера после отмены контекста.
type ShutdownPlan struct {
	// Drain вызывается первым (перевод в draining); nil — пропустить.
	Drain func()
	// DrainDelay — пауза между Drain и закрытием соединений: за это время балансировщик снимает
	// реплику с обслуживания (KUBRON_DRAIN_DELAY). 0 — без паузы.
	DrainDelay time.Duration
	// CloseConns закрывает открытые WebSocket (кадр 1012); nil — пропустить.
	CloseConns func()
	// Timeout — сколько ждать завершения активных HTTP-запросов (по умолчанию 10 с).
	Timeout time.Duration
}

// DefaultShutdownTimeout — ожидание активных запросов при остановке.
const DefaultShutdownTimeout = 10 * time.Second

// ServeWith обслуживает listener до отмены ctx, затем останавливает сервер по плану.
func ServeWith(ctx context.Context, ln net.Listener, h http.Handler, plan ShutdownPlan) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Printf("kubron-server is listening on http://%s", ln.Addr())

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	if plan.Drain != nil {
		plan.Drain()
	}
	if plan.DrainDelay > 0 {
		log.Printf("shutdown: draining for %s before closing connections", plan.DrainDelay)
		select {
		case <-time.After(plan.DrainDelay):
		case err := <-errc:
			if !errors.Is(err, http.ErrServerClosed) {
				return err
			}
		}
	}
	if plan.CloseConns != nil {
		plan.CloseConns()
	}
	timeout := plan.Timeout
	if timeout <= 0 {
		timeout = DefaultShutdownTimeout
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
