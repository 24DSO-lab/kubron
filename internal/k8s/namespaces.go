// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type NamespaceInfo struct {
	Name              string            `json:"name"`
	Status            string            `json:"status"`
	Age               string            `json:"age"`
	CreationTimestamp string            `json:"creationTimestamp"`
	Labels            map[string]string `json:"labels"`
	Annotations       map[string]string `json:"annotations"`
}

func (m *Manager) GetNamespaces() ([]NamespaceInfo, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	nsList, err := conn.Client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var namespaces []NamespaceInfo
	for _, ns := range nsList.Items {
		namespaces = append(namespaces, NamespaceInfo{
			Name:              ns.Name,
			Status:            string(ns.Status.Phase),
			Age:               formatAge(ns.CreationTimestamp.Time),
			CreationTimestamp: ns.CreationTimestamp.Format(time.RFC3339),
			Labels:            ns.Labels,
			Annotations:       ns.Annotations,
		})
	}

	return namespaces, nil
}
