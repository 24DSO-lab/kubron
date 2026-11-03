// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// ─── Типы ──────────────────────────────────────────────────

// TerminalOutput принимает вывод терминальной сессии. По умолчанию (десктоп) вывод уходит
// событиями terminal:data:<id> / terminal:exit:<id>; веб подставляет буфер с WebSocket (internal/termbuf).
// Data вызывается из горутины чтения вывода, а при закрытии по сроку — ещё и из сторожа (сообщение о причине),
// поэтому реализация должна быть безопасна для параллельных вызовов. p переиспользуется после возврата.
type TerminalOutput interface {
	Data(p []byte)
	Exit()
}

// Причины завершения терминала, которые получает TerminalObserver.
const (
	TerminalEndExit        = "exit"         // процесс в контейнере завершился
	TerminalEndClosed      = "closed"       // закрыт пользователем, выходом из системы или остановкой сервера
	TerminalEndMaxDuration = "max-duration" // исчерпан абсолютный срок
	TerminalEndIdle        = "idle"         // долго не было ввода
)

// TerminalObserver вызывается при старте сессии и возвращает функцию, которую менеджер вызовет ровно один раз
// при завершении с причиной (TerminalEnd*). Пользователя и команду наблюдатель определяет при старте и
// запоминает: завершение наступает, когда вызова пользователя уже нет.
type TerminalObserver func(s *TerminalSession) (ended func(reason string))

// SetTerminalObserver задаёт наблюдателя сессий; вызывать до первого старта.
func (m *Manager) SetTerminalObserver(o TerminalObserver) {
	m.viewMu.Lock()
	m.termObserver = o
	m.viewMu.Unlock()
}

// IsNodeShellPod — под, созданный StartNodeShell: привилегированный под с оболочкой хоста. Подключение к нему
// равносильно доступу к узлу, поэтому требует права nodeshell, а не exec.
func IsNodeShellPod(namespace, pod string) bool {
	return namespace == nodeShellNamespace && strings.HasPrefix(pod, nodeShellPrefix)
}

type TerminalSession struct {
	ID        string
	ClusterID string
	Namespace string
	Pod       string
	Container string
	Shell     string
	Started   time.Time

	out      TerminalOutput
	stdinW   io.WriteCloser
	stdoutR  io.ReadCloser
	sizeChan chan remotecommand.TerminalSize
	done     chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc

	mu     sync.Mutex
	closed bool

	lastInput atomic.Int64 // unix-наносекунды последнего ввода пользователя
	endOnce   sync.Once
	onEnd     func(reason string)
}

// finish сообщает наблюдателю о завершении; повторные вызовы игнорируются, причина — от первого.
func (s *TerminalSession) finish(reason string) {
	s.endOnce.Do(func() {
		if s.onEnd != nil {
			s.onEnd(reason)
		}
	})
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
		ClusterID: conn.Name,
		Started:   time.Now(),
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

	session.lastInput.Store(time.Now().UnixNano())
	m.viewMu.RLock()
	obs := m.termObserver
	m.viewMu.RUnlock()
	if obs != nil {
		session.onEnd = obs(session)
	}

	go m.pumpTerminal(session)
	m.watchTerminal(session)

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

	session.lastInput.Store(time.Now().UnixNano())
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
	session.finish(TerminalEndClosed)
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

// ─── Сроки сессии ──────────────────────────────────────────

// watchTerminal закрывает сессию по истечении абсолютного срока или простоя (Limits). Без сроков не запускается.
func (m *Manager) watchTerminal(s *TerminalSession) {
	lim := m.Limits()
	maxDur, idle := lim.TerminalMaxDuration, lim.TerminalIdleTimeout
	if maxDur <= 0 && idle <= 0 {
		return
	}
	every := 5 * time.Second
	for _, d := range []time.Duration{maxDur, idle} {
		if d > 0 && d/4 < every {
			every = d / 4
		}
	}
	if every < 10*time.Millisecond {
		every = 10 * time.Millisecond
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-s.done:
				return
			case <-t.C:
			}
			reason, note := "", ""
			switch {
			case maxDur > 0 && time.Since(s.Started) >= maxDur:
				reason, note = TerminalEndMaxDuration, "maximum session duration reached"
			case idle > 0 && time.Since(time.Unix(0, s.lastInput.Load())) >= idle:
				reason, note = TerminalEndIdle, "closed after being idle"
			default:
				continue
			}
			// Сначала отправляем причину в терминал и только потом уведомляем observer:
			// observer может разбудить вызывающий код, который сразу прочитает вывод.
			s.out.Data([]byte("\r\n[session closed: " + note + "]\r\n"))
			s.finish(reason)
			_ = m.CloseTerminal(s.ID)
			return
		}
	}()
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

	exec, err := spdyExecutorFor(conn.Config, "POST", req.URL()) // через политику адреса API-сервера
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
