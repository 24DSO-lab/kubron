// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package authorization

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/identity"
)

// Ошибки домена (Contract Freeze §8). Сравнивать только через errors.Is, не по тексту. Тексты ошибок не
// содержат значений полей — только их имена.
var (
	// ErrNotFound — запись отсутствует либо лежит вне видимой области (неотличимо).
	ErrNotFound = errors.New("authorization: not found")
	// ErrConflict — несовпадение revision (CAS), повторное создание либо конфликт идемпотентности.
	ErrConflict = errors.New("authorization: revision conflict")
	// ErrInvalid — некорректный ввод: формат, enum, владелец или область.
	ErrInvalid = errors.New("authorization: invalid")
	// ErrForbidden — actor не вправе выполнить административное действие.
	ErrForbidden = errors.New("authorization: forbidden")
	// ErrRevoked — grant/delegation отозван (терминально) либо principal отозван.
	ErrRevoked = errors.New("authorization: revoked")
	// ErrUnavailable — зависимость недоступна или исход нельзя доказать; fail closed.
	ErrUnavailable = errors.New("authorization: dependency unavailable")
)

// invalid — ErrInvalid с именем поля (без значения).
func invalid(field string) error { return fmt.Errorf("%w: %s", ErrInvalid, field) }

const (
	// Пределы размеров защищают хранилище и evaluator от раздутых/злонамеренных записей.
	maxActions       = 256
	maxScopeEntries  = 1024
	maxEnvironments  = 32
	maxK8sNameLen    = 253
	maxK8sUIDLen     = 128
	maxAPITokenLen   = 253
	constraintsVer   = 1 // версия схемы ConstraintSet, хранится в File backend; неизвестная — отказ
	canonicalTimeFmt = time.RFC3339Nano
)

func validID(s string) bool { return identity.ValidID(s) }

// validSegment — безопасный сегмент пути Kubernetes (namespace, name, uid): непустой, ≤max байт, без '/',
// '%', управляющих символов и пробелов, не "." и не "..".
func validSegment(s string, max int) bool {
	if s == "" || len(s) > max || s == "." || s == ".." {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c == 0x7f || c == '/' || c == '%' || c >= 0x80 {
			return false
		}
	}
	return true
}

// validAPIToken — group/version/resource Kubernetes: [A-Za-z0-9.-], непустой, ≤253.
func validAPIToken(s string) bool {
	if s == "" || len(s) > maxAPITokenLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-':
		default:
			return false
		}
	}
	return true
}

// ---------- цели ----------

// TargetRef — типизированная цель запроса. Не несёт owner/environment: они берутся из доверенного каталога.
//
// Уровни: кластер (только ClusterID); namespace (ClusterID+Namespace); коллекция ресурса
// (Group/Version/Resource без Name); объект (Name+UID обязательны: имя само по себе не идентифицирует объект).
type TargetRef struct {
	ClusterID string
	Group     string
	Version   string
	Resource  string
	Namespace string
	Name      string
	UID       string
}

// NamespaceRef — точная ссылка на namespace кластера.
type NamespaceRef struct {
	ClusterID string
	Namespace string
}

// ObjectRef — точная ссылка на существующий объект; UID обязателен.
type ObjectRef struct {
	ClusterID string
	Group     string
	Version   string
	Resource  string
	Namespace string
	Name      string
	UID       string
}

