// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import "errors"

// Ошибки Flux. Тексты английские; перевод — шаблоны в frontend/src/i18n/locales/<язык>/backend.json
// (npm run i18n:check требует запись для каждого errors.New/fmt.Errorf). Исполнители оборачивают их с контекстом:
// fmt.Errorf("%w: %s", errFluxKindUnsupported, kind). Нужна новая ошибка — это изменение контракта (отдельный PR).
var (
	// errFluxNotImplemented — заглушка до реализации действия (задачи B.8 и B.9).
	errFluxNotImplemented = errors.New("not implemented yet")

	// errFluxNotInstalled — Flux не найден в активном кластере (нет CRD или нет прав их увидеть).
	errFluxNotInstalled = errors.New("Flux is not installed in this cluster")

	// errFluxKindUnsupported — вид объекта не поддерживается для этого действия (таблица flux_kinds.go).
	errFluxKindUnsupported = errors.New("this action is not supported for the kind of the object")

	// errFluxInvalidRef — в ссылке на объект нет вида, namespace или имени.
	errFluxInvalidRef = errors.New("invalid reference to a Flux object")

	// errFluxNoObjects — действие вызвано с пустым списком объектов.
	errFluxNoObjects = errors.New("no objects selected")

	// errFluxTooManyObjects — в одном вызове больше fluxMaxActionObjects объектов.
	errFluxTooManyObjects = errors.New("too many objects in one call: the maximum is 200")

	// errFluxVersionUnsupported — CRD есть, но обслуживается только версия API вне списка поддерживаемых.
	errFluxVersionUnsupported = errors.New("the Flux API version is not supported: update Flux to 2.3 or newer")

	// errFluxDeleting — объект уже удаляется (deletionTimestamp).
	errFluxDeleting = errors.New("the object is being deleted")

	// errFluxSuspended — объект приостановлен: действие не имеет эффекта, пока его не возобновят.
	errFluxSuspended = errors.New("the object is suspended: resume it first")
)
