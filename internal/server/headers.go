// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"net"
	"net/http"
	"regexp"
	"strings"
)

// HandlerOption настраивает NewHandler.
type HandlerOption func(*handlerOptions)

type handlerOptions struct {
	webOIDCCallback func(context.Context, any, string, string) error

	cspReportOnly bool
	readyz        ReadyzConfig
	build         string // версия сервера для проверки контракта RPC (WithContractBuild)
}

// WithWebOIDCCallback registers the session-bound OIDC browser callback route.
func WithWebOIDCCallback(fn func(context.Context, any, string, string) error) HandlerOption {
	return func(o *handlerOptions) { o.webOIDCCallback = fn }
}

// WithCSPReportOnly отдаёт политику в заголовке Content-Security-Policy-Report-Only: браузер
// только сообщает о нарушениях в консоль, ничего не блокируя. Нужен на время обкатки политики
// (KUBRON_CSP_REPORT_ONLY); по умолчанию политика применяется.
func WithCSPReportOnly(on bool) HandlerOption {
	return func(o *handlerOptions) { o.cspReportOnly = on }
}

// WithReadyz настраивает GET /readyz (проверка БД и признак draining). Без опции /readyz отвечает 200
// (нечего проверять), но признак draining берётся из Guard.Lifecycle, если защита включена.
func WithReadyz(c ReadyzConfig) HandlerOption {
	return func(o *handlerOptions) { o.readyz = c }
}

// hstsValue — HSTS на год. includeSubDomains не включается намеренно: Kubron может стоять на
// поддомене, рядом с которым живут другие сервисы, ещё не переведённые на HTTPS. preload — тоже
// решение владельца домена, а не приложения.
const hstsValue = "max-age=31536000"

// permissionsPolicy отключает возможности браузера, которые интерфейсу не нужны.
const permissionsPolicy = "camera=(), microphone=(), geolocation=(), payment=(), usb=(), serial=(), " +
	"bluetooth=(), accelerometer=(), gyroscope=(), magnetometer=(), display-capture=(), interest-cohort=()"

// hostRe — допустимое значение Host для подстановки в CSP: имя или IPv4 с портом либо IPv6 в скобках.
// Всё остальное (пробелы, «;», кавычки) не попадает в заголовок: иначе клиент мог бы дописать директивы.
var hostRe = regexp.MustCompile(`^(?:[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?|\[[0-9A-Fa-f:.]+\])(?::[0-9]{1,5})?$`)

// contentSecurityPolicy — политика для страниц и ответов Go-сервера. Единственный источник CSP:
// <meta http-equiv> в index.html не добавляется, иначе политика затронула бы десктопную сборку Wails.
//
//   - script-src 'self': ни 'unsafe-inline', ни 'unsafe-eval'. Vite выдаёт скрипты только файлами.
//   - style-src 'self' 'unsafe-inline': ВЫНУЖДЕННО. Monaco и xterm создают <style> на лету
//     (document.createElement('style')), а в приложении так же подключается шрифт codicon
//     (MonacoEditor.vue, TabbedPanel.vue и др.). Без 'unsafe-inline' редактор остаётся без стилей.
//     Риск ограничен стилями: скрипты по-прежнему только 'self'. Убрать можно, перейдя на nonce
//     для <style> (Monaco этого не умеет) или на предсобранный CSS.
//   - connect-src: 'self' плюс явные ws:// и wss:// своего хоста. Safari исторически не считал
//     'self' совпадающим со схемами WebSocket, а /api/ws и /api/terminal/* работают по WebSocket.
//   - worker-src 'self' blob:: воркеры Monaco (vite ?worker) — файлы своего origin, blob: — запас
//     для режима разработки и встраиваемых воркеров.
//   - img-src 'self' data:: Monaco и графики используют data:-изображения.
//   - font-src 'self': шрифт codicon отдаётся файлом (assetsInlineLimit: 0), data: не нужен.
func contentSecurityPolicy(host string) string {
	connect := "'self'"
	if hostRe.MatchString(host) {
		connect += " ws://" + host + " wss://" + host
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"font-src 'self'",
		"connect-src " + connect,
		"worker-src 'self' blob:",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
}

// isSecure — запрос пришёл по защищённому каналу: напрямую по TLS или через доверенный прокси,
// сообщивший X-Forwarded-Proto: https. Та же проверка решает, помечать ли cookie Secure (Guard.secure)
// и отправлять ли HSTS.
func isSecure(r *http.Request, trustProxy bool) bool {
	return r.TLS != nil || (trustProxy && r.Header.Get("X-Forwarded-Proto") == "https")
}

// isLoopbackRequest — запрос к loopback-адресу (localhost, 127.0.0.0/8, ::1) по заголовку Host.
// HSTS на loopback вреден: браузер запомнит localhost как «только HTTPS» и сломает другие локальные сервисы.
func isLoopbackRequest(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return isLoopbackHost(strings.Trim(host, "[]"))
}

// securityHeaders выставляет заголовки безопасности на каждый ответ (страницы, статику, API, ошибки).
func securityHeaders(next http.Handler, trustProxy bool, o handlerOptions) http.Handler {
	cspHeader := "Content-Security-Policy"
	if o.cspReportOnly {
		cspHeader = "Content-Security-Policy-Report-Only"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set(cspHeader, contentSecurityPolicy(r.Host))
		h.Set("Permissions-Policy", permissionsPolicy)
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		// HSTS только по защищённому каналу и не на loopback: по обычному HTTP заголовок игнорируется
		// браузером, а на loopback он вредит
		if isSecure(r, trustProxy) && !isLoopbackRequest(r) {
			h.Set("Strict-Transport-Security", hstsValue)
		}
		next.ServeHTTP(w, r)
	})
}
