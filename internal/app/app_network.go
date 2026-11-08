// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/k8s"
)

func (a *App) GetServices(namespaces []string) ([]k8s.ServiceInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetServices(namespaces)
}

func (a *App) GetServiceDetail(namespace, name string) (*k8s.ServiceDetail, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetServiceDetail(namespace, name)
}

func (a *App) GetIngresses(namespaces []string) ([]k8s.IngressInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetIngresses(namespaces)
}

func (a *App) GetEndpoints(namespaces []string) ([]k8s.EndpointListInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetEndpoints(namespaces)
}

func (a *App) GetEndpointSlices(namespaces []string) ([]k8s.EndpointSliceInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetEndpointSlices(namespaces)
}

func (a *App) GetNetworkPolicies(namespaces []string) ([]k8s.NetworkPolicyInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetNetworkPolicies(namespaces)
}

func (a *App) GetIngressClasses() ([]k8s.IngressClassInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetIngressClasses()
}

func (a *App) SetDefaultIngressClass(name string, makeDefault bool) error {
	if err := a.authz(auth.PermWrite, "name", name, "default", makeDefault); err != nil {
		return err
	}
	return a.k8sMgr.SetDefaultIngressClass(name, makeDefault)
}
