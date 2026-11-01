// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/24DSO-labs/kubron/internal/netpolicy"
)

// Защита от SSRF для запросов Helm (ADR-0015). Репозитории чартов, индексы, чарты и OCI-реестры задаёт пользователь,
// поэтому сервер по умолчанию не соединяется с loopback, link-local (метаданные облака), частными диапазонами и т. п.
//
//	KUBRON_HELM_ADDR_POLICY — strict (по умолчанию) | permissive | off
//	KUBRON_HELM_ALLOW_CIDRS — исключения: CIDR или адреса через запятую (например, внутренний репозиторий 10.20.0.0/16);
//	                          с метаданных облака, unspecified и multicast запрет не снимают.
//
// Адрес API-сервера кластера из kubeconfig пользователя — тот же риск, но по умолчанию проверка выключена: e2e в CI
// использует kind с API-сервером на 127.0.0.1. Включение по умолчанию — отдельным шагом, когда в CI появится
// исключение (KUBRON_CLUSTER_ADDR_ALLOW_CIDRS=127.0.0.0/8).
//
//	KUBRON_CLUSTER_ADDR_POLICY      — off (по умолчанию) | permissive | strict
//	KUBRON_CLUSTER_ADDR_ALLOW_CIDRS — исключения, как у Helm
const (
	envHelmAddrPolicy    = "KUBRON_HELM_ADDR_POLICY"
	envHelmAllowCIDRs    = "KUBRON_HELM_ALLOW_CIDRS"
	envClusterAddrPolicy = "KUBRON_CLUSTER_ADDR_POLICY"
	envClusterAllowCIDRs = "KUBRON_CLUSTER_ADDR_ALLOW_CIDRS"
)

// setupHelmNetPolicy читает настройки из окружения и включает политику Helm (netpolicy.Set).
func setupHelmNetPolicy(getenv func(string) string) error {
	return setupNetPolicy(getenv, netpolicy.ScopeHelm, "helm", envHelmAddrPolicy, envHelmAllowCIDRs, "strict")
}

// setupClusterAddrPolicy включает политику адреса API-сервера кластера (по умолчанию выключена).
func setupClusterAddrPolicy(getenv func(string) string) error {
	return setupNetPolicy(getenv, netpolicy.ScopeClusterAPI, "cluster-api", envClusterAddrPolicy, envClusterAllowCIDRs, "off")
}

func setupNetPolicy(getenv func(string) string, scope netpolicy.Scope, name, envProfile, envAllow, def string) error {
	raw := getenv(envProfile)
	if raw == "" {
		raw = def
	}
	profile, err := netpolicy.ParseProfile(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", envProfile, err)
	}
	allow, err := netpolicy.ParseAllowList(getenv(envAllow))
	if err != nil {
		return fmt.Errorf("%s: %w", envAllow, err)
	}
	netpolicy.Set(scope, netpolicy.New(name, profile, allow))
	return nil
}
