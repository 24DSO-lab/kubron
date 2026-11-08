// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/streamid"
)

// maxRPCBody — предел тела запроса (манифесты YAML крупные, но не мегабайты).
const maxRPCBody = 4 << 20

type rpcError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// RequestID — идентификатор запроса при внутренней ошибке (тот же, что в журнале сервера и в X-Request-Id).
	RequestID string `json:"requestId,omitempty"`
}

type rpcResponse struct {
	Result any       `json:"result"`
	Error  *rpcError `json:"error,omitempty"`
}

// retryAfterError — ошибка метода «слишком много попыток» (например, ответов на вызов входа в кластер). Сервер не
// импортирует пакет, где она объявлена: достаточно метода.
type retryAfterError interface{ RetryAfterSeconds() int }

// MapError переводит ошибку метода в HTTP-статус и машинный код (единый формат ошибок, задача 1.3).
// Текст сообщения остаётся прежним: фронтенд показывает его так же, как в десктопе, а по тексту
// «требуется вход в систему» распознаёт потерю сессии.
func MapError(err error) (status int, code string) {
	var bad *BadArgsError
	var limited retryAfterError
	switch {
	case errors.Is(err, auth.ErrNotAuthenticated):
		return http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, auth.ErrForbidden):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, auth.ErrClusterNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, streamid.ErrElsewhere):
		return http.StatusConflict, "stream_elsewhere"
	case errors.Is(err, ErrUnknownMethod):
		return http.StatusNotFound, "unknown_method"
	case errors.Is(err, ErrMethodDenied):
		return http.StatusForbidden, "method_denied"
	case errors.As(err, &bad):
		return http.StatusBadRequest, "bad_request"
	case errors.As(err, &limited):
		return http.StatusTooManyRequests, "rate_limited"
	default:
		return http.StatusInternalServerError, "error"
	}
}

// publicMethods — методы, доступные без веб-сессии: первичная настройка, вход, стартовое
// состояние и возможности оболочки для экрана входа. Остальные требуют cookie и CSRF-токен.
var publicMethods = map[string]bool{
	"AuthStatus": true, "SetupAdmin": true, "Login": true, "GetCapabilities": true,
}

