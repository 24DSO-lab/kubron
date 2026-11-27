// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package audit

// Критичный аудит (Phase 1, Lane D, ADR-0038).
//
// В отличие от Logger.Record (best-effort, ошибка записи не прерывает действие), CriticalRecorder
// возвращает ошибку и служит воротами для защищённых изменений identity/credential/grant/delegation:
//
//  1. AppendIntent вернулся без ошибки → запись intent durable (сброшена на диск), можно менять состояние.
//     Ошибка AppendIntent → вызывающий НЕ меняет состояние.
//  2. Выполняется ровно одна CAS-мутация доменного состояния.
//  3. AppendOutcome фиксирует succeeded/failed. Ошибка AppendOutcome означает «исход неизвестен»:
//     вызывающий возвращает ErrOutcomeUnknown (оборачивая причину), не сообщает об успехе и не повторяет
//     мутацию вслепую. Ресурс остаётся непригодным, пока GetOutcome не вернёт CriticalSucceeded.
//
// Сам CriticalRecorder не вызывает доменные операции и не импортирует доменные пакеты (credential и т.п.
// импортируют audit, не наоборот). В записях только непрозрачные идентификаторы, действие, ревизия,
// digest запроса, безопасный код причины и время — никаких секретов и payload провайдеров.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// CriticalResult — исход защищённой мутации.
type CriticalResult string

const (
	// CriticalSucceeded — мутация выполнена и подтверждена.
	CriticalSucceeded CriticalResult = "succeeded"
	// CriticalFailed — мутация не выполнена (известно, что состояние не изменилось).
	CriticalFailed CriticalResult = "failed"
	// CriticalUnknown — результат доказать нельзя; требуется явное безопасное разрешение (reconciliation).
	CriticalUnknown CriticalResult = "unknown"
)

// Ошибки критичного аудита. Сравнивать только через errors.Is, не по тексту.
// Тексты ошибок не содержат значений полей записей — только имена полей.
var (
	// ErrCriticalInvalid — некорректная запись (пустое/недопустимое поле, неверный enum).
	ErrCriticalInvalid = errors.New("audit: invalid critical record")
	// ErrCriticalNotFound — intent/outcome не найден или лежит вне видимой организации.
	ErrCriticalNotFound = errors.New("audit: critical record not found")
	// ErrCriticalConflict — тот же EventID с другим содержимым либо второй, отличающийся outcome.
	ErrCriticalConflict = errors.New("audit: critical record conflict")
	// ErrCriticalDurability — запись не удалось сделать durable (write/short write/disk full/Sync).
	ErrCriticalDurability = errors.New("audit: critical journal write is not durable")
	// ErrCriticalRecoveryRequired — журнал повреждён или состояние записи неизвестно; все операции
	// закрыты (fail closed) до восстановления. Подробности: FileCriticalRecorder.Recovery.
	ErrCriticalRecoveryRequired = errors.New("audit: critical journal requires recovery")
	// ErrCriticalJournalFull — достигнут предел размера журнала; записи не принимаются (fail closed).
	ErrCriticalJournalFull = errors.New("audit: critical journal is full")
	// ErrCriticalClosed — recorder закрыт.
	ErrCriticalClosed = errors.New("audit: critical recorder is closed")
	// ErrOutcomeUnknown — защищённая мутация могла быть выполнена, но её outcome не сохранён.
	// Возвращается вызывающим кодом (протокол §7.2), оборачивая ошибку AppendOutcome:
	//
	//	fmt.Errorf("%w: %w", audit.ErrOutcomeUnknown, err)
	ErrOutcomeUnknown = errors.New("audit: mutation outcome is unknown")
)

// CriticalIntent — неизменяемая запись о намерении выполнить защищённую мутацию.
// Все идентификаторы непрозрачны; секретов и payload провайдеров быть не должно.
type CriticalIntent struct {
	EventID          string // уникален; один intent и один outcome на EventID
	MutationID       string // идентификатор запрошенной доменной мутации
	RequestDigest    string // канонический digest запроса (не сам запрос)
	ActorKind        string
	ActorID          string
	PrincipalKind    string // пусто вместе с PrincipalID, если мутация не относится к principal
	PrincipalID      string
	OrgID            string
	TeamID           string
	ClusterID        string
	Action           string
	ResourceType     string
	ResourceID       string
	ExpectedRevision uint64
	OccurredAt       time.Time
}

// CriticalOutcome — неизменяемый исход, парный к CriticalIntent с тем же EventID.
type CriticalOutcome struct {
	EventID          string
	Result           CriticalResult
	ResourceRevision uint64
	ReasonCode       string // безопасный код (a-z0-9._-), обязателен для failed/unknown
	CompletedAt      time.Time
}

