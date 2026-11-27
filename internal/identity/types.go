// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package identity — канонические типы и жизненный цикл технических principals
// (TeamPrincipal, ServicePrincipal) и их привязок к кластерам (Phase 1, Lane A, ADR-0035).
//
// Пакет владеет PrincipalKind/PrincipalRef/ActorRef/OwnerScope/Identity/CredentialBinding (Phase 1 Contract
// Freeze §2.1, §3, §4). Он НЕ содержит секретов, credential-логики, grants/delegation и Kubernetes-клиента:
// identity и binding сами по себе ничего не разрешают. Использовать identity/binding можно только когда
// критичный аудит их последней мутации подтверждён как succeeded (см. Service.Resolve/ResolveBinding).
//
// Зависимости: пакет импортирует только internal/audit (интерфейс и типы CriticalRecorder), но не его
// реализации; credential/authorization/k8s/pgstore/app его импортируют, не наоборот.
package identity

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Ошибки домена (Contract Freeze §4, §8). Сравнивать только через errors.Is, не по тексту.
// Тексты не содержат значений полей — только их имена.
var (
	// ErrNotFound — запись отсутствует либо лежит вне видимой области (неотличимо).
	ErrNotFound = errors.New("identity: not found")
	// ErrConflict — несовпадение revision (CAS), повторное создание либо конфликт идемпотентности.
	ErrConflict = errors.New("identity: revision conflict")
	// ErrInvalid — некорректная ссылка/поле, несовпадение владельца, недопустимый переход состояния.
	ErrInvalid = errors.New("identity: invalid")
	// ErrForbidden — у actor нет административных полномочий либо principal/binding не может использоваться.
	ErrForbidden = errors.New("identity: forbidden")
	// ErrRevoked — терминальный (отозванный) principal или binding.
	ErrRevoked = errors.New("identity: revoked")
	// ErrUnavailable — зависимость (хранилище, авторизатор, каталог, аудит) недоступна; fail closed.
	ErrUnavailable = errors.New("identity: dependency unavailable")
)

// invalid — ErrInvalid с именем поля (без значения).
func invalid(field string) error { return fmt.Errorf("%w: %s", ErrInvalid, field) }

const (
	maxIDLen          = 128 // совпадает с пределом audit.CriticalIntent
	maxDisplayNameLen = 128 // в рунах
)

