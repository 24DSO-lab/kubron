// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/k8s"
)

func (a *App) GetClusterOverview() (*k8s.ClusterOverview, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetClusterOverview()
}

func (a *App) GetOverviewHotspots(withMetrics bool) (*k8s.OverviewHotspots, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetOverviewHotspots(withMetrics)
}

func (a *App) GetOverviewRisks() (*k8s.OverviewRisks, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetOverviewRisks()
}

func (a *App) GetRecentChanges(windowMinutes int) ([]k8s.ChangeEvent, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetRecentChanges(windowMinutes)
}

// Только имя, субъект и дата окончания; содержимое секретов наружу не уходит.
func (a *App) GetCertificateExpiry() (*k8s.CertScan, error) {
	if err := a.authz(auth.PermSecretsRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetCertificateExpiry()
}
