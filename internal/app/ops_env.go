// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/opsplatform"
)

// Организация и команда десктопа: каталога команд нет, но tenant.Scope обязан быть полным (ADR-0026 §4).
const (
	localOrgID       = "local"
	localDefaultTeam = "default"
)

// orgIDer — хранилище, знающее организацию каталога (PostgreSQL).
type orgIDer interface{ OrgID() string }

// opsEnv — реализация opsplatform.Env поверх вида App. Все пути идут через существующие слои: права — auth.Service,
// подключение — Manager.ConnectionFor (ConfigFor), аудит — audit.Logger, события — Manager.Notify (владелец вида).
type opsEnv struct{ a *App }

func (e opsEnv) Check(p auth.Permission, clusterID string) (auth.Actor, error) {
	if e.a.auth == nil {
		return auth.Actor{}, e.a.authUnavailable()
	}
	return e.a.auth.CheckCluster(p, clusterID)
}

func (e opsEnv) OrgID() string {
	if o, ok := e.a.store.(orgIDer); ok {
		if id := o.OrgID(); id != "" {
			return id
		}
		return ""
	}
	return localOrgID
}

func (e opsEnv) DefaultTeam() string {
	if t := e.a.defaultTeam(); t != "" {
		return t
	}
	if e.a.dir != nil {
		return "" // сервисный режим без команды по умолчанию: пустой scope будет отклонён
	}
	return localDefaultTeam
}

func (e opsEnv) SessionID() string { return e.a.sessionID }

func (e opsEnv) Clients(_ context.Context, clusterID string) (opsplatform.Clients, error) {
	if e.a.k8sMgr == nil {
		return opsplatform.Clients{}, errors.New("cluster manager is not available")
	}
	e.a.bindAuthSession()
	conn, err := e.a.k8sMgr.ConnectionFor(clusterID)
	if err != nil {
		if errors.Is(err, k8s.ErrClusterUnknown) {
			// Несуществующий кластер — тот же ответ, что и для чужого (404), а не «недоступен» (500).
			return opsplatform.Clients{}, auth.ErrClusterNotFound
		}
		return opsplatform.Clients{}, err
	}
	// Интерфейсы заполняются только при наличии клиента: nil-указатель в интерфейсе не должен выглядеть живым.
	var c opsplatform.Clients
	if conn.DynamicClient != nil {
		c.Dynamic = conn.DynamicClient
	}
	if conn.Client != nil {
		c.Core = conn.Client
	}
	if conn.DiscoveryClient != nil {
		c.Discovery = conn.DiscoveryClient
	}
	if c.Dynamic == nil || c.Core == nil || c.Discovery == nil {
		return opsplatform.Clients{}, errors.New("cluster connection is incomplete")
	}
	return c, nil
}

func (e opsEnv) RecordAudit(ev audit.Event) {
	if e.a.audit != nil {
		e.a.audit.Record(ev)
	}
}

// Notify адресует событие владельцу вида: в вебе это его сессия (Event Hub), в десктопе — единственное окно.
// Широковещания нет: у корневого вида веб-сервера уведомителя нет, и событие никуда не уходит.
func (e opsEnv) Notify(event string, data ...any) {
	if e.a.k8sMgr != nil {
		e.a.k8sMgr.Notify(event, data...)
	}
}

// opsProvider возвращает Provider платформы для этого вида. Лимитер общий для процесса (SharedLimiter).
func (a *App) opsProvider() opsplatform.Provider {
	return opsplatform.NewProvider(opsEnv{a}, opsplatform.Config{})
}

// beginOp начинает операцию операционного модуля: проверка права и принадлежности кластера команде, tenant.Scope из
// каталога, контекст с deadline. Вызывающий обязан закрыть операцию: defer op.End(err). Метод — общая точка входа
// для делегатов модулей (Dev 2–4); в RPC не публикуется (имя с маленькой буквы).
func (a *App) beginOp(req opsplatform.OpRequest) (context.Context, *opsplatform.Op, error) {
	return a.opsProvider().Begin(a.baseContext(), req)
}
