// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log"
	"time"

	"github.com/24DSO-labs/kubron/internal/jobs"
	"github.com/24DSO-labs/kubron/internal/pgstore"
	"github.com/24DSO-labs/kubron/internal/websession"
)

// Периоды уборки. Записи о неудачных входах без блокировки забываются через loginFailureWindow (как счётчики по
// адресу в памяти).
const (
	maintenanceEvery   = time.Minute
	auditPurgeEvery    = 5 * time.Minute
	loginFailureWindow = 15 * time.Minute
)

// maintenanceJobs — фоновые задачи сервиса с PostgreSQL. Каждую такт выполняет одна реплика: исполнитель выбирается
// блокировкой pg_try_advisory_lock (ADR-0016, раздел 6), поэтому реплик может быть сколько угодно.
func maintenanceJobs(pg *pgstore.Store, sessions websession.Backend, auditRetention time.Duration) []*jobs.Job {
	lock := pg.Leader(pgstore.JobsKey) // одно соединение с БД на всю группу
	list := []*jobs.Job{{
		Name: "expired web sessions", Every: maintenanceEvery, Lock: lock, Logf: log.Printf,
		Run: func(ctx context.Context) (int, error) {
			if sessions == nil {
				return 0, nil
			}
			return sessions.DeleteExpired(ctx)
		},
	}, {
		Name: "login failures", Every: maintenanceEvery, Lock: lock, Logf: log.Printf,
		Run: func(ctx context.Context) (int, error) { return pg.LoginFailures().Cleanup(ctx, loginFailureWindow) },
	}}
	if auditRetention > 0 { // по умолчанию журнал хранится бессрочно
		list = append(list, &jobs.Job{
			Name: "audit retention", Every: auditPurgeEvery, Lock: lock, Logf: log.Printf,
			Run: func(ctx context.Context) (int, error) {
				return pg.DeleteAuditBefore(ctx, time.Now().Add(-auditRetention))
			},
		})
	}
	return list
}