// RPCHandler обслуживает POST /api/rpc/{method}: тело — JSON-массив аргументов,
// ответ — {"result": …} или {"error": {"code", "message"}}.
//
// Защита: (1) источник запроса — тот же Origin (и Sec-Fetch-Site, если браузер его шлёт);
// (2) Content-Type: application/json (чужая страница не может отправить такой запрос без
// CORS-preflight, на который сервер не отвечает); (3) при заданном guard — веб-сессия в cookie
// и CSRF-токен в заголовке X-CSRF-Token для всех методов, кроме publicMethods.
// guard == nil отключает сессии (тесты).
func RPCHandler(d *Dispatcher, g *Guard) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "forbidden_origin", "cross-origin request rejected")
			return
		}
		if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "bad_request", "expected Content-Type: application/json")
			return
		}
		method := r.PathValue("method")
		// Первичная настройка — единственный анонимный RPC, который создаёт привилегированного пользователя.
		// В сервисном режиме токен проверяется до диспетчеризации и до вызова App.SetupAdmin.
		if method == "SetupAdmin" && g != nil && !g.checkSetup(w, r) {
			return
		}

		var token string
		var sess Session
		var authed bool
		if g != nil {
			token = g.token(r)
			var serr error
			sess, serr = g.Sessions.Lookup(token)
			authed = serr == nil
			if errors.Is(serr, ErrSessionStoreUnavailable) { // БД недоступна: пользователя не выходим из системы
				writeUnavailable(w)
				return
			}
			// Запросы без сессии (экран входа, перебор, зондирование) ограничиваются по адресу клиента
			if !authed && !g.limitAnonymous(w, r) {
				return
			}
			if !publicMethods[method] {
				if !authed {
					writeError(w, http.StatusUnauthorized, "unauthenticated", auth.ErrNotAuthenticated.Error())
					return
				}
				if !csrfEqual(r.Header.Get(csrfHeader), sess.CSRF) {
					writeError(w, http.StatusForbidden, "csrf", "invalid or missing CSRF token")
					return
				}
			}
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRPCBody))
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "bad_request", "request body is too large or could not be read")
			return
		}
		var args []json.RawMessage
		if len(strings.TrimSpace(string(body))) > 0 {
			if err := json.Unmarshal(body, &args); err != nil {
				writeError(w, http.StatusBadRequest, "bad_request", "request body must be a JSON array of arguments")
				return
			}
		}
		// Вид ядра для вызова: у вошедшей сессии — её собственный; вход выполняется на новом виде,
		// который станет видом новой сессии; остальное (анонимные запросы) — общий экземпляр.
		var target, fresh any
		if g != nil {
			switch {
			case g.NewView != nil && (method == "Login" || method == "SetupAdmin"):
				fresh = g.NewView(g.clientIP(r))
				target = fresh
			case authed:
				target = sess.View
				// Сессию открыли на другой реплике: активный кластер подключается при первом запросе здесь.
				if c := g.Sessions.TakePendingCluster(sess.ID); c != "" && g.RestoreCluster != nil && sess.View != nil {
					if err := g.RestoreCluster(sess.View, c); err != nil {
						slog.WarnContext(r.Context(), "rpc: the active cluster of the session was not restored", "session", sess.ID, "error", err)
					}
				}
			}
		}
		result, err := d.CallOn(target, method, args)
		if err != nil {
			g.dropView(fresh)
			status, code := MapError(err)
			msg := err.Error()
			var reqID string
			if status == http.StatusInternalServerError {
				// Внутренние детали (адреса, БД, TLS) уходят только в журнал; клиент получает общий текст и request id.
				reqID, r = requestIDFor(w, r)
				var hidden bool
				msg, hidden = exposeMessage(err, reqID)
				logInternal(r, method, err, hidden)
			}
			var limited retryAfterError
			if errors.As(err, &limited) {
				w.Header().Set("Retry-After", fmt.Sprint(limited.RetryAfterSeconds()))
			}
			if g != nil && code == "unauthenticated" && authed {
				g.endSession(token) // ядро закрыло сессию (простой, выход): cookie больше не действует
				g.clearCookie(w, r)
			}
			writeErrorID(w, status, code, msg, reqID)
			return
		}
		if g != nil {
			var aerr error
			if result, aerr = g.afterCall(w, r, method, result, token, sess, authed, fresh); aerr != nil {
				slog.ErrorContext(r.Context(), "rpc: the web session was not saved", "method", method, "error", aerr)
				writeUnavailable(w)
				return
			}
		}
		writeJSON(w, http.StatusOK, rpcResponse{Result: result})
	})
}

