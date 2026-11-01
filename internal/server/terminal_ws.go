// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/24DSO-labs/kubron/internal/streamid"
	"github.com/gorilla/websocket"
)

// wsSink отдаёт вывод терминала в WebSocket бинарными кадрами. Запись из нескольких горутин
// (вывод, ping, закрытие) сериализуется мьютексом.
type wsSink struct {
	mu sync.Mutex
	ws *websocket.Conn
}

func (s *wsSink) Send(p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.ws.SetWriteDeadline(time.Now().Add(wsWriteWait))
	return s.ws.WriteMessage(websocket.BinaryMessage, p) // WriteMessage копирует данные
}

// Exit сообщает клиенту о завершении процесса и закрывает сокет.
func (s *wsSink) Exit() {
	s.mu.Lock()
	_ = s.ws.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, termCloseExit), time.Now().Add(time.Second))
	s.mu.Unlock()
	time.AfterFunc(time.Second, func() { _ = s.ws.Close() })
}

func (s *wsSink) ping() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.ws.SetWriteDeadline(time.Now().Add(wsWriteWait))
	return s.ws.WriteMessage(websocket.PingMessage, nil)
}

// TerminalHandler обслуживает GET /api/terminal/{id}: двунаправленный поток терминала,
// открытого методом OpenTerminal. Защита как у /api/ws: веб-сессия (cookie) и Origin того же хоста.
// Идентификатор неугадываемый (128 бит) и выдаётся сервером; к одному терминалу может быть
// подключено одно соединение.
func TerminalHandler(g *Guard) http.Handler {
	up := newUpgrader()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.refuseIfDraining(w) {
			return
		}
		token := g.token(r)
		sess, ok := g.Sessions.Get(token)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		id := r.PathValue("id")
		if err := streamid.Check(g.Lifecycle.ReplicaID(), id); err != nil { // терминал открыт на другой реплике
			writeError(w, http.StatusConflict, "stream_elsewhere", "stream is on another replica")
			return
		}
		if !g.Terminals.OwnedBy(id, sess.ID) { // чужой терминал неотличим от несуществующего
			writeError(w, http.StatusNotFound, "not_found", "terminal not found")
			return
		}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()

		sink := &wsSink{ws: ws}
		att, err := g.Terminals.AttachAs(id, sess.ID, sink)
		if err != nil {
			_ = ws.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, termCloseMaxed), time.Now().Add(time.Second))
			return
		}
		defer att.Detach()
		g.termConns.add(sess.ID, ws)
		defer g.termConns.remove(sess.ID, ws)
		if g.Lifecycle.Closing() { // подключились между закрытием соединений и остановкой сервера
			_ = ws.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(CloseServiceRestart, closeReasonRestart), time.Now().Add(time.Second))
			return
		}

		ws.SetReadLimit(termMaxFrame)
		_ = ws.SetReadDeadline(time.Now().Add(wsPongWait))
		ws.SetPongHandler(func(string) error { return ws.SetReadDeadline(time.Now().Add(wsPongWait)) })

		stop := make(chan struct{})
		defer close(stop)
		go func() { // keepalive и проверка, что веб-сессия жива
			t := time.NewTicker(wsPingPeriod)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					if _, ok := g.Sessions.Get(token); !ok || sink.ping() != nil {
						_ = ws.Close()
						return
					}
				}
			}
		}()

		for {
			typ, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			_ = ws.SetReadDeadline(time.Now().Add(wsPongWait))
			// Каждый кадр: веб-сессия жива и терминал всё ещё принадлежит ей (Input/Resize проверяют владельца сами).
			if _, ok := g.Sessions.Get(token); !ok {
				return
			}
			switch typ {
			case websocket.BinaryMessage:
				if err := att.Input(msg); err != nil {
					return // сессия завершена или закрыта
				}
			case websocket.TextMessage:
				cols, rows, err := parseTermControl(msg)
				if err != nil {
					slog.Warn("terminal: bad control message", "terminal", id[:8], "error", err)
					continue
				}
				if att.Resize(cols, rows) != nil {
					return
				}
			}
		}
	})
}
