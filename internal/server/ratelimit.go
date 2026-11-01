// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/24DSO-labs/kubron/internal/auth"
)

// Ограничение частоты анонимных RPC-вызовов по адресу клиента. Вход имеет собственную блокировку
// после неудач (auth.Service); здесь — потолок на сам поток запросов без сессии: AuthStatus,
// GetCapabilities, Login, SetupAdmin и любые вызовы без cookie. Состояние в памяти процесса: при
// нескольких репликах у каждой свой счётчик (docs/http-security.md).
const (
	// DefaultAnonBurst — сколько запросов подряд можно сделать сразу (экран входа делает 3–4).
	DefaultAnonBurst = 20
	// DefaultAnonRate — устойчивая скорость, запросов в секунду на адрес. Адрес — часто целый офис за NAT.
	DefaultAnonRate = 5.0
	// maxBuckets — предел числа отслеживаемых адресов (защита памяти от перебора адресов).
	maxBuckets = 10000
)

type bucket struct {
	tokens float64
	last   time.Time
}

// RateLimiter — token bucket по ключу (адресу клиента). Безопасен для параллельного использования.
type RateLimiter struct {
	rate  float64 // токенов в секунду
	burst float64

	mu      sync.Mutex
	now     func() time.Time
	buckets map[string]*bucket
}

// NewRateLimiter создаёт ограничитель: до burst запросов сразу и rate запросов в секунду дальше.
func NewRateLimiter(rate float64, burst int) *RateLimiter {
	return &RateLimiter{rate: rate, burst: float64(max(burst, 1)), now: time.Now, buckets: map[string]*bucket{}}
}

// Allow списывает один токен ключа. Если токенов нет — false и время до следующего.
func (l *RateLimiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= maxBuckets {
			l.evictLocked(now)
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(l.burst, b.tokens+elapsed*l.rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, max(wait, time.Second/10)
}

// evictLocked освобождает место: сначала бакеты, успевшие полностью восстановиться (по ним ограничивать
// нечего), иначе самый давно использованный. Активные нарушители (пустые бакеты) вытесняются в последнюю очередь.
func (l *RateLimiter) evictLocked(now time.Time) {
	var oldestKey string
	var oldest time.Time
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
			delete(l.buckets, k)
			continue
		}
		if oldestKey == "" || b.last.Before(oldest) {
			oldestKey, oldest = k, b.last
		}
	}
	if len(l.buckets) >= maxBuckets && oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}

// limitAnonymous проверяет лимит для запроса без сессии. При превышении отвечает 429 с Retry-After
// и возвращает false. Ключ — адрес клиента, нормализованный (IPv6 до /64, как у блокировки входа).
func (g *Guard) limitAnonymous(w http.ResponseWriter, r *http.Request) bool {
	if g == nil || g.AnonLimiter == nil {
		return true
	}
	ok, wait := g.AnonLimiter.Allow(auth.NormalizeSource(g.clientIP(r)))
	if ok {
		return true
	}
	secs := int((wait + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", fmt.Sprint(secs))
	// Текст идёт через fmt.Errorf: i18n-check требует для него шаблон в backend.json (en и ru)
	writeError(w, http.StatusTooManyRequests, "rate_limited", fmt.Errorf("too many requests, try again in %d s", secs).Error())
	return false
}
