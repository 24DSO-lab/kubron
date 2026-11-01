// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log"

	"github.com/24DSO-labs/kubron/internal/app"
	"github.com/24DSO-labs/kubron/internal/k8s"
	"github.com/24DSO-labs/kubron/internal/pfproxy"
	"github.com/24DSO-labs/kubron/internal/server"
)

// setupPortForward выбирает режим port-forward (ADR-0017) и при необходимости поднимает прокси потоков:
//   - задан KUBRON_PF_BASE_DOMAIN — режим proxy, порты на хосте не открываются;
//   - режим одного пользователя на loopback — как раньше, localhost-порт;
//   - сервер для нескольких пользователей без домена — port-forward выключен: браузер не достучится до порта
//     на хосте сервера, а сам порт был бы открыт всем локальным процессам.
func setupPortForward(cfg server.Config, core *app.App, sessions *server.Sessions) *pfproxy.Service {
	switch {
	case cfg.PFBaseDomain != "":
		var secret []byte
		if cfg.SecretKey != "" {
			// Ключ подписи выводится из общего ключа сервера: ticket, выданный одной репликой, принимает любая.
			secret = []byte(cfg.SecretKey)
		}
		svc, err := pfproxy.NewService(pfproxy.Config{
			BaseDomain:   cfg.PFBaseDomain,
			Scheme:       cfg.PFScheme,
			IdleTimeout:  cfg.PFIdleTimeout,
			MaxDuration:  cfg.PFMaxDuration,
			SessionAlive: func(owner string) bool { return sessions.Alive(owner) },
		}, secret)
		if err != nil {
			log.Fatalf("port-forward: %v", err)
		}
		app.SetPortForwardProxy(core, svc)
		log.Printf("port-forward: proxy mode, streams on {id}.pf.%s", svc.BaseDomain())
		return svc
	case cfg.ServiceMode():
		app.SetPortForwardMode(core, k8s.PortForwardModeOff)
		log.Printf("port-forward: disabled (set KUBRON_PF_BASE_DOMAIN to enable the proxy)")
	}
	return nil
}
