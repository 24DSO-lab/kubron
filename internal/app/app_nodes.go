// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/k8s"
)

func (a *App) CordonNode(name string, cordon bool) error {
	if err := a.authz(auth.PermOperate, "node", name, "cordon", cordon); err != nil {
		return err
	}
	return a.k8sMgr.CordonNode(name, cordon)
}

func (a *App) DrainNode(name string) error {
	if err := a.authz(auth.PermWrite, "node", name); err != nil {
		return err
	}
	return a.k8sMgr.DrainNode(name)
}

func (a *App) StartNodeShell(name string) (*k8s.NodeShellSession, error) {
	if err := a.authz(auth.PermNodeShell, "node", name); err != nil {
		return nil, err
	}
	if a.interactive.DisableExec {
		return nil, errExecDisabled
	}
	if a.interactive.DisableNodeShell {
		return nil, errNodeShellDisabled
	}
	return a.k8sMgr.StartNodeShell(name)
}

func (a *App) GetNodes() ([]k8s.NodeInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetNodes()
}

func (a *App) GetNodeDetail(name string) (*k8s.NodeDetail, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetNodeDetail(name)
}
