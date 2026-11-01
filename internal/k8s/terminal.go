// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// ─── Типы ──────────────────────────────────────────────────

// TerminalOutput принимает вывод терминальной сессии. По умолчанию (десктоп) вывод уходит
// событиями terminal:data:<id> / terminal:exit:<id>; веб подставляет буфер с WebSocket (internal/termbuf).
// Data вызывается из одной горутины, p переиспользуется после возврата — реализация копирует, если хранит.
type TerminalOutput interface {
	Data(p []byte)
	Exit()
}

type TerminalSession struct {
	ID        string
	Namespace string
	Pod       string
	Container string
	Shell     string

	out      TerminalOutput
	stdinW   io.WriteCloser
	stdoutR  io.ReadCloser
	sizeChan chan remotecommand.TerminalSize
	done     chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc

	mu     sync.Mutex
	closed bool
}

type TerminalSessionManager struct {
	sessions map[string]*TerminalSession
	mu       sync.RWMutex
}

func NewTerminalSessionManager() *TerminalSessionManager {
	return &TerminalSessionManager{
		sessions: make(map[string]*TerminalSession),
	}
}

func (tsm *TerminalSessionManager) count() int {
	if tsm == nil {
		return 0
	}
	tsm.mu.RLock()
	defer tsm.mu.RUnlock()
	return len(tsm.sessions)
}

// tryAdd регистрирует сессию, если не превышен лимит (0 — без ограничения).
func (tsm *TerminalSessionManager) tryAdd(session *TerminalSession, max int) bool {
	tsm.mu.Lock()
	defer tsm.mu.Unlock()
	if max > 0 && len(tsm.sessions) >= max {
		return false
	}
	tsm.sessions[session.ID] = session
	return true
}

func (tsm *TerminalSessionManager) Add(session *TerminalSession) {
	tsm.mu.Lock()
	defer tsm.mu.Unlock()
	tsm.sessions[session.ID] = session
}

func (tsm *TerminalSessionManager) Get(sessionID string) *TerminalSession {
	tsm.mu.RLock()
	defer tsm.mu.RUnlock()
	return tsm.sessions[sessionID]
}

func (tsm *TerminalSessionManager) Remove(sessionID string) {
	tsm.mu.Lock()
	defer tsm.mu.Unlock()
	delete(tsm.sessions, sessionID)
}

// ─── sizeQueue для TerminalSizeQueue ───────────────────────

type sizeQueue struct {
	sizeChan chan remotecommand.TerminalSize
	done     chan struct{}
}

func (sq *sizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case size := <-sq.sizeChan:
		return &size
	case <-sq.done:
		return nil
	}
}

// ─── StartTerminalSession ──────────────────────────────────

// StartTerminalSession запускает сессию с выводом событиями (десктоп): id выбирает вызывающий.
func (m *Manager) StartTerminalSession(sessionID, namespace, pod, container, shell string, cols, rows uint16) error {
	return m.startTerminal(sessionID, namespace, pod, container, shell, cols, rows, nil)
}

// StartTerminalWithOutput запускает сессию с произвольным приёмником вывода (веб).
func (m *Manager) StartTerminalWithOutput(sessionID, namespace, pod, container, shell string, cols, rows uint16, out TerminalOutput) error {
	if out == nil {
		return errors.New("terminal output sink is not set")
	}
	return m.startTerminal(sessionID, namespace, pod, container, shell, cols, rows, out)
}

