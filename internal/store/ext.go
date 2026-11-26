// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"errors"
	"path/filepath"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/baseline"
	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/readiness"
	"github.com/24DSO-lab/kubron/pkg/ext"
)

// FromExt адаптирует публичное хранилище (pkg/ext), например серверное на PostgreSQL из другого
// модуля, к внутреннему Store.
func FromExt(s ext.Store) Store { return extStore{s} }

type extStore struct{ s ext.Store }

func (e extStore) Users() auth.Backend  { return auth.FromExt(e.s.Users()) }
func (e extStore) Audit() audit.Backend { return e.s.Audit() }

// Readiness: публичное хранилище (pkg/ext) отчётов не знает, они лежат в каталоге данных (файловая реализация).
func (e extStore) Readiness() readiness.Backend {
	return fileStore{dir: k8s.GetKubronDir()}.Readiness()
}

// Baselines: публичный интерфейс хранилища (pkg/ext) их не знает, поэтому baseline лежат в каталоге данных,
// как и прочие локальные настройки внешнего хранилища.
func (e extStore) Baselines() baseline.Backend {
	return baseline.NewFileBackend(filepath.Join(k8s.GetKubronDir(), "baselines"))
}
func (e extStore) ArgoTokens() k8s.TokenStore { return e.s.ArgoTokens() }

// Incidents использует хранилище расследований внешнего хранилища, если оно его предоставляет (необязательная
// возможность, как WebOIDC); иначе история лежит в файлах каталога данных приложения.
func (e extStore) Incidents() incident.Backend {
	if c, ok := e.s.(interface{ Incidents() incident.Backend }); ok {
		return c.Incidents()
	}
	return incident.NewFileBackend(filepath.Join(k8s.GetKubronDir(), "incidents"))
}

// webOIDCSettingsStore is an optional extension for public stores that persist application-wide web settings.
// It uses plain types only, so that a store of another module can implement it.
type webOIDCSettingsStore interface {
	WebOIDCClient() (clientID, issuerURL string, err error)
	SetWebOIDCClient(clientID, issuerURL string) error
}

// legacyWebOIDCStore is the first form of that extension: a client id without an issuer.
type legacyWebOIDCStore interface {
	WebOIDCClientID() (string, error)
}

// errWebOIDCIssuerUnsupported: the public store keeps the client id only, so the issuer cannot be saved.
var errWebOIDCIssuerUnsupported = errors.New("the store does not keep the issuer of the web OIDC client")

// Web OIDC settings use the external store when it implements the optional capability.
// Otherwise they fall back to the same local data directory used for file-backed clusters.
func (e extStore) WebOIDC() (WebOIDC, error) {
	switch s := e.s.(type) {
	case webOIDCSettingsStore:
		id, issuer, err := s.WebOIDCClient()
		return WebOIDC{ClientID: id, IssuerURL: issuer}, err
	case legacyWebOIDCStore:
		// The first form keeps no issuer: the client id comes back unbound and so is never applied.
		id, err := s.WebOIDCClientID()
		return WebOIDC{ClientID: id}, err
	}
	return (fileStore{dir: k8s.GetKubronDir()}).WebOIDC()
}

func (e extStore) SetWebOIDC(v WebOIDC) error {
	if s, ok := e.s.(webOIDCSettingsStore); ok {
		return s.SetWebOIDCClient(v.ClientID, v.IssuerURL)
	}
	if _, ok := e.s.(legacyWebOIDCStore); ok {
		return errWebOIDCIssuerUnsupported
	}
	return (fileStore{dir: k8s.GetKubronDir()}).SetWebOIDC(v)
}

// Clusters: публичный интерфейс pkg/ext кластеры пока не описывает, поэтому используются файлы ядра.
func (e extStore) Clusters() k8s.ClusterStore { return k8s.NewFileClusterStore() }
