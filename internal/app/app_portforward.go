// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"time"

	"github.com/24DSO-labs/kubron/internal/audit"
	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/k8s"
	"github.com/24DSO-labs/kubron/internal/pfproxy"
)

// Действия аудита port-forward (подписи — audit.actions.* во фронтенде).
const (
	auditPortForwardStart = "portforward.start"
	auditPortForwardStop  = "portforward.stop"
)

// SetPortForwardProxy включает режим proxy для видов, создаваемых после вызова (ADR-0017). Функция пакета,
// а не метод App: в привязки Wails и RPC она не попадает.
func SetPortForwardProxy(root *App, svc *pfproxy.Service) { root.pfService = svc }

// SetPortForwardMode задаёт режим для видов, создаваемых после вызова: local, proxy (через SetPortForwardProxy)
// или off. Без вызова действует local.
func SetPortForwardMode(root *App, mode string) { root.pfMode = mode }

// configurePortForward применяет режим и наблюдателя аудита к менеджеру вида.
func (a *App) configurePortForward() {
	switch {
	case a.pfMode == k8s.PortForwardModeOff:
		a.pfMgr.SetMode(k8s.PortForwardModeOff)
	case a.pfService != nil:
		a.pfMgr.SetProxy(a.pfService, a.sessionID)
	}
	a.pfMgr.SetObserver(a.portForwardObserver())
}

// portForwardMode — режим, о котором GetCapabilities сообщает фронтенду.
func (a *App) portForwardMode() string {
	switch {
	case a.pfMode == k8s.PortForwardModeOff:
		return k8s.PortForwardModeOff
	case a.pfService != nil:
		return k8s.PortForwardModeProxy
	}
	return k8s.PortForwardModeLocal
}

// portForwardObserver записывает начало и конец проброса. Пользователь и команда определяются при старте и
// запоминаются: конец (простой, срок, закрытие сессии) наступает, когда вызова пользователя уже нет.
func (a *App) portForwardObserver() k8s.PortForwardObserver {
	return func(s *k8s.PortForwardSession) func(reason string) {
		if a.audit == nil || a.auth == nil {
			return nil
		}
		actor := a.auth.Actor()
		if full, err := a.auth.CheckCluster(auth.PermPortForward, s.ClusterID); err == nil {
			actor = full
		}
		actor.ClusterID = s.ClusterID
		started := time.Now()
		detail := fmt.Sprintf("id=%s mode=%s namespace=%s target=%s port=%d", s.ID, s.Mode, s.Namespace, s.Target, s.PodPort)
		a.recordPortForward(actor, auditPortForwardStart, detail)
		return func(reason string) {
			a.recordPortForward(actor, auditPortForwardStop,
				fmt.Sprintf("%s reason=%s duration=%s", detail, reason, time.Since(started).Round(time.Second)))
		}
	}
}

func (a *App) recordPortForward(actor auth.Actor, action, detail string) {
	a.audit.Record(audit.Event{
		User: actor.Username, Role: string(actor.Role), Action: action, Result: audit.ResultOK, Detail: detail,
		Source: actor.Source, TeamID: actor.TeamID, ClusterID: actor.ClusterID,
	})
}

// GetPortForwardURL возвращает адрес, по которому браузер открывает проброс. В режиме proxy адрес одноразовый и
// живёт секунды: его нужно запрашивать при каждом открытии, а не хранить.
func (a *App) GetPortForwardURL(id string) (string, error) {
	if err := a.authz(auth.PermPortForward, "id", id); err != nil {
		return "", err
	}
	return a.pfMgr.PortForwardURL(id)
}
