// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/24DSO-labs/kubron/pkg/ext"
)

// TokenStore — хранилище токенов Argo CD API по идентификатору кластера (ADR-0007, ADR-0008).
// Реализации: файлы (десктоп) и, позже, PostgreSQL с шифрованием (сервер). Интерфейс публичный (pkg/ext).
// Токен приходит уже проверенным (normalizeArgoToken); если токена нет, Get возвращает ошибку,
// для которой errors.Is(err, ext.ErrNotFound) истинно.
type TokenStore = ext.TokenStore

// argoTokens — текущее хранилище токенов. По умолчанию файловое.
var argoTokens TokenStore = fileTokenStore{}

// NewFileTokenStore возвращает файловое хранилище токенов.
func NewFileTokenStore() TokenStore { return fileTokenStore{} }

// SetTokenStore подменяет хранилище токенов Argo CD API (вызывается при запуске приложения).
func SetTokenStore(s TokenStore) { argoTokens = s }

// fileTokenStore — файл 0600 на токен в каталоге argoTokenDir (см. БЕЗОПАСНОСТЬ в argocd_token.go).
type fileTokenStore struct{}

func (fileTokenStore) Put(clusterID, t string) error {
	path, err := argoTokenPath(clusterID)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create token directory: %w", err)
	}
	// Атомарная запись: во временный файл (CreateTemp создаёт его с правами 0600) и переименование.
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to save token: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(t); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to save token: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to save token: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to save token: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to save token: %w", err)
	}
	return nil
}

func (fileTokenStore) Get(clusterID string) (string, error) {
	path, err := argoTokenPath(clusterID)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrArgoTokenNotSet
		}
		return "", fmt.Errorf("failed to read token: %w", err)
	}
	t := strings.TrimSpace(string(data))
	if t == "" {
		return "", ErrArgoTokenNotSet
	}
	return t, nil
}

func (fileTokenStore) Delete(clusterID string) error {
	path, err := argoTokenPath(clusterID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to delete token: %w", err)
	}
	return nil
}