// CriticalRecorder — контракт критичного журнала (см. Phase 1 Contract Freeze §7.1).
// GetOutcome и ListPending ограничены организацией: чужие записи не видны и не отличимы от отсутствующих.
type CriticalRecorder interface {
	// AppendIntent возвращает nil только после durable-записи. Повтор того же EventID с тем же
	// содержимым — идемпотентен (nil), с другим — ErrCriticalConflict.
	AppendIntent(ctx context.Context, in CriticalIntent) error
	// AppendOutcome требует существующий intent; outcome уникален. Повтор идентичного — nil,
	// отличающегося — ErrCriticalConflict. Ошибка трактуется вызывающим как неизвестный исход.
	AppendOutcome(ctx context.Context, out CriticalOutcome) error
	// GetOutcome возвращает ErrCriticalNotFound, пока outcome отсутствует (в т.ч. чужая организация).
	GetOutcome(ctx context.Context, orgID, eventID string) (CriticalOutcome, error)
	// ListPending возвращает intent без outcome, старые первыми. limit ≤ 0 → CriticalDefaultPending,
	// больше CriticalMaxPending обрезается.
	ListPending(ctx context.Context, orgID string, limit int) ([]CriticalIntent, error)
}

const (
	// CriticalDefaultPending и CriticalMaxPending ограничивают выборку ListPending.
	CriticalDefaultPending = 100
	CriticalMaxPending     = 1000

	criticalMaxFieldLen  = 128
	criticalMaxReasonLen = 64
)

// safeToken: непустая строка из [A-Za-z0-9._:@/=+-] — без пробелов, управляющих символов и переводов строк.
// Ограничение алфавита исключает инъекции в журнал и не позволяет положить туда свободный текст/payload.
func validToken(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("._:@/=+-", c) >= 0:
		default:
			return false
		}
	}
	return true
}

func validReason(s string) bool {
	if s == "" || len(s) > criticalMaxReasonLen || !utf8.ValidString(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

func invalidField(name string) error {
	return fmt.Errorf("%w: field %s", ErrCriticalInvalid, name)
}

// Validate проверяет intent. Значения полей в текст ошибки не попадают.
func (in CriticalIntent) Validate() error {
	required := []struct{ name, v string }{
		{"EventID", in.EventID}, {"MutationID", in.MutationID}, {"RequestDigest", in.RequestDigest},
		{"ActorKind", in.ActorKind}, {"ActorID", in.ActorID}, {"OrgID", in.OrgID},
		{"Action", in.Action}, {"ResourceType", in.ResourceType}, {"ResourceID", in.ResourceID},
	}
	for _, f := range required {
		if !validToken(f.v, criticalMaxFieldLen) {
			return invalidField(f.name)
		}
	}
	optional := []struct{ name, v string }{
		{"PrincipalKind", in.PrincipalKind}, {"PrincipalID", in.PrincipalID},
		{"TeamID", in.TeamID}, {"ClusterID", in.ClusterID},
	}
	for _, f := range optional {
		if f.v != "" && !validToken(f.v, criticalMaxFieldLen) {
			return invalidField(f.name)
		}
	}
	if (in.PrincipalKind == "") != (in.PrincipalID == "") {
		return invalidField("PrincipalKind/PrincipalID")
	}
	if in.OccurredAt.IsZero() {
		return invalidField("OccurredAt")
	}
	return nil
}

// Validate проверяет outcome.
func (o CriticalOutcome) Validate() error {
	if !validToken(o.EventID, criticalMaxFieldLen) {
		return invalidField("EventID")
	}
	switch o.Result {
	case CriticalSucceeded:
		if o.ReasonCode != "" && !validReason(o.ReasonCode) {
			return invalidField("ReasonCode")
		}
	case CriticalFailed, CriticalUnknown:
		if !validReason(o.ReasonCode) {
			return invalidField("ReasonCode")
		}
	default:
		return invalidField("Result")
	}
	if o.CompletedAt.IsZero() {
		return invalidField("CompletedAt")
	}
	return nil
}

// normalized возвращает копию с временем в UTC без monotonic-части: каноническая форма для
// сравнения и хранения.
func (in CriticalIntent) normalized() CriticalIntent {
	in.OccurredAt = in.OccurredAt.UTC().Round(0)
	return in
}

func (o CriticalOutcome) normalized() CriticalOutcome {
	o.CompletedAt = o.CompletedAt.UTC().Round(0)
	return o
}

// sameIntent — равенство канонических payload (время сравнивается как момент, не как представление).
func sameIntent(a, b CriticalIntent) bool {
	a, b = a.normalized(), b.normalized()
	return a.OccurredAt.Equal(b.OccurredAt) && a.EventID == b.EventID && a.MutationID == b.MutationID &&
		a.RequestDigest == b.RequestDigest && a.ActorKind == b.ActorKind && a.ActorID == b.ActorID &&
		a.PrincipalKind == b.PrincipalKind && a.PrincipalID == b.PrincipalID && a.OrgID == b.OrgID &&
		a.TeamID == b.TeamID && a.ClusterID == b.ClusterID && a.Action == b.Action &&
		a.ResourceType == b.ResourceType && a.ResourceID == b.ResourceID &&
		a.ExpectedRevision == b.ExpectedRevision
}

func sameOutcome(a, b CriticalOutcome) bool {
	a, b = a.normalized(), b.normalized()
	return a.CompletedAt.Equal(b.CompletedAt) && a.EventID == b.EventID && a.Result == b.Result &&
		a.ResourceRevision == b.ResourceRevision && a.ReasonCode == b.ReasonCode
}
