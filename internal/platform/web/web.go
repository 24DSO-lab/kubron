// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package webplatform — реализация ext.Platform для HTTP-оболочки (kubron-server).
package webplatform

import (
	"errors"
	"sync"

	"github.com/24DSO-labs/kubron/internal/termbuf"
	"github.com/24DSO-labs/kubron/pkg/ext"
)

// ErrNotSupported — операция есть только в десктопе.
var ErrNotSupported = errors.New("unavailable in web mode")

// Platform: события уходят подписчикам через эмиттер (WebSocket-хаб, задача 1.5);
// пока эмиттер не задан, события отбрасываются.
type Platform struct {
	mu     sync.RWMutex
	emit   func(event string, data ...any)
	emitTo func(session, event string, data ...any)

	termOnce  sync.Once
	terminals *termbuf.Registry
}

// Terminals — реестр веб-терминалов (создаётся при первом обращении). Его же использует
// WebSocket-обработчик /api/terminal/{id}.
func (p *Platform) Terminals() *termbuf.Registry {
	p.termOnce.Do(func() { p.terminals = termbuf.NewRegistry() })
	return p.terminals
}

// SetEmitter подключает доставку событий браузеру.
func (p *Platform) SetEmitter(f func(event string, data ...any)) {
	p.mu.Lock()
	p.emit = f
	p.mu.Unlock()
}

// SetSessionEmitter подключает адресную доставку событий одной веб-сессии.
func (p *Platform) SetSessionEmitter(f func(session, event string, data ...any)) {
	p.mu.Lock()
	p.emitTo = f
	p.mu.Unlock()
}

// EmitTo доставляет событие только указанной сессии (без адресной доставки — всем).
func (p *Platform) EmitTo(session, event string, data ...any) {
	p.mu.RLock()
	f := p.emitTo
	p.mu.RUnlock()
	if f != nil {
		f(session, event, data...)
		return
	}
	p.Emit(event, data...)
}

func (p *Platform) Emit(event string, data ...any) {
	p.mu.RLock()
	f := p.emit
	p.mu.RUnlock()
	if f != nil {
		f(event, data...)
	}
}

// PickFile: нативного диалога на сервере нет; браузер загружает файл сам (<input type=file>).
func (p *Platform) PickFile(string) (string, error) { return "", ErrNotSupported }

// OpenURL: ссылки в вебе открывает сам браузер.
func (p *Platform) OpenURL(string) {}

// Capabilities веб-оболочки. FileDialog и LocalKubeconfig выключены: kubeconfig передаётся
// текстом, а не путём на файловой системе сервера.
func (p *Platform) Capabilities() ext.Capabilities {
	return ext.Capabilities{
		FileDialog:      false,
		PortForward:     true,
		NodeShell:       true,
		LocalKubeconfig: false,
		SSO:             false,
	}
}
