// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log"

	"github.com/24DSO-labs/kubron/internal/pgstore"
	"github.com/24DSO-labs/kubron/internal/server"
)

// readyzOption подключает /readyz: проверка БД (если сервер работает с PostgreSQL) и признак draining
// из lifecycle. pgstore не имеет собственного Ping, поэтому используется самый дешёвый уже
// существующий запрос — чтение одной небольшой таблицы состояний кластеров через пул (он же проверяет,
// что схема на месте). Если появится pgstore.Store.Ping, замена — одна строка здесь.
func readyzOption(pg *pgstore.Store, lifecycle *server.Lifecycle) server.HandlerOption {
	cfg := server.ReadyzConfig{Drain: lifecycle, Logf: log.Printf}
	if pg != nil { // nil *Store в интерфейсе дал бы «не nil» DBPinger: подставляем только настоящий
		health := pg.Health()
		cfg.DB = server.DBPingFunc(func(ctx context.Context) error {
			_, err := health.Load(ctx)
			return err
		})
	}
	return server.WithReadyz(cfg)
}
