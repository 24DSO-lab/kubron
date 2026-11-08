// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log/slog"

	"github.com/24DSO-lab/kubron/internal/app"
	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/pfproxy"
	"github.com/24DSO-lab/kubron/internal/server"
)

// setupPortForward выбирает режим port-forward (ADR-0017) и при необходимости поднимает прокси потоков:
//   - задан KUBRON_PF_BASE_DOMAIN — режим proxy, порты на хосте не открываются;
//   - режим одного пользователя на loopback — как раньше, localhost-порт;
//   - сервер для нескольких пользователей без домена — port-forward выключен: браузер не достучится до порта
//     на хосте сервера, а сам порт был бы открыт всем локальным процессам.
func setupPortForward(cfg server.Config, core *app.App, sessions *server.Sessions, replicaID string) *pfproxy.Service {
	switch {
	case cfg.DisablePortForward:
		app.SetPortForwardMode(core, k8s.PortForwardModeOff)
		slog.Info("port-forward: disabled by KUBRON_DISABLE_PORT_FORWARD")
	case cfg.PFBaseDomain != "":
		var secret []byte
		if cfg.SecretKey != "" {
			// Ключ подписи выводится из общего ключа сервера: ticket, выданный одной репликой, принимает любая.
			secret = []byte(cfg.SecretKey)
		}
		svc, err := pfproxy.NewService(pfproxy.Config{
			BaseDomain:   cfg.PFBaseDomain,
			Scheme:       cfg.PFScheme,
			ReplicaID:    replicaID,
			IdleTimeout:  cfg.PFIdleTimeout,
			MaxDuration:  cfg.PFMaxDuration,
			SessionAlive: func(owner string) bool { return sessions.Alive(owner) },
		}, secret)
		if err != nil {
			fatal("port-forward", err)
		}
		app.SetPortForwardProxy(core, svc)
		slog.Info("port-forward: proxy mode", "streams_on", "{id}.pf."+svc.BaseDomain())
		return svc
	case cfg.ServiceMode():
		app.SetPortForwardMode(core, k8s.PortForwardModeOff)
		slog.Info("port-forward: disabled (set KUBRON_PF_BASE_DOMAIN to enable the proxy)")
	}
	return nil
}
