// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package jobs — фоновые задачи, которые должна выполнять одна реплика из нескольких (ADR-0016, раздел 6):
// очистка просроченных строк, хранение журнала аудита. Исполнитель выбирается блокировкой pg_try_advisory_lock
// (свой ключ на задачу): реплика, потерявшая соединение с БД или упавшая, отпускает блокировку, и работу
// подхватывает другая на ближайшем такте. Пакет не знает про PostgreSQL: блокировку даёт Locker.
package jobs

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Locker — выбор исполнителя. Acquire возвращает true, если эта реплика исполнитель сейчас (блокировка уже за
// ней или взята сейчас); Release отдаёт её при остановке. Совпадает с clusterhealth.Leader.
type Locker interface {
	Acquire(ctx context.Context) bool
	Release()
}

// Job — одна периодическая задача.
type Job struct {
	Name  string
	Every time.Duration // пауза между тактами; 0 — минута
	// Lock необязателен: без него (одна реплика, файловое хранилище) задача выполняется на каждом такте.
	Lock Locker
	// Run выполняет работу и возвращает, сколько записей обработано.
	Run func(ctx context.Context) (int, error)
	// Logf — журнал процесса; nil — молча.
	Logf func(format string, args ...any)
}

func (j *Job) every() time.Duration {
	if j.Every > 0 {
		return j.Every
	}
	return time.Minute
}

func (j *Job) logf(format string, args ...any) {
	if j.Logf != nil {
		j.Logf(format, args...)
	}
}

// Tick выполняет один такт: если эта реплика исполнитель, запускает задачу. Паника задачи не роняет процесс.
func (j *Job) Tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if j.Lock != nil && !j.Lock.Acquire(ctx) {
		return
	}
	n, err := j.run(ctx)
	switch {
	case err != nil:
		if ctx.Err() == nil {
			j.logf("job %s: %v", j.Name, err)
		}
	case n > 0:
		j.logf("job %s: %d processed", j.Name, n)
	}
}

func (j *Job) run(ctx context.Context) (n int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return j.Run(ctx)
}

// Run запускает задачи (по горутине на каждую) и ждёт отмены ctx; перед возвратом отпускает блокировки.
func Run(ctx context.Context, list ...*Job) {
	var wg sync.WaitGroup
	for _, j := range list {
		wg.Add(1)
		go func(j *Job) {
			defer wg.Done()
			if j.Lock != nil {
				defer j.Lock.Release()
			}
			j.Tick(ctx)
			t := time.NewTicker(j.every())
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					j.Tick(ctx)
				}
			}
		}(j)
	}
	wg.Wait()
}
