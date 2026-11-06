// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"strings"

	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/k8s"
)

func (a *App) GetResourceYAML(namespace, kind, name string, clean bool) (string, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return "", err
	}
	// YAML секрета содержит его данные, поэтому требует того же права, что и GetSecretValue.
	if strings.EqualFold(strings.TrimSpace(kind), "secret") {
		if err := a.authz(auth.PermSecretsRead); err != nil {
			return "", err
		}
	}
	return a.k8sMgr.GetResourceYAML(namespace, kind, name, clean)
}

func (a *App) ApplyYAML(namespace, kind, name, jsonData string) error {
	if err := a.authz(auth.PermWrite, "namespace", namespace, "kind", kind, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.ApplyYAML(namespace, kind, name, jsonData)
}

// CreateResourceYAML создаёт новый объект из манифеста (кнопка «+» в списках) и возвращает его имя.
// kind — ключ типа (id определения ресурса на фронтенде). namespace — запасное значение,
// если в манифесте metadata.namespace не указан.
func (a *App) CreateResourceYAML(namespace, kind, yamlData string) (string, error) {
	// authz — первый оператор (его проверяет policy_test). namespace и name в журнал берём из самого
	// манифеста: именно их увидит кластер.
	if err := a.authz(auth.PermWrite, "namespace", k8s.ManifestNamespace(yamlData, namespace),
		"kind", kind, "name", k8s.ManifestName(yamlData)); err != nil {
		return "", err
	}
	return a.k8sMgr.CreateResourceYAML(namespace, kind, yamlData)
}

// DeleteResource удаляет ресурс любого поддерживаемого типа. kind — ключ типа
// (совпадает с id определения ресурса на фронтенде, например "deployment").
func (a *App) DeleteResource(kind, namespace, name string) error {
	if err := a.authz(auth.PermWrite, "kind", kind, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.DeleteResource(kind, namespace, name)
}

func (a *App) GetCRDs() ([]k8s.CRDInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetCRDs()
}

func (a *App) GetCustomResources(group, version, plural string, namespaces []string) ([]k8s.CustomResourceInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetCustomResources(group, version, plural, namespaces)
}
