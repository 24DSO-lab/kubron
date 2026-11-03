// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"
)

// Версия RPC-контракта. Страница фронтенда собирается вместе с сервером и знает его версию (VITE_KUBRON_BUILD, в образе —
// VERSION). Если вкладка осталась открытой, а сервер обновили (или кэш отдал старый index.html), набор и сигнатуры методов
// у них разные. Тогда вызов отклоняется понятным 409 contract_mismatch, а страница перезагружается, а не получает
// «unknown_method» или молча неверные аргументы.
const (
	// BuildHeader — заголовок версии сборки страницы в RPC-запросах и ответах.
	BuildHeader = "X-Kubron-Build"
	// buildQuery — то же для WebSocket: браузер не умеет задавать заголовки при открытии сокета.
	buildQuery = "build"
	// devBuild — сборка из исходников: проверка отключена (dev-сервер Vite, go run, e2e в CI).
	devBuild = "dev"
)

// contractChecked сообщает, включена ли проверка для версии сервера build: пустая и "dev" её отключают.
func contractChecked(build string) bool { return build != "" && build != devBuild }

// contractMatches — совместим ли клиент с сервером. Клиент без версии (curl, проверки здоровья, скрипты) и клиент "dev"
// не проверяются: проверка защищает от устаревшей страницы браузера, а не от прочих вызовов.
func contractMatches(server, client string) bool {
	return !contractChecked(server) || client == "" || client == devBuild || client == server
}

// WithContractBuild включает проверку версии контракта для RPC и WebSocket; build — версия сервера (main.version).
func WithContractBuild(build string) HandlerOption {
	return func(o *handlerOptions) { o.build = build }
}

// contractGuard отклоняет запрос страницы другой версии кодом 409 contract_mismatch. Без включённой проверки возвращает next.
func contractGuard(build string, next http.Handler) http.Handler {
	if !contractChecked(build) {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client := r.Header.Get(BuildHeader)
		if client == "" {
			client = r.URL.Query().Get(buildQuery)
		}
		w.Header().Set(BuildHeader, build)
		if !contractMatches(build, client) {
			writeError(w, http.StatusConflict, "contract_mismatch",
				"the page was built for another server version: reload the page")
			return
		}
		next.ServeHTTP(w, r)
	})
}
