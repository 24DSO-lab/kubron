// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"strings"

	"github.com/24DSO-labs/kubron/internal/audit"
	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/credential"
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

// Audit actions of the cluster sign-in (labels: audit.actions.cluster.auth.*).
const (
	auditAuthChallenge = "cluster.auth.challenge"
	auditAuthSuccess   = "cluster.auth.success"
	auditAuthFailure   = "cluster.auth.failure"
	auditAuthRevoked   = "cluster.auth.revoked"
)

// authAuditDetail builds the detail of a sign-in event from fixed keys. Only identifiers and reason codes go
// here: never an answer, a token, a client secret, a URL of the sign-in or the text of an error.
func authAuditDetail(clusterID string, kv ...string) string {
	parts := []string{"cluster=" + clusterID}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			parts = append(parts, kv[i]+"="+kv[i+1])
		}
	}
	return strings.Join(parts, " ")
}

// recordAuthEvent writes a sign-in event for the user of this view. principal is the Kubernetes subject (the sub
// claim or the login) and is written next to the application user.
func (a *App) recordAuthEvent(action, result, clusterID, principal string, kv ...string) {
	if a.audit == nil || a.auth == nil {
		return
	}
	actor := a.auth.Actor()
	if clusterID != "" {
		if full, err := a.auth.CheckCluster(auth.PermRead, clusterID); err == nil {
			actor = full
		}
	}
	a.recordAuthEventFor(actor, action, result, clusterID, principal, kv...)
}

// recordAuthEventFor writes a sign-in event for an explicit actor (used when no session of the user is at hand,
// for example when a user is removed from a team).
func (a *App) recordAuthEventFor(actor auth.Actor, action, result, clusterID, principal string, kv ...string) {
	if a.audit == nil {
		return
	}
	actor.ClusterID = clusterID
	a.audit.Record(audit.Event{
		User: actor.Username, Role: string(actor.Role), Action: action, Result: result,
		Detail: authAuditDetail(clusterID, append([]string{"principal", principal}, kv...)...),
		Source: actor.Source, TeamID: actor.TeamID, ClusterID: actor.ClusterID,
	})
}

// authFailureReason maps a sign-in error to a fixed code for the journal.
func authFailureReason(err error) string {
	switch {
	case errors.Is(err, credential.ErrAnswerRejected):
		return "rejected"
	case errors.Is(err, credential.ErrInvalidAnswer):
		return "invalid-answer"
	case errors.Is(err, credential.ErrServiceUnavailable), errors.Is(err, context.DeadlineExceeded):
		return "unavailable" // the identity provider or the network (or its answer took too long), not the user's credentials
	case errors.Is(err, credential.ErrChallengeExpired):
		return "expired"
	case errors.Is(err, credential.ErrInvalidGrant):
		return "invalid-grant"
	case errors.Is(err, credential.ErrUnsupported):
		return "unsupported"
	}
	return "error"
}
