// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type RevisionInfo struct {
	Revision string `json:"revision"`
	Name     string `json:"name"`
	Replicas int32  `json:"replicas"`
	Ready    int32  `json:"ready"`
	Age      string `json:"age"`
	Images   string `json:"images"`
}

// RestartDeployment выполняет rollout restart
func (m *Manager) RestartDeployment(namespace, name string) error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	// Патчим аннотацию чтобы триггернуть рестарт
	patch := fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"%s"}}}}}`,
		time.Now().Format(time.RFC3339),
	)

	_, err = conn.Client.AppsV1().Deployments(namespace).Patch(
		ctx, name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to restart deployment: %w", err)
	}

	return nil
}

// GetDeploymentRevisions получает список ReplicaSets (ревизий)
func (m *Manager) GetDeploymentRevisions(namespace, name string) ([]RevisionInfo, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	// Получаем deployment для selector
	dep, err := conn.Client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// Получаем ReplicaSets
	selector := metav1.FormatLabelSelector(dep.Spec.Selector)
	rsList, err := conn.Client.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, err
	}

	var revisions []RevisionInfo
	for _, rs := range rsList.Items {
		revision := rs.Annotations["deployment.kubernetes.io/revision"]

		var images []string
		for _, c := range rs.Spec.Template.Spec.Containers {
			images = append(images, c.Image)
		}

		var replicas int32 = 0
		if rs.Spec.Replicas != nil {
			replicas = *rs.Spec.Replicas
		}

		revisions = append(revisions, RevisionInfo{
			Revision: revision,
			Name:     rs.Name,
			Replicas: replicas,
			Ready:    rs.Status.ReadyReplicas,
			Age:      formatAge(rs.CreationTimestamp.Time),
			Images:   joinImages(images),
		})
	}

	return revisions, nil
}

// RollbackDeployment откатывает к указанной ревизии
func (m *Manager) RollbackDeployment(namespace, name string, revision int64) error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	// Находим ReplicaSet с нужной ревизией
	dep, err := conn.Client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	selector := metav1.FormatLabelSelector(dep.Spec.Selector)
	rsList, err := conn.Client.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return err
	}

	revStr := fmt.Sprintf("%d", revision)
	var target *appsv1.ReplicaSet
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		if !metav1.IsControlledBy(rs, dep) {
			continue // ReplicaSet с похожими лейблами, но другого владельца
		}
		if rs.Annotations["deployment.kubernetes.io/revision"] == revStr {
			target = rs
			break
		}
	}
	if target == nil {
		return fmt.Errorf("revision %d not found", revision)
	}

	// Как и kubectl rollout undo: берём template целевого ReplicaSet без лейбла
	// pod-template-hash, который ReplicaSet-контроллер добавляет сам.
	template := target.Spec.Template.DeepCopy()
	delete(template.Labels, appsv1.DefaultDeploymentUniqueLabelKey)
	dep.Spec.Template = *template

	_, err = conn.Client.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to roll back: %w", err)
	}

	return nil
}

func joinImages(images []string) string {
	result := ""
	for i, img := range images {
		if i > 0 {
			result += ", "
		}
		result += img
	}
	return result
}
