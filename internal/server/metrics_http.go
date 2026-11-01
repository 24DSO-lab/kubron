// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"
	"strings"
	"time"
)

// RPCObserver принимает результаты RPC-вызовов (реализует internal/metrics). Пакет server от
// Prometheus не зависит.
type RPCObserver interface {
	ObserveRPC(method string, code int, d time.Duration)
	ObserveStatus(code int)
}

// unknownMethod — значение лейбла для имён, которых нет в allowlist: пользовательская строка в
// метрику не попадает (кардинальность и приватность).
const unknownMethod = "unknown"

// has — метод есть в allowlist диспетчера.
func (d *Dispatcher) has(name string) bool {
	_, ok := d.methods[name]
	return ok
}

// rpcPrefix — путь RPC-вызовов (см. NewHandler).
const rpcPrefix = "/api/rpc/"

// WithRPCObserver оборачивает готовый handler и учитывает RPC-вызовы (POST /api/rpc/{method}): число,
// коды ответов и задержку по имени метода. Остальные запросы, включая WebSocket, проходят мимо с тем же
// ResponseWriter (иначе пропадает http.Hijacker). Имя метода берётся из пути только если оно есть в
// allowlist диспетчера, иначе "unknown".
func WithRPCObserver(next http.Handler, d *Dispatcher, o RPCObserver) http.Handler {
	if o == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, rpcPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		method := strings.TrimPrefix(r.URL.Path, rpcPrefix)
		if d == nil || !d.has(method) {
			method = unknownMethod
		}
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		o.ObserveRPC(method, rec.code, time.Since(start))
		o.ObserveStatus(rec.code)
	})
}

// statusRecorder запоминает код ответа. Unwrap нужен http.ResponseController (Flush и др.).
type statusRecorder struct {
	http.ResponseWriter
	code        int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.code, s.wroteHeader = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
