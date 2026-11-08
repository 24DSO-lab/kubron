// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"strings"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/k8s"
)

func (a *App) SuspendArgoAutoSync(apps []k8s.ArgoAppRef) ([]k8s.ArgoActionResult, error) {
	if err := a.authz(auth.PermWrite, "apps", argoAuditApps(apps)); err != nil {
		return nil, err
	}
	return a.k8sMgr.SuspendArgoAutoSync(apps)
}

func (a *App) ResumeArgoAutoSync(apps []k8s.ArgoAppRef) ([]k8s.ArgoActionResult, error) {
	if err := a.authz(auth.PermWrite, "apps", argoAuditApps(apps)); err != nil {
		return nil, err
	}
	return a.k8sMgr.ResumeArgoAutoSync(apps)
}

func (a *App) DeleteArgoApplication(namespace, name string, cascade bool) error {
	if err := a.authz(auth.PermWrite, "namespace", namespace, "name", name, "cascade", cascade); err != nil {
		return err
	}
	return a.k8sMgr.DeleteArgoApplication(namespace, name, cascade)
}

// ArgoInstalled сообщает, установлен ли Argo CD (CRD Application) в активном кластере.
func (a *App) ArgoInstalled() (bool, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return false, err
	}
	return a.k8sMgr.ArgoInstalled()
}

// GetArgoApplications возвращает Argo CD Application; пустой namespace — из всех namespace.
func (a *App) GetArgoApplications(namespaces []string) ([]k8s.ArgoApplicationInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetArgoApplications(namespaces)
}

// SyncArgoApplication запускает синхронизацию одного или нескольких Application.
// Результат — по каждому приложению отдельно (одна ошибка не отменяет остальные).
func (a *App) SyncArgoApplication(apps []k8s.ArgoAppRef, opts k8s.ArgoSyncOptions) ([]k8s.ArgoActionResult, error) {
	if err := a.authz(auth.PermOperate, "apps", argoAuditApps(apps), "prune", opts.Prune, "force", opts.Force, "dryRun", opts.DryRun, "revision", opts.Revision); err != nil {
		return nil, err
	}
	return a.k8sMgr.SyncArgoApplication(apps, opts)
}

func (a *App) RefreshArgoApplication(apps []k8s.ArgoAppRef, hard bool) ([]k8s.ArgoActionResult, error) {
	if err := a.authz(auth.PermOperate, "apps", argoAuditApps(apps), "hard", hard); err != nil {
		return nil, err
	}
	return a.k8sMgr.RefreshArgoApplication(apps, hard)
}

func (a *App) TerminateArgoOperation(apps []k8s.ArgoAppRef) ([]k8s.ArgoActionResult, error) {
	if err := a.authz(auth.PermOperate, "apps", argoAuditApps(apps)); err != nil {
		return nil, err
	}
	return a.k8sMgr.TerminateArgoOperation(apps)
}

func (a *App) RollbackArgoApplication(namespace, name string, id int64, prune, dryRun bool) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name, "id", id, "prune", prune, "dryRun", dryRun); err != nil {
		return err
	}
	return a.k8sMgr.RollbackArgoApplication(namespace, name, id, prune, dryRun)
}

// GetArgoApplicationSets возвращает Argo CD ApplicationSet; пустой namespace — из всех namespace.
func (a *App) GetArgoApplicationSets(namespaces []string) ([]k8s.ArgoApplicationSetInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetArgoApplicationSets(namespaces)
}

// GetArgoProjects возвращает Argo CD AppProject; пустой namespace — из всех namespace.
func (a *App) GetArgoProjects(namespaces []string) ([]k8s.ArgoProjectInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetArgoProjects(namespaces)
}

// GetArgoRepositories возвращает репозитории Argo CD (санитизировано: без паролей, токенов и ключей).
func (a *App) GetArgoRepositories(namespaces []string) ([]k8s.ArgoRepositoryInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetArgoRepositories(namespaces)
}

// GetArgoClusters возвращает кластеры Argo CD (санитизировано: без config с токенами и сертификатами).
func (a *App) GetArgoClusters(namespaces []string) ([]k8s.ArgoClusterInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetArgoClusters(namespaces)
}

// GetArgoApplicationGraph возвращает граф ресурсов Application (владение, Service → Pod, Ingress → Service).
func (a *App) GetArgoApplicationGraph(namespace, name string) (*k8s.ArgoGraph, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetArgoApplicationGraph(namespace, name)
}

// GetArgoAPIStatus сообщает, задан ли токен Argo CD API для активного кластера (сам токен наружу не отдаётся).
func (a *App) GetArgoAPIStatus() (*k8s.ArgoAPIStatus, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetArgoAPIStatus()
}

// SetArgoAPIToken сохраняет токен Argo CD API для активного кластера.
// Токен нельзя передавать в authz: аргументы попадают в журнал аудита.
func (a *App) SetArgoAPIToken(token string) error {
	// Токен относится к активному кластеру: право проверяется по роли в его команде, а не «где-нибудь админ».
	if err := a.authzCluster(auth.PermClusterManage, a.k8sMgr.GetActiveContext()); err != nil {
		return err
	}
	return a.k8sMgr.SetArgoAPIToken(token)
}

// GenerateArgoAPIToken получает токен Argo CD API для аккаунта и сохраняет его для активного кластера.
// Логин и пароль Argo CD нужны только для этого вызова: они не сохраняются и не передаются в authz
// (аргументы authz попадают в журнал аудита); токен фронтенду не возвращается.
func (a *App) GenerateArgoAPIToken(account, username, password string, days int) error {
	if err := a.authzCluster(auth.PermClusterManage, a.k8sMgr.GetActiveContext(), "account", account); err != nil {
		return err
	}
	return a.pfMgr.GenerateArgoAPIToken(account, username, password, days)
}

// DeleteArgoAPIToken удаляет токен Argo CD API активного кластера.
func (a *App) DeleteArgoAPIToken() error {
	if err := a.authz(auth.PermWrite); err != nil {
		return err
	}
	return a.k8sMgr.DeleteArgoAPIToken()
}

// GetArgoApplicationDiff сравнивает желаемое (git) и текущее состояние ресурсов Application через Argo CD API.
// Поднимает туннель к argocd-server, поэтому требует права на проброс портов.
func (a *App) GetArgoApplicationDiff(namespace, name string) (*k8s.ArgoDiff, error) {
	if err := a.authz(auth.PermPortForward, "namespace", namespace, "name", name); err != nil {
		return nil, err
	}
	return a.pfMgr.GetArgoApplicationDiff(namespace, name)
}

// OpenArgoUI пробрасывает порт на argocd-server и возвращает URL для браузера.
func (a *App) OpenArgoUI() (*k8s.ArgoUIInfo, error) {
	if err := a.authz(auth.PermPortForward); err != nil {
		return nil, err
	}
	return a.pfMgr.OpenArgoUI()
}

// argoAuditApps — компактная цель действия для журнала: первые приложения и счётчик остальных.
func argoAuditApps(apps []k8s.ArgoAppRef) string {
	const show = 5
	names := make([]string, 0, show)
	for i, ref := range apps {
		if i == show {
			break
		}
		names = append(names, ref.Namespace+"/"+ref.Name)
	}
	s := strings.Join(names, ",")
	if len(apps) > show {
		s += fmt.Sprintf(" +%d", len(apps)-show)
	}
	return s
}

func (a *App) GetApplications(namespaces []string) ([]k8s.ApplicationInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetApplications(namespaces)
}
