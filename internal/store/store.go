// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package store собирает хранилища приложения в одно целое (ADR-0007).
//
// Десктоп использует файловую реализацию (NewFile): users.json, audit.log и файлы токенов
// в каталоге данных. Серверная версия добавит реализацию на PostgreSQL с шифрованием секретов
// (ADR-0008) и подставит её без изменений в ядре.
//
// Кластеры (метаданные и kubeconfig) хранятся через k8s.ClusterStore (задача 3.0): файлы в десктопе,
// PostgreSQL с шифрованием — в сервисе.
package store

import (
	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/readiness"
)

// Store — всё, что приложение хранит постоянно.
type Store interface {
	// Users — пользователи и настройки безопасности.
	Users() auth.Backend
	// Audit — журнал аудита.
	Audit() audit.Backend
	// Readiness — история отчётов Upgrade Readiness (ADR-0025).
	Readiness() readiness.Backend
	// ArgoTokens — токены Argo CD API по кластерам.
	ArgoTokens() k8s.TokenStore
	// Clusters — кластеры: метаданные и kubeconfig (владение командой добавит этап 3).
	Clusters() k8s.ClusterStore
	// WebOIDC — публичный клиент веб-версии для входа через браузер и провайдер, для которого он зарегистрирован.
	WebOIDC() (WebOIDC, error)
	SetWebOIDC(WebOIDC) error
}

// WebOIDC — настройка входа через браузер в веб-версии: публичный client_id и издатель (issuer), у которого он
// зарегистрирован. Клиент применяется вместо клиента из kubeconfig только для кластеров с этим издателем и
// только при входе через authorization code; остальные кластеры и остальные виды входа не затрагиваются.
// Запись без IssuerURL (прежний формат настройки) не применяется: без привязки клиент подошёл бы любому провайдеру.
type WebOIDC struct {
	ClientID  string `json:"clientId"`
	IssuerURL string `json:"issuerUrl"`
}
