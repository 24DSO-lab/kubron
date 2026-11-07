// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/k8s"
)

// PreviewHelmUpgrade — dry-run апгрейда values; в ответе манифесты (Secret скрыты), нужен secrets.read.
func (a *App) PreviewHelmUpgrade(namespace, name, valuesYAML string) (*k8s.HelmUpgradePreview, error) {
	if err := a.authz(auth.PermSecretsRead, "namespace", namespace, "name", name); err != nil {
		return nil, err
	}
	return a.k8sMgr.PreviewHelmUpgrade(namespace, name, valuesYAML)
}

// UpgradeHelmRelease применяет новые values; возвращает номер новой ревизии.
func (a *App) UpgradeHelmRelease(namespace, name, valuesYAML, opID string) (int, error) {
	if err := a.authz(auth.PermWrite, "namespace", namespace, "name", name); err != nil {
		return 0, err
	}
	return a.k8sMgr.UpgradeHelmRelease(namespace, name, valuesYAML, opID)
}

// GetHelmUpgradeSources — репозитории, из которых можно обновить чарт релиза, и их версии (локальные индексы).
func (a *App) GetHelmUpgradeSources(namespace, name string) (*k8s.HelmUpgradeSources, error) {
	if err := a.authz(auth.PermRead, "namespace", namespace, "name", name); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmUpgradeSources(namespace, name)
}

// PreviewHelmChartUpgrade — dry-run апгрейда релиза на другую версию чарта; в ответе манифесты
// (Secret скрыты) и values, нужен secrets.read.
func (a *App) PreviewHelmChartUpgrade(req k8s.HelmChartUpgradeRequest) (*k8s.HelmChartUpgradePreview, error) {
	if err := a.authz(auth.PermSecretsRead, "namespace", req.Namespace, "name", req.Name, "repo", req.Repo, "version", req.Version); err != nil {
		return nil, err
	}
	return a.k8sMgr.PreviewHelmChartUpgrade(req)
}

// UpgradeHelmChart переводит релиз на другую версию чарта; возвращает номер новой ревизии.
// В authz не передаём Values: там могут быть пароли, а аргументы попадают в журнал аудита.
func (a *App) UpgradeHelmChart(req k8s.HelmChartUpgradeRequest) (int, error) {
	if err := a.authz(auth.PermWrite, "namespace", req.Namespace, "name", req.Name, "repo", req.Repo, "version", req.Version); err != nil {
		return 0, err
	}
	return a.k8sMgr.UpgradeHelmChart(req)
}

// ListHelmRepos — репозитории чартов (локальный файл Kubron; пароли наружу не отдаются).
func (a *App) ListHelmRepos() ([]k8s.HelmRepoInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.ListHelmRepos()
}

// AddHelmRepo добавляет репозиторий и проверяет его, скачав index.yaml.
func (a *App) AddHelmRepo(name, url, username, password string, insecureTLS bool) error {
	if err := a.authzCluster(auth.PermClusterManage, a.activeClusterID(), "name", name); err != nil {
		return err
	}
	a.rememberHelmActor()
	return a.k8sMgr.AddHelmRepo(name, url, username, password, insecureTLS)
}

func (a *App) RemoveHelmRepo(name string) error {
	if err := a.authzCluster(auth.PermClusterManage, a.activeClusterID(), "name", name); err != nil {
		return err
	}
	return a.k8sMgr.RemoveHelmRepo(name)
}

// UpdateHelmRepos обновляет индекс одного репозитория; пустое имя — всех.
func (a *App) UpdateHelmRepos(name string) ([]k8s.HelmRepoUpdateResult, error) {
	if err := a.authzCluster(auth.PermClusterManage, a.activeClusterID(), "name", name); err != nil {
		return nil, err
	}
	return a.k8sMgr.UpdateHelmRepos(name)
}

func (a *App) SearchHelmCharts(query, repo string) (*k8s.HelmCatalogResult, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.SearchHelmCharts(query, repo)
}

func (a *App) GetHelmChartVersions(repo, chart string) ([]k8s.HelmChartVersionInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmChartVersions(repo, chart)
}

// GetHelmChartPackage скачивает архив чарта из репозитория и отдаёт README и values.yaml.
func (a *App) GetHelmChartPackage(repo, chart, version string) (*k8s.HelmChartPackage, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmChartPackage(repo, chart, version)
}

func (a *App) GetHelmReleases() ([]k8s.HelmReleaseInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmReleases()
}

func (a *App) GetHelmCharts() ([]k8s.HelmChartInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmCharts()
}

func (a *App) GetHelmReleaseDetail(namespace, name string) (*k8s.HelmReleaseDetail, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmReleaseDetail(namespace, name)
}

