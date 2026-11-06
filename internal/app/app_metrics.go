// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"time"

	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/k8s"
)

func (a *App) GetPodMetrics(namespaces []string) ([]k8s.PodMetrics, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetPodMetrics(namespaces)
}

func (a *App) GetNodeMetrics() ([]k8s.NodeMetrics, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetNodeMetrics()
}

func (a *App) GetMetricsStatus() (*k8s.MetricsStackStatus, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetMetricsStatus()
}

func (a *App) InstallMetricsStack() error {
	if err := a.authz(auth.PermWrite); err != nil {
		return err
	}
	return a.k8sMgr.InstallMetricsStack()
}

func (a *App) UninstallMetricsStack() error {
	if err := a.authz(auth.PermWrite); err != nil {
		return err
	}
	return a.k8sMgr.UninstallMetricsStack()
}

func (a *App) GetClusterMetricsSnapshot() (*k8s.ClusterMetricsSnapshot, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetClusterMetricsSnapshot()
}

func (a *App) QueryPrometheus(query string) (*k8s.PromQueryResult, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.QueryPrometheus(query)
}

func (a *App) QueryPrometheusRange(query string, startUnix, endUnix int64, step string) (*k8s.PromQueryResult, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	start := time.Unix(startUnix, 0)
	end := time.Unix(endUnix, 0)
	return a.k8sMgr.QueryPrometheusRange(query, start, end, step)
}
