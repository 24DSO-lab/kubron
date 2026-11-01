// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPortForwardHostsNeverReachTheMainHandler(t *testing.T) {
	main := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "main") })
	pf := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "pf") })
	h := WithPortForwardHosts(main, pf, func(host string) bool { return strings.HasSuffix(host, ".pf.kubron.test") })

	for host, want := range map[string]string{"kubron.test": "main", "abc.pf.kubron.test": "pf", "evil.pf.kubron.test": "pf"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Body.String() != want {
			t.Errorf("%s: %q, want %q", host, w.Body.String(), want)
		}
	}
}

func TestPortForwardConfig(t *testing.T) {
	cfg, err := LoadConfig(nil, env(map[string]string{
		"KUBRON_PF_BASE_DOMAIN": "kubron.example.com", "KUBRON_PF_IDLE_TIMEOUT": "5m", "KUBRON_PF_MAX_DURATION": "2h",
	}), io.Discard)
	if err != nil || cfg.PFBaseDomain != "kubron.example.com" || cfg.PFIdleTimeout != 5*time.Minute || cfg.PFMaxDuration != 2*time.Hour {
		t.Fatalf("%+v %v", cfg, err)
	}
	for name, e := range map[string]map[string]string{
		"bad scheme":        {"KUBRON_PF_SCHEME": "ftp"},
		"http on real host": {"KUBRON_PF_SCHEME": "http", "KUBRON_PF_BASE_DOMAIN": "kubron.example.com"},
		"bad duration":      {"KUBRON_PF_IDLE_TIMEOUT": "soon"},
		"negative":          {"KUBRON_PF_MAX_DURATION": "-1h"},
	} {
		if _, err := LoadConfig(nil, env(e), io.Discard); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	if _, err := LoadConfig(nil, env(map[string]string{"KUBRON_PF_SCHEME": "http", "KUBRON_PF_BASE_DOMAIN": "localhost:8080"}), io.Discard); err != nil {
		t.Errorf("http on localhost: %v", err)
	}
}

func TestServiceMode(t *testing.T) {
	cases := []struct {
		cfg  Config
		want bool
	}{
		{Config{Listen: "127.0.0.1:8080"}, false},
		{Config{Listen: "localhost:8080"}, false},
		{Config{Listen: "127.0.0.1:8080", DatabaseURL: "postgres://x"}, true},
		{Config{Listen: "0.0.0.0:8080", AllowRemote: true}, true},
		{Config{Listen: ":8080"}, true},
	}
	for _, c := range cases {
		if got := c.cfg.ServiceMode(); got != c.want {
			t.Errorf("%+v: %v", c.cfg, got)
		}
	}
}

func TestSessionsAlive(t *testing.T) {
	s := NewSessions(time.Hour, false)
	_, sess := s.Create("u1", nil)
	if !s.Alive(sess.ID) || s.Alive("nope") {
		t.Fatal("Alive")
	}
}
