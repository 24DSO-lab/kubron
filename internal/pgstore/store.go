// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/24DSO-lab/kubron/internal/migrate"
	"github.com/24DSO-lab/kubron/internal/secretbox"
)

// DefaultName — имя организации и команды, создаваемых при первом запуске (задача 3.1d переносит в них данные десктопа).
const DefaultName = "Default"

// opTimeout — предел одной операции хранилища. Где интерфейс принимает context (веб-сессии, счётчики входа),
// таймаут накладывается поверх него; остальные интерфейсы ядра context пока не принимают.
const opTimeout = 15 * time.Second

// Store — хранилище сервиса на PostgreSQL.
type Store struct {
	pool   *pgxpool.Pool
	keys   *secretbox.Keyring
	orgID  string
	teamID string

	accessMu          sync.Mutex
	accessSnap        *accessSnap
	accessTTLOverride *time.Duration // тесты
	accessNotify      func()         // сообщает другим репликам об изменении прав (SetAccessNotifier)
}

// Open подключается к БД. Схему создаёт Migrate; без неё Open сразу создавать организацию не станет:
// это делает Init.
// OrgID возвращает идентификатор организации каталога (tenant.Scope операционных модулей, ADR-0026 §4).
func (s *Store) OrgID() string { return s.orgID }

func Open(ctx context.Context, url string, keys *secretbox.Keyring) (*Store, error) {
	if keys == nil {
		return nil, secretbox.ErrNoKey
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect to the database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to the database: %w", err)
	}
	return &Store{pool: pool, keys: keys}, nil
}

// Close закрывает соединения.
func (s *Store) Close() { s.pool.Close() }

// Migrate применяет миграции схемы под блокировкой БД (безопасно при одновременном старте нескольких реплик).
func (s *Store) Migrate(ctx context.Context) (migrate.Result, error) {
	ms, err := Migrations()
	if err != nil {
		return migrate.Result{}, err
	}
	db := stdlib.OpenDBFromPool(s.pool)
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return migrate.Result{}, err
	}
	defer conn.Close()
	return (&migrate.Migrator{DB: migrate.Postgres{Conn: conn}, Migrations: ms}).Up(ctx)
}

// Init создаёт организацию и команду «Default», если их нет, и запоминает их идентификаторы.
// Вызывается после Migrate; повторный вызов безопасен.
func (s *Store) Init(ctx context.Context) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(0x6B756273)); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO organizations (name) VALUES ($1)
			ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id`, DefaultName).Scan(&s.orgID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO teams (org_id, name) VALUES ($1, $2)
			ON CONFLICT (org_id, name) DO UPDATE SET name = EXCLUDED.name RETURNING id`, s.orgID, DefaultName).Scan(&s.teamID)
	})
}

func (s *Store) ready() error {
	if s.orgID == "" {
		return ErrNotInitialized
	}
	return nil
}

// Ошибки хранилища.
var (
	ErrNotInitialized  = errors.New("database store is not initialized")
	ErrClusterNotFound = errors.New("cluster not found")
	ErrConflict        = errors.New("a record with this name already exists")
	ErrTeamNotEmpty    = errors.New("the team still has clusters: move or delete them first")
	ErrDefaultTeam     = errors.New("the default team cannot be deleted")
)

func opCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), opTimeout)
}

// pgCode возвращает SQLSTATE ошибки PostgreSQL или "".
func pgCode(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

func isUnique(err error) bool { return pgCode(err) == "23505" }
