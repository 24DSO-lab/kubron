// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness

import "errors"

// Ошибки чтения кластера. Пакет не импортирует платформу (ADR-0026): слой связки приводит ошибки Reader платформы
// к этим значениям, а домен сравнивает их через errors.Is и не разбирает текст.
var (
	// ErrSourceNotFound — вида ресурса нет в кластере.
	ErrSourceNotFound = errors.New("cluster resource not found")
	// ErrSourceForbidden — чтение запрещено платформой или Kubernetes RBAC.
	ErrSourceForbidden = errors.New("reading the cluster resource is forbidden")
	// ErrSourceUnavailable — источник не ответил: таймаут, сеть, перегруженный API. Это не пустой успех.
	ErrSourceUnavailable = errors.New("cluster source is unavailable")
	// ErrSourceTooLarge — ответ превысил лимит размера.
	ErrSourceTooLarge = errors.New("cluster response is too large")
	// ErrSourcePartial — источник доступен частично: вместе с ошибкой возвращены прочитанные данные.
	ErrSourcePartial = errors.New("cluster source is partially available")
)

// Ошибки запроса проверки.
var (
	// ErrInvalidTarget — целевая версия не разобрана или не входит в окно.
	ErrInvalidTarget = errors.New("target Kubernetes version is invalid")
	// ErrTargetOutOfWindow — целевая версия вне окна поддерживаемых версий матрицы.
	ErrTargetOutOfWindow = errors.New("target Kubernetes version is outside the supported window")
	// ErrTargetOlder — целевая версия старше текущей: обновление «назад» не оценивается.
	ErrTargetOlder = errors.New("target Kubernetes version is older than the current one")
	// ErrCurrentVersion — версию кластера определить не удалось.
	ErrCurrentVersion = errors.New("failed to determine the cluster version")
)

// Ошибки хранилища (контракт Backend).
var (
	// ErrNotFound — отчёта нет либо он принадлежит другой области (tenant.Scope): чужая область отвечает как отсутствующая.
	ErrNotFound = errors.New("readiness report not found")
	// ErrInvalidScope — область доступа не заполнена полностью: хранилище отказывает (fail closed).
	ErrInvalidScope = errors.New("readiness scope is incomplete")
	// ErrInvalidReport — отчёт не прошёл проверку и не сохраняется (в том числе без заполненного coverage).
	ErrInvalidReport = errors.New("readiness report is invalid")
	// ErrBadCursor — курсор страницы истории повреждён.
	ErrBadCursor = errors.New("history cursor is invalid")
)
