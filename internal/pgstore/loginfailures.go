// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"crypto/sha256"
	"time"
)

// MaxLoginFailures — верхняя граница таблицы login_failures. Лишние записи без действующей блокировки удаляются
// при очистке (старые первыми); действующие блокировки не удаляются никогда.
const MaxLoginFailures = 10000

// LoginFailures — счётчики неудачных входов по логину, общие для всех реплик (ADR-0016, раздел 5).
// Реализует auth.FailureBackend. Обновление атомарное (INSERT … ON CONFLICT DO UPDATE): параллельные неудачи на
// разных репликах не теряются.
type LoginFailures struct{ s *Store }

// LoginFailures возвращает хранилище счётчиков входа.
func (s *Store) LoginFailures() *LoginFailures { return &LoginFailures{s} }

// failureKey — ключ строки: хэш нормализованного ключа блокировки.
func failureKey(key string) []byte {
	h := sha256.Sum256([]byte(key))
	return h[:]
}

// LockRemaining возвращает остаток блокировки ключа (0 — не заблокирован).
func (f *LoginFailures) LockRemaining(key string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	var secs float64
	err := f.s.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT GREATEST(EXTRACT(EPOCH FROM locked_until - now()), 0)
		                 FROM login_failures WHERE key = $1), 0)::float8`, failureKey(key)).Scan(&secs)
	if err != nil {
		return 0, err
	}
	return time.Duration(secs * float64(time.Second)), nil
}

// RecordFailure учитывает неудачу. С free-й неудачи ключ блокируется на base·2^(n−free) секунд (показатель не
// больше 6), но не дольше max — как счётчик в памяти.
func (f *LoginFailures) RecordFailure(key string, free int, base, max time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	// $2 — число бесплатных попыток, $3 и $4 — базовая и предельная блокировка в секундах.
	_, err := f.s.pool.Exec(ctx, `
		INSERT INTO login_failures (key, count, locked_until, last_at)
		VALUES ($1, 1,
		        CASE WHEN 1 >= $2::int
		             THEN now() + make_interval(secs => LEAST($4::float8, $3::float8 * power(2, LEAST(1 - $2::int, 6))))
		        END,
		        now())
		ON CONFLICT (key) DO UPDATE SET
		    count = login_failures.count + 1,
		    last_at = now(),
		    locked_until = CASE WHEN login_failures.count + 1 >= $2::int
		        THEN now() + make_interval(secs => LEAST($4::float8, $3::float8 * power(2, LEAST(login_failures.count + 1 - $2::int, 6))))
		        ELSE login_failures.locked_until END`,
		failureKey(key), free, base.Seconds(), max.Seconds())
	return err
}

// ClearFailures забывает счётчик ключа.
func (f *LoginFailures) ClearFailures(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	_, err := f.s.pool.Exec(ctx, `DELETE FROM login_failures WHERE key = $1`, failureKey(key))
	return err
}

// Cleanup удаляет записи без действующей блокировки, не обновлявшиеся дольше window, а если таблица всё равно
// больше MaxLoginFailures — самые старые из незаблокированных. Возвращает число удалённых. Выполняет одна
// реплика (pg_try_advisory_lock).
func (f *LoginFailures) Cleanup(ctx context.Context, window time.Duration) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	tag, err := f.s.pool.Exec(ctx, `
		DELETE FROM login_failures
		WHERE (locked_until IS NULL OR locked_until <= now()) AND last_at < now() - make_interval(secs => $1::float8)`,
		window.Seconds())
	if err != nil {
		return 0, err
	}
	n := int(tag.RowsAffected())
	tag, err = f.s.pool.Exec(ctx, `
		DELETE FROM login_failures WHERE key IN (
		    SELECT key FROM login_failures
		    WHERE locked_until IS NULL OR locked_until <= now()
		    ORDER BY last_at ASC
		    OFFSET 0 LIMIT GREATEST((SELECT count(*) FROM login_failures) - $1::int, 0))`, MaxLoginFailures)
	if err != nil {
		return n, err
	}
	return n + int(tag.RowsAffected()), nil
}
