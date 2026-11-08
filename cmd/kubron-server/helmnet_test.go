// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/netpolicy"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestSetupHelmNetPolicy(t *testing.T) {
	t.Cleanup(func() { netpolicy.Set(netpolicy.ScopeHelm, nil) })

	// По умолчанию на сервере действует strict: частные адреса и loopback закрыты.
	if err := setupHelmNetPolicy(envOf(nil)); err != nil {
		t.Fatal(err)
	}
	p := netpolicy.For(netpolicy.ScopeHelm)
	if p == nil || p.Profile != netpolicy.ProfileStrict {
		t.Fatalf("strict by default: %+v", p)
	}
	for _, u := range []string{"http://10.0.0.5/index.yaml", "http://127.0.0.1/", "http://169.254.169.254/"} {
		if p.CheckURL(u) == nil {
			t.Errorf("%s must be blocked by default", u)
		}
	}

	// Исключение оператора открывает свою сеть, но не метаданные.
	if err := setupHelmNetPolicy(envOf(map[string]string{envHelmAllowCIDRs: "10.20.0.0/16, 169.254.0.0/16"})); err != nil {
		t.Fatal(err)
	}
	p = netpolicy.For(netpolicy.ScopeHelm)
	if p.CheckURL("http://10.20.1.1/") != nil || p.CheckURL("http://10.30.1.1/") == nil || p.CheckURL("http://169.254.169.254/") == nil {
		t.Error("the allow list must open only its own ranges, never cloud metadata")
	}

	// off снимает проверку совсем.
	if err := setupHelmNetPolicy(envOf(map[string]string{envHelmAddrPolicy: "off"})); err != nil {
		t.Fatal(err)
	}
	if netpolicy.For(netpolicy.ScopeHelm) != nil {
		t.Error("off must leave no policy")
	}

	// Опечатка в настройке не должна молча выключать защиту.
	if err := setupHelmNetPolicy(envOf(map[string]string{envHelmAddrPolicy: "strikt"})); err == nil {
		t.Error("an unknown profile must be an error")
	}
	if err := setupHelmNetPolicy(envOf(map[string]string{envHelmAllowCIDRs: "10.0.0.0/99"})); err == nil {
		t.Error("an invalid CIDR must be an error")
	}
}

func TestSetupClusterAddrPolicy(t *testing.T) {
	t.Cleanup(func() { netpolicy.Set(netpolicy.ScopeClusterAPI, nil) })

	// Режим одного пользователя на loopback: по умолчанию выключена.
	if err := setupClusterAddrPolicy(envOf(nil), false); err != nil {
		t.Fatal(err)
	}
	if netpolicy.For(netpolicy.ScopeClusterAPI) != nil {
		t.Fatal("the cluster address policy must be off by default")
	}

	// permissive закрывает loopback и метаданные, но не частные сети.
	if err := setupClusterAddrPolicy(envOf(map[string]string{envClusterAddrPolicy: "permissive"}), false); err != nil {
		t.Fatal(err)
	}
	p := netpolicy.For(netpolicy.ScopeClusterAPI)
	if p == nil || p.Profile != netpolicy.ProfilePermissive {
		t.Fatalf("%+v", p)
	}
	if p.CheckURL("https://10.0.0.5:6443") != nil || p.CheckURL("https://127.0.0.1:6443") == nil || p.CheckURL("https://169.254.169.254") == nil {
		t.Error("permissive: private networks are allowed, loopback and metadata are not")
	}

	// Исключение для CI (kind на 127.0.0.1) открывает loopback, но не метаданные.
	if err := setupClusterAddrPolicy(envOf(map[string]string{envClusterAddrPolicy: "permissive", envClusterAllowCIDRs: "127.0.0.0/8"}), false); err != nil {
		t.Fatal(err)
	}
	p = netpolicy.For(netpolicy.ScopeClusterAPI)
	if p.CheckURL("https://127.0.0.1:6443") != nil || p.CheckURL("https://169.254.169.254") == nil {
		t.Error("the CI exception must open loopback only")
	}

	if err := setupClusterAddrPolicy(envOf(map[string]string{envClusterAddrPolicy: "stict"}), false); err == nil {
		t.Error("an unknown profile must be an error")
	}
	// Политики Helm и кластера независимы.
	netpolicy.Set(netpolicy.ScopeHelm, nil)
	if err := setupClusterAddrPolicy(envOf(map[string]string{envClusterAddrPolicy: "strict"}), false); err != nil {
		t.Fatal(err)
	}
	if netpolicy.For(netpolicy.ScopeHelm) != nil {
		t.Error("enabling the cluster policy must not enable the Helm one")
	}
}

// В сервисном режиме защита от SSRF для адреса API-сервера и IdP включена по умолчанию (permissive).
func TestClusterAddrPolicyDefaultsToPermissiveInServiceMode(t *testing.T) {
	t.Cleanup(func() { netpolicy.Set(netpolicy.ScopeClusterAPI, nil) })
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	if err := setupClusterAddrPolicy(envOf(nil), true); err != nil {
		t.Fatal(err)
	}
	p := netpolicy.For(netpolicy.ScopeClusterAPI)
	if p == nil || p.Profile != netpolicy.ProfilePermissive {
		t.Fatalf("permissive by default in the service mode: %+v", p)
	}
	for _, u := range []string{"https://127.0.0.1:6443", "https://[::1]:6443", "https://169.254.169.254/", "https://[fe80::1]:6443", "https://[::ffff:169.254.169.254]/"} {
		if p.CheckURL(u) == nil {
			t.Errorf("%s must be blocked by default", u)
		}
	}
	if p.CheckURL("https://10.0.0.5:6443") != nil {
		t.Error("private networks stay allowed in permissive")
	}
	if logBuf.Len() != 0 {
		t.Errorf("the default must not warn: %s", logBuf.String())
	}

	// Исключение для CI открывает loopback, но не метаданные.
	if err := setupClusterAddrPolicy(envOf(map[string]string{envClusterAllowCIDRs: "127.0.0.0/8"}), true); err != nil {
		t.Fatal(err)
	}
	p = netpolicy.For(netpolicy.ScopeClusterAPI)
	if p == nil || p.CheckURL("https://127.0.0.1:6443") != nil || p.CheckURL("https://169.254.169.254/") == nil {
		t.Error("the allow list must override the loopback ban and never the metadata ban")
	}

	// Явное off: защиты нет, сервер предупреждает.
	if err := setupClusterAddrPolicy(envOf(map[string]string{envClusterAddrPolicy: "off"}), true); err != nil {
		t.Fatal(err)
	}
	if netpolicy.For(netpolicy.ScopeClusterAPI) != nil {
		t.Error("an explicit off must leave no policy")
	}
	if !strings.Contains(logBuf.String(), "WARN") || !strings.Contains(logBuf.String(), envClusterAddrPolicy) {
		t.Errorf("an explicit off in the service mode must be logged as a warning: %q", logBuf.String())
	}

	// Вне сервисного режима off — значение по умолчанию и без предупреждения.
	logBuf.Reset()
	if err := setupClusterAddrPolicy(envOf(nil), false); err != nil {
		t.Fatal(err)
	}
	if netpolicy.For(netpolicy.ScopeClusterAPI) != nil || logBuf.Len() != 0 {
		t.Errorf("single-user mode: no policy, no warning: %q", logBuf.String())
	}

	// Опечатка не выключает защиту молча.
	if err := setupClusterAddrPolicy(envOf(map[string]string{envClusterAddrPolicy: "permisive"}), true); err == nil {
		t.Error("an unknown profile must be an error")
	}
}
