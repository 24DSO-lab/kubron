// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"

	"github.com/24DSO-lab/kubron/internal/logging"
)

// requestIDHeader — заголовок идентификатора запроса (его ставят и прокси: ingress-nginx, Envoy).
const requestIDHeader = "X-Request-Id"

// WithRequestID присваивает каждому запросу идентификатор и кладёт его в контекст: записи журнала,
// сделанные с этим контекстом (slog.*Context), получают поле request_id. Идентификатор возвращается
// в заголовке ответа X-Request-Id, чтобы пользователь мог назвать его при обращении в поддержку.
//
// Присланный клиентом или прокси идентификатор принимается, только если он короткий и состоит из
// букв, цифр и «._-» (иначе им можно подделать запись журнала); в остальных случаях создаётся новый.
// ResponseWriter не подменяется: WebSocket и Flush работают как раньше.
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if !logging.ValidRequestID(id) {
			id = logging.NewRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(logging.WithRequestID(r.Context(), id)))
	})
}
