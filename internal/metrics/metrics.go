// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package metrics — метрики Prometheus сервера Kubron.
//
// Правила содержимого (приватность и кардинальность): в значениях лейблов нет имён пользователей,
// команд, кластеров, пространств имён и идентификаторов сессий. Допустимые лейблы — имя RPC-метода из
// allowlist диспетчера (или "unknown"), код HTTP-ответа и состояние проверки кластера из фиксированного
// набора. Тест TestNoPrivateLabels следит за этим.
//
// Пакет не знает про app, pgstore и server: данные приходят через Snapshot, который собирает
// cmd/kubron-server. Снимок берётся один раз за скрейп, поэтому метрики согласованы между собой.
package metrics

import (
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Namespace — префикс всех метрик.
const Namespace = "kubron"

// ClusterStates — фиксированный набор состояний (clusterhealth.State): метрика всегда отдаёт их все,
// в том числе нули, чтобы алерты не зависели от «пропавшего ряда».
var ClusterStates = []string{"ok", "unknown", "unreachable", "unauthorized", "misconfigured"}

// DBPool — статистика пула соединений с БД; nil в снимке — БД не используется (файловое хранилище).
type DBPool struct {
	Acquired, Idle, Total, Max                     int32
	AcquireCount, EmptyAcquireCount, CanceledCount int64
	AcquireSeconds                                 float64
}

// Snapshot — значения, которые читаются при скрейпе.
type Snapshot struct {
	Sessions, WSConns, Terminals, LogStreams, PortForwards int
	ClusterStates                                          map[string]int
	HealthLeader                                           bool
	Draining                                               bool
	LoginBlocked, LockoutsStarted, LoginSharedDegraded     int64
	DB                                                     *DBPool
}

// Metrics — реестр и счётчики запросов.
type Metrics struct {
	reg         *prometheus.Registry
	rpcTotal    *prometheus.CounterVec
	rpcDuration *prometheus.HistogramVec
	rateLimited prometheus.Counter
}

// New создаёт реестр. version — версия сборки (build info); snap вызывается при каждом скрейпе.
func New(version string, snap func() Snapshot) *Metrics {
	m := &Metrics{reg: prometheus.NewRegistry()}
	m.rpcTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "rpc_requests_total",
		Help: "RPC requests by method and HTTP status code.",
	}, []string{"method", "code"})
	m.rpcDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: Namespace, Name: "rpc_request_duration_seconds",
		Help:    "RPC request duration by method.",
		Buckets: []float64{.005, .025, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{"method"})
	m.rateLimited = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: Namespace, Name: "http_rate_limited_total",
		Help: "Responses with status 429 (any endpoint).",
	})
	m.reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.rpcTotal, m.rpcDuration, m.rateLimited,
		newSnapshotCollector(version, snap),
	)
	return m
}

// ObserveRPC учитывает один RPC-вызов. method обязан быть из allowlist или "unknown": значение
// пользователя в лейбл не попадает.
func (m *Metrics) ObserveRPC(method string, code int, d time.Duration) {
	m.rpcTotal.WithLabelValues(method, strconv.Itoa(code)).Inc()
	m.rpcDuration.WithLabelValues(method).Observe(d.Seconds())
}

// ObserveStatus учитывает ответ любого эндпоинта (сейчас только 429).
func (m *Metrics) ObserveStatus(code int) {
	if code == http.StatusTooManyRequests {
		m.rateLimited.Inc()
	}
}

// Registry — реестр (тесты, расширения).
func (m *Metrics) Registry() *prometheus.Registry { return m.reg }

// Handler отдаёт метрики в текстовом формате Prometheus. Только GET и HEAD; Accept-Encoding gzip
// включён по умолчанию.
func (m *Metrics) Handler() http.Handler {
	h := promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{Registry: m.reg, MaxRequestsInFlight: 4})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// snapshotCollector превращает Snapshot в метрики при каждом скрейпе.
