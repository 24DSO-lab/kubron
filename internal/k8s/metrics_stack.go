// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// metricsInstallGrace — сколько после создания namespace отсутствие или неготовность
// компонентов считается «установкой», а не неисправностью.
const metricsInstallGrace = 5 * time.Minute

func (m *Manager) GetMetricsStatus() (*MetricsStackStatus, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	status := &MetricsStackStatus{
		Status:     "not_installed",
		Components: make(map[string]MetricsComponentStatus),
	}

	ns, err := conn.Client.CoreV1().Namespaces().Get(ctx, MetricsNamespace, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return status, nil
		}
		return nil, fmt.Errorf("failed to check namespace: %w", err)
	}

	var missing, notReady []string
	record := func(name string, desired, available int32, getErr error) error {
		if getErr != nil {
			if errors.IsNotFound(getErr) {
				missing = append(missing, name)
				return nil
			}
			return fmt.Errorf("failed to check %s: %w", name, getErr)
		}
		c := MetricsComponentStatus{
			Ready:             desired > 0 && available >= desired,
			Replicas:          desired,
			AvailableReplicas: available,
		}
		status.Components[name] = c
		if !c.Ready {
			notReady = append(notReady, name)
		}
		return nil
	}

	apps := conn.Client.AppsV1()

	sts, err := apps.StatefulSets(MetricsNamespace).Get(ctx, "prometheus", metav1.GetOptions{})
	var desired, avail int32
	if err == nil {
		desired, avail = 1, sts.Status.ReadyReplicas
		if sts.Spec.Replicas != nil {
			desired = *sts.Spec.Replicas
		}
	}
	if err := record("prometheus", desired, avail, err); err != nil {
		return nil, err
	}

	ds, err := apps.DaemonSets(MetricsNamespace).Get(ctx, "node-exporter", metav1.GetOptions{})
	desired, avail = 0, 0
	if err == nil {
		desired, avail = ds.Status.DesiredNumberScheduled, ds.Status.NumberReady
	}
	if err := record("node-exporter", desired, avail, err); err != nil {
		return nil, err
	}

	dep, err := apps.Deployments(MetricsNamespace).Get(ctx, "kube-state-metrics", metav1.GetOptions{})
	desired, avail = 0, 0
	if err == nil {
		desired, avail = 1, dep.Status.AvailableReplicas
		if dep.Spec.Replicas != nil {
			desired = *dep.Spec.Replicas
		}
	}
	if err := record("kube-state-metrics", desired, avail, err); err != nil {
		return nil, err
	}

	if len(missing) == 0 && len(notReady) == 0 {
		status.Status = "installed"
		status.PrometheusURL = PrometheusSvcURL
		return status, nil
	}

	if time.Since(ns.CreationTimestamp.Time) < metricsInstallGrace {
		status.Status = "installing"
		status.Message = "Components are starting"
		return status, nil
	}

	status.Status = "degraded"
	switch {
	case len(missing) > 0:
		status.Message = "Components not found: " + strings.Join(missing, ", ")
	default:
		status.Message = "Components not ready: " + strings.Join(notReady, ", ")
	}
	return status, nil
}

func (m *Manager) InstallMetricsStack() error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}
	data, err := loadManifestData()
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	objects, err := parseManifest(data)
	if err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 60*time.Second)
	defer cancel()

	for _, obj := range objects {
		if err := m.applyObject(ctx, conn, obj); err != nil {
			return fmt.Errorf("failed to apply %s %s: %w", obj.GetKind(), obj.GetName(), err)
		}
	}

	return nil
}

func (m *Manager) UninstallMetricsStack() error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}
	data, err := loadManifestData()
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	objects, err := parseManifest(data)
	if err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 60*time.Second)
	defer cancel()

	// Delete in reverse order, skip namespace
	for i := len(objects) - 1; i >= 0; i-- {
		obj := objects[i]
		if obj.GetKind() == "Namespace" {
			continue
		}
		if err := m.deleteObject(ctx, conn, obj); err != nil {
			if !errors.IsNotFound(err) {
				return fmt.Errorf("failed to delete %s %s: %w", obj.GetKind(), obj.GetName(), err)
			}
		}
	}

	// Delete namespace last
	err = conn.Client.CoreV1().Namespaces().Delete(ctx, MetricsNamespace, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete namespace: %w", err)
	}

	return nil
}