// afterCall связывает результат метода с веб-сессией: вход открывает её, выход закрывает,
// смена пароля и отключение пользователя завершают его сессии на всех репликах, а стартовое состояние
// без сессии не раскрывает пользователя (ядро пока одно на всех). Ошибка — не удалось записать сессию
// в общее хранилище (вход тогда не состоялся).
func (g *Guard) afterCall(w http.ResponseWriter, r *http.Request, method string, result any, token string, sess Session, authed bool, fresh any) (any, error) {
	switch method {
	case "Login", "SetupAdmin":
		if u, ok := result.(*auth.UserInfo); ok && u != nil {
			if authed {
				g.endSession(token)
			}
			tok, ns, err := g.Sessions.Open(u.ID, fresh, g.clientIP(r))
			if err != nil {
				g.dropView(fresh)
				return nil, err
			}
			if fresh != nil && g.Bind != nil {
				g.Bind(fresh, ns.ID)
			}
			g.setCookie(w, r, tok, time.Until(ns.Expires))
			w.Header().Set(csrfHeader, ns.CSRF)
		} else {
			g.dropView(fresh)
		}
	case "Logout":
		if authed {
			g.endSession(token)
		}
		g.clearCookie(w, r)
	case "ReportActivity":
		if authed {
			g.Sessions.Activity(sess.ID)
		}
	case "ChangePassword":
		// Остальные сессии пользователя (в том числе на других репликах) завершаются, текущая получает новый токен.
		if authed {
			if err := g.Sessions.EndUser(sess.UserID, sess.ID); err != nil {
				slog.WarnContext(r.Context(), "rpc ChangePassword: the other sessions of the user were not ended", "error", err)
			}
			if tok, err := g.Sessions.Rotate(token); err == nil {
				g.setCookie(w, r, tok, time.Until(sess.Expires))
			} else {
				slog.WarnContext(r.Context(), "rpc ChangePassword: the token was not rotated", "error", err)
			}
		}
	case "SetUserDisabled", "ResetUserPassword":
		if u, ok := result.(*auth.UserInfo); ok && u != nil && (method == "ResetUserPassword" || u.Disabled) {
			if err := g.Sessions.EndUser(u.ID, ""); err != nil {
				slog.WarnContext(r.Context(), "rpc: the sessions of the user were not ended", "method", method, "error", err)
			}
		}
	case "AuthStatus":
		if st, ok := result.(*auth.Status); ok && st != nil {
			cp := *st
			changed := false
			if !authed && cp.User != nil {
				cp.User, changed = nil, true
			}
			if cp.NeedsSetup && g.Setup != nil { // экран настройки запросит токен
				cp.SetupTokenRequired, changed = true, true
			}
			if changed {
				return &cp, nil
			}
		}
	}
	if authed && sess.View != nil && g.ActiveCluster != nil && g.Sessions.Backend != nil {
		// Активный кластер запоминается в общем хранилище: на другой реплике сессия продолжится с ним же.
		if cur := g.ActiveCluster(sess.View); cur != sess.ActiveCluster {
			g.Sessions.SetActiveCluster(sess.ID, cur)
		}
	}
	return result, nil
}

// endSession закрывает веб-сессию; ошибку хранилища пишет в журнал (cookie всё равно сбрасывается).
func (g *Guard) endSession(token string) {
	if err := g.Sessions.Delete(token); err != nil {
		slog.Warn("sessions: the session was not removed from the store", "error", err)
	}
}

func writeUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "5")
	writeError(w, http.StatusServiceUnavailable, "service_unavailable", "service is temporarily unavailable")
}

// SessionHandler обслуживает GET /api/session: даёт странице CSRF-токен текущей сессии
// (после перезагрузки страницы) и признак входа. Без сессии токена нет.
func SessionHandler(g *Guard) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "forbidden_origin", "cross-origin request rejected")
			return
		}
		out := map[string]any{"authenticated": false}
		if g != nil {
			sess, err := g.Sessions.Lookup(g.token(r))
			if errors.Is(err, ErrSessionStoreUnavailable) {
				writeUnavailable(w)
				return
			}
			if err == nil {
				out = map[string]any{"authenticated": true, "csrfToken": sess.CSRF}
			}
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// sameOrigin: запрос без Origin (не браузерный клиент, curl) пропускается; с Origin —
// только если его хост совпадает с Host запроса. Если браузер сообщает Sec-Fetch-Site,
// допустимы только same-origin и none.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, rpcResponse{Error: &rpcError{Code: code, Message: msg}})
}

func writeErrorID(w http.ResponseWriter, status int, code, msg, requestID string) {
	writeJSON(w, status, rpcResponse{Error: &rpcError{Code: code, Message: msg, RequestID: requestID}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("rpc: failed to write response", "error", err)
	}
}

// dropView освобождает вид, который не стал видом сессии (вход не удался).
func (g *Guard) dropView(view any) {
	if view != nil && g.Sessions.Release != nil {
		g.Sessions.Release(view)
	}
}
