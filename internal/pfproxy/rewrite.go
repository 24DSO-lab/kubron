// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pfproxy

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// CookieName — cookie доступа к потоку. Приложению в поде она не передаётся и им не перезаписывается.
const CookieName = "kpf"

// internalHost: адрес, который приложение в поде считает «своим» (localhost, адреса кластера, имена сервиса и пода).
// Редирект на такой адрес браузеру недоступен и переписывается на хост потока.
func internalHost(host string, t Target) bool {
	h := strings.ToLower(host)
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	switch {
	case h == "", h == "localhost", h == "pod.invalid":
		return true
	case t.Service != "" && (h == strings.ToLower(t.Service) || strings.HasPrefix(h, strings.ToLower(t.Service)+".")):
		return true
	case t.Pod != "" && (h == strings.ToLower(t.Pod) || strings.HasPrefix(h, strings.ToLower(t.Pod)+".")):
		return true
	case strings.HasSuffix(h, ".svc"), strings.HasSuffix(h, ".cluster.local"), strings.HasSuffix(h, ".svc.cluster.local"):
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
	}
	return false
}

// rewriteLocation переписывает значение Location: адрес приложения (внутренний или совпадающий с хостом потока)
// превращается в путь на том же хосте, внешние адреса остаются без изменений. Второй результат — менялся ли заголовок.
func rewriteLocation(loc, requestHost string, t Target) (string, bool) {
	u, err := url.Parse(loc)
	if err != nil {
		return loc, false
	}
	if u.Host == "" { // относительный адрес или схема без хоста («mailto:») остаётся как есть
		return loc, false
	}
	if u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https" {
		return loc, false
	}
	if !strings.EqualFold(u.Host, requestHost) && !internalHost(u.Host, t) {
		return loc, false
	}
	rel := &url.URL{Path: u.Path, RawPath: u.RawPath, RawQuery: u.RawQuery, ForceQuery: u.ForceQuery, Fragment: u.Fragment}
	out := rel.String()
	if out == "" || out[0] != '/' { // путь без ведущего «/» ("http://h") — корень
		out = "/" + strings.TrimPrefix(out, "/")
	}
	if strings.HasPrefix(out, "//") { // «//evil.example» иначе стал бы адресом другого хоста
		out = "/" + strings.TrimLeft(out, "/")
	}
	return out, true
}

// rewriteSetCookie делает cookie приложения привязанными к хосту потока: Domain убирается (cookie остаётся host-only),
// при HTTPS ставится Secure, а SameSite по умолчанию — Lax. Cookie с именем доступа к потоку отбрасывается.
func rewriteSetCookie(h http.Header, secure bool) {
	raw := h.Values("Set-Cookie")
	if len(raw) == 0 {
		return
	}
	h.Del("Set-Cookie")
	resp := http.Response{Header: http.Header{"Set-Cookie": raw}}
	for _, c := range resp.Cookies() {
		if c.Name == CookieName {
			continue
		}
		c.Domain = ""
		if secure {
			c.Secure = true
		}
		if c.SameSite == 0 {
			c.SameSite = http.SameSiteLaxMode
		}
		if c.SameSite == http.SameSiteNoneMode && !c.Secure {
			c.SameSite = http.SameSiteLaxMode // None без Secure браузеры отбрасывают
		}
		h.Add("Set-Cookie", c.String())
	}
}

// stripAccessCookie убирает cookie доступа к потоку из заголовка Cookie запроса к приложению.
func stripAccessCookie(h http.Header) {
	vals := h.Values("Cookie")
	if len(vals) == 0 {
		return
	}
	h.Del("Cookie")
	var keep []string
	for _, line := range vals {
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			if part == "" || part == CookieName || strings.HasPrefix(part, CookieName+"=") {
				continue
			}
			keep = append(keep, part)
		}
	}
	if len(keep) > 0 {
		h.Set("Cookie", strings.Join(keep, "; "))
	}
}

// hardenResponse добавляет заголовки, которых приложение не задало. CSP приложения не трогается: sandbox и
// default-src 'self' ломают Argo CD и Grafana; защита потока — изоляция origin.
func hardenResponse(h http.Header) {
	h.Del("Strict-Transport-Security")
	h.Set("X-Content-Type-Options", "nosniff")
	if h.Get("Referrer-Policy") == "" {
		h.Set("Referrer-Policy", "no-referrer")
	}
	if h.Get("Permissions-Policy") == "" {
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), serial=(), bluetooth=(), interest-cohort=()")
	}
	if h.Get("X-Frame-Options") == "" && !strings.Contains(strings.ToLower(h.Get("Content-Security-Policy")), "frame-ancestors") {
		h.Set("X-Frame-Options", "SAMEORIGIN")
	}
}
