// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strings"
)

// NewHandler собирает маршруты: RPC, служебные ручки, статика фронтенда с SPA-fallback.
// g — защита веб-API (сессии, CSRF); nil отключает её. static — содержимое собранного фронтенда (корень с index.html); может быть nil.
func NewHandler(d *Dispatcher, static fs.FS, g *Guard, opts ...HandlerOption) http.Handler {
	var o handlerOptions
	for _, opt := range opts {
		opt(&o)
	}
	mux := http.NewServeMux()
	mux.Handle("POST /api/rpc/{method}", RPCHandler(d, g))
	mux.Handle("GET /api/session", SessionHandler(g))
	if g != nil && g.Hub != nil {
		mux.Handle("GET /api/ws", WSHandler(g))
	}
	if g != nil && g.Terminals != nil {
		mux.Handle("GET /api/terminal/{id}", TerminalHandler(g))
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such API endpoint")
	})
	mux.Handle("/", spaHandler(static))
	return securityHeaders(mux, g != nil && g.TrustProxy, o)
}

// spaHandler отдаёт файлы фронтенда; для любого пути без файла (клиентский роутинг) — index.html.
func spaHandler(static fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if static == nil {
			http.Error(w, "frontend is not embedded in the build", http.StatusServiceUnavailable)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if st, err := fs.Stat(static, name); err != nil || st.IsDir() {
			name = "index.html" // клиентский маршрут
		}
		f, err := static.Open(name)
		if err != nil {
			http.Error(w, "frontend is not built (no index.html): build the frontend first", http.StatusServiceUnavailable)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable") // имена с хешем
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		rs, ok := f.(interface {
			Read([]byte) (int, error)
			Seek(int64, int) (int64, error)
		})
		if !ok {
			http.Error(w, "file does not support seeking", http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, r, name, st.ModTime(), rs)
	})
}

// Run слушает cfg.Listen и обслуживает h до отмены ctx, затем корректно завершает соединения.
func Run(ctx context.Context, cfg Config, h http.Handler) error {
	return RunWith(ctx, cfg, h, ShutdownPlan{})
}

// RunWith — Run с заданным порядком остановки (draining, закрытие WebSocket кадром 1012).
func RunWith(ctx context.Context, cfg Config, h http.Handler, plan ShutdownPlan) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	return ServeWith(ctx, ln, h, plan)
}

// Serve обслуживает listener до отмены ctx (graceful shutdown до 10 секунд).
func Serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	return ServeWith(ctx, ln, h, ShutdownPlan{})
}

// webBuildMarker — метка, которую `vite build --mode web` добавляет в index.html.
const webBuildMarker = `name="kubron-target" content="web"`

// CheckWebBuild убеждается, что встроенный фронтенд собран для веба. Десктопная сборка
// (`wails build`, `npm run build`) обращается к рантайму Wails и в браузере не запускается:
// без этой проверки результат — пустая страница и ошибка в консоли. Нет index.html — не ошибка
// (фронтенд не встроен, spaHandler ответит 503 с подсказкой).
func CheckWebBuild(static fs.FS) error {
	b, err := fs.ReadFile(static, "index.html")
	if err != nil {
		return nil
	}
	if !strings.Contains(string(b), webBuildMarker) {
		return errors.New("the embedded frontend/dist was built for desktop, not for web: " +
			"run `cd frontend && npm run build:web` and rebuild the server (go build ./cmd/kubron-server); " +
			"`wails build` overwrites frontend/dist with the desktop build")
	}
	return nil
}
