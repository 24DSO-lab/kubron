// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"strings"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/k8s"
)

// RPC-методы Flux CD (ADR-0023). Каждый начинается с a.authz и делегирует в k8sMgr.
// Права: чтение — read, reconcile — operate, suspend, resume и delete — write.

// GetFluxStatus сообщает, какие контроллеры Flux есть в активном кластере и какая версия API обслуживается.
func (a *App) GetFluxStatus() (*k8s.FluxStatus, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetFluxStatus()
}

// GetFluxKustomizations возвращает Kustomization; пустой набор namespace — из всех namespace.
func (a *App) GetFluxKustomizations(namespaces []string) ([]k8s.FluxKustomizationInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetFluxKustomizations(namespaces)
}

// GetFluxHelmReleases возвращает HelmRelease (spec.values и valuesFrom наружу не отдаются).
func (a *App) GetFluxHelmReleases(namespaces []string) ([]k8s.FluxHelmReleaseInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetFluxHelmReleases(namespaces)
}

// GetFluxSources возвращает источники: GitRepository, OCIRepository, HelmRepository, HelmChart, Bucket.
func (a *App) GetFluxSources(namespaces []string) ([]k8s.FluxSourceInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetFluxSources(namespaces)
}

// GetFluxNotifications возвращает Provider, Alert и Receiver (адреса вебхуков и пути наружу не отдаются).
func (a *App) GetFluxNotifications(namespaces []string) (*k8s.FluxNotifications, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetFluxNotifications(namespaces)
}

// GetFluxImageAutomation возвращает ImageRepository, ImagePolicy и ImageUpdateAutomation.
func (a *App) GetFluxImageAutomation(namespaces []string) (*k8s.FluxImageAutomation, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetFluxImageAutomation(namespaces)
}

// GetFluxGraph возвращает граф цепочки «источник → Kustomization/HelmRelease → ресурсы».
func (a *App) GetFluxGraph(ref k8s.FluxRef) (*k8s.FluxGraph, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetFluxGraph(ref)
}

// ReconcileFlux запрашивает reconcile объектов; withSource — вместе с источником.
// Результат — по каждому объекту отдельно (одна ошибка не отменяет остальные).
func (a *App) ReconcileFlux(refs []k8s.FluxRef, withSource bool) ([]k8s.FluxActionResult, error) {
	if err := a.authz(auth.PermOperate, "objects", fluxAuditRefs(refs), "withSource", withSource); err != nil {
		return nil, err
	}
	return a.k8sMgr.ReconcileFlux(refs, withSource)
}

// SuspendFlux приостанавливает объекты (spec.suspend: true).
func (a *App) SuspendFlux(refs []k8s.FluxRef) ([]k8s.FluxActionResult, error) {
	if err := a.authz(auth.PermWrite, "objects", fluxAuditRefs(refs)); err != nil {
		return nil, err
	}
	return a.k8sMgr.SuspendFlux(refs)
}

// ResumeFlux возобновляет объекты (spec.suspend: false и requestedAt, как flux resume).
func (a *App) ResumeFlux(refs []k8s.FluxRef) ([]k8s.FluxActionResult, error) {
	if err := a.authz(auth.PermWrite, "objects", fluxAuditRefs(refs)); err != nil {
		return nil, err
	}
	return a.k8sMgr.ResumeFlux(refs)
}

// DeleteFlux удаляет один Kustomization или HelmRelease. В аудит попадают вид, namespace и имя: значение prune
// authz не видит (объект ещё не прочитан), его пишет в журнал процесса исполнитель (задача B.9).
func (a *App) DeleteFlux(ref k8s.FluxRef) error {
	if err := a.authz(auth.PermWrite, "kind", ref.Kind, "namespace", ref.Namespace, "name", ref.Name); err != nil {
		return err
	}
	return a.k8sMgr.DeleteFlux(ref)
}

// fluxAuditRefs — компактная цель действия для журнала: первые объекты и счётчик остальных (вид/namespace/имя).
func fluxAuditRefs(refs []k8s.FluxRef) string {
	const show = 5
	names := make([]string, 0, show)
	for i, ref := range refs {
		if i == show {
			break
		}
		names = append(names, ref.Kind+"/"+ref.Namespace+"/"+ref.Name)
	}
	s := strings.Join(names, ",")
	if len(refs) > show {
		s += fmt.Sprintf(" +%d", len(refs)-show)
	}
	return s
}