// validID — единый валидатор непрозрачных идентификаторов: непустой, ≤128 байт, первый символ буква/цифра,
// далее [A-Za-z0-9._:@+=-]. Алфавит — строгое подмножество алфавита критичного аудита (без '/'), поэтому
// любой валидный ID можно положить в CriticalIntent, и он безопасен как часть имени файла/ключа.
// Отображаемое имя ID не является.
func validID(s string) bool {
	if s == "" || len(s) > maxIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case i > 0 && strings.IndexByte("._:@+=-", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// ValidID сообщает, что s допустим как ID (principal, org, team, cluster, binding, mutation, audit event).
func ValidID(s string) bool { return validID(s) }

// PrincipalKind — вид principal.
type PrincipalKind string

const (
	PrincipalUser    PrincipalKind = "user"
	PrincipalTeam    PrincipalKind = "team"
	PrincipalService PrincipalKind = "service"
)

// PrincipalRef — ссылка на principal. Не является credential, правом или доказательством доступа.
//
// Team: ID — ID технической identity, TeamID — ID команды-владельца (разные значения); OrgID, TeamID обязательны.
// Service: ID и OrgID обязательны, TeamID пуст.
// User: ID обязателен (существующий пользователь Kubron); OrgID может быть пуст (personal Desktop); TeamID пуст.
type PrincipalRef struct {
	Kind   PrincipalKind
	ID     string
	OrgID  string
	TeamID string
}

// Validate проверяет допустимую комбинацию Kind/ID/OrgID/TeamID без fallback'ов.
func (r PrincipalRef) Validate() error {
	if !validID(r.ID) {
		return invalid("PrincipalRef.ID")
	}
	switch r.Kind {
	case PrincipalUser:
		if r.OrgID != "" && !validID(r.OrgID) {
			return invalid("PrincipalRef.OrgID")
		}
		if r.TeamID != "" {
			return invalid("PrincipalRef.TeamID")
		}
	case PrincipalTeam:
		if !validID(r.OrgID) {
			return invalid("PrincipalRef.OrgID")
		}
		if !validID(r.TeamID) {
			return invalid("PrincipalRef.TeamID")
		}
	case PrincipalService:
		if !validID(r.OrgID) {
			return invalid("PrincipalRef.OrgID")
		}
		if r.TeamID != "" {
			return invalid("PrincipalRef.TeamID")
		}
	default:
		return invalid("PrincipalRef.Kind")
	}
	return nil
}

// IsTechnical сообщает, что principal — Team или Service (то, что хранит этот пакет).
func (r PrincipalRef) IsTechnical() bool {
	return r.Kind == PrincipalTeam || r.Kind == PrincipalService
}

// validateTechnical — Validate + запрет UserPrincipal (он не хранится как техническая identity).
func (r PrincipalRef) validateTechnical() error {
	if err := r.Validate(); err != nil {
		return err
	}
	if !r.IsTechnical() {
		return invalid("PrincipalRef.Kind")
	}
	return nil
}

// Owner возвращает владельца, который следует из самой ссылки: Team → команда, Service → организация.
// Для User возвращает ErrInvalid. Это единственное место, где владелец выводится из ссылки; хранимый Owner
// обязан с ней совпадать.
func (r PrincipalRef) Owner() (OwnerScope, error) {
	if err := r.validateTechnical(); err != nil {
		return OwnerScope{}, err
	}
	if r.Kind == PrincipalTeam {
		return OwnerScope{Kind: OwnerTeam, OrgID: r.OrgID, TeamID: r.TeamID}, nil
	}
	return OwnerScope{Kind: OwnerOrganization, OrgID: r.OrgID}, nil
}

// ActorKind — вид инициатора действия.
type ActorKind string

const (
	ActorUser      ActorKind = "user"
	ActorSystem    ActorKind = "system"
	ActorScheduler ActorKind = "scheduler"
)

// ActorRef — кто выполняет действие. Для административных мутаций identity допустим только ActorUser.
type ActorRef struct {
	Kind ActorKind
	ID   string
}

// Validate проверяет вид и ID.
func (a ActorRef) Validate() error {
	switch a.Kind {
	case ActorUser, ActorSystem, ActorScheduler:
	default:
		return invalid("ActorRef.Kind")
	}
	if !validID(a.ID) {
		return invalid("ActorRef.ID")
	}
	return nil
}

// OwnerKind — вид владельца identity.
type OwnerKind string

const (
	OwnerOrganization OwnerKind = "organization"
	OwnerTeam         OwnerKind = "team"
)

// OwnerScope — владелец identity. Не заменяет tenant.Scope (тот требует кластер).
// Organization: OrgID обязателен, TeamID пуст. Team: OrgID и TeamID обязательны.
type OwnerScope struct {
	Kind   OwnerKind
	OrgID  string
	TeamID string
}

// Validate проверяет допустимую комбинацию.
func (o OwnerScope) Validate() error {
	if !validID(o.OrgID) {
		return invalid("OwnerScope.OrgID")
	}
	switch o.Kind {
	case OwnerOrganization:
		if o.TeamID != "" {
			return invalid("OwnerScope.TeamID")
		}
	case OwnerTeam:
		if !validID(o.TeamID) {
			return invalid("OwnerScope.TeamID")
		}
	default:
		return invalid("OwnerScope.Kind")
	}
	return nil
}

// IdentityState — состояние жизненного цикла технической identity.
type IdentityState string

const (
	IdentityActive    IdentityState = "active"
	IdentitySuspended IdentityState = "suspended"
	IdentityRevoked   IdentityState = "revoked"
)

// Identity — техническая identity (Team или Service). Секретов не содержит.
type Identity struct {
	Ref              PrincipalRef
	Owner            OwnerScope
	DisplayName      string
	State            IdentityState
	Revision         uint64
	CreatedBy        ActorRef // только provenance: не владелец, не authority, не grant
	CreatedAt        time.Time
	UpdatedAt        time.Time
	LastMutationID   string
	LastAuditEventID string
}

// BindingState — состояние привязки principal к кластеру.
type BindingState string

const (
	// BindingPending резервирует кортеж principal+cluster, но не может использоваться.
	BindingPending BindingState = "pending"
	BindingActive  BindingState = "active"
	BindingRevoked BindingState = "revoked"
)

// CredentialBinding — явное назначение одного principal одному кластеру. Секретов не содержит и сам по себе
// не даёт ни Kubron grant, ни доступа.
type CredentialBinding struct {
	ID               string
	Principal        PrincipalRef
	ClusterID        string
	State            BindingState
	Revision         uint64
	CreatedBy        ActorRef
	CreatedAt        time.Time
	UpdatedAt        time.Time
	LastMutationID   string
	LastAuditEventID string
}

// validDisplayName: 1..128 рун, валидный UTF-8, без управляющих символов и краевых пробелов.
func validDisplayName(s string) bool {
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxDisplayNameLen || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// validate проверяет структурные инварианты identity. stored=false — запись на создание (Revision 0,
// метки времени и LastAuditEventID заполняет вызывающий, состояние — active); stored=true — сохранённая запись.
func (i Identity) validate(stored bool) error {
	if err := i.Ref.validateTechnical(); err != nil {
		return err
	}
	if err := i.Owner.Validate(); err != nil {
		return err
	}
	want, err := i.Ref.Owner()
	if err != nil {
		return err
	}
	if i.Owner != want {
		// Team identity — ровно одна Team, Service identity — ровно одна Organization; владелец следует из ссылки.
		return invalid("Identity.Owner")
	}
	if !validDisplayName(i.DisplayName) {
		return invalid("Identity.DisplayName")
	}
	switch i.State {
	case IdentityActive:
	case IdentitySuspended, IdentityRevoked:
		if !stored {
			return invalid("Identity.State")
		}
	default:
		return invalid("Identity.State")
	}
	if err := i.CreatedBy.Validate(); err != nil {
		return err
	}
	if !validID(i.LastMutationID) {
		return invalid("Identity.LastMutationID")
	}
	if !validID(i.LastAuditEventID) {
		return invalid("Identity.LastAuditEventID")
	}
	if stored {
		if i.Revision == 0 {
			return invalid("Identity.Revision")
		}
		if i.CreatedAt.IsZero() || i.UpdatedAt.IsZero() || i.UpdatedAt.Before(i.CreatedAt) {
			return invalid("Identity.Timestamps")
		}
	} else if i.Revision != 0 {
		return invalid("Identity.Revision")
	}
	return nil
}

// Validate проверяет сохранённую identity (Revision ≥ 1, метки времени, последние mutation/audit ID).
func (i Identity) Validate() error { return i.validate(true) }

// validate — см. Identity.validate. Для новой привязки: State=pending, Revision=0.
func (b CredentialBinding) validate(stored bool) error {
	if !validID(b.ID) {
		return invalid("CredentialBinding.ID")
	}
	if err := b.Principal.validateTechnical(); err != nil {
		return err
	}
	if !validID(b.ClusterID) {
		return invalid("CredentialBinding.ClusterID")
	}
	switch b.State {
	case BindingPending:
	case BindingActive, BindingRevoked:
		if !stored {
			return invalid("CredentialBinding.State")
		}
	default:
		return invalid("CredentialBinding.State")
	}
	if err := b.CreatedBy.Validate(); err != nil {
		return err
	}
	if stored {
		if !validID(b.LastMutationID) {
			return invalid("CredentialBinding.LastMutationID")
		}
		if !validID(b.LastAuditEventID) {
			return invalid("CredentialBinding.LastAuditEventID")
		}
		if b.Revision == 0 {
			return invalid("CredentialBinding.Revision")
		}
		if b.CreatedAt.IsZero() || b.UpdatedAt.IsZero() || b.UpdatedAt.Before(b.CreatedAt) {
			return invalid("CredentialBinding.Timestamps")
		}
	} else if b.Revision != 0 {
		return invalid("CredentialBinding.Revision")
	}
	return nil
}

// Validate проверяет сохранённую привязку.
func (b CredentialBinding) Validate() error { return b.validate(true) }

// ValidateIdentityTransition проверяет переход состояния identity: active ↔ suspended; active|suspended →
// revoked; revoked терминален (повторное включение — только новый principal ID).
// Возвращает ErrRevoked из revoked, ErrInvalid для неизвестных состояний и переходов «в себя».
func ValidateIdentityTransition(from, to IdentityState) error {
	if !knownIdentityState(from) || !knownIdentityState(to) {
		return invalid("IdentityState")
	}
	if from == IdentityRevoked {
		return fmt.Errorf("%w: %s", ErrRevoked, "identity")
	}
	if from == to {
		return invalid("IdentityState transition")
	}
	return nil // active→suspended, suspended→active, active|suspended→revoked
}

// ValidateBindingTransition проверяет переход состояния привязки: pending → active; pending|active → revoked;
// revoked терминален; назад (active → pending) нельзя.
func ValidateBindingTransition(from, to BindingState) error {
	if !knownBindingState(from) || !knownBindingState(to) {
		return invalid("BindingState")
	}
	if from == BindingRevoked {
		return fmt.Errorf("%w: %s", ErrRevoked, "binding")
	}
	switch {
	case from == BindingPending && (to == BindingActive || to == BindingRevoked):
		return nil
	case from == BindingActive && to == BindingRevoked:
		return nil
	}
	return invalid("BindingState transition")
}

func knownIdentityState(s IdentityState) bool {
	return s == IdentityActive || s == IdentitySuspended || s == IdentityRevoked
}

func knownBindingState(s BindingState) bool {
	return s == BindingPending || s == BindingActive || s == BindingRevoked
}

// canon — каноническая форма метки времени для хранения и сравнения: UTC без monotonic-части.
func canon(t time.Time) time.Time { return t.UTC().Round(0) }
