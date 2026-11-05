// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadConfigDefaultsEnvAndFlags(t *testing.T) {
	cfg, err := LoadConfig(nil, env(nil), io.Discard)
	if err != nil || cfg.Listen != "127.0.0.1:8080" || cfg.AllowRemote {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	cfg, err = LoadConfig(nil, env(map[string]string{"KUBRON_LISTEN": "127.0.0.1:9000"}), io.Discard)
	if err != nil || cfg.Listen != "127.0.0.1:9000" {
		t.Fatalf("env: %+v %v", cfg, err)
	}
	cfg, err = LoadConfig([]string{"--listen", "localhost:7000"}, env(map[string]string{"KUBRON_LISTEN": "127.0.0.1:9000"}), io.Discard)
	if err != nil || cfg.Listen != "localhost:7000" {
		t.Fatalf("flag takes precedence over env: %+v %v", cfg, err)
	}
	if _, err := LoadConfig([]string{"--unknown"}, env(nil), io.Discard); err == nil {
		t.Error("an unknown flag must produce an error")
	}
}

func TestRemoteListenNeedsExplicitAllow(t *testing.T) {
	for _, addr := range []string{":8080", "0.0.0.0:8080", "192.168.1.5:8080", "[::]:8080"} {
		if _, err := LoadConfig([]string{"--listen", addr}, env(nil), io.Discard); err == nil {
			t.Errorf("%s without --allow-remote must be rejected", addr)
		}
		if _, err := LoadConfig([]string{"--listen", addr, "--allow-remote"}, env(nil), io.Discard); err != nil {
			t.Errorf("%s with --allow-remote: %v", addr, err)
		}
	}
	for _, addr := range []string{"127.0.0.1:1", "localhost:1", "[::1]:1"} {
		if _, err := LoadConfig([]string{"--listen", addr}, env(nil), io.Discard); err != nil {
			t.Errorf("loopback %s: %v", addr, err)
		}
	}
	if _, err := LoadConfig([]string{"--listen", "bad"}, env(nil), io.Discard); err == nil {
		t.Error("an address without a port must be rejected")
	}
	cfg, err := LoadConfig(nil, env(map[string]string{"KUBRON_LISTEN": "0.0.0.0:80", "KUBRON_ALLOW_REMOTE": "1"}), io.Discard)
	if err != nil || !cfg.AllowRemote {
		t.Errorf("env allow-remote: %+v %v", cfg, err)
	}
}

func TestServeAndGracefulShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, testHandler(t)) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("healthz: %v %v", resp, err)
	}
	resp.Body.Close()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve after cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not stop after the context was canceled")
	}
}

