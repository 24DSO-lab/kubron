// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package ext

// Platform — всё, что ядру нужно от оболочки, в которой оно запущено (ADR-0002).
// Реализации: Wails (десктоп) и HTTP-сервер.
type Platform interface {
	// Emit отправляет событие фронтенду (cluster:changed, logs:* и т. д.).
	Emit(event string, data ...any)
	// PickFile показывает диалог выбора файла и возвращает путь ("" — отмена).
	PickFile(title string) (string, error)
	// OpenURL открывает ссылку в браузере пользователя.
	OpenURL(url string)
	// Capabilities сообщает, что доступно в этой оболочке (фронтенд скрывает недоступное).
	Capabilities() Capabilities
}

// Capabilities — возможности оболочки (ADR-0010). Десктоп: всё локальное доступно.
// Веб-сервер: часть возможностей отключена или иная.
type Capabilities struct {
	FileDialog  bool `json:"fileDialog"`  // нативный диалог выбора файла
	PortForward bool `json:"portForward"` // проброс портов
	// PortForwardMode — как открывается проброс: "local" (порт на localhost, десктоп и режим одного пользователя),
	// "proxy" (через прокси сервера на отдельном origin, ADR-0016) или "off". Адрес всегда приходит в ответе методов.
	PortForwardMode string `json:"portForwardMode"`
	NodeShell       bool   `json:"nodeShell"`       // оболочка на узле
	LocalKubeconfig bool   `json:"localKubeconfig"` // чтение kubeconfig с диска машины, где запущено ядро
	SSO             bool   `json:"sso"`             // вход через SSO/OIDC (платный модуль, этап 6)
	InCluster       bool   `json:"inCluster"`       // сервер работает внутри Kubernetes: можно добавить его кластер (режим in-cluster)
	Teams           bool   `json:"teams"`           // команды и кластеры по командам (сервисный режим; выставляет ядро по хранилищу)
}
