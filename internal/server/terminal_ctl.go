// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Протокол /api/terminal/{id}:
//
//	сервер → клиент: бинарные кадры — вывод терминала; закрытие кадром Close 1000 «exit» — процесс завершился;
//	клиент → сервер: бинарные кадры — ввод; текстовый кадр — управляющий JSON:
//	    {"type":"resize","cols":120,"rows":40}
const (
	ctlResize      = "resize"
	maxTermDim     = 1000
	termMaxFrame   = 64 << 10
	termCloseExit  = "exit"
	termCloseMaxed = "terminal is already attached or not found"
)

type termControl struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// parseTermControl разбирает управляющий кадр и проверяет размеры.
func parseTermControl(raw []byte) (cols, rows uint16, err error) {
	var c termControl
	if err := json.Unmarshal(raw, &c); err != nil {
		return 0, 0, errors.New("control frame must be JSON")
	}
	if c.Type != ctlResize {
		return 0, 0, errors.New("unknown control frame: " + c.Type)
	}
	if c.Cols < 1 || c.Cols > maxTermDim || c.Rows < 1 || c.Rows > maxTermDim {
		return 0, 0, errors.New("invalid terminal size")
	}
	return uint16(c.Cols), uint16(c.Rows), nil
}

// closer — соединение, которое можно закрыть снаружи (при завершении сессии).
type closer interface{ Close() error }

// termConns — открытые терминальные сокеты по идентификатору веб-сессии.
type termConns struct {
	mu sync.Mutex
	m  map[string]map[closer]struct{}
}

func (t *termConns) add(session string, c closer) {
	t.mu.Lock()
	if t.m == nil {
		t.m = map[string]map[closer]struct{}{}
	}
	if t.m[session] == nil {
		t.m[session] = map[closer]struct{}{}
	}
	t.m[session][c] = struct{}{}
	t.mu.Unlock()
}

func (t *termConns) remove(session string, c closer) {
	t.mu.Lock()
	delete(t.m[session], c)
	if len(t.m[session]) == 0 {
		delete(t.m, session)
	}
	t.mu.Unlock()
}

func (t *termConns) closeSession(session string) {
	t.mu.Lock()
	victims := make([]closer, 0, len(t.m[session]))
	for c := range t.m[session] {
		victims = append(victims, c)
	}
	delete(t.m, session)
	t.mu.Unlock()
	for _, c := range victims {
		_ = c.Close()
	}
}

// shutdown закрывает все терминальные сокеты кадром Close с кодом code (остановка реплики). Процессы
// терминалов не трогаются: клиент откроет терминал заново на живой реплике.
func (t *termConns) shutdown(code int, reason string) {
	t.mu.Lock()
	var victims []closer
	for _, set := range t.m {
		for c := range set {
			victims = append(victims, c)
		}
	}
	t.mu.Unlock()
	msg := websocket.FormatCloseMessage(code, reason)
	for _, c := range victims {
		if w, ok := c.(interface {
			WriteControl(int, []byte, time.Time) error
		}); ok {
			_ = w.WriteControl(websocket.CloseMessage, msg, time.Now().Add(time.Second))
		}
		_ = c.Close()
	}
}
