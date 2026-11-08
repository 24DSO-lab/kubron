// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/streamid"
)

func (a *App) StartLogStream(streamID, namespace, pod, container string, tail int64) error {
	if err := a.authz(auth.PermRead); err != nil {
		return err
	}
	if err := streamid.Check(a.replicaID, streamID); err != nil { // поток должен открыться там, где браузер держит WebSocket событий
		return err
	}
	return a.k8sMgr.StartLogStream(streamID, namespace, pod, container, tail)
}

func (a *App) StopLogStream(streamID string) error {
	if err := a.authz(auth.PermRead); err != nil {
		return err
	}
	if err := streamid.Check(a.replicaID, streamID); err != nil {
		return err
	}
	a.k8sMgr.StopLogStream(streamID)
	return nil
}
