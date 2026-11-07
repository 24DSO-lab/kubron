// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"log/slog"

	"github.com/24DSO-lab/kubron/internal/netpolicy"
)

// Защита от SSRF для запросов Helm (ADR-0015). Репозитории чартов, индексы, чарты и OCI-реестры задаёт пользователь,
// поэтому сервер по умолчанию не соединяется с loopback, link-local (метаданные облака), частными диапазонами и т. п.
//
//	KUBRON_HELM_ADDR_POLICY — strict (по умолчанию) | permissive | off
//	KUBRON_HELM_ALLOW_CIDRS — исключения: CIDR или адреса через запятую (например, внутренний репозиторий 10.20.0.0/16);
//	                          с метаданных облака, unspecified и multicast запрет не снимают.
//
// Адрес API-сервера кластера из kubeconfig пользователя — тот же риск (SSRF на metadata-сервис и внутренние службы),
// поэтому в сервисном режиме (БД или адрес не на loopback) по умолчанию действует permissive: запрещены loopback,
// link-local и метаданные облака, частные сети разрешены. Режим одного пользователя на loopback по умолчанию без
// проверки. Тот же профиль применяется к запросам к IdP при oidc-password (ADR-0015, дополнение). Выключить в
// сервисном режиме можно только явно (off); сервер пишет предупреждение. e2e на kind с API-сервером на 127.0.0.1
// открывают loopback исключением KUBRON_CLUSTER_ADDR_ALLOW_CIDRS=127.0.0.0/8 (метаданные оно не открывает).
//
//	KUBRON_CLUSTER_ADDR_POLICY      — permissive (по умолчанию в сервисном режиме) | strict | off
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

// setupClusterAddrPolicy включает политику адреса API-сервера кластера и IdP. В сервисном режиме значение по умолчанию
// permissive, иначе off; явное off в сервисном режиме оставляет сервер без защиты от SSRF, о чём пишется предупреждение.
func setupClusterAddrPolicy(getenv func(string) string, serviceMode bool) error {
	def := "off"
	if serviceMode {
		def = "permissive"
	}
	if err := setupNetPolicy(getenv, netpolicy.ScopeClusterAPI, "cluster-api", envClusterAddrPolicy, envClusterAllowCIDRs, def); err != nil {
		return err
	}
	if serviceMode && netpolicy.For(netpolicy.ScopeClusterAPI) == nil {
		slog.Warn("network policy: " + envClusterAddrPolicy + "=off, so kubeconfig and IdP addresses are not checked: " +
			"a user can point the server at loopback, link-local and cloud metadata addresses; use permissive or strict")
	}
	return nil
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
