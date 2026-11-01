// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import "net/http"

// WithPortForwardHosts направляет запросы к хостам потоков port-forward (match(host) == true) в pf, остальные — в
// основной обработчик. Должен стоять снаружи NewHandler: ответы потоков не получают заголовков Kubron
// (CSP, X-Frame-Options DENY, CORP), а запросы к хостам потоков никогда не доходят до RPC и сессий Kubron.
func WithPortForwardHosts(main, pf http.Handler, match func(host string) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if match(r.Host) {
			pf.ServeHTTP(w, r)
			return
		}
		main.ServeHTTP(w, r)
	})
}