// Validate проверяет форму цели без fallback'ов.
func (t TargetRef) Validate() error {
	if !validID(t.ClusterID) {
		return invalid("TargetRef.ClusterID")
	}
	if t.Group != "" && !validAPIToken(t.Group) {
		return invalid("TargetRef.Group")
	}
	if t.Version != "" && !validAPIToken(t.Version) {
		return invalid("TargetRef.Version")
	}
	if t.Resource != "" && !validAPIToken(t.Resource) {
		return invalid("TargetRef.Resource")
	}
	if (t.Group != "" || t.Version != "") && t.Resource == "" {
		return invalid("TargetRef.Resource")
	}
	if t.Resource != "" && t.Version == "" {
		return invalid("TargetRef.Version")
	}
	if t.Namespace != "" && !validSegment(t.Namespace, maxK8sNameLen) {
		return invalid("TargetRef.Namespace")
	}
	if t.Name != "" && !validSegment(t.Name, maxK8sNameLen) {
		return invalid("TargetRef.Name")
	}
	if t.UID != "" && !validSegment(t.UID, maxK8sUIDLen) {
		return invalid("TargetRef.UID")
	}
	if t.Name != "" && (t.UID == "" || t.Resource == "") {
		return invalid("TargetRef.UID") // имя без UID — не идентичность объекта
	}
	if t.UID != "" && t.Name == "" {
		return invalid("TargetRef.Name")
	}
	return nil
}

// isObject сообщает, что цель адресует конкретный объект.
func (t TargetRef) isObject() bool { return t.Name != "" && t.UID != "" }

// Validate проверяет ссылку на namespace.
func (n NamespaceRef) Validate() error {
	if !validID(n.ClusterID) {
		return invalid("NamespaceRef.ClusterID")
	}
	if !validSegment(n.Namespace, maxK8sNameLen) {
		return invalid("NamespaceRef.Namespace")
	}
	return nil
}

// Validate проверяет ссылку на объект: все идентифицирующие части обязательны, включая UID.
func (o ObjectRef) Validate() error {
	t := TargetRef(o)
	if t.Resource == "" || t.Version == "" || t.Name == "" || t.UID == "" {
		return invalid("ObjectRef")
	}
	if err := t.Validate(); err != nil {
		return invalid("ObjectRef")
	}
	return nil
}

// TargetScope — набор явных селекторов цели. Селекторы объединяются по union (OR); OrgID — внешняя граница.
// Пустые дочерние списки означают «все потомки» только внутри явно заданного родителя; пустой OrgID либо
// полностью пустой набор селекторов недопустимы («все тенанты» не существует). Скриптов, label-selector'ов
// и Target Groups в Phase 1 нет.
type TargetScope struct {
	OrgID      string
	TeamIDs    []string
	ClusterIDs []string
	Namespaces []NamespaceRef
	Objects    []ObjectRef
}

func (s TargetScope) empty() bool {
	return len(s.TeamIDs) == 0 && len(s.ClusterIDs) == 0 && len(s.Namespaces) == 0 && len(s.Objects) == 0
}

// canonical возвращает копию: списки отсортированы и без дубликатов (общий для хранения и digest порядок).
func (s TargetScope) canonical() TargetScope {
	return TargetScope{
		OrgID:      s.OrgID,
		TeamIDs:    sortedUnique(s.TeamIDs, strings.Compare),
		ClusterIDs: sortedUnique(s.ClusterIDs, strings.Compare),
		Namespaces: sortedUnique(s.Namespaces, func(a, b NamespaceRef) int {
			if c := strings.Compare(a.ClusterID, b.ClusterID); c != 0 {
				return c
			}
			return strings.Compare(a.Namespace, b.Namespace)
		}),
		Objects: sortedUnique(s.Objects, compareObjects),
	}
}

func compareObjects(a, b ObjectRef) int {
	for _, p := range [][2]string{
		{a.ClusterID, b.ClusterID}, {a.Group, b.Group}, {a.Version, b.Version}, {a.Resource, b.Resource},
		{a.Namespace, b.Namespace}, {a.Name, b.Name}, {a.UID, b.UID},
	} {
		if c := strings.Compare(p[0], p[1]); c != 0 {
			return c
		}
	}
	return 0
}

// sortedUnique — отсортированная копия без дубликатов; nil для пустого входа (единая каноническая форма).
func sortedUnique[T comparable](in []T, cmp func(a, b T) int) []T {
	if len(in) == 0 {
		return nil
	}
	out := slices.Clone(in)
	slices.SortFunc(out, cmp)
	return slices.Compact(out)
}

