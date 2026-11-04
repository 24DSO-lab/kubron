// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"github.com/24DSO-labs/kubron/internal/audit"
	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/k8s"
	"github.com/24DSO-labs/kubron/pkg/ext"
)

// FromExt адаптирует публичное хранилище (pkg/ext), например серверное на PostgreSQL из другого
// модуля, к внутреннему Store.
func FromExt(s ext.Store) Store { return extStore{s} }

type extStore struct{ s ext.Store }

func (e extStore) Users() auth.Backend        { return auth.FromExt(e.s.Users()) }
func (e extStore) Audit() audit.Backend       { return e.s.Audit() }
func (e extStore) ArgoTokens() k8s.TokenStore { return e.s.ArgoTokens() }

// webOIDCSettingsStore is an optional extension for public stores that persist application-wide web settings.
type webOIDCSettingsStore interface {
	WebOIDCClientID() (string, error)
	SetWebOIDCClientID(string) error
}

// Web OIDC settings use the external store when it implements the optional capability.
// Otherwise they fall back to the same local data directory used for file-backed clusters.
func (e extStore) WebOIDCClientID() (string, error) {
	if s, ok := e.s.(webOIDCSettingsStore); ok {
		return s.WebOIDCClientID()
	}
	return (fileStore{dir: k8s.GetKubronDir()}).WebOIDCClientID()
}

func (e extStore) SetWebOIDCClientID(clientID string) error {
	if s, ok := e.s.(webOIDCSettingsStore); ok {
		return s.SetWebOIDCClientID(clientID)
	}
	return (fileStore{dir: k8s.GetKubronDir()}).SetWebOIDCClientID(clientID)
}

// Clusters: публичный интерфейс pkg/ext кластеры пока не описывает, поэтому используются файлы ядра.
func (e extStore) Clusters() k8s.ClusterStore { return k8s.NewFileClusterStore() }
