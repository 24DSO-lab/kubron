// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"fmt"
	"path/filepath"
)

// ClusterAuthSource is what the sign-in broker needs to work out how a stored cluster signs in: the stored
// kubeconfig and the selected context. For clusters with their own connection mode (in-cluster) Kubeconfig is
// empty: there is nothing to sign in to.
type ClusterAuthSource struct {
	Kubeconfig  []byte
	ContextName string
	Mode        string
	TeamID      string
}

// LoadClusterAuthSource reads the sign-in related data of a stored cluster. The kubeconfig content is returned
// to the caller only to be described (credential.Broker.Describe); it must not be logged or sent to a client.
func LoadClusterAuthSource(clusterID string) (ClusterAuthSource, error) {
	meta, err := LoadClusterMeta(clusterID)
	if err != nil {
		return ClusterAuthSource{}, fmt.Errorf("cluster metadata not found: %w", err)
	}
	src := ClusterAuthSource{ContextName: meta.ContextName, Mode: meta.Mode, TeamID: meta.TeamID}
	// in-cluster has no kubeconfig and no sign-in. The shared account of a team keeps its kubeconfig (the exec block
	// says how to reach the IdP) and signs in as the team (see PlanFor).
	if meta.Mode != "" && meta.Mode != ModeKubeconfig && meta.Mode != ModeTeamAccount {
		return src, nil
	}
	data, err := clusterStore.Kubeconfig(clusterID)
	if err != nil {
		return ClusterAuthSource{}, fmt.Errorf("kubeconfig not found for cluster %s", clusterID)
	}
	// Desktop only: the identity provider CA files that oidc-login and auth-provider refer to are embedded before the
	// broker describes the sign-in (the web policy never reads files of the server machine).
	if currentPolicy().AllowLocalFiles {
		base := ""
		if p := GetClusterKubeconfigPath(clusterID); p != "" {
			base = filepath.Dir(p)
		}
		if data, err = embedIdPFiles(data, meta.ContextName, base); err != nil {
			return ClusterAuthSource{}, err
		}
	}
	src.Kubeconfig = data
	return src, nil
}