// Validate проверяет область (на любой, не только канонической форме).
func (s TargetScope) Validate() error {
	if !validID(s.OrgID) {
		return invalid("TargetScope.OrgID")
	}
	if s.empty() {
		return invalid("TargetScope.selectors") // OrgID сам по себе — не wildcard
	}
	if len(s.TeamIDs) > maxScopeEntries || len(s.ClusterIDs) > maxScopeEntries ||
		len(s.Namespaces) > maxScopeEntries || len(s.Objects) > maxScopeEntries {
		return invalid("TargetScope.size")
	}
	for _, id := range s.TeamIDs {
		if !validID(id) {
			return invalid("TargetScope.TeamIDs")
		}
	}
	for _, id := range s.ClusterIDs {
		if !validID(id) {
			return invalid("TargetScope.ClusterIDs")
		}
	}
	for _, n := range s.Namespaces {
		if err := n.Validate(); err != nil {
			return err
		}
	}
	for _, o := range s.Objects {
		if err := o.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// covers сообщает, что цель t (с доверенными org/team владельца) входит в область. Использует только
// явные селекторы; безопасна на любых (в т.ч. повреждённых) данных: не паникует и не расширяется.
func (s TargetScope) covers(orgID, teamID string, t TargetRef) bool {
	if s.OrgID == "" || s.OrgID != orgID {
		return false
	}
	if teamID != "" && slices.Contains(s.TeamIDs, teamID) {
		return true
	}
	if t.ClusterID != "" && slices.Contains(s.ClusterIDs, t.ClusterID) {
		return true
	}
	if t.ClusterID != "" && t.Namespace != "" {
		for _, n := range s.Namespaces {
			if n.ClusterID == t.ClusterID && n.Namespace == t.Namespace {
				return true
			}
		}
	}
	if t.isObject() {
		for _, o := range s.Objects {
			if o == ObjectRef(t) {
				return true
			}
		}
	}
	return false
}

// clusterIDs возвращает все кластеры, упомянутые селекторами (для проверки владения при мутации).
func (s TargetScope) clusterIDs() []string {
	var ids []string
	ids = append(ids, s.ClusterIDs...)
	for _, n := range s.Namespaces {
		ids = append(ids, n.ClusterID)
	}
	for _, o := range s.Objects {
		ids = append(ids, o.ClusterID)
	}
	return sortedUnique(ids, strings.Compare)
}

// ---------- действия ----------

// OperationAction — ТОЧНАЯ пара OperationID+ActionID. Отдельные списки операций и действий не существуют:
// иначе возникло бы непреднамеренное декартово произведение.
type OperationAction struct {
	OperationID string
	ActionID    string
}

// Validate проверяет пару.
func (a OperationAction) Validate() error {
	if !validID(a.OperationID) {
		return invalid("OperationAction.OperationID")
	}
	if !validID(a.ActionID) {
		return invalid("OperationAction.ActionID")
	}
	return nil
}

func canonicalActions(in []OperationAction) []OperationAction {
	return sortedUnique(in, func(a, b OperationAction) int {
		if c := strings.Compare(a.OperationID, b.OperationID); c != 0 {
			return c
		}
		return strings.Compare(a.ActionID, b.ActionID)
	})
}

func validateActions(in []OperationAction) error {
	if len(in) == 0 || len(in) > maxActions {
		return invalid("Actions")
	}
	for _, a := range in {
		if err := a.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func hasAction(in []OperationAction, op, act string) bool {
	return slices.Contains(in, OperationAction{OperationID: op, ActionID: act})
}

// ---------- constraints ----------

// ConstraintSet — типизированные constraints Phase 1. Внутри одного grant/delegation объединяются по AND.
// Поддерживаются только: AllowedEnvironments (сверяется с доверенными метаданными кластера),
// DenyDestructive (сверяется с доверенным каталогом действий), NotBefore (включительно, UTC).
// MaxConcurrency/MaxBudget и прочие execution-time constraints отложены: их нельзя принять молча.
//
// Версия схемы (constraintsVer) хранится рядом с записями File backend; запись неизвестной версии/вида
// не читается (fail closed). Поле версии намеренно не добавлено в замороженный struct.
type ConstraintSet struct {
	AllowedEnvironments []string
	DenyDestructive     bool
	NotBefore           *time.Time
}

func (c ConstraintSet) canonical() ConstraintSet {
	out := ConstraintSet{
		AllowedEnvironments: sortedUnique(c.AllowedEnvironments, strings.Compare),
		DenyDestructive:     c.DenyDestructive,
	}
	if c.NotBefore != nil {
		t := canon(*c.NotBefore)
		out.NotBefore = &t
	}
	return out
}

// Validate проверяет, что constraints поддерживаются и вычислимы.
func (c ConstraintSet) Validate() error {
	if len(c.AllowedEnvironments) > maxEnvironments {
		return invalid("Constraints.AllowedEnvironments")
	}
	for _, e := range c.AllowedEnvironments {
		if !validID(e) {
			return invalid("Constraints.AllowedEnvironments")
		}
	}
	if c.NotBefore != nil && c.NotBefore.IsZero() {
		return invalid("Constraints.NotBefore")
	}
	return nil
}

func sameConstraints(a, b ConstraintSet) bool {
	a, b = a.canonical(), b.canonical()
	if a.DenyDestructive != b.DenyDestructive || !slices.Equal(a.AllowedEnvironments, b.AllowedEnvironments) {
		return false
	}
	if (a.NotBefore == nil) != (b.NotBefore == nil) {
		return false
	}
	return a.NotBefore == nil || a.NotBefore.Equal(*b.NotBefore)
}

// evalFacts — доверенные факты одной проверки.
type evalFacts struct {
	now         time.Time
	environment string
	destructive bool
}

// satisfied вычисляет все constraints (AND). Пустое/неизвестное окружение не удовлетворяет непустому
// AllowedEnvironments.
func (c ConstraintSet) satisfied(f evalFacts) bool {
	if len(c.AllowedEnvironments) > 0 && (f.environment == "" || !slices.Contains(c.AllowedEnvironments, f.environment)) {
		return false
	}
	if c.DenyDestructive && f.destructive {
		return false
	}
	if c.NotBefore != nil && f.now.Before(*c.NotBefore) {
		return false
	}
	return true
}

// ---------- Grant ----------

// GrantState — состояние grant.
type GrantState string

const (
	GrantActive  GrantState = "active"
	GrantRevoked GrantState = "revoked"
)

// Grant — явное разрешение техническому principal'у на точные пары действий над целями. Не наследуется от
// создателя/администратора/команды. Явных Deny нет.
type Grant struct {
	ID               string
	Principal        identity.PrincipalRef
	Actions          []OperationAction
	Targets          TargetScope
	Constraints      ConstraintSet
	State            GrantState
	Revision         uint64
	ExpiresAt        time.Time // исключительно: при now >= ExpiresAt grant не действует
	CreatedBy        identity.ActorRef
	LastMutationID   string
	LastAuditEventID string
}

// canon — каноническая форма метки времени: UTC без monotonic-части.
func canon(t time.Time) time.Time { return t.UTC().Round(0) }

// Canonical возвращает глубокую каноническую копию (списки отсортированы, без дубликатов, время UTC).
// Результат не разделяет память с исходным значением.
func (g Grant) Canonical() Grant {
	g.Actions = canonicalActions(g.Actions)
	g.Targets = g.Targets.canonical()
	g.Constraints = g.Constraints.canonical()
	if !g.ExpiresAt.IsZero() {
		g.ExpiresAt = canon(g.ExpiresAt)
	}
	return g
}

// validate проверяет структурные инварианты. stored=false — запись на создание (Revision 0, active).
func (g Grant) validate(stored bool) error {
	if !validID(g.ID) {
		return invalid("Grant.ID")
	}
	if g.Principal.Validate() != nil {
		return invalid("Grant.Principal")
	}
	if !g.Principal.IsTechnical() {
		return invalid("Grant.Principal.Kind") // UserPrincipal не получает technical grants
	}
	if err := validateActions(g.Actions); err != nil {
		return err
	}
	if err := g.Targets.Validate(); err != nil {
		return err
	}
	if g.Targets.OrgID != g.Principal.OrgID {
		return invalid("Grant.Targets.OrgID")
	}
	if g.Principal.Kind == identity.PrincipalTeam {
		// TeamPrincipal ограничен своей Team: чужая Team в селекторе — ошибка даже при «лишнем» совпадении.
		for _, t := range g.Targets.TeamIDs {
			if t != g.Principal.TeamID {
				return invalid("Grant.Targets.TeamIDs")
			}
		}
	}
	if err := g.Constraints.Validate(); err != nil {
		return err
	}
	if g.ExpiresAt.IsZero() {
		return invalid("Grant.ExpiresAt")
	}
	if g.Constraints.NotBefore != nil && !g.Constraints.NotBefore.Before(g.ExpiresAt) {
		return invalid("Grant.Constraints.NotBefore")
	}
	if g.CreatedBy.Validate() != nil {
		return invalid("Grant.CreatedBy")
	}
	if !validID(g.LastMutationID) {
		return invalid("Grant.LastMutationID")
	}
	if !validID(g.LastAuditEventID) {
		return invalid("Grant.LastAuditEventID")
	}
	switch g.State {
	case GrantActive:
	case GrantRevoked:
		if !stored {
			return invalid("Grant.State")
		}
	default:
		return invalid("Grant.State")
	}
	if stored {
		if g.Revision == 0 {
			return invalid("Grant.Revision")
		}
	} else if g.Revision != 0 {
		return invalid("Grant.Revision")
	}
	return nil
}

// Validate проверяет сохранённый grant (Revision ≥ 1).
func (g Grant) Validate() error { return g.validate(true) }

func sameGrantContent(a, b Grant) bool {
	a, b = a.Canonical(), b.Canonical()
	return a.Principal == b.Principal && slices.Equal(a.Actions, b.Actions) && sameScope(a.Targets, b.Targets) &&
		sameConstraints(a.Constraints, b.Constraints) && a.ExpiresAt.Equal(b.ExpiresAt)
}

func sameScope(a, b TargetScope) bool {
	a, b = a.canonical(), b.canonical()
	return a.OrgID == b.OrgID && slices.Equal(a.TeamIDs, b.TeamIDs) && slices.Equal(a.ClusterIDs, b.ClusterIDs) &&
		slices.Equal(a.Namespaces, b.Namespaces) && slices.Equal(a.Objects, b.Objects)
}

// ---------- Delegation ----------

// DelegationState — состояние delegation.
type DelegationState string

const (
	DelegationActive    DelegationState = "active"
	DelegationSuspended DelegationState = "suspended"
	DelegationRevoked   DelegationState = "revoked"
)

// Delegation — явное согласие Team-владельца целей на работу конкретного ServicePrincipal той же
// Organization. Выдаётся только Team (Organization Admin за Team её не выдаёт). Сужает, а не расширяет:
// без grant сама по себе ничего не разрешает.
type Delegation struct {
	ID               string
	OrgID            string
	OwnerTeamID      string
	ServicePrincipal identity.PrincipalRef
	Actions          []OperationAction
	Targets          TargetScope
	Constraints      ConstraintSet
	State            DelegationState
	Revision         uint64
	ExpiresAt        time.Time // исключительно
	CreatedBy        identity.ActorRef
	LastMutationID   string
	LastAuditEventID string
}

// Canonical — глубокая каноническая копия (см. Grant.Canonical).
func (d Delegation) Canonical() Delegation {
	d.Actions = canonicalActions(d.Actions)
	d.Targets = d.Targets.canonical()
	d.Constraints = d.Constraints.canonical()
	if !d.ExpiresAt.IsZero() {
		d.ExpiresAt = canon(d.ExpiresAt)
	}
	return d
}

func (d Delegation) validate(stored bool) error {
	if !validID(d.ID) {
		return invalid("Delegation.ID")
	}
	if !validID(d.OrgID) {
		return invalid("Delegation.OrgID")
	}
	if !validID(d.OwnerTeamID) {
		return invalid("Delegation.OwnerTeamID")
	}
	if d.ServicePrincipal.Validate() != nil {
		return invalid("Delegation.ServicePrincipal")
	}
	if d.ServicePrincipal.Kind != identity.PrincipalService || d.ServicePrincipal.OrgID != d.OrgID {
		// Только ServicePrincipal той же Organization; TeamPrincipal delegation получить не может.
		return invalid("Delegation.ServicePrincipal")
	}
	if err := validateActions(d.Actions); err != nil {
		return err
	}
	if err := d.Targets.Validate(); err != nil {
		return err
	}
	if d.Targets.OrgID != d.OrgID {
		return invalid("Delegation.Targets.OrgID")
	}
	for _, t := range d.Targets.TeamIDs { // область ограничена Team-владельцем
		if t != d.OwnerTeamID {
			return invalid("Delegation.Targets.TeamIDs")
		}
	}
	if err := d.Constraints.Validate(); err != nil {
		return err
	}
	if d.ExpiresAt.IsZero() {
		return invalid("Delegation.ExpiresAt")
	}
	if d.Constraints.NotBefore != nil && !d.Constraints.NotBefore.Before(d.ExpiresAt) {
		return invalid("Delegation.Constraints.NotBefore")
	}
	if d.CreatedBy.Validate() != nil {
		return invalid("Delegation.CreatedBy")
	}
	if !validID(d.LastMutationID) {
		return invalid("Delegation.LastMutationID")
	}
	if !validID(d.LastAuditEventID) {
		return invalid("Delegation.LastAuditEventID")
	}
	switch d.State {
	case DelegationActive:
	case DelegationSuspended, DelegationRevoked:
		if !stored {
			return invalid("Delegation.State")
		}
	default:
		return invalid("Delegation.State")
	}
	if stored {
		if d.Revision == 0 {
			return invalid("Delegation.Revision")
		}
	} else if d.Revision != 0 {
		return invalid("Delegation.Revision")
	}
	return nil
}

// Validate проверяет сохранённую delegation.
func (d Delegation) Validate() error { return d.validate(true) }

func sameDelegationContent(a, b Delegation) bool {
	a, b = a.Canonical(), b.Canonical()
	return a.OrgID == b.OrgID && a.OwnerTeamID == b.OwnerTeamID && a.ServicePrincipal == b.ServicePrincipal &&
		slices.Equal(a.Actions, b.Actions) && sameScope(a.Targets, b.Targets) &&
		sameConstraints(a.Constraints, b.Constraints) && a.ExpiresAt.Equal(b.ExpiresAt) && a.State == b.State
}

// validDelegationTransition: active ↔ suspended; active|suspended → revoked; revoked терминален.
func validDelegationTransition(from, to DelegationState) error {
	known := func(s DelegationState) bool {
		return s == DelegationActive || s == DelegationSuspended || s == DelegationRevoked
	}
	if !known(from) || !known(to) {
		return invalid("DelegationState")
	}
	if from == DelegationRevoked {
		return fmt.Errorf("%w: %s", ErrRevoked, "delegation")
	}
	return nil
}
