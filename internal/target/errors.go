// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package target

// Kind — категория ошибки домена.
type Kind string

const (
	// KindInvalid — некорректный ввод: формат, enum, владелец, превышение предела.
	KindInvalid Kind = "invalid"
	// KindNotFound — запись отсутствует либо лежит вне видимой области (неотличимо).
	KindNotFound Kind = "not_found"
	// KindConflict — несовпадение revision (CAS), повторное создание, деактивированная группа, смена revision
	// группы или ownership во время resolution.
	KindConflict Kind = "conflict"
	// KindForbidden — actor не вправе выполнить действие.
	KindForbidden Kind = "forbidden"
	// KindUnavailable — зависимость недоступна, таймаут или исход нельзя доказать; fail closed.
	KindUnavailable Kind = "unavailable"
	// KindIncomplete — resolution неполон: snapshot не создаётся.
	KindIncomplete Kind = "incomplete"
)

// Стабильные Reason'ы ошибок. Это публичный контракт: значения не меняются и не переиспользуются.
const (
	ReasonStaleRevision  = "stale_revision"
	ReasonAlreadyExists  = "already_exists"
	ReasonDeactivated    = "group_deactivated"
	ReasonCanceled       = "canceled"
	ReasonRequestInvalid = "request_invalid"
)

// Error — ошибка домена. Текст содержит только категорию, стабильный код и имя поля: значений полей,
// идентификаторов и имён чужих целей в нём нет, поэтому его безопасно показывать и логировать.
type Error struct {
	Kind   Kind
	Reason string
	Field  string
}

// Error возвращает безопасное описание.
func (e *Error) Error() string {
	s := "target: " + string(e.Kind)
	if e.Reason != "" {
		s += ": " + e.Reason
	}
	if e.Field != "" {
		s += " (" + e.Field + ")"
	}
	return s
}

// Is реализует сопоставление для errors.Is: пустые Reason/Field цели означают «любые».
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok || t == nil {
		return false
	}
	return e.Kind == t.Kind && (t.Reason == "" || t.Reason == e.Reason) && (t.Field == "" || t.Field == e.Field)
}

// Сентинелы категорий и частых причин: errors.Is(err, target.ErrConflict).
var (
	ErrInvalid     = &Error{Kind: KindInvalid}
	ErrNotFound    = &Error{Kind: KindNotFound}
	ErrConflict    = &Error{Kind: KindConflict}
	ErrForbidden   = &Error{Kind: KindForbidden}
	ErrUnavailable = &Error{Kind: KindUnavailable}
	ErrIncomplete  = &Error{Kind: KindIncomplete}

	// ErrStaleRevision — ExpectedRevision не совпал с текущей revision (ADR-0054: conflict/stale_revision).
	ErrStaleRevision = &Error{Kind: KindConflict, Reason: ReasonStaleRevision}
	// ErrAlreadyExists — ID уже занят.
	ErrAlreadyExists = &Error{Kind: KindConflict, Reason: ReasonAlreadyExists}
	// ErrDeactivated — группа деактивирована (tombstone), изменения запрещены.
	ErrDeactivated = &Error{Kind: KindConflict, Reason: ReasonDeactivated}
	// ErrGroupRevisionChanged — revision группы изменилась во время resolution (ADR-0055).
	ErrGroupRevisionChanged = &Error{Kind: KindConflict, Reason: string(ReasonGroupRevisionChanged)}
)

func invalid(field string) error {
	return &Error{Kind: KindInvalid, Reason: ReasonRequestInvalid, Field: field}
}

func invalidSelector(field string) error {
	return &Error{Kind: KindInvalid, Reason: string(ReasonSelectorInvalid), Field: field}
}

func limitExceeded(field string) error {
	return &Error{Kind: KindInvalid, Reason: string(ReasonSelectorLimitExceeded), Field: field}
}

func notFound() error    { return &Error{Kind: KindNotFound} }
func forbidden() error   { return &Error{Kind: KindForbidden} }
func unavailable() error { return &Error{Kind: KindUnavailable} }

func unavailableReason(reason string) error {
	return &Error{Kind: KindUnavailable, Reason: reason}
}

func conflictReason(reason string) error {
	return &Error{Kind: KindConflict, Reason: reason}
}

// normalize приводит ошибку зависимости к доменной: *Error сохраняется как есть, всё прочее — недоступность.
// Текст чужих ошибок наружу не передаётся (он может содержать данные другого tenant'а).
func normalize(err error) error {
	if err == nil {
		return nil
	}
	if e, ok := asError(err); ok {
		return e
	}
	return unavailable()
}

func asError(err error) (*Error, bool) {
	for err != nil {
		if e, ok := err.(*Error); ok {
			return e, true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return nil, false
		}
		err = u.Unwrap()
	}
	return nil, false
}
