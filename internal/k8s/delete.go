// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeleteResource удаляет ресурс любого поддерживаемого типа (см. kindTable).
// kind — тот же ключ, что и ResourceDefinition.id на фронтенде.
// Для кластерных ресурсов namespace игнорируется.
func (m *Manager) DeleteResource(kind, namespace, name string) error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}
	ri, _, err := resourceClient(conn, kind, namespace)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	// Background: зависимые объекты (поды Deployment/Job и т.п.) удаляются сборщиком мусора.
	// Без явной политики dynamic-клиент для batch/v1 оставляет их «сиротами».
	policy := metav1.DeletePropagationBackground
	opts := metav1.DeleteOptions{PropagationPolicy: &policy}
	if err := ri.Delete(ctx, name, opts); err != nil {
		return fmt.Errorf("failed to delete %s %s: %w", kind, name, err)
	}
	return nil
}
