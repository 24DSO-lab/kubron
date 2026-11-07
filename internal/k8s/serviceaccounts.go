// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (m *Manager) GetServiceAccountDetail(namespace string, name string) (interface{}, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	sa, err := conn.Client.CoreV1().ServiceAccounts(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return sa, nil
}

func (m *Manager) GetServiceAccounts(namespaces []string) (interface{}, error) {
	return scopedList(m, "GetServiceAccounts", "", "serviceaccounts", namespaces, listServiceAccounts,
		func(it corev1.ServiceAccount) string { return it.Namespace })
}

// listServiceAccounts читает ресурсы одного namespace ("" — все) для GetServiceAccounts.
func listServiceAccounts(ctx context.Context, conn *ClusterConnection, namespace string) ([]corev1.ServiceAccount, error) {
	list, err := conn.Client.CoreV1().ServiceAccounts(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}
