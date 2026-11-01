// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"fmt"
	"time"

	"github.com/24DSO-labs/kubron/internal/audit"
	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/k8s"
)

// Действия аудита интерактивных сессий (подписи — audit.actions.* во фронтенде).
const (
	auditTerminalStart  = "terminal.start"
	auditTerminalStop   = "terminal.stop"
	auditNodeShellStart = "nodeshell.start"
	auditNodeShellStop  = "nodeshell.stop"
)

// InteractivePolicy — что администратор сервера отключил (KUBRON_DISABLE_*). Отключённое недоступно всем,
// независимо от ролей; GetCapabilities сообщает об этом интерфейсу.
type InteractivePolicy struct {
	DisableExec      bool // терминалы подов и оболочка на узле (оба используют exec)
	DisableNodeShell bool // только оболочка на узле
}

// SetInteractivePolicy задаёт политику для видов, создаваемых после вызова. Функция пакета, а не метод App:
// в привязки Wails и RPC она не попадает.
func SetInteractivePolicy(root *App, p InteractivePolicy) { root.interactive = p }

var (
	errExecDisabled      = errors.New("terminals are disabled by the administrator")
	errNodeShellDisabled = errors.New("node shell is disabled by the administrator")
)

func (a *App) checkExecEnabled() error {
	if a.interactive.DisableExec {
		return errExecDisabled
	}
	return nil
}

// checkTerminalTarget — проверки сверх authz(exec): отключение терминалов и право nodeshell для пода оболочки узла
// (подключение к нему равносильно доступу к хосту).
func (a *App) checkTerminalTarget(namespace, pod string) error {
	if err := a.checkExecEnabled(); err != nil {
		return err
	}
	if k8s.IsNodeShellPod(namespace, pod) {
		if a.interactive.DisableNodeShell {
			return errNodeShellDisabled
		}
		return a.authz(auth.PermNodeShell, "namespace", namespace, "pod", pod)
	}
	return nil
}

// configureTerminals подключает наблюдателя аудита к менеджеру вида.
func (a *App) configureTerminals() {
	a.k8sMgr.SetTerminalObserver(a.terminalObserver())
}

// terminalObserver записывает начало и конец сессии. Пользователь определяется при старте и запоминается: конец
// (выход, простой, срок, закрытие вкладки) наступает, когда вызова пользователя уже нет.
func (a *App) terminalObserver() k8s.TerminalObserver {
	return func(s *k8s.TerminalSession) func(reason string) {
		if a.audit == nil || a.auth == nil {
			return nil
		}
		actor := a.auth.Actor()
		actor.ClusterID = s.ClusterID
		if full, err := a.auth.CheckCluster(auth.PermExec, s.ClusterID); err == nil {
			actor = full
			actor.ClusterID = s.ClusterID
		}
		start, stop := auditTerminalStart, auditTerminalStop
		if k8s.IsNodeShellPod(s.Namespace, s.Pod) {
			start, stop = auditNodeShellStart, auditNodeShellStop
		}
		detail := fmt.Sprintf("id=%s namespace=%s pod=%s container=%s shell=%s", s.ID[:min(8, len(s.ID))], s.Namespace, s.Pod, s.Container, s.Shell)
		a.recordInteractive(actor, start, detail)
		return func(reason string) {
			a.recordInteractive(actor, stop,
				fmt.Sprintf("%s reason=%s duration=%s", detail, reason, time.Since(s.Started).Round(time.Second)))
		}
	}
}

func (a *App) recordInteractive(actor auth.Actor, action, detail string) {
	a.audit.Record(audit.Event{
		User: actor.Username, Role: string(actor.Role), Action: action, Result: audit.ResultOK, Detail: detail,
		Source: actor.Source, TeamID: actor.TeamID, ClusterID: actor.ClusterID,
	})
}