func (m *Manager) startTerminal(sessionID, namespace, pod, container, shell string, cols, rows uint16, out TerminalOutput) error {
	if out == nil {
		out = eventTerminalOutput{m: m, id: sessionID}
	}
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}
	if max := m.Limits().Terminals; max > 0 && m.terminalSessions.count() >= max {
		return fmt.Errorf("too many open terminals (maximum %d)", max)
	}

	// Пустой shell — режим attach к основному процессу контейнера.
	attach := shell == ""
	req := conn.Client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod).
		Namespace(namespace)

	tty, stdin := true, true // для exec
	if attach {
		tty, stdin = false, false
		ctxP, cancelP := context.WithTimeout(context.Background(), 10*time.Second)
		p, perr := conn.Client.CoreV1().Pods(namespace).Get(ctxP, pod, metav1.GetOptions{})
		cancelP()
		if perr != nil {
			return perr
		}
		for _, c := range p.Spec.Containers {
			if c.Name == container {
				tty, stdin = c.TTY, c.Stdin
			}
		}
		req = req.SubResource("attach").
			VersionedParams(&corev1.PodAttachOptions{
				Container: container,
				Stdin:     stdin,
				Stdout:    true,
				Stderr:    !tty, // при TTY stderr объединён с stdout
				TTY:       tty,
			}, scheme.ParameterCodec)
	} else {
		req = req.SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
				Container: container,
				Command:   []string{shell},
				Stdin:     true,
				Stdout:    true,
				Stderr:    true,
				TTY:       true,
			}, scheme.ParameterCodec)
	}

	executor, err := spdyExecutorFor(conn.Config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("create executor: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	sizeChan := make(chan remotecommand.TerminalSize, 1)
	done := make(chan struct{})

	sizeChan <- remotecommand.TerminalSize{
		Width:  cols,
		Height: rows,
	}

	sq := &sizeQueue{
		sizeChan: sizeChan,
		done:     done,
	}

	shellLabel := shell
	if attach {
		shellLabel = "attach"
	}
	session := &TerminalSession{
		ID:        sessionID,
		Namespace: namespace,
		Pod:       pod,
		Container: container,
		Shell:     shellLabel,
		out:       out,
		stdinW:    stdinW,
		stdoutR:   stdoutR,
		sizeChan:  sizeChan,
		done:      done,
		ctx:       ctx,
		cancel:    cancel,
		closed:    false,
	}

	if m.terminalSessions == nil {
		m.terminalSessions = NewTerminalSessionManager()
	}
	if !m.terminalSessions.tryAdd(session, m.Limits().Terminals) {
		cancel()
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return fmt.Errorf("too many open terminals (maximum %d)", m.Limits().Terminals)
	}

	go m.pumpTerminal(session)

	go func() {
		opts := remotecommand.StreamOptions{
			Stdout:            stdoutW,
			Tty:               tty,
			TerminalSizeQueue: sq,
		}
		if stdin {
			opts.Stdin = stdinR
		}
		if !tty {
			opts.Stderr = stdoutW
		}

		if err := executor.StreamWithContext(ctx, opts); err != nil && ctx.Err() == nil {
			_, _ = fmt.Fprintf(stdoutW, "\r\n[stream error: %v]\r\n", err)
		}

		stdoutW.Close()
		stdinR.Close()
		deleteNodeShellPod(conn, namespace, pod)
		if m.terminalSessions != nil {
			m.terminalSessions.Remove(sessionID)
		}

		select {
		case <-done:
		default:
			close(done)
		}
	}()

	return nil
}

// ─── WriteTerminal ─────────────────────────────────────────

func (m *Manager) WriteTerminal(sessionID string, data []byte) error {
	if m.terminalSessions == nil {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	session := m.terminalSessions.Get(sessionID)
	if session == nil {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	if session.closed {
		return fmt.Errorf("session closed: %s", sessionID)
	}

	_, err := session.stdinW.Write(data)
	return err
}

// ─── ReadTerminalBytes ─────────────────────────────────────

// ─── ResizeTerminal ────────────────────────────────────────

func (m *Manager) ResizeTerminal(sessionID string, cols, rows uint16) error {
	if m.terminalSessions == nil {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	session := m.terminalSessions.Get(sessionID)
	if session == nil {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	if session.closed {
		return nil
	}

	select {
	case <-session.sizeChan:
	default:
	}

	select {
	case session.sizeChan <- remotecommand.TerminalSize{Width: cols, Height: rows}:
	default:
	}

	return nil
}

// ─── CloseTerminal ─────────────────────────────────────────

func (m *Manager) CloseTerminal(sessionID string) error {
	if m.terminalSessions == nil {
		return nil
	}

	session := m.terminalSessions.Get(sessionID)
	if session == nil {
		return nil
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	if session.closed {
		return nil
	}

	session.closed = true
	session.cancel()
	session.stdinW.Close()
	session.stdoutR.Close()

	select {
	case <-session.done:
	default:
		close(session.done)
	}

	m.terminalSessions.Remove(sessionID)

	return nil
}

// ─── DetectShell ───────────────────────────────────────────

func (m *Manager) DetectShell(namespace, pod, container string) (string, error) {
	shells := []string{"/bin/bash", "/bin/sh", "/bin/ash"}

	for _, shell := range shells {
		_, err := m.execRaw(namespace, pod, container, []string{shell, "-c", "echo ok"})
		if err == nil {
			return shell, nil
		}
	}

	return "/bin/sh", nil
}

// ─── execRaw ───────────────────────────────────────────────

func (m *Manager) execRaw(namespace, pod, container string, command []string) (string, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return "", err
	}

	req := conn.Client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(conn.Config, "POST", req.URL())
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%s", stderr.String())
		}
		return "", err
	}

	return stdout.String(), nil
}

// CloseAllTerminals закрывает все открытые терминальные сессии (например, при выходе пользователя).
func (m *Manager) CloseAllTerminals() {
	if m.terminalSessions == nil {
		return
	}
	m.terminalSessions.mu.RLock()
	ids := make([]string, 0, len(m.terminalSessions.sessions))
	for id := range m.terminalSessions.sessions {
		ids = append(ids, id)
	}
	m.terminalSessions.mu.RUnlock()

	for _, id := range ids {
		_ = m.CloseTerminal(id)
	}
}
