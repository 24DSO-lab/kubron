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
	"github.com/24DSO-labs/kubron/internal/streamid"
	"github.com/24DSO-labs/kubron/internal/termbuf"
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

func (a *App) DetectShell(namespace, pod, container string) (string, error) {
	if err := a.authz(auth.PermExec, "namespace", namespace, "pod", pod, "container", container); err != nil {
		return "", err
	}
	if err := a.checkTerminalTarget(namespace, pod); err != nil {
		return "", err
	}
	return a.k8sMgr.DetectShell(namespace, pod, container)
}

func (a *App) StartTerminalSession(sessionID, namespace, pod, container, shell string, cols, rows uint16) error {
	if err := a.authz(auth.PermExec, "namespace", namespace, "pod", pod, "container", container, "shell", shell); err != nil {
		return err
	}
	if err := a.checkTerminalTarget(namespace, pod); err != nil {
		return err
	}
	return a.k8sMgr.StartTerminalSession(sessionID, namespace, pod, container, shell, cols, rows)
}

// terminalHost — оболочка, которая держит веб-терминалы (HTTP-оболочка). У десктопа её нет.
type terminalHost interface{ Terminals() *termbuf.Registry }

// OpenTerminal открывает терминал для веб-оболочки: идентификатор выдаёт сервер (неугадываемый),
// вывод копится, пока браузер не подключится к WebSocket /api/terminal/{id}; ввод и размер идут
// по этому же сокету. Десктоп использует StartTerminalSession.
func (a *App) OpenTerminal(namespace, pod, container, shell string, cols, rows uint16) (string, error) {
	if err := a.authz(auth.PermExec, "namespace", namespace, "pod", pod, "container", container, "shell", shell); err != nil {
		return "", err
	}
	if err := a.checkTerminalTarget(namespace, pod); err != nil {
		return "", err
	}
	host, ok := a.platform.(terminalHost)
	if !ok {
		return "", errors.New("web terminal is unavailable in this shell")
	}
	reg := host.Terminals()
	id := streamid.New(a.replicaID)
	buf, err := reg.Register(id, termbuf.Hooks{
		Input:  func(p []byte) error { return a.k8sMgr.WriteTerminal(id, p) },
		Resize: func(c, r uint16) error { return a.k8sMgr.ResizeTerminal(id, c, r) },
		Close:  func() { _ = a.k8sMgr.CloseTerminal(id) },
		Owner:  a.sessionID,
	})
	if err != nil {
		return "", err
	}
	if err := a.k8sMgr.StartTerminalWithOutput(id, namespace, pod, container, shell, cols, rows, buf); err != nil {
		reg.Remove(id)
		return "", err
	}
	return id, nil
}

func (a *App) WriteTerminal(sessionID string, data []byte) error {
	if err := a.authz(auth.PermExec); err != nil {
		return err
	}
	return a.k8sMgr.WriteTerminal(sessionID, data)
}

func (a *App) ResizeTerminal(sessionID string, cols, rows uint16) error {
	if err := a.authz(auth.PermExec); err != nil {
		return err
	}
	return a.k8sMgr.ResizeTerminal(sessionID, cols, rows)
}

func (a *App) CloseTerminal(sessionID string) error {
	if err := a.authz(auth.PermExec); err != nil {
		return err
	}
	return a.k8sMgr.CloseTerminal(sessionID)
}
