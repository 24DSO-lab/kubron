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
const (
	envHelmAddrPolicy = "KUBRON_HELM_ADDR_POLICY"
	envHelmAllowCIDRs = "KUBRON_HELM_ALLOW_CIDRS"
)

// setupHelmNetPolicy читает настройки из окружения и включает политику (netpolicy.Set).
func setupHelmNetPolicy(getenv func(string) string) error {
	raw := getenv(envHelmAddrPolicy)
	if raw == "" {
		raw = "strict"
	}
	profile, err := netpolicy.ParseProfile(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", envHelmAddrPolicy, err)
	}
	allow, err := netpolicy.ParseAllowList(getenv(envHelmAllowCIDRs))
	if err != nil {
		return fmt.Errorf("%s: %w", envHelmAllowCIDRs, err)
	}
	netpolicy.Set(netpolicy.ScopeHelm, netpolicy.New("helm", profile, allow))
	return nil
}