// GetHelmReleaseContent отдаёт values, notes и манифест ревизии. Содержимое не передаётся в authz:
// аргументы попадают в журнал аудита.
func (a *App) GetHelmReleaseContent(namespace, name string, revision int) (*k8s.HelmReleaseContent, error) {
	if err := a.authz(auth.PermSecretsRead, "namespace", namespace, "name", name, "revision", revision); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmReleaseContent(namespace, name, revision)
}

func (a *App) RollbackHelmRelease(namespace, name string, revision int, opID string) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name, "revision", revision); err != nil {
		return err
	}
	return a.k8sMgr.RollbackHelmRelease(namespace, name, revision, opID)
}

func (a *App) UninstallHelmRelease(namespace, name string, keepHistory bool, opID string) error {
	if err := a.authz(auth.PermWrite, "namespace", namespace, "name", name, "keepHistory", keepHistory); err != nil {
		return err
	}
	return a.k8sMgr.UninstallHelmRelease(namespace, name, keepHistory, opID)
}

// GetHelmRevisionDiff сравнивает ревизии релиза (values и манифест); как и GetHelmReleaseContent, требует secrets.read.
func (a *App) GetHelmRevisionDiff(namespace, name string, from, to int) (*k8s.HelmRevisionDiff, error) {
	if err := a.authz(auth.PermSecretsRead, "namespace", namespace, "name", name, "from", from, "to", to); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmRevisionDiff(namespace, name, from, to)
}

// PreviewHelmInstall — dry-run установки чарта из каталога (кластер не затрагивается).
func (a *App) PreviewHelmInstall(req k8s.HelmInstallRequest) (*k8s.HelmInstallResult, error) {
	if err := a.authz(auth.PermWrite, "namespace", req.Namespace, "release", req.ReleaseName, "chart", req.Chart); err != nil {
		return nil, err
	}
	return a.k8sMgr.PreviewHelmInstall(req)
}

// InstallHelmChart устанавливает чарт из репозитория каталога.
func (a *App) InstallHelmChart(req k8s.HelmInstallRequest) (*k8s.HelmInstallResult, error) {
	if err := a.authz(auth.PermWrite, "namespace", req.Namespace, "release", req.ReleaseName, "chart", req.Chart); err != nil {
		return nil, err
	}
	return a.k8sMgr.InstallHelmChart(req)
}

// CancelHelmOperation прерывает идущую операцию (установка и апгрейд); откат и удаление отменить нельзя.
func (a *App) CancelHelmOperation(opID string) error {
	if err := a.authz(auth.PermWrite, "opID", opID); err != nil {
		return err
	}
	return a.k8sMgr.CancelHelmOperation(opID)
}

// AddHelmOCIRepo добавляет OCI-реестр с набором чартов. В authz — только имя: пароль и адрес не логируются.
func (a *App) AddHelmOCIRepo(name, url string, charts []string, username, password string) error {
	if err := a.authzCluster(auth.PermClusterManage, a.activeClusterID(), "name", name); err != nil {
		return err
	}
	a.rememberHelmActor()
	return a.k8sMgr.AddHelmOCIRepo(name, url, charts, username, password)
}

func (a *App) AddHelmOCIChart(repo, chart string) error {
	if err := a.authzCluster(auth.PermClusterManage, a.activeClusterID(), "repo", repo, "chart", chart); err != nil {
		return err
	}
	return a.k8sMgr.AddHelmOCIChart(repo, chart)
}

func (a *App) RemoveHelmOCIChart(repo, chart string) error {
	if err := a.authzCluster(auth.PermClusterManage, a.activeClusterID(), "repo", repo, "chart", chart); err != nil {
		return err
	}
	return a.k8sMgr.RemoveHelmOCIChart(repo, chart)
}

// ListHelmOperations — история Helm-операций (без журнала Helm).
func (a *App) ListHelmOperations(q k8s.HelmOperationQuery) ([]k8s.HelmOperationRecord, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.ListHelmOperations(q)
}

// GetHelmOperation — запись истории вместе с журналом Helm.
func (a *App) GetHelmOperation(id string) (*k8s.HelmOperationRecord, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmOperation(id)
}

// ClearHelmOperations стирает историю Helm-операций.
func (a *App) ClearHelmOperations() error {
	if err := a.authzCluster(auth.PermClusterManage, a.activeClusterID()); err != nil {
		return err
	}
	return a.k8sMgr.ClearHelmOperations()
}

// rememberHelmActor сообщает ядру, кто добавляет репозиторий (created_by в сервисе). Вызывается после authz.
func (a *App) rememberHelmActor() {
	if a.auth == nil {
		return
	}
	if u, err := a.auth.Current(); err == nil && u != nil {
		a.k8sMgr.SetHelmActor(u.ID)
	}
}
