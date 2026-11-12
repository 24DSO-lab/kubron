// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"

	"github.com/24DSO-lab/kubron/internal/app"
	"github.com/24DSO-lab/kubron/internal/jobs"
)

// setupDesktopIncidents подключает расследования Kubron Intelligence (ADR-0024): история лежит в каталоге данных
// приложения, задача retention работает без блокировки (один процесс). Неверная KUBRON_INCIDENT_* не мешает
// запуску: расследования выключены, остальное приложение работает.
func setupDesktopIncidents(ctx context.Context, a *app.App, getenv func(string) string) {
	job, err := app.SetupIncidents(a, getenv)
	if err != nil {
		slog.Error("incidents: disabled because of an invalid setting", "error", err.Error())
		return
	}
	go jobs.Run(ctx, job)
}
