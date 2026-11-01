// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// DBPinger — всё, что /readyz требует от хранилища: проверить, что БД отвечает. Интерфейс маленький
// намеренно: пакет server не знает про pgstore, адаптер подставляется из cmd/kubron-server.
type DBPinger interface {
	Ping(ctx context.Context) error
}

// DBPingFunc превращает функцию в DBPinger.
type DBPingFunc func(ctx context.Context) error

// Ping вызывает f.
func (f DBPingFunc) Ping(ctx context.Context) error { return f(ctx) }

// ReadyzConfig настраивает проверку готовности.
type ReadyzConfig struct {
	// DB проверяется при каждом запросе (с кэшем на CacheTTL). nil — БД не используется (файловое
	// хранилище, десктоп): проверка пропускается и не влияет на готовность.
	DB DBPinger
	// Drain — признак остановки реплики (Lifecycle). nil — реплика никогда не «останавливается».
	Drain Drainer
	// Timeout — предел одной проверки БД; по умолчанию 2 с (меньше timeoutSeconds пробы).
	Timeout time.Duration
	// CacheTTL — сколько держать результат проверки БД, чтобы частые пробы (kubelet, балансировщик,
	// несколько наблюдателей) не превращались в нагрузку на БД; по умолчанию 1 с.
	CacheTTL time.Duration
	// Logf — необязательный журнал; пишет только при смене состояния готовности.
	Logf func(format string, args ...any)
}

// Статусы проверок в ответе. Текст ошибки БД намеренно не отдаётся: /readyz открыт без входа.
const (
	checkOK      = "ok"
	checkFail    = "fail"
	checkSkipped = "skipped"
)

type readyzResponse struct {
	Status   string            `json:"status"` // ready | not_ready
	Draining bool              `json:"draining"`
	Checks   map[string]string `json:"checks"`
}

type readyz struct {
	cfg ReadyzConfig

	mu       sync.Mutex
	checked  time.Time
	dbErr    error
	lastGood *bool // предыдущее итоговое состояние: для журнала переходов
}

// ReadyzHandler возвращает обработчик GET /readyz: 200 и {"status":"ready"}, если реплика принимает
// трафик; 503 и {"status":"not_ready"}, если она останавливается (draining) или БД недоступна.
func ReadyzHandler(cfg ReadyzConfig) http.Handler {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Second
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = time.Second
	}
	rz := &readyz{cfg: cfg}
	return http.HandlerFunc(rz.serve)
}

func (rz *readyz) serve(w http.ResponseWriter, r *http.Request) {
	resp := readyzResponse{Status: "ready", Checks: map[string]string{"database": checkSkipped}}
	if rz.cfg.Drain != nil && rz.cfg.Drain.Draining() {
		resp.Draining = true
		resp.Status = "not_ready"
	}
	if rz.cfg.DB != nil {
		if err := rz.pingDB(r.Context()); err != nil {
			resp.Checks["database"] = checkFail
			resp.Status = "not_ready"
		} else {
			resp.Checks["database"] = checkOK
		}
	}
	rz.logTransition(resp)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	code := http.StatusOK
	if resp.Status != "ready" {
		code = http.StatusServiceUnavailable
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(resp)
}

// pingDB выполняет проверку не чаще, чем раз в CacheTTL. Запросы в течение окна получают общий
// результат; окно не продлевается отменённым запросом (ошибка контекста клиента не кэшируется).
func (rz *readyz) pingDB(ctx context.Context) error {
	rz.mu.Lock()
	defer rz.mu.Unlock()
	if !rz.checked.IsZero() && time.Since(rz.checked) < rz.cfg.CacheTTL {
		return rz.dbErr
	}
	// Контекст отвязан от запроса: проверку может ждать несколько проб, обрыв одной не должен
	// записать «БД недоступна» в общий кэш.
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rz.cfg.Timeout)
	defer cancel()
	rz.dbErr = rz.cfg.DB.Ping(pctx)
	rz.checked = time.Now()
	return rz.dbErr
}

func (rz *readyz) logTransition(resp readyzResponse) {
	if rz.cfg.Logf == nil {
		return
	}
	ready := resp.Status == "ready"
	rz.mu.Lock()
	changed := rz.lastGood == nil || *rz.lastGood != ready
	rz.lastGood = &ready
	err := rz.dbErr
	rz.mu.Unlock()
	if !changed {
		return
	}
	if ready {
		rz.cfg.Logf("readyz: ready")
		return
	}
	rz.cfg.Logf("readyz: not ready (draining=%t, database=%s, db error: %v)", resp.Draining, resp.Checks["database"], err)
}

// registerReadyz подключает /readyz; вынесено из NewHandler, чтобы не расти server.go.
func registerReadyz(mux *http.ServeMux, g *Guard, o handlerOptions) {
	cfg := o.readyz
	if cfg.Drain == nil && g != nil && g.Lifecycle != nil {
		cfg.Drain = g.Lifecycle
	}
	mux.Handle("GET /readyz", ReadyzHandler(cfg))
}
