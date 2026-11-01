// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/24DSO-labs/kubron/internal/netpolicy"
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

	// По умолчанию выключена: e2e в CI ходит на API-сервер kind по 127.0.0.1.
	if err := setupClusterAddrPolicy(envOf(nil)); err != nil {
		t.Fatal(err)
	}
	if netpolicy.For(netpolicy.ScopeClusterAPI) != nil {
		t.Fatal("the cluster address policy must be off by default")
	}

	// permissive закрывает loopback и метаданные, но не частные сети.
	if err := setupClusterAddrPolicy(envOf(map[string]string{envClusterAddrPolicy: "permissive"})); err != nil {
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
	if err := setupClusterAddrPolicy(envOf(map[string]string{envClusterAddrPolicy: "permissive", envClusterAllowCIDRs: "127.0.0.0/8"})); err != nil {
		t.Fatal(err)
	}
	p = netpolicy.For(netpolicy.ScopeClusterAPI)
	if p.CheckURL("https://127.0.0.1:6443") != nil || p.CheckURL("https://169.254.169.254") == nil {
		t.Error("the CI exception must open loopback only")
	}

	if err := setupClusterAddrPolicy(envOf(map[string]string{envClusterAddrPolicy: "stict"})); err == nil {
		t.Error("an unknown profile must be an error")
	}
	// Политики Helm и кластера независимы.
	netpolicy.Set(netpolicy.ScopeHelm, nil)
	if err := setupClusterAddrPolicy(envOf(map[string]string{envClusterAddrPolicy: "strict"})); err != nil {
		t.Fatal(err)
	}
	if netpolicy.For(netpolicy.ScopeHelm) != nil {
		t.Error("enabling the cluster policy must not enable the Helm one")
	}
}
