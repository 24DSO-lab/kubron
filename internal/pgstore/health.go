// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/24DSO-labs/kubron/internal/clusterhealth"
)

// Health — состояние соединения кластеров (задача 3.6); общее для всех реплик сервиса.
// Реализует clusterhealth.Sink: пакет clusterhealth не зависит от k8s, поэтому pgstore его использует напрямую.
type Health struct{ s *Store }

var (
	_ clusterhealth.Sink   = (*Health)(nil)
	_ clusterhealth.Leader = (*Leader)(nil)
)

// Health возвращает хранилище состояний.
func (s *Store) Health() *Health { return &Health{s} }

// Save записывает результат проверки. Для удалённого кластера запись не создаётся (внешний ключ):
// это не ошибка, проверка могла закончиться уже после удаления.
func (h *Health) Save(ctx context.Context, clusterID string, r clusterhealth.Health) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	if r.CheckedAt.IsZero() {
		r.CheckedAt = time.Now()
	}
	_, err := h.s.pool.Exec(ctx, `
		INSERT INTO cluster_health (cluster_id, state, message, latency_ms, checked_at)
		SELECT $1::text, $2::text, $3::text, $4::bigint, $5::timestamptz WHERE EXISTS (SELECT 1 FROM clusters WHERE id = $1::text)
		ON CONFLICT (cluster_id) DO UPDATE
		SET state = EXCLUDED.state, message = EXCLUDED.message, latency_ms = EXCLUDED.latency_ms, checked_at = EXCLUDED.checked_at`,
		clusterID, string(r.State), r.Message, r.LatencyMs, r.CheckedAt)
	return err
}

// Load возвращает последние результаты по всем кластерам.
func (h *Health) Load(ctx context.Context) (map[string]clusterhealth.Health, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	rows, err := h.s.pool.Query(ctx, `SELECT cluster_id, state, message, latency_ms, checked_at FROM cluster_health`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]clusterhealth.Health{}
	for rows.Next() {
		var id string
		var r clusterhealth.Health
		var state string
		if err := rows.Scan(&id, &state, &r.Message, &r.LatencyMs, &r.CheckedAt); err != nil {
			return nil, err
		}
		r.State = clusterhealth.State(state)
		out[id] = r
	}
	return out, rows.Err()
}

// LeaderKey — ключ advisory-блокировки, по которой реплики выбирают ту, что проверяет кластеры.
const LeaderKey int64 = 0x6B756275

// Leader — блокировка pg_advisory_lock уровня сессии БД: держится на отдельном соединении, пока
// соединение живо. Если реплика упала или потеряла БД, сервер снимает блокировку сам, и проверку
// подхватывает другая реплика.
type Leader struct {
	pool *pgxpool.Pool
	key  int64

	mu   sync.Mutex
	conn *pgxpool.Conn
}

// Leader возвращает блокировку лидера проверок; key — LeaderKey (в тестах — свой).
func (s *Store) Leader(key int64) *Leader { return &Leader{pool: s.pool, key: key} }

// Acquire возвращает true, если эта реплика сейчас лидер: блокировка уже за ней или взята сейчас.
// Если соединение с блокировкой оборвалось, реплика перестаёт считаться лидером и пробует взять её заново.
func (l *Leader) Acquire(ctx context.Context) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	if l.conn != nil {
		var one int
		if err := l.conn.QueryRow(ctx, `SELECT 1`).Scan(&one); err == nil {
			return true
		}
		l.conn.Release()
		l.conn = nil
	}
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return false
	}
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, l.key).Scan(&ok); err != nil || !ok {
		conn.Release()
		return false
	}
	l.conn = conn
	return true
}

// Release отдаёт блокировку (при остановке сервера); безопасно вызывать повторно.
func (l *Leader) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := l.conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, l.key); err != nil {
		// Соединение в неизвестном состоянии: закрываем его, сервер снимет блокировку сам.
		_ = l.conn.Conn().Close(context.Background())
	}
	l.conn.Release()
	l.conn = nil
}
