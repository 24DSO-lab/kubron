// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ApplyYAML заменяет объект в кластере манифестом из редактора (get -> update).
// Удалённые в редакторе поля реально удаляются.
func (m *Manager) ApplyYAML(namespace, kind, name, yamlData string) error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}
	ri, info, err := resourceClient(conn, kind, namespace)
	if err != nil {
		return err
	}

	obj, err := parseSingleManifest(yamlData)
	if err != nil {
		return err
	}

	// Защита от применения не к тому объекту
	if obj.GetName() != name {
		return fmt.Errorf("metadata.name cannot be changed (was %q, now %q)", name, obj.GetName())
	}
	if want := info.gvr.GroupVersion().String(); obj.GetAPIVersion() != want {
		return fmt.Errorf("apiVersion must be %q, not %q", want, obj.GetAPIVersion())
	}
	if info.namespaced {
		if ns := obj.GetNamespace(); ns != "" && ns != namespace {
			return fmt.Errorf("metadata.namespace cannot be changed (was %q, now %q)", namespace, ns)
		}
		obj.SetNamespace(namespace)
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 15*time.Second)
	defer cancel()

	cur, err := ri.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get current object: %w", err)
	}
	// resourceVersion из редактора вырезан: берём актуальный, иначе Update у части ресурсов отклоняется
	obj.SetResourceVersion(cur.GetResourceVersion())

	if _, err := ri.Update(ctx, obj, metav1.UpdateOptions{}); err != nil {
		if apierrors.IsConflict(err) {
			return fmt.Errorf("object was modified in the cluster, close the tab and reopen the YAML: %w", err)
		}
		return err
	}
	return nil
}

// defaultNamespace — namespace для нового объекта, если он не указан ни в манифесте, ни в вызове.
const defaultNamespace = "default"

// ManifestNamespace — metadata.namespace манифеста, а если он не указан или манифест не разбирается,
// fallback. Нужен для журнала аудита, поэтому в кластер не ходит и ошибок не возвращает.
func ManifestNamespace(yamlData, fallback string) string {
	if obj, err := parseSingleManifest(yamlData); err == nil && obj.GetNamespace() != "" {
		return obj.GetNamespace()
	}
	return fallback
}

// ManifestName — metadata.name манифеста (пусто, если не указан или манифест не разбирается).
func ManifestName(yamlData string) string {
	obj, err := parseSingleManifest(yamlData)
	if err != nil {
		return ""
	}
	return obj.GetName()
}

func parseSingleManifest(yamlData string) (*unstructured.Unstructured, error) {
	// yaml.v3 корректно обрабатывает строки вроде "n" (в yaml.v2 это булево false),
	// но парсит числа как int, а Kubernetes ожидает int64 — поэтому конвертируем.
	var data map[string]interface{}
	if err := yaml.Unmarshal([]byte(yamlData), &data); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}
	converted := convertYAMLNumbers(data)
	return &unstructured.Unstructured{Object: converted.(map[string]interface{})}, nil
}

// convertYAMLNumbers рекурсивно заменяет int на int64 в map/slice,
// потому что runtime.DeepCopyJSONValue паникует на int.
func convertYAMLNumbers(v interface{}) interface{} {
	switch x := v.(type) {
	case map[string]interface{}:
		for k, val := range x {
			x[k] = convertYAMLNumbers(val)
		}
		return x
	case []interface{}:
		for i, val := range x {
			x[i] = convertYAMLNumbers(val)
		}
		return x
	case int:
		return int64(x)
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case uint:
		return int64(x)
	case uint8:
		return int64(x)
	case uint16:
		return int64(x)
	case uint32:
		return int64(x)
	case uint64:
		return int64(x)
	default:
		return x
	}
}

// CreateResourceYAML создаёт новый объект из манифеста редактора и возвращает его имя.
// kind — ключ типа (ResourceDefinition.id на фронтенде). Namespace берётся из metadata.namespace,
// затем из аргумента, затем "default"; для кластерных ресурсов он игнорируется.
// Если объект с таким именем уже есть, возвращается ошибка: существующие объекты Create не трогает.
func (m *Manager) CreateResourceYAML(namespace, kind, yamlData string) (string, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return "", err
	}

	obj, err := parseSingleManifest(yamlData)
	if err != nil {
		return "", err
	}

	ns := obj.GetNamespace()
	if ns == "" {
		ns = namespace
	}
	if ns == "" {
		ns = defaultNamespace
	}

	ri, info, err := resourceClient(conn, kind, ns)
	if err != nil {
		return "", err
	}

	if obj.GetKind() == "" {
		return "", fmt.Errorf("manifest has no kind")
	}
	if want := info.gvr.GroupVersion().String(); obj.GetAPIVersion() != want {
		return "", fmt.Errorf("apiVersion must be %q, not %q", want, obj.GetAPIVersion())
	}
	if obj.GetName() == "" && obj.GetGenerateName() == "" {
		return "", fmt.Errorf("manifest has no metadata.name")
	}
	if info.namespaced {
		obj.SetNamespace(ns)
	} else {
		obj.SetNamespace("") // для кластерного ресурса API-сервер отклонил бы заданный namespace
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 15*time.Second)
	defer cancel()

	created, err := ri.Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return "", fmt.Errorf("%s %q already exists: %w", obj.GetKind(), obj.GetName(), err)
		}
		return "", err
	}
	return created.GetName(), nil
}
