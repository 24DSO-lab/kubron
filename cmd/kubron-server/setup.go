// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log/slog"

	"github.com/24DSO-lab/kubron/internal/app"
	"github.com/24DSO-lab/kubron/internal/server"
)

// setupGate возвращает защиту первичной настройки: в сервисном режиме SetupAdmin требует токен
// (KUBRON_SETUP_TOKEN, заголовок X-Kubron-Setup-Token). В режиме одного пользователя на loopback — nil, токен не нужен.
func setupGate(cfg server.Config, core *app.App) *server.SetupGate {
	if !cfg.ServiceMode() {
		return nil
	}
	pending := func() bool {
		st, err := core.AuthStatus()
		return err != nil || st.NeedsSetup // хранилище недоступно: токен требуется, отказывать безопаснее
	}
	if cfg.SetupToken == "" && pending() {
		// Токен не генерируется и не пишется в журнал: администратора создаёт оператор.
		slog.Warn("setup: there are no users and KUBRON_SETUP_TOKEN is not set; network setup is disabled, run kubron-server create-admin")
	}
	return server.NewSetupGate(cfg.SetupToken, pending)
}
