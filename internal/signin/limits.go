// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package signin

import (
	"fmt"
	"time"
)

// AttemptLimiter — ведро токенов на ключ: та же форма, что у server.RateLimiter, который веб-сервер сюда и передаёт,
// поэтому попытки входа ограничиваются уже существующим лимитером сервера.
type AttemptLimiter interface {
	Allow(key string) (ok bool, retryAfter time.Duration)
}

// RateLimitedError возвращается, когда ответов отправлено слишком много. Веб-сервер превращает её в 429 с Retry-After
// (он узнаёт метод RetryAfterSeconds, поэтому пакет сервера её не импортирует).
type RateLimitedError struct{ wait time.Duration }

// RetryAfterSeconds — ожидание до следующей попытки, округлённое вверх.
func (e *RateLimitedError) RetryAfterSeconds() int {
	return max(1, int((e.wait+time.Second-1)/time.Second))
}

// Error идёт через fmt.Errorf, чтобы i18n-check требовал шаблон в backend.json.
func (e *RateLimitedError) Error() string {
	return fmt.Errorf("too many sign-in attempts, try again in %d s", e.RetryAfterSeconds()).Error()
}

// Limiter — лимит ответов на пользователя приложения и на провайдера входа. Нулевой лимитер (nil-поля) не ограничивает
// (десктоп).
type Limiter struct {
	User, IdP AttemptLimiter
}

// Take берёт одну попытку из ведра пользователя и из ведра провайдера входа. Своё ведро пользователя идёт первым: один
// пользователь не может израсходовать запас всех остальных у того же провайдера. userID вызывается только если задан
// лимит по пользователям; пустой id — без лимита по пользователю.
func (l Limiter) Take(userID func() string, idp string) error {
	if l.User != nil {
		if uid := userID(); uid != "" {
			if ok, wait := l.User.Allow("answer:" + uid); !ok {
				return &RateLimitedError{wait: wait}
			}
		}
	}
	if l.IdP != nil {
		if ok, wait := l.IdP.Allow("answer:" + idp); !ok {
			return &RateLimitedError{wait: wait}
		}
	}
	return nil
}
