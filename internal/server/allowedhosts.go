// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
)

// Список разрешённых значений заголовка Host (KUBRON_ALLOWED_HOSTS). Без него любое имя, указывающее на сервер, получает
// ответ приложения: подделка Host (в ссылках сброса, редиректах, при кэшировании) и DNS rebinding работают против
// адресов, которых оператор не планировал. Пустой список — проверка выключена, как раньше.
//
// Запись списка — имя или IP-адрес, необязательно с портом: «kubron.example.com» принимает любой порт, а
// «kubron.example.com:8443» — только 8443. IPv6 записывается в квадратных скобках: «[::1]» или «[::1]:8080». Регистр и
// завершающая точка не важны. Подстановочных знаков нет: потоки port-forward (*.pf.<домен>) разрешаются отдельно, когда
// port-forward включён.

// hostPolicyExempt — пути без проверки Host: пробы оркестратора ходят по адресу пода, а ответ у них не зависит от Host.
var hostPolicyExempt = map[string]bool{"/healthz": true, "/readyz": true}

// ErrInvalidAllowedHost — запись списка разрешённых хостов не разобрана.
var ErrInvalidAllowedHost = errors.New("an allowed host must be a host name or an IP address with an optional port, without scheme or path")

// canonHost приводит значение Host (или запись списка) к виду «имя» и «имя:порт»: нижний регистр, без завершающей
// точки, IPv6 в скобках в краткой записи. ok == false — значение не похоже на хост.
func canonHost(raw string) (host, port string, ok bool) {
	h := strings.ToLower(strings.TrimSpace(raw))
	if h == "" || strings.ContainsAny(h, "/\\@*?# \t") {
		return "", "", false
	}
	if hh, pp, err := net.SplitHostPort(h); err == nil {
		h, port = hh, pp
		if port != "" {
			n, err := strconv.ParseUint(port, 10, 16)
			if err != nil || n == 0 {
				return "", "", false
			}
			port = strconv.FormatUint(n, 10)
		}
	} else if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	} else if strings.Contains(h, ":") {
		return "", "", false // «::1» без скобок или порт без хоста
	}
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return "", "", false
	}
	if a, err := netip.ParseAddr(h); err == nil {
		a = a.WithZone("").Unmap()
		if a.Is6() {
			return "[" + a.String() + "]", port, true
		}
		return a.String(), port, true
	}
	if strings.ContainsAny(h, "[]:") {
		return "", "", false
	}
	return h, port, true
}

// ParseAllowedHosts разбирает список через запятую в канонические записи; пустая строка — пустой список.
func ParseAllowedHosts(list string) ([]string, error) {
	var out []string
	for _, item := range strings.Split(list, ",") {
		if strings.TrimSpace(item) == "" {
			continue
		}
		host, port, ok := canonHost(item)
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrInvalidAllowedHost, strings.TrimSpace(item))
		}
		if port != "" {
			host += ":" + port
		}
		out = append(out, host)
	}
	return out, nil
}

type hostMatcher struct {
	anyPort map[string]bool // «имя»: любой порт
	exact   map[string]bool // «имя:порт»
}

func newHostMatcher(entries []string) *hostMatcher {
	m := &hostMatcher{anyPort: map[string]bool{}, exact: map[string]bool{}}
	for _, e := range entries {
		host, port, ok := canonHost(e)
		if !ok {
			continue // Config проверил список; сюда недопустимая запись не доходит
		}
		if port == "" {
			m.anyPort[host] = true
		} else {
			m.exact[host+":"+port] = true
		}
	}
	return m
}

func (m *hostMatcher) allowed(requestHost string) bool {
	host, port, ok := canonHost(requestHost)
	if !ok {
		return false
	}
	return m.anyPort[host] || (port != "" && m.exact[host+":"+port])
}

// WithAllowedHosts отклоняет запросы с Host вне списка до маршрутизации и сессий. allowed — записи KUBRON_ALLOWED_HOSTS
// (пусто — проверка выключена, возвращается next). extra — дополнительные разрешённые хосты, например потоки port-forward
// (*.pf.<домен>); может быть nil. Ответ на любой отказ один и тот же и не раскрывает список.
func WithAllowedHosts(next http.Handler, allowed []string, extra func(host string) bool) http.Handler {
	if len(allowed) == 0 {
		return next
	}
	m := newHostMatcher(allowed)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hostPolicyExempt[r.URL.Path] || m.allowed(r.Host) || (extra != nil && extra(r.Host)) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusMisdirectedRequest)
		_, _ = w.Write([]byte("misdirected request\n"))
	})
}
