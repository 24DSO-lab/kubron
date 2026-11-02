// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func hostHandler(allowed []string, extra func(string) bool) (http.Handler, *int) {
	hits := new(int)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		_, _ = io.WriteString(w, "app")
	})
	return WithAllowedHosts(next, allowed, extra), hits
}

func getHost(h http.Handler, host, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Host = host
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAllowedHostsOwnForeignCasePortIPv6(t *testing.T) {
	allowed, err := ParseAllowedHosts("Kubron.Example.com, 10.0.0.5:8080 ,[::1], [2001:db8::1]:8443, strict.example.com:9443")
	if err != nil {
		t.Fatal(err)
	}
	h, hits := hostHandler(allowed, nil)

	ok := []string{
		"kubron.example.com", "KUBRON.EXAMPLE.COM", "Kubron.Example.Com:8443", "kubron.example.com.", "kubron.example.com.:443",
		"10.0.0.5:8080", "[::1]", "[::1]:3000", "[2001:db8::1]:8443", "[2001:DB8::1]:8443", "strict.example.com:9443",
	}
	for _, host := range ok {
		if w := getHost(h, host, "/"); w.Code != 200 || w.Body.String() != "app" {
			t.Errorf("%q must be allowed: %d", host, w.Code)
		}
	}
	bad := []string{
		"evil.example.com", "kubron.example.com.evil.test", "evil.test:8443", "xkubron.example.com", "sub.kubron.example.com",
		"10.0.0.5", "10.0.0.5:9090", "[2001:db8::1]", "[2001:db8::1]:80", "strict.example.com", "strict.example.com:80",
		"", "[::2]", "kubron.example.com:notaport", "kubron.example.com@evil.test", "kubron.example.com/../x", "127.0.0.1",
	}
	before := *hits
	var first string
	for _, host := range bad {
		w := getHost(h, host, "/api/session")
		if w.Code != http.StatusMisdirectedRequest {
			t.Errorf("%q must be rejected: %d", host, w.Code)
		}
		if first == "" {
			first = w.Body.String() + w.Header().Get("Content-Type")
		} else if w.Body.String()+w.Header().Get("Content-Type") != first {
			t.Errorf("%q: the answer must be identical for every rejected host", host)
		}
		if strings.Contains(w.Body.String(), "kubron.example.com") {
			t.Error("the answer must not reveal the list")
		}
	}
	if *hits != before {
		t.Error("a rejected host must never reach the application")
	}
}

func TestAllowedHostsRunBeforeRouting(t *testing.T) {
	d, _ := NewDispatcher(&guardTarget{}, []string{"Login", "AuthStatus"}, nil)
	app := NewHandler(d, nil, &Guard{Sessions: NewSessions(3600e9, true)})
	h := WithAllowedHosts(app, []string{"kubron.example.com"}, nil)
	for _, path := range []string{"/", "/api/rpc/Login", "/api/session", "/api/ws", "/nope"} {
		w := getHost(h, "evil.test", path)
		if w.Code != http.StatusMisdirectedRequest || w.Header().Get("Content-Security-Policy") != "" {
			t.Errorf("%s: rejected before routing, no application headers: %d", path, w.Code)
		}
	}
	if w := getHost(h, "kubron.example.com", "/healthz"); w.Code != 200 {
		t.Errorf("an allowed host: %d", w.Code)
	}
}

func TestAllowedHostsHealthProbesAreExempt(t *testing.T) {
	h, _ := hostHandler([]string{"kubron.example.com"}, nil)
	for _, p := range []string{"/healthz", "/readyz"} {
		if w := getHost(h, "10.1.2.3:8080", p); w.Code != 200 {
			t.Errorf("%s must work by the pod address: %d", p, w.Code)
		}
	}
	if w := getHost(h, "10.1.2.3:8080", "/healthz/x"); w.Code == 200 {
		t.Error("only the exact probe paths are exempt")
	}
}

func TestAllowedHostsDisabledWhenEmpty(t *testing.T) {
	h, _ := hostHandler(nil, nil)
	if w := getHost(h, "anything.example", "/"); w.Code != 200 {
		t.Fatalf("an empty list disables the check: %d", w.Code)
	}
}

func TestAllowedHostsPortForwardZoneAllowedAutomatically(t *testing.T) {
	zone := func(host string) bool {
		h := strings.ToLower(host)
		if i := strings.LastIndex(h, ":"); i >= 0 {
			h = h[:i]
		}
		return strings.HasSuffix(h, ".pf.kubron.example.com")
	}
	h, _ := hostHandler([]string{"kubron.example.com"}, zone)
	for _, host := range []string{"abc.pf.kubron.example.com", "abc.pf.kubron.example.com:8443", "ABC.PF.KUBRON.EXAMPLE.COM:8443"} {
		if w := getHost(h, host, "/"); w.Code != 200 {
			t.Errorf("%q: the stream zone is allowed: %d", host, w.Code)
		}
	}
	if w := getHost(h, "abc.pf.evil.test", "/"); w.Code != http.StatusMisdirectedRequest {
		t.Errorf("a foreign zone is rejected: %d", w.Code)
	}
	// Без port-forward (extra == nil) зона потоков не разрешена
	h2, _ := hostHandler([]string{"kubron.example.com"}, nil)
	if w := getHost(h2, "abc.pf.kubron.example.com", "/"); w.Code != http.StatusMisdirectedRequest {
		t.Errorf("without port-forward the zone is not allowed: %d", w.Code)
	}
}

func TestParseAllowedHosts(t *testing.T) {
	got, err := ParseAllowedHosts("")
	if err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	got, err = ParseAllowedHosts(" A.example.com. , ,[::1]:80,10.0.0.1 ")
	if err != nil || strings.Join(got, "|") != "a.example.com|[::1]:80|10.0.0.1" {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"https://a.example.com", "a.example.com/path", "*.example.com", "a.example.com:0", "a.example.com:99999",
		"::1", "a b", "user@a.example.com", "[::1", "a.example.com:x", ":8080"} {
		if _, err := ParseAllowedHosts(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestConfigAllowedHostsFromEnv(t *testing.T) {
	cfg, err := LoadConfig(nil, env(map[string]string{"KUBRON_ALLOWED_HOSTS": "Kubron.example.com,[::1]:8080"}), io.Discard)
	if err != nil || len(cfg.AllowedHosts) != 2 || cfg.AllowedHosts[0] != "kubron.example.com" {
		t.Fatalf("%+v %v", cfg.AllowedHosts, err)
	}
	if _, err := LoadConfig(nil, env(map[string]string{"KUBRON_ALLOWED_HOSTS": "https://x"}), io.Discard); err == nil {
		t.Fatal("a typo in the list must not silently disable the check")
	}
	if cfg, err := LoadConfig(nil, env(nil), io.Discard); err != nil || cfg.AllowedHosts != nil {
		t.Fatalf("unset means disabled: %+v %v", cfg.AllowedHosts, err)
	}
}