type snapshotCollector struct {
	version string
	snap    func() Snapshot
	d       map[string]*prometheus.Desc
}

func newSnapshotCollector(version string, snap func() Snapshot) *snapshotCollector {
	desc := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(Namespace, "", name), help, labels, nil)
	}
	c := &snapshotCollector{version: version, snap: snap, d: map[string]*prometheus.Desc{}}
	for name, v := range map[string]*prometheus.Desc{
		"build_info":      desc("build_info", "Build information; the value is always 1.", "version", "go_version"),
		"sessions":        desc("sessions_active", "Active web sessions on this replica."),
		"ws":              desc("ws_connections", "Open event WebSocket connections on this replica."),
		"terminals":       desc("terminals_active", "Open terminals on this replica."),
		"logs":            desc("log_streams_active", "Running log streams on this replica."),
		"pf":              desc("port_forwards_active", "Active port-forwards on this replica."),
		"clusters":        desc("clusters", "Clusters by last health check state.", "state"),
		"leader":          desc("cluster_health_leader", "1 if this replica runs cluster health checks."),
		"draining":        desc("draining", "1 if this replica is shutting down (readiness reports 503)."),
		"login_blocked":   desc("login_blocked_total", "Login attempts rejected by a lockout."),
		"lockouts":        desc("login_lockouts_total", "Lockouts started after repeated failures."),
		"degraded":        desc("login_lockout_degraded_total", "Login lockout checks and failure records done without the shared store (store unavailable, local lockouts only)."),
		"db_acquired":     desc("db_pool_acquired_connections", "Connections in use."),
		"db_idle":         desc("db_pool_idle_connections", "Idle connections."),
		"db_total":        desc("db_pool_total_connections", "Connections in the pool."),
		"db_max":          desc("db_pool_max_connections", "Configured pool size."),
		"db_acquires":     desc("db_pool_acquires_total", "Connection acquisitions."),
		"db_empty":        desc("db_pool_empty_acquires_total", "Acquisitions that had to wait for a connection."),
		"db_canceled":     desc("db_pool_canceled_acquires_total", "Acquisitions canceled while waiting."),
		"db_acquire_wait": desc("db_pool_acquire_wait_seconds_total", "Total time spent waiting for connections."),
	} {
		c.d[name] = v
	}
	return c
}

func (c *snapshotCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range c.d {
		ch <- d
	}
}

func (c *snapshotCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.snap()
	g := func(k string, v float64, lv ...string) {
		ch <- prometheus.MustNewConstMetric(c.d[k], prometheus.GaugeValue, v, lv...)
	}
	ct := func(k string, v float64) { ch <- prometheus.MustNewConstMetric(c.d[k], prometheus.CounterValue, v) }
	b := func(v bool) float64 {
		if v {
			return 1
		}
		return 0
	}
	g("build_info", 1, c.version, runtime.Version())
	g("sessions", float64(s.Sessions))
	g("ws", float64(s.WSConns))
	g("terminals", float64(s.Terminals))
	g("logs", float64(s.LogStreams))
	g("pf", float64(s.PortForwards))
	for _, st := range ClusterStates {
		g("clusters", float64(s.ClusterStates[st]), st)
	}
	g("leader", b(s.HealthLeader))
	g("draining", b(s.Draining))
	ct("login_blocked", float64(s.LoginBlocked))
	ct("lockouts", float64(s.LockoutsStarted))
	ct("degraded", float64(s.LoginSharedDegraded))
	if d := s.DB; d != nil {
		g("db_acquired", float64(d.Acquired))
		g("db_idle", float64(d.Idle))
		g("db_total", float64(d.Total))
		g("db_max", float64(d.Max))
		ct("db_acquires", float64(d.AcquireCount))
		ct("db_empty", float64(d.EmptyAcquireCount))
		ct("db_canceled", float64(d.CanceledCount))
		ct("db_acquire_wait", d.AcquireSeconds)
	}
}
