// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"time"
)

// Границы времени операций вида. Они выше таймаутов нижних слоёв (запрос к провайдеру входа — 15 с в idpnet,
// внешний плагин — 30 с в execplugin), поэтому ограничивают вызов целиком, но не обрывают штатную работу.
const (
	// signInTimeout — вызов, который ходит к провайдеру входа: Acquire, Answer, Poll.
	signInTimeout = 60 * time.Second
	// storeTimeout — вызов, который работает с хранилищем входов и вызовов: List, Cancel, Revoke.
	storeTimeout = 15 * time.Second
)

// baseContext — контекст жизни вида. Он заканчивается при остановке ядра (корневой вид) или при освобождении
// вида (веб-сессия), поэтому закрытая сессия не оставляет за собой запросов к провайдерам входа. Вид, не прошедший
// startup (тесты), работает без отмены.
func (a *App) baseContext() context.Context {
	if a.base != nil {
		return a.base
	}
	return context.Background()
}

// opCtx — контекст одной операции вида: отменяется вместе с видом и не живёт дольше timeout. Wails и RPC не
// передают в методы контекст запроса, поэтому именно жизнь вида — граница отмены для интерфейса.
func (a *App) opCtx(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(a.baseContext(), timeout)
}

// detachedCtx — контекст для действия, которое должно дойти до конца, даже если вид освободили, пока оно шло:
// отзыв входов, отмена вызова. Значения контекста сохраняются, отмена родителя — нет; время ограничено timeout.
func (a *App) detachedCtx(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(a.baseContext()), timeout)
}
