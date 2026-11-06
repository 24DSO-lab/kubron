// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (m *Manager) GetClusterRoleBindings() (interface{}, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	list, err := conn.Client.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}
