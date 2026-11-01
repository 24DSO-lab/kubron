// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"github.com/24DSO-labs/kubron/internal/audit"
	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/k8s"
)

// fileStore — хранилище десктопа: файлы в каталоге данных приложения (~/.kubron).
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
