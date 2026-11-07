// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// cleanYAMLForEditing убирает серверные поля, которые не нужны при редактировании.
// Volume'ы и mount'ы serviceaccount намеренно НЕ трогаем: у пода менять spec.volumes нельзя,
// и применение такого YAML apiserver отклонил бы.
func cleanYAMLForEditing(obj map[string]interface{}) {
	delete(obj, "status")
	for _, f := range []string{"resourceVersion", "uid", "selfLink", "managedFields", "generation"} {
		unstructured.RemoveNestedField(obj, "metadata", f)
	}
}

func (m *Manager) GetResourceYAML(namespace, kind, name string, clean bool) (string, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return "", err
	}
	ri, _, err := resourceClient(conn, kind, namespace)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	obj, err := ri.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get %s/%s: %w", kind, name, err)
	}

	data := obj.Object
	unstructured.RemoveNestedField(data, "metadata", "managedFields")
	if clean {
		cleanYAMLForEditing(data)
	}

	out, err := yaml.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("failed to serialize: %w", err)
	}
	return string(out), nil
}
