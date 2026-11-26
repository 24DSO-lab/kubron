// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package opsplatform

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
)

// Лимит одновременных чтений одного кластера операционными модулями (ADR-0026, «Решено при согласовании»).
const (
	// DefaultClusterConcurrency — стартовое значение: 4 одновременных чтения на кластер.
	DefaultClusterConcurrency = 4
	// MaxClusterConcurrency — верхняя граница настройки: лимит защищает kube-apiserver, а не снимается конфигурацией.
	MaxClusterConcurrency = 32
	// EnvClusterConcurrency — переменная окружения, меняющая лимит.
	EnvClusterConcurrency = "KUBRON_OPS_CLUSTER_CONCURRENCY"
)

// ClusterLimiter ограничивает число одновременных чтений одного кластера. Лимит действует в рамках процесса:
// на уровне нескольких реплик он не координируется (в первой версии нет фоновых запусков, нагрузку создают
// только пользователи). Запись о кластере живёт, пока есть ожидающие или держащие слот: map не растёт.
type ClusterLimiter struct {
	limit int

	mu      sync.Mutex
	entries map[string]*limiterEntry
}

type limiterEntry struct {
	slots chan struct{} // ёмкость = limit; занятый слот — элемент в канале
	refs  int           // число держащих и ожидающих
}

// NewClusterLimiter создаёт лимитер. limit < 1 заменяется значением по умолчанию, больше Max — границей.
func NewClusterLimiter(limit int) *ClusterLimiter {
	if limit < 1 {
		limit = DefaultClusterConcurrency
	}
	return &ClusterLimiter{limit: min(limit, MaxClusterConcurrency), entries: map[string]*limiterEntry{}}
}

// Limit возвращает действующий лимит на кластер.
func (l *ClusterLimiter) Limit() int { return l.limit }

// Acquire занимает слот чтения кластера clusterID и возвращает функцию освобождения. Ожидание прерывается отменой
// ctx: тогда слот не занят, а ошибка содержит ErrUnavailable и причину ctx (errors.Is работает с обеими).
// release безопасно вызывать несколько раз; обязателен ровно один вызов (defer release()).
func (l *ClusterLimiter) Acquire(ctx context.Context, clusterID string) (release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	l.mu.Lock()
	e := l.entries[clusterID]
	if e == nil {
		e = &limiterEntry{slots: make(chan struct{}, l.limit)}
		l.entries[clusterID] = e
	}
	e.refs++
	l.mu.Unlock()

	select {
	case e.slots <- struct{}{}:
	case <-ctx.Done():
		l.unref(clusterID, e)
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-e.slots
			l.unref(clusterID, e)
		})
	}, nil
}

func (l *ClusterLimiter) unref(clusterID string, e *limiterEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.refs--; e.refs <= 0 && l.entries[clusterID] == e {
		delete(l.entries, clusterID)
	}
}

// tracked возвращает число кластеров с активными или ожидающими чтениями (для тестов и диагностики).
func (l *ClusterLimiter) tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// ConcurrencyFromEnv читает лимит из KUBRON_OPS_CLUSTER_CONCURRENCY. Пустое, нечисловое или меньшее 1 значение
// даёт значение по умолчанию; значение больше MaxClusterConcurrency ограничивается им.
func ConcurrencyFromEnv(getenv func(string) string) int {
	n, err := strconv.Atoi(getenv(EnvClusterConcurrency))
	if err != nil || n < 1 {
		return DefaultClusterConcurrency
	}
	return min(n, MaxClusterConcurrency)
}

var sharedLimiter = sync.OnceValue(func() *ClusterLimiter {
	return NewClusterLimiter(ConcurrencyFromEnv(os.Getenv))
})

// SharedLimiter возвращает общий лимитер процесса: один счётчик на кластер для всех сессий и операций.
func SharedLimiter() *ClusterLimiter { return sharedLimiter() }
