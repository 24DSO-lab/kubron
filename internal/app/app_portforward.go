// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"net"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/pfproxy"
	"github.com/24DSO-lab/kubron/internal/streamid"
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
	if !pfproxy.OwnedBy(a.replicaID, id) { // поток открыт на другой реплике: клиент повторит запрос
		return "", streamid.ErrElsewhere
	}
	return a.pfMgr.PortForwardURL(id)
}

func (a *App) StartPortForward(namespace, pod string, localPort, podPort int) (*k8s.PortForwardSession, error) {
	if err := a.authz(auth.PermPortForward, "namespace", namespace, "pod", pod, "localPort", localPort, "podPort", podPort); err != nil {
		return nil, err
	}
	return a.pfMgr.StartPortForward(namespace, pod, localPort, podPort)
}

func (a *App) StartServicePortForward(namespace, service string, localPort, servicePort int) (*k8s.PortForwardSession, error) {
	if err := a.authz(auth.PermPortForward, "namespace", namespace, "service", service, "localPort", localPort, "servicePort", servicePort); err != nil {
		return nil, err
	}
	return a.pfMgr.StartServicePortForward(namespace, service, localPort, servicePort)
}

func (a *App) StopPortForward(id string) error {
	if err := a.authz(auth.PermPortForward, "id", id); err != nil {
		return err
	}
	if !pfproxy.OwnedBy(a.replicaID, id) { // поток открыт на другой реплике: клиент повторит запрос
		return streamid.ErrElsewhere
	}
	return a.pfMgr.StopPortForward(id)
}

func (a *App) ListPortForwards() []k8s.PortForwardSession {
	if a.authz(auth.PermRead) != nil {
		return nil
	}
	return a.pfMgr.ListPortForwards()
}

func (a *App) GetFreePort() (int, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return 0, err
	}
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (a *App) OpenBrowserURL(url string) {
	if a.authz(auth.PermRead) != nil {
		return
	}
	a.platform.OpenURL(url)
}
