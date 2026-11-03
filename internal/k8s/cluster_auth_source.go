// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import "fmt"

// ClusterAuthSource is what the sign-in broker needs to work out how a stored cluster signs in: the stored
// kubeconfig and the selected context. For clusters with their own connection mode (in-cluster, oidc-password)
// Kubeconfig is empty: the broker is not involved in those yet.
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
	if meta.Mode != "" {
		return src, nil
	}
	data, err := clusterStore.Kubeconfig(clusterID)
	if err != nil {
		return ClusterAuthSource{}, fmt.Errorf("kubeconfig not found for cluster %s", clusterID)
	}
	src.Kubeconfig = data
	return src, nil
}
