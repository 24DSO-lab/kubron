// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"github.com/24DSO-labs/kubron/internal/audit"
	"github.com/24DSO-labs/kubron/internal/auth"
)

// auditClusterCredentialsUpdate — действие аудита «секреты кластера заменены» (подпись — audit.actions.cluster.credentials.update).
const auditClusterCredentialsUpdate = "cluster.credentials.update"

// recordCredentialsUpdate пишет в журнал факт замены секретов в kubeconfig кластера. Значения не записываются.
func (a *App) recordCredentialsUpdate(clusterID string) {
	if a.audit == nil || a.auth == nil {
		return
	}
	actor := a.auth.Actor()
	if full, err := a.auth.CheckCluster(auth.PermClusterManage, clusterID); err == nil {
		actor = full
	}
	actor.ClusterID = clusterID
	a.audit.Record(audit.Event{
		User: actor.Username, Role: string(actor.Role), Action: auditClusterCredentialsUpdate, Result: audit.ResultOK,
		Detail: "id=" + clusterID, Source: actor.Source, TeamID: actor.TeamID, ClusterID: actor.ClusterID,
	})
}
