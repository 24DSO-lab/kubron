// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"time"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/update"
)

// Проверка обновлений (ADR-0028). Ядро ничего не устанавливает: оно сообщает версию, говорит, вышла ли новая,
// и как именно обновиться для способа установки, который определила оболочка (internal/update).

// updateCheckTimeout ограничивает ручную проверку целиком; запрос к GitHub сам укладывается в 15 секунд.
const updateCheckTimeout = 30 * time.Second

// SetUpdates подключает проверку обновлений; вызывается оболочкой до запуска ядра. Это функция пакета, а не метод
// App: она не должна попадать в привязки Wails и RPC.
func SetUpdates(a *App, c *update.Checker) { a.updates = c }

// StartUpdateChecks запускает фоновую проверку на время жизни ctx. Вызывается один раз на процесс: корневой
// экземпляр, а не виды веб-сессий (они делят один Checker).
func StartUpdateChecks(ctx context.Context, a *App) {
	if a != nil && a.updates != nil {
		go a.updates.Run(ctx)
	}
}

// GetVersionInfo — версия этой сборки и оболочка (десктоп или веб). Без обращения к сети. Доступна любому вошедшему
// пользователю: версию нужно называть в обращениях в поддержку.
func (a *App) GetVersionInfo() (update.Version, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return update.Version{}, err
	}
	if a.updates == nil {
		return update.Version{Version: update.DevVersion}, nil
	}
	return a.updates.Version(), nil
}

// GetUpdateStatus — что известно о новой версии: номер, ссылка на релиз, способ установки и готовые команды.
// Без обращения к сети. Только администратору: способ установки и имя релиза Helm — сведения о развёртывании,
// а обновить Kubron может тот, кто им управляет.
func (a *App) GetUpdateStatus() (update.Info, error) {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return update.Info{}, err
	}
	if a.updates == nil {
		return update.Info{Current: update.DevVersion}, nil
	}
	return a.updates.Info(), nil
}

// CheckForUpdates — проверка по запросу администратора (кнопка «Проверить»). Чаще раза в минуту отдаёт известное
// состояние без запроса к GitHub: лимит API общий для адреса сервера. При запрете проверки (KUBRON_UPDATE_CHECK=off)
// сеть не используется.
func (a *App) CheckForUpdates() (update.Info, error) {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return update.Info{}, err
	}
	if a.updates == nil {
		return update.Info{Current: update.DevVersion}, nil
	}
	ctx, cancel := a.opCtx(updateCheckTimeout)
	defer cancel()
	return a.updates.CheckNow(ctx), nil
}

// SetUpdateCheckEnabled включает или выключает фоновую проверку; настройка сохраняется. Только десктоп: на сервере
// проверку задаёт администратор развёртывания через KUBRON_UPDATE_CHECK, поэтому в веб-режиме метод закрыт
// (internal/server/methods.go).
func (a *App) SetUpdateCheckEnabled(enabled bool) error {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return err
	}
	if a.updates == nil {
		return update.ErrUnavailable
	}
	if a.updates.Locked() {
		return update.ErrLocked
	}
	if err := update.SavePreferences(k8s.GetKubronDir(), update.Preferences{AutoCheck: enabled}); err != nil {
		return err
	}
	a.updates.SetAutoCheck(enabled)
	return nil
}
