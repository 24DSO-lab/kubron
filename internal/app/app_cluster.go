// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"strconv"
	"strings"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/k8s"
)

func (a *App) GetClusterStatuses() []k8s.ClusterStatus {
	if a.authz(auth.PermRead) != nil {
		return nil
	}
	// В сервисном режиме пользователь видит только кластеры своих команд.
	all := a.k8sMgr.GetClusterStatuses()
	out := make([]k8s.ClusterStatus, 0, len(all))
	for _, st := range all {
		if a.auth.CanSeeCluster(st.ID) {
			out = append(out, st)
		}
	}
	return out
}

// PickKubeconfigFile opens a native file dialog and returns the chosen path
// (empty string when the dialog was cancelled). The dialog title is supplied
// by the frontend (already translated); an empty title falls back to English.
func (a *App) PickKubeconfigFile(title string) (string, error) {
	if err := a.authz(auth.PermClusterManage); err != nil {
		return "", err
	}
	if title == "" {
		title = "Select kubeconfig file"
	}
	return a.platform.PickFile(title)
}

// AddClusterFromFile adds a cluster from a kubeconfig file previously chosen
// with PickKubeconfigFile.
func (a *App) AddClusterFromFile(path string, settings k8s.ClusterSettings) (*k8s.ClusterMeta, error) {
	if err := a.authzTeam(auth.PermClusterManage, a.defaultTeam(), "displayName", settings.DisplayName); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("kubeconfig file is not selected")
	}
	return k8s.AddClusterFromFile(path, settings)
}

func (a *App) AddClusterFromText(kubeconfigText string, settings k8s.ClusterSettings) (*k8s.ClusterMeta, error) {
	// Без указания команды кластер попадает в команду по умолчанию: добавлять в неё может только её администратор.
	if err := a.authzTeam(auth.PermClusterManage, a.defaultTeam(), "displayName", settings.DisplayName); err != nil {
		return nil, err
	}
	return k8s.AddClusterFromText(kubeconfigText, settings)
}

// InspectKubeconfig разбирает kubeconfig (файл, текст или сохранённый кластер) и возвращает
// контексты без секретов — для выбора контекста в диалогах кластера.
func (a *App) InspectKubeconfig(src k8s.KubeconfigSource) (*k8s.KubeconfigInfo, error) {
	if err := a.authz(auth.PermClusterManage); err != nil {
		return nil, err
	}
	if err := a.checkKubeconfigSource(src); err != nil {
		return nil, err
	}
	return k8s.InspectKubeconfig(src)
}

// TestKubeconfig пробно подключается к кластеру по kubeconfig, ничего не сохраняя.
func (a *App) TestKubeconfig(src k8s.KubeconfigSource, contextName string) (*k8s.ConnectionCheck, error) {
	if err := a.authz(auth.PermClusterManage, "context", contextName); err != nil {
		return nil, err
	}
	if err := a.checkKubeconfigSource(src); err != nil {
		return nil, err
	}
	return k8s.CheckKubeconfig(src, contextName)
}

// GetClusterKubeconfig возвращает текст сохранённого kubeconfig (с секретами) для правки в диалоге.
func (a *App) GetClusterKubeconfig(id string) (string, error) {
	if err := a.authzCluster(auth.PermClusterManage, id, "id", id); err != nil {
		return "", err
	}
	return k8s.ReadClusterKubeconfig(id)
}

func (a *App) DeleteCluster(id string) error {
	if err := a.authzCluster(auth.PermClusterManage, id, "id", id); err != nil {
		return err
	}
	if err := k8s.DeleteCluster(id); err != nil {
		return err
	}
	// Подключение и проброшенные порты удалённого активного кластера больше не нужны.
	if a.k8sMgr.RemoveConnection(id) {
		a.pfMgr.StopAll()
	}
	return nil
}

// UpdateCluster сохраняет настройки кластера; непустой kubeconfig заменяет сохранённый.
// Содержимое kubeconfig в журнал не попадает — только способ замены.
func (a *App) UpdateCluster(id string, settings k8s.ClusterSettings, kubeconfig k8s.KubeconfigSource) (*k8s.ClusterUpdateResult, error) {
	if err := a.authzCluster(auth.PermClusterManage, id, "id", id, "displayName", settings.DisplayName, "kubeconfig", kubeconfigChange(kubeconfig),
		"sharedAccount", strconv.FormatBool(settings.SharedAccount)); err != nil {
		return nil, err
	}
	kubeconfig.BaseClusterID = id // скрытые секреты берутся только у редактируемого кластера
	if err := a.checkKubeconfigSource(kubeconfig); err != nil {
		return nil, err
	}
	res, err := k8s.UpdateClusterSettings(id, settings, kubeconfig)
	if err != nil {
		return nil, err
	}
	if res.CredentialsChanged {
		a.recordCredentialsUpdate(id)
	}
	if res.AccountModeChanged {
		// Личные и общие входы разного владельца: прежние сохранённые входы кластера больше не подходят.
		if err := a.revokeClusterSignIns(id); err != nil {
			return nil, err
		}
	}
	// Подключение собрано по прежнему kubeconfig/контексту: забываем его, следующее
	// подключение построит новое. Проброшенные порты старого активного кластера теряют смысл.
	if res.ConnectionChanged && a.k8sMgr.RemoveConnection(id) {
		a.pfMgr.StopAll()
	}
	return res, nil
}

// kubeconfigChange описывает способ замены kubeconfig для журнала аудита (без содержимого).
func kubeconfigChange(src k8s.KubeconfigSource) string {
	switch {
	case src.Path != "":
		return "file"
	case strings.TrimSpace(src.Text) != "":
		return "text"
	}
	return "unchanged"
}

func (a *App) GetLastActiveCluster() string {
	if a.authz(auth.PermRead) != nil {
		return ""
	}
	// В веб-режиме не читаем общую настройку: она раскрывает выбор другого пользователя
	// и заставляет новую сессию автоматически подключаться к его кластеру.
	if a.sessionID != "" {
		return a.k8sMgr.GetActiveContext()
	}
	config, err := k8s.LoadKubronConfig()
	if err != nil || !a.auth.CanSeeCluster(config.LastActiveCluster) {
		return "" // настройка общая: чужой кластер не должен выдавать даже своё имя
	}
	return config.LastActiveCluster
}

func (a *App) GetAppSettings() (*k8s.KubronConfig, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return k8s.LoadKubronConfig()
}

func (a *App) SaveAppSettings(settings *k8s.KubronConfig) error {
	if err := a.authz(auth.PermRead); err != nil {
		return err
	}
	return k8s.SaveKubronConfig(settings)
}

func (a *App) GetActiveContext() string {
	if a.authz(auth.PermRead) != nil {
		return ""
	}
	return a.k8sMgr.GetActiveContext()
}
