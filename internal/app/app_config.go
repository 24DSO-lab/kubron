// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/k8s"
)

func (a *App) GetEvents(namespaces []string) ([]k8s.EventInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetEvents(namespaces)
}

func (a *App) GetConfigMaps(namespaces []string) ([]k8s.ConfigMapInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetConfigMaps(namespaces)
}

func (a *App) GetConfigMapDetail(namespace, name string) (*k8s.ConfigMapInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetConfigMapDetail(namespace, name)
}

func (a *App) UpdateConfigMap(namespace, name string, data map[string]string) error {
	if err := a.authz(auth.PermWrite, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.UpdateConfigMap(namespace, name, data)
}

func (a *App) GetSecrets(namespaces []string) ([]k8s.SecretInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetSecrets(namespaces)
}

func (a *App) GetSecretValue(namespace, name, key string) (string, error) {
	if err := a.authz(auth.PermSecretsRead, "namespace", namespace, "name", name, "key", key); err != nil {
		return "", err
	}
	return a.k8sMgr.GetSecretValue(namespace, name, key)
}

func (a *App) GetSecretBase64Value(namespace, name, key string) (string, error) {
	if err := a.authz(auth.PermSecretsRead, "namespace", namespace, "name", name, "key", key); err != nil {
		return "", err
	}
	return a.k8sMgr.GetSecretBase64Value(namespace, name, key)
}

func (a *App) UpdateSecret(namespace, name string, data map[string]string) error {
	if err := a.authz(auth.PermWrite, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.UpdateSecret(namespace, name, data)
}

func (a *App) GetPersistentVolumes() ([]k8s.PVInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetPersistentVolumes()
}

func (a *App) GetPersistentVolumeClaims(namespaces []string) ([]k8s.PVCInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetPersistentVolumeClaims(namespaces)
}

func (a *App) GetStorageClasses() ([]k8s.StorageClassInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetStorageClasses()
}

func (a *App) GetLimitRanges(namespaces []string) ([]k8s.LimitRangeInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetLimitRanges(namespaces)
}

func (a *App) GetHPA(namespaces []string) ([]k8s.HPAInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHPA(namespaces)
}

func (a *App) GetPodDisruptionBudgets(namespaces []string) ([]k8s.PDBInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetPodDisruptionBudgets(namespaces)
}

func (a *App) GetPriorityClasses() ([]k8s.PriorityClassInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetPriorityClasses()
}

func (a *App) GetRuntimeClasses() ([]k8s.RuntimeClassInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetRuntimeClasses()
}

func (a *App) GetLeases(namespaces []string) ([]k8s.LeaseInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetLeases(namespaces)
}

func (a *App) GetMutatingWebhookConfigurations() ([]k8s.WebhookConfigurationInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetMutatingWebhookConfigurations()
}

func (a *App) GetValidatingWebhookConfigurations() ([]k8s.WebhookConfigurationInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetValidatingWebhookConfigurations()
}

func (a *App) GetResourceQuotas(namespaces []string) ([]k8s.ResourceQuotaInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetResourceQuotas(namespaces)
}
