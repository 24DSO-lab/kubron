// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"

	"github.com/24DSO-lab/kubron/internal/app"
	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/update"
)

// version — версия сборки. Релиз задаёт её через `wails build -ldflags "-X main.version=<версия>"`
// (.github/workflows/release.yml); без этого (wails dev, go build) остаётся "dev", и проверка обновлений отключена.
var version = update.DevVersion

// setupDesktopUpdates подключает проверку обновлений десктопа (ADR-0028). Приложение раз в сутки спрашивает GitHub
// о последнем релизе и подсказывает, как обновиться для способа установки (.deb, архив, macOS-приложение); файл
// заменить само не может. Пользователь выключает фоновую проверку в настройках, администратор организации —
// переменной KUBRON_UPDATE_CHECK=off (тогда запросов нет совсем).
func setupDesktopUpdates(ctx context.Context, a *app.App, getenv func(string) string) {
	locked, err := update.PolicyFromEnv(getenv)
	if err != nil {
		// Опечатка в политике не должна оставить запросы включёнными: при сомнении проверка выключена.
		slog.Error("update check: disabled because of an invalid setting", "error", err.Error())
		locked = true
	}
	env := update.OSEnv()
	env.Getenv = getenv
	install := update.Detect(update.TargetDesktop, env)
	app.SetUpdates(a, update.NewChecker(update.Config{
		Current:   version,
		Install:   install,
		Locked:    locked,
		AutoCheck: update.LoadPreferences(k8s.GetKubronDir()).AutoCheck,
	}))
	app.StartUpdateChecks(ctx, a)
	slog.Info("update check", "version", version, "install", string(install.Method), "locked", locked)
}
