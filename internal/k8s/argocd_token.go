// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/24DSO-labs/kubron/pkg/ext"
)

// ErrArgoTokenNotSet — для активного кластера не задан токен Argo CD API.
var ErrArgoTokenNotSet error = notSetError{}

// notSetError — «токен не задан»; совпадает и с ext.ErrNotFound, чтобы внешние хранилища
// могли сообщать об отсутствии токена общей ошибкой.
type notSetError struct{}

func (notSetError) Error() string        { return "Argo CD API token is not set" }
func (notSetError) Is(target error) bool { return target == ext.ErrNotFound }

const argoTokenMaxLen = 8192

// argoTokenDir — каталог токенов; переменная нужна тестам (по умолчанию ~/.kubron/argocd).
var argoTokenDir = func() string { return filepath.Join(GetKubronDir(), "argocd") }

// ArgoAPIStatus — состояние опционального провайдера Argo CD API. Сам токен фронтенду не отдаётся никогда.
type ArgoAPIStatus struct {
	Configured bool `json:"configured"`
}

// БЕЗОПАСНОСТЬ. Токен хранится так же, как kubeconfig-файлы кластеров: файл 0600 в каталоге 0700 внутри
// ~/.kubron (отдельного хранилища секретов в приложении нет). Имя файла — хеш идентификатора кластера,
// поэтому оно не зависит от символов в имени и не позволяет выйти из каталога. Токен нельзя:
// возвращать во фронтенд, писать в логи, передавать в a.authz (попадёт в аудит) и включать в тексты ошибок.
func argoTokenPath(clusterID string) (string, error) {
	if clusterID == "" {
		return "", errors.New("no active cluster")
	}
	sum := sha256.Sum256([]byte(clusterID))
	return filepath.Join(argoTokenDir(), hex.EncodeToString(sum[:])+".token"), nil
}

// normalizeArgoToken убирает пробелы по краям и отклоняет заведомо некорректные значения.
// Сообщения об ошибках намеренно не содержат само значение.
func normalizeArgoToken(token string) (string, error) {
	t := strings.TrimSpace(token)
	if t == "" {
		return "", errors.New("token is empty")
	}
	if len(t) > argoTokenMaxLen {
		return "", errors.New("token is too long")
	}
	for _, r := range t {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", errors.New("token contains whitespace or control characters")
		}
	}
	return t, nil
}

func saveArgoToken(clusterID, token string) error {
	t, err := normalizeArgoToken(token)
	if err != nil {
		return err
	}
	return argoTokens.Put(clusterID, t)
}

func loadArgoToken(clusterID string) (string, error) {
	t, err := argoTokens.Get(clusterID)
	if errors.Is(err, ext.ErrNotFound) {
		return "", ErrArgoTokenNotSet
	}
	return t, err
}

func deleteArgoToken(clusterID string) error {
	return argoTokens.Delete(clusterID)
}

// GetArgoAPIStatus сообщает, задан ли токен для активного кластера.
func (m *Manager) GetArgoAPIStatus() (*ArgoAPIStatus, error) {
	id := m.GetActiveContext()
	if id == "" {
		return nil, errors.New("no active connection")
	}
	_, err := loadArgoToken(id)
	switch {
	case err == nil:
		return &ArgoAPIStatus{Configured: true}, nil
	case errors.Is(err, ErrArgoTokenNotSet):
		return &ArgoAPIStatus{Configured: false}, nil
	default:
		return nil, err
	}
}

// SetArgoAPIToken сохраняет токен Argo CD API для активного кластера.
func (m *Manager) SetArgoAPIToken(token string) error {
	id := m.GetActiveContext()
	if id == "" {
		return errors.New("no active connection")
	}
	return saveArgoToken(id, token)
}

// DeleteArgoAPIToken удаляет токен активного кластера.
func (m *Manager) DeleteArgoAPIToken() error {
	id := m.GetActiveContext()
	if id == "" {
		return errors.New("no active connection")
	}
	return deleteArgoToken(id)
}
