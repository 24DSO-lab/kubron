// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestValidateReplicaID(t *testing.T) {
	for _, ok := range []string{"a", "kubron-0", "Kubron_Pod-7f9c", strings.Repeat("a", MaxReplicaIDLen)} {
		if err := ValidateReplicaID(ok); err != nil {
			t.Errorf("%q must be valid: %v", ok, err)
		}
	}
	// точка запрещена: она отделяет replica_id от случайной части идентификатора потока
	for _, bad := range []string{"", "a.b", "a b", "a/b", "ж", strings.Repeat("a", MaxReplicaIDLen+1)} {
		if err := ValidateReplicaID(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestAutoReplicaID(t *testing.T) {
	cases := []struct{ host, want string }{
		{"kubron-7d9f", "kubron-7d9f-0a1b2c3d"},
		{"node1.example.com", "node1-example-com-0a1b2c3d"},
		{"", "kubron-0a1b2c3d"},
		{"...", "kubron-0a1b2c3d"},
	}
	for _, c := range cases {
		if got := autoReplicaID(c.host, "0a1b2c3d"); got != c.want {
			t.Errorf("autoReplicaID(%q) = %q, want %q", c.host, got, c.want)
		}
	}
	long := autoReplicaID(strings.Repeat("h", 200), "0a1b2c3d")
	if len(long) > MaxReplicaIDLen {
		t.Errorf("a long host name must be shortened: %d characters", len(long))
	}
	if err := ValidateReplicaID(long); err != nil {
		t.Errorf("the automatic identifier must pass validation: %v", err)
	}
}

func TestResolveReplicaID(t *testing.T) {
	id, err := ResolveReplicaID("fixed-1", "ignored")
	if err != nil || id != "fixed-1" {
		t.Fatalf("explicit: %q %v", id, err)
	}
	if _, err := ResolveReplicaID("bad.id", "h"); err == nil {
		t.Error("an invalid explicit identifier must be an error")
	}
	// перезапущенный под с тем же именем получает другой идентификатор
	a, err1 := ResolveReplicaID("", "pod-1")
	b, err2 := ResolveReplicaID("", "pod-1")
	if err1 != nil || err2 != nil || a == b || !strings.HasPrefix(a, "pod-1-") {
		t.Errorf("auto: %q %q %v %v", a, b, err1, err2)
	}
	if err := ValidateReplicaID(a); err != nil {
		t.Errorf("auto identifier: %v", err)
	}
}

func TestConfigReplicaAndDrainDelay(t *testing.T) {
	cfg, err := LoadConfig(nil, env(map[string]string{"KUBRON_REPLICA_ID": " r-1 ", "KUBRON_DRAIN_DELAY": "5s"}), io.Discard)
	if err != nil || cfg.ReplicaID != "r-1" || cfg.DrainDelay != 5*time.Second {
		t.Fatalf("env: %+v %v", cfg, err)
	}
	cfg, err = LoadConfig([]string{"--replica-id", "r-2", "--drain-delay", "2s"}, env(map[string]string{"KUBRON_REPLICA_ID": "r-1"}), io.Discard)
	if err != nil || cfg.ReplicaID != "r-2" || cfg.DrainDelay != 2*time.Second {
		t.Fatalf("flags take precedence: %+v %v", cfg, err)
	}
	cfg, err = LoadConfig(nil, env(nil), io.Discard)
	if err != nil || cfg.ReplicaID != "" || cfg.DrainDelay != 0 {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	for name, m := range map[string]map[string]string{
		"replica id with a dot": {"KUBRON_REPLICA_ID": "a.b"},
		"negative delay":        {"KUBRON_DRAIN_DELAY": "-1s"},
		"delay above the limit": {"KUBRON_DRAIN_DELAY": "2m"},
		"unparsable delay":      {"KUBRON_DRAIN_DELAY": "soon"},
	} {
		if _, err := LoadConfig(nil, env(m), io.Discard); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}
