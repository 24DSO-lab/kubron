// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/24DSO-lab/kubron/internal/clusterhealth"
	"github.com/24DSO-lab/kubron/internal/replicaevents"
)

// События о состоянии кластеров и доступе (фронтенд перечитывает список кластеров).
const (
	// EventClustersHealth — у какого-то кластера изменилось состояние соединения; приходит всем,
	// а список каждый клиент перечитывает сам и получает только свои кластеры.
	EventClustersHealth = "clusters:health"
	// EventClusterAccessRevoked — пользователь потерял доступ к активному кластеру: ресурсы закрыты.
	EventClusterAccessRevoked = "cluster:access-revoked"
)

// StartHealthMonitor запускает фоновую проверку соединения с кластерами (задача 3.6) до отмены ctx.
// interval <= 0 — не запускать. С PostgreSQL проверяет одна реплика (pg_advisory_lock), остальные показывают
// её результаты из общей таблицы; без БД монитор работает в одном процессе.
// Это функция пакета, а не метод App: она не должна попадать в привязки Wails и RPC.
func StartHealthMonitor(ctx context.Context, root *App, interval time.Duration) {
	if root == nil || root.k8sMgr == nil || interval <= 0 {
		return
	}
	mon := root.k8sMgr.NewHealthMonitor()
	mon.Interval = interval
	// Logf монитора — printf-style; пересылаем в slog, чтобы записи шли тем же структурированным потоком.
	mon.Logf = func(format string, args ...any) {
		slog.Warn(fmt.Sprintf(format, args...), "component", "clusterhealth")
	}
	if hs, ok := root.store.(interface{ HealthSink() clusterhealth.Sink }); ok {
		mon.Sink = hs.HealthSink()
	}
	if hl, ok := root.store.(interface{ HealthLeader() clusterhealth.Leader }); ok {
		mon.Leader = hl.HealthLeader()
	}
	mon.OnChange = func() { root.platform.Emit(EventClustersHealth) }
	// Лидер сообщает остальным репликам, что результаты проверок в БД обновились (health.changed).
	mon.OnPublish = func() {
		if root.publish != nil {
			root.publish(replicaevents.Message{Kind: replicaevents.HealthChanged})
		}
	}
	root.health = mon
	go mon.Run(ctx)
}

// SetReplicaPublisher задаёт отправку сообщений другим репликам (ADR-0016); вызывается до StartHealthMonitor.
// Это функция пакета, а не метод App: она не должна попадать в привязки Wails и RPC.
// SetReplicaID задаёт идентификатор реплики для потоков (терминал, логи); вызывается до создания видов.
func SetReplicaID(root *App, id string) { root.replicaID = id }

func SetReplicaPublisher(root *App, publish func(replicaevents.Message)) { root.publish = publish }

// RefreshHealth немедленно читает результаты проверок лидера из общего хранилища (сообщение health.changed).
func RefreshHealth(ctx context.Context, root *App) {
	if root != nil && root.health != nil {
		root.health.Refresh(ctx)
	}
}

// NotifyClustersChanged сообщает всем клиентам этой реплики, что список кластеров или доступ к ним изменились
// (сообщения access.changed и directory.changed): клиент перечитывает список и получает только свои кластеры.
func NotifyClustersChanged(root *App) {
	if root != nil {
		root.platform.Emit(EventClustersHealth)
	}
}

// EnforceAccess закрывает ресурсы вида, если пользователь больше не видит свой активный кластер:
// его перенесли в чужую команду, пользователя исключили или отключили, кластер удалили. Открытые
// терминалы, потоки логов и port-forward завершаются, активный кластер сбрасывается, а клиент получает
// EventClusterAccessRevoked. Права проверяются при каждом вызове, но уже запущенные потоки сами их не
// перепроверяют; эта функция и есть перепроверка (сервер вызывает её раз в несколько секунд).
// Возвращает true, если доступ был закрыт.
// Это функция пакета, а не метод App: она не должна попадать в привязки Wails и RPC.
func EnforceAccess(v *App) bool {
	if !ViewActive(v) || v.k8sMgr == nil {
		return false
	}
	id := v.k8sMgr.GetActiveContext()
	if id == "" {
		return v.applyRevocations()
	}
	// Сначала проверяем членство: исключение из команды одновременно отзывает личные credentials,
	// но для активного кластера это потеря доступа, а не просто необходимость повторного входа.
	if !v.auth.CanSeeCluster(id) {
		v.endSessionResources()
		v.k8sMgr.Detach()
		v.k8sMgr.Notify(EventClusterAccessRevoked, id)
		return true
	}
	// Если доступ к команде остался, но отозван личный вход (смена пароля, выход, «отключиться»),
	// отключаем ресурсы и просим пройти авторизацию заново.
	if v.applyRevocations() {
		return true
	}
	return false
}
