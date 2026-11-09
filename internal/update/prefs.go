// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package update

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// prefsFile — файл настройки в каталоге данных Kubron. Отдельный от настроек приложения (k8s.KubronConfig):
// общий объект настроек сохраняется целиком из интерфейса, и поле, которого интерфейс не знает, он бы затёр.
const prefsFile = "updates.json"

// Preferences — настройка пользователя десктопа. На сервере проверку задаёт KUBRON_UPDATE_CHECK, файл не используется.
type Preferences struct {
	// AutoCheck — искать обновления в фоне. По умолчанию включено.
	AutoCheck bool `json:"autoCheck"`
}

// LoadPreferences читает настройку из каталога dir. Нет файла или он повреждён — значения по умолчанию:
// настройка обновлений не должна мешать запуску.
func LoadPreferences(dir string) Preferences {
	p := Preferences{AutoCheck: true}
	b, err := os.ReadFile(filepath.Join(dir, prefsFile))
	if err != nil {
		return p
	}
	var stored struct {
		AutoCheck *bool `json:"autoCheck"`
	}
	if json.Unmarshal(b, &stored) == nil && stored.AutoCheck != nil {
		p.AutoCheck = *stored.AutoCheck
	}
	return p
}

// SavePreferences записывает настройку атомарно: сначала во временный файл, затем переименованием.
func SavePreferences(dir string, p Preferences) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, prefsFile+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // после успешного Rename файла уже нет, ошибка безразлична
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dir, prefsFile))
}
