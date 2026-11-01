// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package websession описывает общее хранилище веб-сессий (ADR-0016, раздел 1): строка сессии лежит
// в PostgreSQL, вид ядра (подключения к кластерам, терминалы, informers) строится лениво на той реплике,
// которая приняла запрос. Пакет не зависит ни от сервера, ни от PostgreSQL: его используют и pgstore
// (реализация), и internal/server (потребитель).
package websession

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"
)

// Hash — SHA-256 токена из cookie. Сам токен нигде не хранится.
type Hash = [sha256.Size]byte

// ErrNotFound — сессии нет, её срок истёк или пользователь отключён/удалён.
var ErrNotFound = errors.New("web session not found")

// PrevTokenGrace — сколько после ротации токена действует прежний (параллельные запросы со старой cookie).
const PrevTokenGrace = 30 * time.Second

// Record — то, что хранится в строке сессии. Вида ядра здесь нет: он строится заново.
type Record struct {
	TokenHash  Hash
	ID         string // публичный идентификатор (адресат событий), не меняется при ротации токена
	UserID     string
	CSRF       string
	CreatedAt  time.Time
	ExpiresAt  time.Time // абсолютный срок жизни
	LastActive time.Time // последнее действие пользователя в интерфейсе (ReportActivity)
	Source     string    // адрес клиента при входе
	// ActiveCluster — идентификатор активного кластера (только идентификатор; подключение строится лениво).
	ActiveCluster string
	// ReplicaID — реплика, создавшая сессию (диагностика, не владение).
	ReplicaID string
}

// Backend — хранилище сессий. Все методы безопасны для одновременного вызова с разных реплик.
type Backend interface {
	// Create записывает новую сессию.
	Create(ctx context.Context, r Record) error
	// Get возвращает действующую сессию по хэшу токена: текущему или прежнему (в течение PrevTokenGrace
	// после ротации). ErrNotFound — нет строки, истёк срок или пользователь отключён.
	// В Record.TokenHash — текущий хэш сессии.
	Get(ctx context.Context, h Hash) (Record, error)
	// Touch продлевает отметку последней активности (не уменьшает её).
	Touch(ctx context.Context, id string, at time.Time) error
	// SetActiveCluster запоминает активный кластер сессии ("" — сбросить).
	SetActiveCluster(ctx context.Context, id, clusterID string) error
	// Rotate заменяет токен сессии; прежний хэш действует до prevValidUntil.
	Rotate(ctx context.Context, id string, newHash Hash, prevValidUntil time.Time) error
	// Delete удаляет сессию (идемпотентно).
	Delete(ctx context.Context, id string) error
	// DeleteUser удаляет все сессии пользователя, кроме exceptID (пусто — все), и возвращает их идентификаторы.
	DeleteUser(ctx context.Context, userID, exceptID string) ([]string, error)
	// DeleteExpired удаляет сессии с истёкшим сроком и возвращает их число. Выполняет один исполнитель
	// под блокировкой (PR 5).
	DeleteExpired(ctx context.Context) (int, error)
}

// Provider — необязательный интерфейс хранилища приложения (как Access или Directory): файловое хранилище
// и режим одного пользователя его не реализуют, и сессии остаются в памяти процесса.
type Provider interface {
	Sessions() Backend
}
