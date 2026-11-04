// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"sync"

	"github.com/24DSO-labs/kubron/internal/audit"
	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/k8s"
)

// revocations is the replica-wide record of revoked personal sign-ins. Views of the same user live in different
// sessions, so a revoke made in one of them is found by the others through this record (EnforceAccess polls it).
// It is shared by all views (a pointer in App); a nil value ignores everything.
type revocations struct {
	mu   sync.Mutex
	seq  uint64
	list []revocation
}

type revocation struct {
	seq     uint64
	owner   string
	cluster string // "" - every cluster of the owner
}

const maxRevocations = 512

func (r *revocations) add(owner, cluster string) {
	if r == nil || owner == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	r.list = append(r.list, revocation{seq: r.seq, owner: owner, cluster: cluster})
	if len(r.list) > maxRevocations {
		r.list = append([]revocation(nil), r.list[len(r.list)-maxRevocations:]...)
	}
}

// pending returns the clusters revoked for owner after the given sequence number and the newest sequence number.
func (r *revocations) pending(owner string, after uint64) (clusters []string, last uint64) {
	if r == nil {
		return nil, after
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	last = r.seq
	for _, e := range r.list {
		if e.seq > after && e.owner == owner {
			clusters = append(clusters, e.cluster)
		}
	}
	return clusters, last
}

// revokePersonal removes the saved personal sign-in of a user (for one cluster, or for all if clusterID is
// empty), closes the user's connections to the pool and records the revoke in the journal. reason is a fixed code.
func (a *App) revokePersonal(userID, clusterID, reason string) error {
	if a.broker == nil || userID == "" {
		return nil
	}
	err := a.broker.Revoke(context.Background(), credential.RevokeFilter{ClusterID: clusterID, OwnerID: userID, Scope: credential.ScopeUser})
	if err != nil {
		return err
	}
	a.k8sMgr.DropPersonal(clusterID, userID)
	a.rev.add(userID, clusterID)
	a.recordAuthEvent(auditAuthRevoked, audit.ResultOK, clusterID, "", "reason", reason, "owner", userID)
	return nil
}

// revokeTeamClusters revokes the personal sign-ins of a user for every cluster of a team.
func (a *App) revokeTeamClusters(userID, teamID, reason string) error {
	if a.broker == nil {
		return nil
	}
	clusters, err := k8s.ListClusters()
	if err != nil {
		return err
	}
	var first error
	for _, c := range clusters {
		if c.TeamID != teamID {
			continue
		}
		if err := a.revokePersonal(userID, c.ID, reason); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// currentUserID is the id of the signed-in user of this view; empty if there is none.
func (a *App) currentUserID() string {
	if a.auth == nil {
		return ""
	}
	if u, err := a.auth.Current(); err == nil {
		return u.ID
	}
	return ""
}

// applyRevocations closes the resources of this view (terminals, logs, port-forwards, the active cluster) when a
// sign-in of its user for the active cluster was revoked, and tells the client that a sign-in is needed again.
// Returns true if something was closed.
func (a *App) applyRevocations() bool {
	if a.rev == nil || a.conn == nil || a.auth == nil || a.k8sMgr == nil {
		return false
	}
	owner := a.currentUserID()
	if owner == "" {
		return false
	}
	clusters, last := a.rev.pending(owner, a.conn.seen())
	a.conn.markSeen(last)
	active := a.k8sMgr.GetActiveContext()
	if active == "" {
		return false
	}
	for _, c := range clusters {
		if c == "" || c == active {
			a.endSessionResources()
			a.k8sMgr.Detach()
			a.emit(EventAuthRequired, authEventData{ClusterID: active})
			return true
		}
	}
	return false
}

// dropTeamSignIn forgets the saved shared sign-in of a cluster in the broker. The administrator has just set
// credentials through the old methods (which write the old store); the broker's record must not outlive them as a
// stale password, so the next connection asks the administrator once through a challenge.
func (a *App) dropTeamSignIn(clusterID string) error {
	if a.broker == nil || clusterID == "" {
		return nil
	}
	return a.broker.Revoke(context.Background(), credential.RevokeFilter{ClusterID: clusterID, Scope: credential.ScopeTeam})
}
