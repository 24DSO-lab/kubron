// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("scrape: %d %s", rec.Code, rec.Body)
	}
	return rec.Body.String()
}

func full() Snapshot {
	return Snapshot{
		Sessions: 3, WSConns: 5, Terminals: 2, LogStreams: 4, PortForwards: 1,
		ClusterStates: map[string]int{"ok": 2, "unreachable": 1},
		HealthLeader:  true, LoginBlocked: 7, LockoutsStarted: 2, LoginSharedDegraded: 3,
		DB: &DBPool{Acquired: 1, Idle: 3, Total: 4, Max: 10, AcquireCount: 100, EmptyAcquireCount: 5, CanceledCount: 1, AcquireSeconds: 0.5},
	}
}

func TestScrapeSnapshot(t *testing.T) {
	m := New("v1.2.3", full)
	body := scrape(t, m)
	for _, want := range []string{
		`kubron_sessions_active 3`,
		`kubron_ws_connections 5`,
		`kubron_terminals_active 2`,
		`kubron_log_streams_active 4`,
		`kubron_port_forwards_active 1`,
		`kubron_clusters{state="ok"} 2`,
		`kubron_clusters{state="unreachable"} 1`,
		`kubron_clusters{state="unauthorized"} 0`, // весь набор состояний отдаётся всегда, включая нули
		`kubron_cluster_health_leader 1`,
		`kubron_draining 0`,
		`kubron_login_blocked_total 7`,
		`kubron_login_lockouts_total 2`,
		`kubron_login_lockout_degraded_total 3`,
		`kubron_db_pool_max_connections 10`,
		`kubron_db_pool_acquires_total 100`,
		`version="v1.2.3"`,
		`go_goroutines`, // стандартный сборщик Go подключён
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q", want)
		}
	}
}

func TestNoDatabaseMetricsWithoutDatabase(t *testing.T) {
	m := New("dev", func() Snapshot { return Snapshot{} })
	if body := scrape(t, m); strings.Contains(body, "kubron_db_pool_") {
		t.Error("file storage has no pool: db metrics must be absent")
	}
}

func TestRPCCountersAndHistogram(t *testing.T) {
	m := New("dev", func() Snapshot { return Snapshot{} })
	m.ObserveRPC("GetPods", 200, 20*time.Millisecond)
	m.ObserveRPC("GetPods", 403, time.Millisecond)
	m.ObserveRPC("unknown", 429, time.Millisecond)
	m.ObserveStatus(429)
	m.ObserveStatus(200)
	body := scrape(t, m)
	for _, want := range []string{
		`kubron_rpc_requests_total{code="200",method="GetPods"} 1`,
		`kubron_rpc_requests_total{code="403",method="GetPods"} 1`,
		`kubron_rpc_requests_total{code="429",method="unknown"} 1`,
		`kubron_rpc_request_duration_seconds_count{method="GetPods"} 2`,
		`kubron_http_rate_limited_total 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q", want)
		}
	}
}

// Приватность и кардинальность: ни одно значение из данных снимка не становится лейблом, а состояния
// кластеров берутся только из фиксированного набора.
func TestNoPrivateLabels(t *testing.T) {
	m := New("dev", func() Snapshot {
		s := full()
		s.ClusterStates = map[string]int{"ok": 1, "prod-cluster-of-alice": 1}
		return s
	})
	body := scrape(t, m)
	if strings.Contains(body, "alice") || strings.Contains(body, "prod-cluster") {
		t.Fatalf("a value outside the fixed state set leaked into the output:\n%s", body)
	}
}

func TestOnlyGetAndHead(t *testing.T) {
	m := New("dev", func() Snapshot { return Snapshot{} })
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /metrics: %d", rec.Code)
	}
}

func TestObserveOpLabelsAreBounded(t *testing.T) {
	m := New("dev", func() Snapshot { return Snapshot{} })
	m.ObserveOp("incident", "open", "ok", time.Millisecond)
	m.ObserveOp("incident", "open", "failed", time.Millisecond)
	// значения, не похожие на идентификаторы из кода, сворачиваются
	m.ObserveOp("alice@example.com", "prod-cluster-of-alice/ns", "weird status", time.Millisecond)
	body := scrape(t, m)
	for _, want := range []string{
		`kubron_ops_operations_total{module="incident",operation="open",status="ok"} 1`,
		`kubron_ops_operations_total{module="incident",operation="open",status="failed"} 1`,
		`kubron_ops_operations_total{module="other",operation="other",status="other"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "alice") || strings.Contains(body, "prod-cluster") {
		t.Fatalf("a private value leaked into labels:\n%s", body)
	}
}
