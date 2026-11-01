// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

const (
	wsReadLimit  = 64 << 10 // кадры клиента — только подписки
	wsWriteWait  = 10 * time.Second
	wsPongWait   = 60 * time.Second
	wsPingPeriod = 25 * time.Second
)

// WSHandler обслуживает GET /api/ws: события ядра в браузер.
//
// Защита: действующая веб-сессия (cookie) и обязательный Origin того же хоста — браузер всегда
// шлёт Origin при установке WebSocket, а заголовок CSRF для него недоступен.
// Сессия перепроверяется на каждом ping: если она закрыта, соединение закрывается.
func WSHandler(g *Guard) http.Handler {
	up := newUpgrader()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.refuseIfDraining(w) { // реплика останавливается: клиент подключится к другой
			return
		}
		token := g.token(r)
		sess, ok := g.Sessions.Get(token)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return // Upgrade уже ответил
		}
		c := g.Hub.Register(sess.ID)
		defer c.Close()
		serveConn(ws, c, func() bool { _, ok := g.Sessions.Get(token); return ok })
	})
}

// newUpgrader — общие настройки WebSocket: Origin обязателен и должен совпадать с Host.
func newUpgrader() websocket.Upgrader {
	return websocket.Upgrader{
		ReadBufferSize: 4 << 10, WriteBufferSize: 16 << 10,
		CheckOrigin: func(r *http.Request) bool {
			o := r.Header.Get("Origin")
			if o == "" {
				return false
			}
			u, err := url.Parse(o)
			return err == nil && u.Host == r.Host
		},
	}
}

func serveConn(ws *websocket.Conn, c *Conn, alive func() bool) {
	defer ws.Close()
	ws.SetReadLimit(wsReadLimit)
	_ = ws.SetReadDeadline(time.Now().Add(wsPongWait))
	ws.SetPongHandler(func(string) error { return ws.SetReadDeadline(time.Now().Add(wsPongWait)) })

	go func() { // чтение: команды клиента
		defer c.Close()
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			_ = ws.SetReadDeadline(time.Now().Add(wsPongWait)) // любой кадр — признак жизни
			if reply := c.Handle(msg); reply != nil && !c.Send(reply) {
				return
			}
		}
	}()

	_ = c.Send(mustJSON(map[string]any{"op": "ready"}))
	tick := time.NewTicker(wsPingPeriod)
	defer tick.Stop()
	for { // запись: единственная горутина пишет в сокет
		select {
		case frame := <-c.Out():
			_ = ws.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := ws.WriteMessage(websocket.TextMessage, frame); err != nil {
				log.Printf("ws: failed to write: %v", err)
				return
			}
		case <-tick.C:
			if !alive() {
				return
			}
			_ = ws.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.Done():
			code, reason := c.closeInfo()
			_ = ws.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
			return
		}
	}
}
