// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"net"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/24DSO-lab/kubron/internal/replicaevents"
)

// Транспорт сообщений между репликами на PostgreSQL LISTEN/NOTIFY (ADR-0016, раздел 3).
//
// Слушатель — одно выделенное прямое соединение на реплику (pgx.Conn вне пула): pgbouncer в режиме transaction
// LISTEN не поддерживает, поэтому адрес слушателя можно задать отдельно (KUBRON_DB_LISTEN_URL). NOTIFY не
// гарантирует доставку (теряется при обрыве соединения слушателя), поэтому после каждого (пере)подключения вызывается
// onResync — полная сверка с БД; её же делает периодическая фоновая проверка. Недоступный слушатель — не отказ:
// сервис работает по опросу, хуже по задержке, не хуже по корректности.

const (
	notifyQueue    = 256
	notifyTimeout  = 5 * time.Second
	listenKeepIdle = 15 * time.Second
	backoffMin     = time.Second
	backoffMax     = 30 * time.Second
	// stableAfter — соединение, продержавшееся дольше, считается удачным: пауза переподключения сбрасывается.
	stableAfter = 30 * time.Second
)

// Notifier публикует и принимает сообщения между репликами.
type Notifier struct {
	pool      *pgxpool.Pool
	listenURL string
	logf      func(format string, args ...any)
	out       chan string
	listening atomic.Bool
}

// NewNotifier создаёт транспорт. listenURL — прямой адрес PostgreSQL для LISTEN (не pgbouncer в режиме transaction);
// logf — журнал (nil — молча).
func (s *Store) NewNotifier(listenURL string, logf func(format string, args ...any)) *Notifier {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Notifier{pool: s.pool, listenURL: listenURL, logf: logf, out: make(chan string, notifyQueue)}
}

// Listening — слушатель подключён (иначе сообщения от других реплик не приходят, работает опрос).
func (n *Notifier) Listening() bool { return n.listening.Load() }

// Publish ставит сообщение в очередь отправки и не блокируется. Очередь переполнена — сообщение теряется
// (ускорение, не источник истины): другие реплики увидят изменение при сверке.
func (n *Notifier) Publish(payload string) {
	select {
	case n.out <- payload:
	default:
		n.logf("replica events: the send queue is full, a message was dropped")
	}
}

// Run отправляет сообщения из очереди и слушает канал до отмены ctx. handle вызывается для каждого принятого
// сообщения (последовательно); onResync — после каждого успешного (пере)подключения слушателя. Блокирует.
func (n *Notifier) Run(ctx context.Context, handle func(payload string), onResync func()) {
	go n.send(ctx)
	backoff := backoffMin
	for ctx.Err() == nil {
		started := time.Now()
		err := n.listenOnce(ctx, handle, onResync)
		n.listening.Store(false)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > stableAfter {
			backoff = backoffMin
		}
		n.logf("replica events: the listener is down (%v), retrying in %s; events between replicas work by polling", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

func (n *Notifier) send(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-n.out:
			c, cancel := context.WithTimeout(ctx, notifyTimeout)
			if _, err := n.pool.Exec(c, `SELECT pg_notify($1, $2)`, replicaevents.Channel, p); err != nil && ctx.Err() == nil {
				n.logf("replica events: the message was not sent: %v", err)
			}
			cancel()
		}
	}
}

func (n *Notifier) listenOnce(ctx context.Context, handle func(string), onResync func()) error {
	cfg, err := pgx.ParseConfig(n.listenURL)
	if err != nil {
		return err
	}
	// Короткий keepalive: обрыв без FIN (сеть, отказ узла) обнаруживается за секунды-минуту, а не за часы.
	cfg.DialFunc = (&net.Dialer{KeepAlive: listenKeepIdle}).DialContext
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = conn.Close(c)
	}()
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{replicaevents.Channel}.Sanitize()); err != nil {
		return err
	}
	n.listening.Store(true)
	onResync()
	for {
		nt, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		handle(nt.Payload)
	}
}