func TestLoadConfigSessionAndProxy(t *testing.T) {
	cfg, err := LoadConfig(nil, env(nil), io.Discard)
	if err != nil || cfg.SessionTTL != 12*time.Hour || cfg.TrustProxy {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	cfg, err = LoadConfig([]string{"--session-ttl", "30m"}, env(map[string]string{"KUBRON_TRUST_PROXY": "1"}), io.Discard)
	if err != nil || cfg.SessionTTL != 30*time.Minute || !cfg.TrustProxy {
		t.Fatalf("flags and env: %+v %v", cfg, err)
	}
	for _, bad := range []map[string]string{{"KUBRON_SESSION_TTL": "fast"}, {"KUBRON_SESSION_TTL": "-1h"}} {
		if _, err := LoadConfig(nil, env(bad), io.Discard); err == nil {
			t.Errorf("%v: expected an error", bad)
		}
	}
}

func TestLoadConfigClusterCheckInterval(t *testing.T) {
	cfg, err := LoadConfig(nil, env(nil), io.Discard)
	if err != nil || cfg.ClusterCheckInterval != DefaultClusterCheckInterval {
		t.Fatalf("default: %+v %v", cfg, err)
	}
	cfg, err = LoadConfig(nil, env(map[string]string{"KUBRON_CLUSTER_CHECK_INTERVAL": "1m"}), io.Discard)
	if err != nil || cfg.ClusterCheckInterval != time.Minute {
		t.Fatalf("env: %+v %v", cfg, err)
	}
	cfg, err = LoadConfig([]string{"--cluster-check-interval", "0"}, env(map[string]string{"KUBRON_CLUSTER_CHECK_INTERVAL": "1m"}), io.Discard)
	if err != nil || cfg.ClusterCheckInterval != 0 {
		t.Fatalf("0 turns the check off: %+v %v", cfg, err)
	}
	for _, bad := range []string{"often", "-5s", "1s"} {
		if _, err := LoadConfig(nil, env(map[string]string{"KUBRON_CLUSTER_CHECK_INTERVAL": bad}), io.Discard); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestLoadConfigDataDir(t *testing.T) {
	cfg, err := LoadConfig(nil, env(map[string]string{"KUBRON_DATA_DIR": "/srv/kubron"}), io.Discard)
	if err != nil || cfg.DataDir != "/srv/kubron" {
		t.Fatalf("env: %+v %v", cfg, err)
	}
	cfg, err = LoadConfig([]string{"--data-dir", "/tmp/x"}, env(map[string]string{"KUBRON_DATA_DIR": "/srv/kubron"}), io.Discard)
	if err != nil || cfg.DataDir != "/tmp/x" {
		t.Fatalf("flag takes precedence over env: %+v %v", cfg, err)
	}
	if cfg, _ = LoadConfig(nil, env(nil), io.Discard); cfg.DataDir != "" {
		t.Errorf("empty by default: %q", cfg.DataDir)
	}
}

func TestLoadConfigResourceLimits(t *testing.T) {
	cfg, err := LoadConfig(nil, env(nil), io.Discard)
	if err != nil || cfg.MaxTerminals != 8 || cfg.MaxLogStreams != 20 || cfg.MaxPortForwards != 10 {
		t.Fatalf("default values: %+v %v", cfg, err)
	}
	cfg, err = LoadConfig([]string{"--max-log-streams", "5"},
		env(map[string]string{"KUBRON_MAX_TERMINALS": "3", "KUBRON_MAX_LOG_STREAMS": "9", "KUBRON_MAX_PORT_FORWARDS": "0"}), io.Discard)
	if err != nil || cfg.MaxTerminals != 3 || cfg.MaxLogStreams != 5 || cfg.MaxPortForwards != 0 {
		t.Fatalf("env and flags (flag takes precedence over env, 0 means unlimited): %+v %v", cfg, err)
	}
	if _, err := LoadConfig(nil, env(map[string]string{"KUBRON_MAX_TERMINALS": "many"}), io.Discard); err == nil {
		t.Error("a non-numeric value must be rejected")
	}
	if _, err := LoadConfig([]string{"--max-terminals", "-1"}, env(nil), io.Discard); err == nil {
		t.Error("a negative limit must be rejected")
	}
}

func TestLoadConfigDatabase(t *testing.T) {
	env := map[string]string{
		"KUBRON_DATABASE_URL":        "postgres://u:p@db/kubron",
		"KUBRON_SECRET_KEY":          " k2:abc ",
		"KUBRON_SECRET_KEY_PREVIOUS": "k1:xyz, ,k0:qqq",
	}
	cfg, err := LoadConfig(nil, func(k string) string { return env[k] }, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseURL != env["KUBRON_DATABASE_URL"] || cfg.SecretKey != "k2:abc" ||
		len(cfg.PreviousSecretKeys) != 2 || cfg.PreviousSecretKeys[1] != "k0:qqq" {
		t.Errorf("%+v", cfg)
	}
	// Флаг переопределяет окружение; без ключа с БД работать нельзя.
	if _, err := LoadConfig([]string{"--database-url", "postgres://x/y"}, func(string) string { return "" }, io.Discard); err == nil {
		t.Error("база без KUBRON_SECRET_KEY должна отклоняться")
	}
	// Без БД ключ не нужен (файловое хранилище).
	if _, err := LoadConfig(nil, func(string) string { return "" }, io.Discard); err != nil {
		t.Error(err)
	}
}

func TestLoadConfigProxyTrustAndCSP(t *testing.T) {
	cfg, err := LoadConfig(nil, env(nil), io.Discard)
	if err != nil || cfg.TrustedProxyHops != 1 || len(cfg.TrustedProxies) != 0 || cfg.CSPReportOnly {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	cfg, err = LoadConfig(nil, env(map[string]string{
		"KUBRON_TRUST_PROXY": "1", "KUBRON_TRUSTED_PROXIES": "10.0.0.0/8, 192.168.0.1", "KUBRON_CSP_REPORT_ONLY": "1",
	}), io.Discard)
	if err != nil || len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[1].String() != "192.168.0.1/32" || !cfg.CSPReportOnly {
		t.Fatalf("env: %+v %v", cfg, err)
	}
	cfg, err = LoadConfig([]string{"--trusted-proxy-hops", "2", "--csp-report-only"}, env(map[string]string{"KUBRON_TRUST_PROXY": "1"}), io.Discard)
	if err != nil || cfg.TrustedProxyHops != 2 || !cfg.CSPReportOnly {
		t.Fatalf("flags: %+v %v", cfg, err)
	}
	cfg, err = LoadConfig(nil, env(map[string]string{"KUBRON_TRUST_PROXY": "1", "KUBRON_TRUSTED_PROXY_HOPS": "3"}), io.Discard)
	if err != nil || cfg.TrustedProxyHops != 3 {
		t.Fatalf("hops from env: %+v %v", cfg, err)
	}
	for name, bad := range map[string]map[string]string{
		"proxies without trust-proxy": {"KUBRON_TRUSTED_PROXIES": "10.0.0.0/8"},
		"hops without trust-proxy":    {"KUBRON_TRUSTED_PROXY_HOPS": "2"},
		"malformed CIDR":              {"KUBRON_TRUST_PROXY": "1", "KUBRON_TRUSTED_PROXIES": "10.0.0.0/99"},
		"not an address":              {"KUBRON_TRUST_PROXY": "1", "KUBRON_TRUSTED_PROXIES": "proxy.example.com"},
		"hops not a number":           {"KUBRON_TRUST_PROXY": "1", "KUBRON_TRUSTED_PROXY_HOPS": "many"},
		"negative hops":               {"KUBRON_TRUST_PROXY": "1", "KUBRON_TRUSTED_PROXY_HOPS": "-1"},
	} {
		if _, err := LoadConfig(nil, env(bad), io.Discard); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestAuditRetentionConfig(t *testing.T) {
	cfg, err := LoadConfig(nil, env(nil), io.Discard)
	if err != nil || cfg.AuditRetention != 0 {
		t.Fatalf("default must keep the audit log forever: %v %v", cfg.AuditRetention, err)
	}
	cfg, err = LoadConfig(nil, env(map[string]string{"KUBRON_AUDIT_RETENTION": "2160h"}), io.Discard)
	if err != nil || cfg.AuditRetention != 2160*time.Hour {
		t.Fatalf("env: %v %v", cfg.AuditRetention, err)
	}
	cfg, err = LoadConfig([]string{"--audit-retention", "48h"}, env(map[string]string{"KUBRON_AUDIT_RETENTION": "2160h"}), io.Discard)
	if err != nil || cfg.AuditRetention != 48*time.Hour {
		t.Fatalf("flag must win: %v %v", cfg.AuditRetention, err)
	}
	for _, bad := range []string{"90", "90d", "-24h", "1h"} { // без единиц, дни не поддерживаются, отрицательное, меньше суток
		if _, err := LoadConfig(nil, env(map[string]string{"KUBRON_AUDIT_RETENTION": bad}), io.Discard); err == nil {
			t.Errorf("KUBRON_AUDIT_RETENTION=%q must be rejected", bad)
		}
	}
}

func TestLoadConfigMetricsListen(t *testing.T) {
	cfg, err := LoadConfig(nil, env(nil), io.Discard)
	if err != nil || cfg.MetricsListen != "" {
		t.Fatalf("metrics must be off by default: %+v %v", cfg, err)
	}
	cfg, err = LoadConfig(nil, env(map[string]string{"KUBRON_METRICS_LISTEN": " 127.0.0.1:9090 "}), io.Discard)
	if err != nil || cfg.MetricsListen != "127.0.0.1:9090" {
		t.Fatalf("env: %+v %v", cfg, err)
	}
	cfg, err = LoadConfig([]string{"--metrics-listen", "127.0.0.1:9100"}, env(map[string]string{"KUBRON_METRICS_LISTEN": "127.0.0.1:9090"}), io.Discard)
	if err != nil || cfg.MetricsListen != "127.0.0.1:9100" {
		t.Fatalf("flag takes precedence over env: %+v %v", cfg, err)
	}
	// Метрики на публичном порту не допускаются; адрес без порта — ошибка конфигурации
	if _, err := LoadConfig([]string{"--listen", "127.0.0.1:8080", "--metrics-listen", "127.0.0.1:8080"}, env(nil), io.Discard); err == nil {
		t.Error("metrics on the main listen address must be rejected")
	}
	if _, err := LoadConfig([]string{"--metrics-listen", "not-an-address"}, env(nil), io.Discard); err == nil {
		t.Error("a metrics address without a port must be rejected")
	}
}

func TestLoadConfigPublicURL(t *testing.T) {
	if cfg, err := LoadConfig(nil, env(nil), io.Discard); err != nil || cfg.PublicURL != "" {
		t.Fatalf("empty public URL should disable browser callbacks: %+v %v", cfg, err)
	}

	for _, value := range []string{
		"https://kubron.example.com",
		"https://kubron.example.com/",
		"http://localhost:8080",
		"http://dev.localhost:8080",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
	} {
		cfg, err := LoadConfig(nil, env(map[string]string{"KUBRON_PUBLIC_URL": value}), io.Discard)
		if err != nil || cfg.PublicURL != value {
			t.Errorf("KUBRON_PUBLIC_URL=%q: cfg=%+v err=%v", value, cfg, err)
		}
	}

	for _, value := range []string{
		"kubron.example.com",
		"https://",
		"ftp://kubron.example.com",
		"http://kubron.example.com",
		"http://192.0.2.10",
		"https://user:password@kubron.example.com",
		"https://kubron.example.com?next=/",
		"https://kubron.example.com#callback",
		"https://kubron.example.com/prefix",
		"https://kubron.example.com:bad",
		"https://kubron.example.com:0",
		"https://kubron.example.com:65536",
		"https://kubron.example.com#",
	} {
		if _, err := LoadConfig(nil, env(map[string]string{"KUBRON_PUBLIC_URL": value}), io.Discard); err == nil {
			t.Errorf("KUBRON_PUBLIC_URL=%q: expected validation error", value)
		}
	}
}
