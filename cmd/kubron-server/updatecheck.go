// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"

	"github.com/24DSO-lab/kubron/internal/app"
	"github.com/24DSO-lab/kubron/internal/update"
)

// Проверка обновлений (ADR-0028). Сервер раз в сутки спрашивает GitHub о последнем релизе и показывает
// администраторам, как обновиться для своего способа установки; сам ничего не устанавливает.
//
//	KUBRON_UPDATE_CHECK — on (по умолчанию) | off
//
// off запрещает любые исходящие запросы проверки (в изолированных сетях и при строгой политике egress).
// Способ установки задают KUBRON_INSTALL_METHOD (helm | kubernetes | docker | binary), KUBRON_HELM_RELEASE и
// KUBRON_HELM_NAMESPACE: чарт выставляет их сам, без них способ определяется по окружению пода или контейнера.
// Сборка из исходников (version == "dev") ничего не проверяет.
func setupUpdateCheck(ctx context.Context, core *app.App, getenv func(string) string) error {
	locked, err := update.PolicyFromEnv(getenv)
	if err != nil {
		return err
	}
	env := update.OSEnv()
	env.Getenv = getenv
	install := update.Detect(update.TargetWeb, env)
	app.SetUpdates(core, update.NewChecker(update.Config{
		Current:   version,
		Install:   install,
		Locked:    locked,
		AutoCheck: true,
	}))
	app.StartUpdateChecks(ctx, core)
	slog.Info("update check", "enabled", !locked && version != update.DevVersion, "install", string(install.Method))
	return nil
}
