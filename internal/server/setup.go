// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/24DSO-labs/kubron/internal/auth"
)

// Первичная настройка в сервисном режиме. SetupAdmin анонимен (иначе первого администратора нечем создать), поэтому
// при пустой БД любой, кто достучался до сервера, стал бы администратором. В сервисном режиме метод требует токен
// из KUBRON_SETUP_TOKEN в заголовке X-Kubron-Setup-Token. Проверка стоит в слое server, а не в App.SetupAdmin:
// сигнатура метода и привязки Wails не меняются, десктоп и loopback ведут себя по-прежнему.
const (
	// SetupTokenHeader — заголовок с токеном первичной настройки.
	SetupTokenHeader = "X-Kubron-Setup-Token"
	// MinSetupTokenLen — минимальная длина токена (его вводят вручную, но подбирать он должен быть невыгодно).
	MinSetupTokenLen = 16

	// Неверные токены ограничиваются по адресу клиента: после setupFailBurst неудач подряд — по одной попытке
	// в setupFailEvery. Верные попытки не расходуют запас.
	setupFailBurst = 5
	setupFailEvery = 30 * time.Second
)

var (
	// ErrSetupDisabled — токен не задан: сетевой SetupAdmin отклоняется. Токен не генерируется и не пишется в журнал.
	ErrSetupDisabled = errors.New("initial setup over the network is disabled: set KUBRON_SETUP_TOKEN or run kubron-server create-admin")
	// ErrSetupTokenInvalid — токен не передан или не совпал. Ответ одинаков в обоих случаях.
	ErrSetupTokenInvalid = errors.New("the setup token is missing or invalid")
)

// SetupGate — защита первичной настройки. nil у Guard означает «токен не нужен» (десктоп, loopback, тесты).
type SetupGate struct {
	// Token — ожидаемое значение; пусто — сетевая настройка отключена.
	Token string
	// Pending сообщает, что пользователей ещё нет. Когда они есть, токен не проверяется: ядро само ответит
	// «настройка уже выполнена». nil — считать, что настройка нужна.
	Pending func() bool
	// Limiter ограничивает неудачные попытки по адресу клиента; nil — без ограничения.
	Limiter *RateLimiter
}

// NewSetupGate создаёт защиту с ограничителем неудачных попыток по умолчанию.
func NewSetupGate(token string, pending func() bool) *SetupGate {
	return &SetupGate{Token: token, Pending: pending,
		Limiter: NewRateLimiter(1/setupFailEvery.Seconds(), setupFailBurst)}
}

// tokenEqual сравнивает токены за постоянное время; хеширование выравнивает длины (длина токена не раскрывается).
func tokenEqual(got, want string) bool {
	a, b := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// checkSetup пропускает запрос SetupAdmin дальше или отвечает отказом (тогда возвращает false).
func (g *Guard) checkSetup(w http.ResponseWriter, r *http.Request) bool {
	gate := g.Setup
	if gate == nil {
		return true
	}
	if gate.Pending != nil && !gate.Pending() {
		return true
	}
	if gate.Token == "" {
		writeError(w, http.StatusForbidden, "setup_disabled", ErrSetupDisabled.Error())
		return false
	}
	key := auth.NormalizeSource(g.clientIP(r))
	if gate.Limiter != nil {
		if blocked, wait := gate.Limiter.Blocked(key); blocked {
			secs := int((wait + time.Second - 1) / time.Second)
			w.Header().Set("Retry-After", fmt.Sprint(secs))
			writeError(w, http.StatusTooManyRequests, "rate_limited", fmt.Errorf("too many requests, try again in %d s", secs).Error())
			return false
		}
	}
	if !tokenEqual(r.Header.Get(SetupTokenHeader), gate.Token) {
		if gate.Limiter != nil {
			gate.Limiter.Allow(key) // списывается только за неверный токен
		}
		slog.WarnContext(r.Context(), "setup: the setup token was rejected", "source", key)
		writeError(w, http.StatusForbidden, "setup_token_invalid", ErrSetupTokenInvalid.Error())
		return false
	}
	return true
}
