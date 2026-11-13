// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/24DSO-lab/kubron/internal/baseline"
	"github.com/24DSO-lab/kubron/internal/jobs"
	"github.com/24DSO-lab/kubron/internal/pgstore"
	"github.com/24DSO-lab/kubron/internal/websession"
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
func maintenanceJobs(pg *pgstore.Store, sessions websession.Backend, auditRetention time.Duration, bl baselineRetention) []*jobs.Job {
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
	}, {
		// Запросы входа в кластер, которые никто не завершил: хранят зашифрованное состояние, живут 10 минут.
		Name: "expired sign-in requests", Every: maintenanceEvery, Lock: lock, Logf: log.Printf,
		Run: func(ctx context.Context) (int, error) { return pg.Challenges().DeleteExpired(ctx, time.Now()) },
	}}
	if auditRetention > 0 { // по умолчанию журнал хранится бессрочно
		list = append(list, &jobs.Job{
			Name: "audit retention", Every: auditPurgeEvery, Lock: lock, Logf: log.Printf,
			Run: func(ctx context.Context) (int, error) {
				return pg.DeleteAuditBefore(ctx, time.Now().Add(-auditRetention))
			},
		})
	}
	list = append(list, baselineJobs(pg.Baselines(), lock, bl)...)
	return list
}

// baselineRetention — хранение данных baseline конфигурации (ADR-0027).
type baselineRetention struct {
	Results     time.Duration // срок хранения результатов проверок
	MinVersions int           // сколько последних версий каждого baseline сохраняется (активная — всегда)
}

const baselinePurgeEvery = time.Hour

// baselineRetentionFromEnv читает KUBRON_BASELINE_RESULTS_RETENTION_DAYS (по умолчанию 90) и KUBRON_BASELINE_MIN_VERSIONS
// (по умолчанию и минимум 20). Неверное значение — ошибка запуска, а не молчаливая подстановка: опечатка не должна
// сократить хранение.
func baselineRetentionFromEnv(getenv func(string) string) (baselineRetention, error) {
	r := baselineRetention{Results: baseline.DefaultResultsRetention, MinVersions: baseline.MinVersionsKept}
	if v := getenv("KUBRON_BASELINE_RESULTS_RETENTION_DAYS"); v != "" {
		days, err := strconv.Atoi(v)
		if err != nil || days < 1 || days > 3650 {
			return r, fmt.Errorf("KUBRON_BASELINE_RESULTS_RETENTION_DAYS must be a number of days from 1 to 3650, got %q", v)
		}
		r.Results = time.Duration(days) * 24 * time.Hour
	}
	if v := getenv("KUBRON_BASELINE_MIN_VERSIONS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < baseline.MinVersionsKept || n > 10000 {
			return r, fmt.Errorf("KUBRON_BASELINE_MIN_VERSIONS must be a number from %d to 10000, got %q", baseline.MinVersionsKept, v)
		}
		r.MinVersions = n
	}
	return r, nil
}

// loadBaselineRetention читает настройки из окружения процесса.
func loadBaselineRetention() (baselineRetention, error) { return baselineRetentionFromEnv(os.Getenv) }

// baselineJobs — задачи хранения baseline: старые результаты проверок и лишние версии. Активная версия не удаляется никогда.
func baselineJobs(b baseline.Backend, lock jobs.Locker, r baselineRetention) []*jobs.Job {
	return []*jobs.Job{{
		Name: "baseline results retention", Every: baselinePurgeEvery, Lock: lock, Logf: log.Printf,
		Run: func(ctx context.Context) (int, error) { return b.DeleteResultsBefore(ctx, time.Now().Add(-r.Results)) },
	}, {
		Name: "baseline versions retention", Every: baselinePurgeEvery, Lock: lock, Logf: log.Printf,
		Run: func(ctx context.Context) (int, error) { return b.PruneVersions(ctx, r.MinVersions) },
	}}
}
