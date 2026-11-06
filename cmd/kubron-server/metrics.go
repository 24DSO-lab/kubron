// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/24DSO-labs/kubron/internal/app"
	"github.com/24DSO-labs/kubron/internal/metrics"
	"github.com/24DSO-labs/kubron/internal/pgstore"
	"github.com/24DSO-labs/kubron/internal/server"
)

// metricsDeps — то, из чего собираются метрики. Всё читается при скрейпе.
type metricsDeps struct {
	Core      *app.App
	Sessions  *server.Sessions
	Hub       *server.Hub
	Terminals interface{ Len() int }
	PG        *pgstore.Store // nil — файловое хранилище
	Lifecycle *server.Lifecycle
}

// setupMetrics включает /metrics, если задан KUBRON_METRICS_LISTEN: запускает отдельный листенер и
// возвращает handler с учётом RPC-вызовов. Без адреса возвращает handler как есть (метрики выключены,
// накладных расходов нет). Порт метрик — не публичный: Ingress на него не смотрит.
func setupMetrics(ctx context.Context, cfg server.Config, d metricsDeps, handler http.Handler, disp *server.Dispatcher) http.Handler {
	if cfg.MetricsListen == "" {
		return handler
	}
	m := metrics.New(version, func() metrics.Snapshot { return snapshot(d) })
	ln, err := net.Listen("tcp", cfg.MetricsListen)
	if err != nil {
		log.Fatalf("metrics: %v", err) // адрес задан явно: молча работать без метрик нельзя
	}
	go serveMetrics(ctx, ln, m.Handler())
	log.Printf("metrics: listening on %s (separate port, not served by the main listener)", ln.Addr())
	return server.WithRPCObserver(handler, disp, m)
}

func snapshot(d metricsDeps) metrics.Snapshot {
	st := app.CollectStats(d.Core, d.Sessions.Views())
	s := metrics.Snapshot{
		Sessions:        d.Sessions.Len(),
		WSConns:         d.Hub.Len(),
		Terminals:       d.Terminals.Len(),
		LogStreams:      st.LogStreams,
		PortForwards:    st.PortForwards,
		ClusterStates:   st.ClusterStates,
		HealthLeader:    st.HealthLeader,
		Draining:        d.Lifecycle.Draining(),
		LoginBlocked:    st.LoginBlocked,
		LockoutsStarted: st.LockoutsStarted,

		LoginSharedDegraded: st.SharedDegraded,
	}
	if d.PG != nil {
		p := d.PG.PoolStats()
		s.DB = &metrics.DBPool{
			Acquired: p.AcquiredConns, Idle: p.IdleConns, Total: p.TotalConns, Max: p.MaxConns,
			AcquireCount: p.AcquireCount, EmptyAcquireCount: p.EmptyAcquireCount, CanceledCount: p.CanceledAcquireCount,
			AcquireSeconds: p.AcquireDuration.Seconds(),
		}
	}
	return s
}

// serveMetrics обслуживает только GET /metrics; любой другой путь — 404. Таймауты защищают порт от
// медленных клиентов.
func serveMetrics(ctx context.Context, ln net.Listener, h http.Handler) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", h)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("metrics: %v", err)
	}
}
