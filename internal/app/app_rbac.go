// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"github.com/24DSO-labs/kubron/internal/auth"
)

func (a *App) GetServiceAccounts(namespaces []string) (interface{}, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetServiceAccounts(namespaces)
}

func (a *App) GetRoles(namespaces []string) (interface{}, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetRoles(namespaces)
}

func (a *App) GetRoleBindings(namespaces []string) (interface{}, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetRoleBindings(namespaces)
}

func (a *App) GetClusterRoles() (interface{}, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetClusterRoles()
}

func (a *App) GetClusterRoleBindings() (interface{}, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetClusterRoleBindings()
}

func (a *App) GetServiceAccountDetail(namespace, name string) (interface{}, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetServiceAccountDetail(namespace, name)
}
