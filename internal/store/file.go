// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/24DSO-labs/kubron/internal/audit"
	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/k8s"
)

type webOIDCSettings struct {
	ClientID string `json:"clientId"`
}

// fileStore — хранилище десктопа и локального web-режима: файлы в каталоге данных приложения (~/.kubron).
type fileStore struct {
	dir string
}

// NewFile возвращает файловое хранилище. Пользователи и журнал лежат в dir; токены Argo CD
// — в собственном каталоге k8s (argoTokenDir), как раньше.
func NewFile(dir string) Store { return fileStore{dir: dir} }

func (s fileStore) Users() auth.Backend        { return auth.NewFileBackend(s.dir) }
func (s fileStore) Audit() audit.Backend       { return audit.NewFileBackend(s.dir) }
func (s fileStore) ArgoTokens() k8s.TokenStore { return k8s.NewFileTokenStore() }

// Clusters — каталог clusters в каталоге данных (k8s.GetKubronDir): путь к kubeconfig нужен client-go и Helm.
func (s fileStore) Clusters() k8s.ClusterStore { return k8s.NewFileClusterStore() }

func (s fileStore) WebOIDCClientID() (string, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, "web-oidc.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var cfg webOIDCSettings
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", err
	}
	return cfg.ClientID, nil
}

func (s fileStore) SetWebOIDCClientID(clientID string) error {
	path := filepath.Join(s.dir, "web-oidc.json")
	if clientID == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(webOIDCSettings{ClientID: clientID})
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
