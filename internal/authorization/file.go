// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package authorization

// File backend (Desktop, один процесс). Всё состояние — один JSON-файл authorization.json.
//
// Гарантии (Contract Freeze §8) те же, что у File backend identity:
//   - read-modify-write сериализован мьютексом одного процесса; многопроцессный доступ не заявляется;
//   - запись — атомарная замена: временный файл (0600, O_EXCL) → полная запись с проверкой длины → fsync →
//     rename → fsync каталога; на диске всегда либо прежний, либо новый файл целиком;
//   - память меняется только после подтверждённой записи; если исход неясен (сбой fsync каталога после
//     rename), backend «отравляется»: всё отвечает ErrUnavailable до переоткрытия (fail closed);
//   - файл читается строго (неизвестные поля, хвост, версия схемы constraints, инварианты). Повреждённый
//     файл не «лечится» и не отбрасывается молча: OpenFile возвращает ErrUnavailable. Запись с неизвестным
//     constraint-полем/версией — тоже повреждение: constraint нельзя пропустить.
//
// Пакет не использует часы: у grants/delegation нет меток времени; истечение вычисляет Evaluator по Clock.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/24DSO-lab/kubron/internal/identity"
)

const (
	authFileName     = "authorization.json"
	tempPrefix       = ".authorization-"
	tempSuffix       = ".tmp"
	fileVersion      = 1
	maxStateFileSize = 64 << 20
)

// FileOption настраивает FileBackend.
type FileOption func(*FileBackend)

// withFS подменяет файловые операции (внутренние тесты: сбои записи/sync).
func withFS(fs stateFS) FileOption { return func(b *FileBackend) { b.fs = fs } }

// FileBackend — реализация GrantBackend и DelegationBackend на файле. Безопасна для параллельных вызовов в
// одном процессе.
type FileBackend struct {
	mu     sync.RWMutex
	dir    string
	path   string
	fs     stateFS
	st     *snapshot
	poison error
}

var (
	_ GrantBackend      = (*FileBackend)(nil)
	_ DelegationBackend = (*FileBackend)(nil)
)

// OpenFile открывает (или создаёт пустое) хранилище в каталоге dir.
func OpenFile(dir string, opts ...FileOption) (*FileBackend, error) {
	b := &FileBackend{dir: dir, path: filepath.Join(dir, authFileName), fs: osFS{}}
	for _, o := range opts {
		o(b)
	}
	if err := b.fs.MkdirAll(dir); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, "create state directory")
	}
	b.removeStaleTemps()
	st, err := b.load()
	if err != nil {
		return nil, err
	}
	b.st = st
	return b, nil
}

func (b *FileBackend) removeStaleTemps() {
	names, err := b.fs.ReadDir(b.dir)
	if err != nil {
		return
	}
	for _, n := range names {
		if strings.HasPrefix(n, tempPrefix) && strings.HasSuffix(n, tempSuffix) {
			_ = b.fs.Remove(filepath.Join(b.dir, n))
		}
	}
}

func (b *FileBackend) load() (*snapshot, error) {
	data, err := b.fs.ReadFile(b.path, maxStateFileSize)
	if errors.Is(err, os.ErrNotExist) {
		return newSnapshot(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, "read state file")
	}
	return decodeSnapshot(data)
}

// ---------- снимок состояния ----------

type snapshot struct {
	grants      map[string]Grant
	delegations map[string]Delegation
}

func newSnapshot() *snapshot {
	return &snapshot{grants: map[string]Grant{}, delegations: map[string]Delegation{}}
}

func (s *snapshot) clone() *snapshot {
	c := &snapshot{
		grants:      make(map[string]Grant, len(s.grants)),
		delegations: make(map[string]Delegation, len(s.delegations)),
	}
	for k, v := range s.grants {
		c.grants[k] = v // значения в снимке канонические и никогда не изменяются на месте
	}
	for k, v := range s.delegations {
		c.delegations[k] = v
	}
	return c
}

func conflict(why string) error { return fmt.Errorf("%w: %s", ErrConflict, why) }

// ---- grants ----

func (s *snapshot) createGrant(in Grant) (Grant, bool, error) {
	in = in.Canonical()
	if err := in.validate(false); err != nil {
		return Grant{}, false, err
	}
	if cur, ok := s.grants[in.ID]; ok {
		if cur.Revision == 1 && cur.LastMutationID == in.LastMutationID && cur.CreatedBy == in.CreatedBy &&
			sameGrantContent(cur, in) {
			return cur.Canonical(), false, nil // идемпотентный повтор
		}
		return Grant{}, false, conflict("grant exists")
	}
	in.State = GrantActive
	in.Revision = 1
	s.grants[in.ID] = in
	return in.Canonical(), true, nil
}

func (s *snapshot) grant(id string) (Grant, error) {
	if !validID(id) {
		return Grant{}, invalid("Grant.ID")
	}
	cur, ok := s.grants[id]
	if !ok {
		return Grant{}, ErrNotFound
	}
	return cur.Canonical(), nil
}

func (s *snapshot) listGrants(p identity.PrincipalRef) ([]Grant, error) {
	if p.Validate() != nil {
		return nil, invalid("PrincipalRef")
	}
	out := []Grant{}
	for _, g := range s.grants {
		if g.Principal == p {
			out = append(out, g.Canonical())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *snapshot) updateGrant(in Grant, expected uint64) (Grant, bool, error) {
	if expected == 0 || (in.Revision != 0 && in.Revision != expected) {
		return Grant{}, false, invalid("Grant.Revision")
	}
	in.Revision = 0
	in = in.Canonical()
	if in.State == "" {
		in.State = GrantActive
	}
	if err := in.validate(false); err != nil {
		return Grant{}, false, err
	}
	cur, ok := s.grants[in.ID]
	if !ok {
		return Grant{}, false, ErrNotFound
	}
	if cur.Principal != in.Principal || cur.CreatedBy != in.CreatedBy {
		return Grant{}, false, invalid("Grant immutable field")
	}
	if cur.LastMutationID == in.LastMutationID {
		if cur.Revision == expected+1 && cur.State == GrantActive && sameGrantContent(cur, in) {
			return cur.Canonical(), false, nil
		}
		return Grant{}, false, conflict("mutation id reused")
	}
	if cur.Revision != expected {
		return Grant{}, false, conflict("stale revision")
	}
	if cur.State == GrantRevoked {
		return Grant{}, false, fmt.Errorf("%w: %s", ErrRevoked, "grant")
	}
	in.Revision = cur.Revision + 1
	s.grants[in.ID] = in
	return in.Canonical(), true, nil
}

func (s *snapshot) revokeGrant(id string, expected uint64, mutationID, eventID string) (Grant, bool, error) {
	if expected == 0 || !validID(id) || !validID(mutationID) || !validID(eventID) {
		return Grant{}, false, invalid("RevokeGrant arguments")
	}
	cur, ok := s.grants[id]
	if !ok {
		return Grant{}, false, ErrNotFound
	}
	if cur.LastMutationID == mutationID {
		if cur.State == GrantRevoked && cur.Revision == expected+1 {
			return cur.Canonical(), false, nil
		}
		return Grant{}, false, conflict("mutation id reused")
	}
	if cur.Revision != expected {
		return Grant{}, false, conflict("stale revision")
	}
	if cur.State == GrantRevoked {
		return Grant{}, false, fmt.Errorf("%w: %s", ErrRevoked, "grant")
	}
	cur.State = GrantRevoked
	cur.Revision++
	cur.LastMutationID, cur.LastAuditEventID = mutationID, eventID
	s.grants[id] = cur
	return cur.Canonical(), true, nil
}

// ---- delegations ----

func (s *snapshot) createDelegation(in Delegation) (Delegation, bool, error) {
	in = in.Canonical()
	if err := in.validate(false); err != nil {
		return Delegation{}, false, err
	}
	if cur, ok := s.delegations[in.ID]; ok {
		if cur.Revision == 1 && cur.LastMutationID == in.LastMutationID && cur.CreatedBy == in.CreatedBy &&
			sameDelegationContent(cur, in) {
			return cur.Canonical(), false, nil
		}
		return Delegation{}, false, conflict("delegation exists")
	}
	in.State = DelegationActive
	in.Revision = 1
	s.delegations[in.ID] = in
	return in.Canonical(), true, nil
}

func (s *snapshot) delegation(id string) (Delegation, error) {
	if !validID(id) {
		return Delegation{}, invalid("Delegation.ID")
	}
	cur, ok := s.delegations[id]
	if !ok {
		return Delegation{}, ErrNotFound
	}
	return cur.Canonical(), nil
}

func (s *snapshot) listDelegations(service identity.PrincipalRef, ownerTeamID string) ([]Delegation, error) {
	if service.Validate() != nil {
		return nil, invalid("PrincipalRef")
	}
	if service.Kind != identity.PrincipalService || !validID(ownerTeamID) {
		return nil, invalid("ListDelegations arguments")
	}
	out := []Delegation{}
	for _, d := range s.delegations {
		if d.ServicePrincipal == service && d.OwnerTeamID == ownerTeamID {
			out = append(out, d.Canonical())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *snapshot) updateDelegation(in Delegation, expected uint64) (Delegation, bool, error) {
	if expected == 0 || (in.Revision != 0 && in.Revision != expected) {
		return Delegation{}, false, invalid("Delegation.Revision")
	}
	if in.State != DelegationActive && in.State != DelegationSuspended {
		return Delegation{}, false, invalid("Delegation.State") // отзыв — только RevokeDelegation
	}
	next := in.State
	in.Revision = 0
	in.State = DelegationActive // форма «на создание»; итоговое состояние задаёт next
	in = in.Canonical()
	if err := in.validate(false); err != nil {
		return Delegation{}, false, err
	}
	in.State = next
	cur, ok := s.delegations[in.ID]
	if !ok {
		return Delegation{}, false, ErrNotFound
	}
	if cur.OrgID != in.OrgID || cur.OwnerTeamID != in.OwnerTeamID || cur.ServicePrincipal != in.ServicePrincipal ||
		cur.CreatedBy != in.CreatedBy {
		return Delegation{}, false, invalid("Delegation immutable field")
	}
	if cur.LastMutationID == in.LastMutationID {
		if cur.Revision == expected+1 && cur.State != DelegationRevoked && sameDelegationContent(cur, in) {
			return cur.Canonical(), false, nil
		}
		return Delegation{}, false, conflict("mutation id reused")
	}
	if cur.Revision != expected {
		return Delegation{}, false, conflict("stale revision")
	}
	if err := validDelegationTransition(cur.State, in.State); err != nil {
		return Delegation{}, false, err
	}
	in.Revision = cur.Revision + 1
	s.delegations[in.ID] = in
	return in.Canonical(), true, nil
}

func (s *snapshot) revokeDelegation(id string, expected uint64, mutationID, eventID string) (Delegation, bool, error) {
	if expected == 0 || !validID(id) || !validID(mutationID) || !validID(eventID) {
		return Delegation{}, false, invalid("RevokeDelegation arguments")
	}
	cur, ok := s.delegations[id]
	if !ok {
		return Delegation{}, false, ErrNotFound
	}
	if cur.LastMutationID == mutationID {
		if cur.State == DelegationRevoked && cur.Revision == expected+1 {
			return cur.Canonical(), false, nil
		}
		return Delegation{}, false, conflict("mutation id reused")
	}
	if cur.Revision != expected {
		return Delegation{}, false, conflict("stale revision")
	}
	if err := validDelegationTransition(cur.State, DelegationRevoked); err != nil {
		return Delegation{}, false, err
	}
	cur.State = DelegationRevoked
	cur.Revision++
	cur.LastMutationID, cur.LastAuditEventID = mutationID, eventID
	s.delegations[id] = cur
	return cur.Canonical(), true, nil
}

// ---------- Backend ----------

func (b *FileBackend) guard(ctx context.Context) error {
	if b.poison != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, "state file requires reopen")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, "context")
	}
	return nil
}

// mutate выполняет fn над копией снимка и фиксирует результат на диске; changed=false запись не вызывает.
func (b *FileBackend) mutate(ctx context.Context, fn func(s *snapshot) (changed bool, err error)) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.guard(ctx); err != nil {
		return err
	}
	next := b.st.clone()
	changed, err := fn(next)
	if err != nil || !changed {
		return err
	}
	data, err := encodeSnapshot(next)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, "encode state")
	}
	if err := b.persist(data); err != nil {
		return err
	}
	b.st = next
	return nil
}

func (b *FileBackend) read(ctx context.Context, fn func(s *snapshot) error) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if err := b.guard(ctx); err != nil {
		return err
	}
	return fn(b.st)
}

func (b *FileBackend) persist(data []byte) error {
	f, err := b.fs.CreateTemp(b.dir)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, "create temp file")
	}
	tmp := f.Name()
	fail := func(step string) error {
		_ = f.Close()
		_ = b.fs.Remove(tmp)
		return fmt.Errorf("%w: %s", ErrUnavailable, step)
	}
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fail("write state")
	}
	if err := f.Sync(); err != nil {
		return fail("sync state")
	}
	if err := f.Close(); err != nil {
		_ = b.fs.Remove(tmp)
		return fmt.Errorf("%w: %s", ErrUnavailable, "close state")
	}
	if err := b.fs.Rename(tmp, b.path); err != nil {
		_ = b.fs.Remove(tmp)
		return fmt.Errorf("%w: %s", ErrUnavailable, "replace state")
	}
	if err := b.fs.SyncDir(b.dir); err != nil {
		b.poison = err
		return fmt.Errorf("%w: %s", ErrUnavailable, "sync state directory")
	}
	return nil
}

func (b *FileBackend) CreateGrant(ctx context.Context, in Grant) (Grant, error) {
	var out Grant
	err := b.mutate(ctx, func(s *snapshot) (bool, error) {
		got, changed, err := s.createGrant(in)
		out = got
		return changed, err
	})
	if err != nil {
		return Grant{}, err
	}
	return out, nil
}

func (b *FileBackend) GetGrant(ctx context.Context, id string) (Grant, error) {
	var out Grant
	err := b.read(ctx, func(s *snapshot) error {
		got, err := s.grant(id)
		out = got
		return err
	})
	if err != nil {
		return Grant{}, err
	}
	return out, nil
}

func (b *FileBackend) ListGrants(ctx context.Context, principal identity.PrincipalRef) ([]Grant, error) {
	var out []Grant
	err := b.read(ctx, func(s *snapshot) error {
		got, err := s.listGrants(principal)
		out = got
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (b *FileBackend) UpdateGrant(ctx context.Context, in Grant, expectedRevision uint64) (Grant, error) {
	var out Grant
	err := b.mutate(ctx, func(s *snapshot) (bool, error) {
		got, changed, err := s.updateGrant(in, expectedRevision)
		out = got
		return changed, err
	})
	if err != nil {
		return Grant{}, err
	}
	return out, nil
}

func (b *FileBackend) RevokeGrant(ctx context.Context, id string, expectedRevision uint64, mutationID, auditEventID string) (Grant, error) {
	var out Grant
	err := b.mutate(ctx, func(s *snapshot) (bool, error) {
		got, changed, err := s.revokeGrant(id, expectedRevision, mutationID, auditEventID)
		out = got
		return changed, err
	})
	if err != nil {
		return Grant{}, err
	}
	return out, nil
}

func (b *FileBackend) CreateDelegation(ctx context.Context, in Delegation) (Delegation, error) {
	var out Delegation
	err := b.mutate(ctx, func(s *snapshot) (bool, error) {
		got, changed, err := s.createDelegation(in)
		out = got
		return changed, err
	})
	if err != nil {
		return Delegation{}, err
	}
	return out, nil
}

func (b *FileBackend) GetDelegation(ctx context.Context, id string) (Delegation, error) {
	var out Delegation
	err := b.read(ctx, func(s *snapshot) error {
		got, err := s.delegation(id)
		out = got
		return err
	})
	if err != nil {
		return Delegation{}, err
	}
	return out, nil
}

func (b *FileBackend) ListDelegations(ctx context.Context, service identity.PrincipalRef, ownerTeamID string) ([]Delegation, error) {
	var out []Delegation
	err := b.read(ctx, func(s *snapshot) error {
		got, err := s.listDelegations(service, ownerTeamID)
		out = got
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (b *FileBackend) UpdateDelegation(ctx context.Context, in Delegation, expectedRevision uint64) (Delegation, error) {
	var out Delegation
	err := b.mutate(ctx, func(s *snapshot) (bool, error) {
		got, changed, err := s.updateDelegation(in, expectedRevision)
		out = got
		return changed, err
	})
	if err != nil {
		return Delegation{}, err
	}
	return out, nil
}

func (b *FileBackend) RevokeDelegation(ctx context.Context, id string, expectedRevision uint64, mutationID, auditEventID string) (Delegation, error) {
	var out Delegation
	err := b.mutate(ctx, func(s *snapshot) (bool, error) {
		got, changed, err := s.revokeDelegation(id, expectedRevision, mutationID, auditEventID)
		out = got
		return changed, err
	})
	if err != nil {
		return Delegation{}, err
	}
	return out, nil
}

// ---------- сериализация ----------

type principalDTO struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	OrgID  string `json:"org_id,omitempty"`
	TeamID string `json:"team_id,omitempty"`
}

type actorDTO struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type actionDTO struct {
	OperationID string `json:"operation_id"`
	ActionID    string `json:"action_id"`
}

type namespaceDTO struct {
	ClusterID string `json:"cluster_id"`
	Namespace string `json:"namespace"`
}

type objectDTO struct {
	ClusterID string `json:"cluster_id"`
	Group     string `json:"group,omitempty"`
	Version   string `json:"version"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	UID       string `json:"uid"`
}

type scopeDTO struct {
	OrgID      string         `json:"org_id"`
	TeamIDs    []string       `json:"team_ids,omitempty"`
	ClusterIDs []string       `json:"cluster_ids,omitempty"`
	Namespaces []namespaceDTO `json:"namespaces,omitempty"`
	Objects    []objectDTO    `json:"objects,omitempty"`
}

// constraintsDTO — версионированная схема constraints. Неизвестное поле либо версия отвергаются строгим
// декодером: хранимый constraint нельзя «пропустить».
type constraintsDTO struct {
	Version             int        `json:"version"`
	AllowedEnvironments []string   `json:"allowed_environments,omitempty"`
	DenyDestructive     bool       `json:"deny_destructive,omitempty"`
	NotBefore           *time.Time `json:"not_before,omitempty"`
}

type grantDTO struct {
	ID               string         `json:"id"`
	Principal        principalDTO   `json:"principal"`
	Actions          []actionDTO    `json:"actions"`
	Targets          scopeDTO       `json:"targets"`
	Constraints      constraintsDTO `json:"constraints"`
	State            string         `json:"state"`
	Revision         uint64         `json:"revision"`
	ExpiresAt        time.Time      `json:"expires_at"`
	CreatedBy        actorDTO       `json:"created_by"`
	LastMutationID   string         `json:"last_mutation_id"`
	LastAuditEventID string         `json:"last_audit_event_id"`
}

type delegationDTO struct {
	ID               string         `json:"id"`
	OrgID            string         `json:"org_id"`
	OwnerTeamID      string         `json:"owner_team_id"`
	ServicePrincipal principalDTO   `json:"service_principal"`
	Actions          []actionDTO    `json:"actions"`
	Targets          scopeDTO       `json:"targets"`
	Constraints      constraintsDTO `json:"constraints"`
	State            string         `json:"state"`
	Revision         uint64         `json:"revision"`
	ExpiresAt        time.Time      `json:"expires_at"`
	CreatedBy        actorDTO       `json:"created_by"`
	LastMutationID   string         `json:"last_mutation_id"`
	LastAuditEventID string         `json:"last_audit_event_id"`
}

type fileDoc struct {
	Version     int             `json:"version"`
	Grants      []grantDTO      `json:"grants"`
	Delegations []delegationDTO `json:"delegations"`
}

func toPrincipalDTO(r identity.PrincipalRef) principalDTO {
	return principalDTO{Kind: string(r.Kind), ID: r.ID, OrgID: r.OrgID, TeamID: r.TeamID}
}

func fromPrincipalDTO(d principalDTO) identity.PrincipalRef {
	return identity.PrincipalRef{Kind: identity.PrincipalKind(d.Kind), ID: d.ID, OrgID: d.OrgID, TeamID: d.TeamID}
}

func toActionsDTO(in []OperationAction) []actionDTO {
	out := make([]actionDTO, 0, len(in))
	for _, a := range in {
		out = append(out, actionDTO(a))
	}
	return out
}

func fromActionsDTO(in []actionDTO) []OperationAction {
	out := make([]OperationAction, 0, len(in))
	for _, a := range in {
		out = append(out, OperationAction(a))
	}
	return out
}

func toScopeDTO(s TargetScope) scopeDTO {
	d := scopeDTO{OrgID: s.OrgID, TeamIDs: s.TeamIDs, ClusterIDs: s.ClusterIDs}
	for _, n := range s.Namespaces {
		d.Namespaces = append(d.Namespaces, namespaceDTO(n))
	}
	for _, o := range s.Objects {
		d.Objects = append(d.Objects, objectDTO(o))
	}
	return d
}

func fromScopeDTO(d scopeDTO) TargetScope {
	s := TargetScope{OrgID: d.OrgID, TeamIDs: d.TeamIDs, ClusterIDs: d.ClusterIDs}
	for _, n := range d.Namespaces {
		s.Namespaces = append(s.Namespaces, NamespaceRef(n))
	}
	for _, o := range d.Objects {
		s.Objects = append(s.Objects, ObjectRef(o))
	}
	return s
}

func toConstraintsDTO(c ConstraintSet) constraintsDTO {
	return constraintsDTO{Version: constraintsVer, AllowedEnvironments: c.AllowedEnvironments,
		DenyDestructive: c.DenyDestructive, NotBefore: c.NotBefore}
}

func fromConstraintsDTO(d constraintsDTO) (ConstraintSet, error) {
	if d.Version != constraintsVer {
		return ConstraintSet{}, corrupt("constraints version")
	}
	return ConstraintSet{AllowedEnvironments: d.AllowedEnvironments, DenyDestructive: d.DenyDestructive, NotBefore: d.NotBefore}, nil
}

// encodeSnapshot сериализует снимок детерминированно (записи по ID, списки в каноническом порядке).
func encodeSnapshot(s *snapshot) ([]byte, error) {
	doc := fileDoc{Version: fileVersion, Grants: []grantDTO{}, Delegations: []delegationDTO{}}
	for _, g := range s.grants {
		g = g.Canonical()
		doc.Grants = append(doc.Grants, grantDTO{
			ID: g.ID, Principal: toPrincipalDTO(g.Principal), Actions: toActionsDTO(g.Actions),
			Targets: toScopeDTO(g.Targets), Constraints: toConstraintsDTO(g.Constraints),
			State: string(g.State), Revision: g.Revision, ExpiresAt: g.ExpiresAt,
			CreatedBy:      actorDTO{Kind: string(g.CreatedBy.Kind), ID: g.CreatedBy.ID},
			LastMutationID: g.LastMutationID, LastAuditEventID: g.LastAuditEventID,
		})
	}
	for _, d := range s.delegations {
		d = d.Canonical()
		doc.Delegations = append(doc.Delegations, delegationDTO{
			ID: d.ID, OrgID: d.OrgID, OwnerTeamID: d.OwnerTeamID, ServicePrincipal: toPrincipalDTO(d.ServicePrincipal),
			Actions: toActionsDTO(d.Actions), Targets: toScopeDTO(d.Targets), Constraints: toConstraintsDTO(d.Constraints),
			State: string(d.State), Revision: d.Revision, ExpiresAt: d.ExpiresAt,
			CreatedBy:      actorDTO{Kind: string(d.CreatedBy.Kind), ID: d.CreatedBy.ID},
			LastMutationID: d.LastMutationID, LastAuditEventID: d.LastAuditEventID,
		})
	}
	sort.Slice(doc.Grants, func(i, j int) bool { return doc.Grants[i].ID < doc.Grants[j].ID })
	sort.Slice(doc.Delegations, func(i, j int) bool { return doc.Delegations[i].ID < doc.Delegations[j].ID })
	return json.MarshalIndent(doc, "", "  ")
}

func corrupt(reason string) error {
	return fmt.Errorf("%w: %s", ErrUnavailable, "state file is corrupt: "+reason)
}

// decodeSnapshot строго разбирает файл и проверяет инварианты записей.
func decodeSnapshot(data []byte) (*snapshot, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc fileDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, corrupt("decode")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, corrupt("trailing data")
	}
	if doc.Version != fileVersion {
		return nil, corrupt("version")
	}
	s := newSnapshot()
	for _, d := range doc.Grants {
		c, err := fromConstraintsDTO(d.Constraints)
		if err != nil {
			return nil, err
		}
		g := Grant{
			ID: d.ID, Principal: fromPrincipalDTO(d.Principal), Actions: fromActionsDTO(d.Actions),
			Targets: fromScopeDTO(d.Targets), Constraints: c, State: GrantState(d.State), Revision: d.Revision,
			ExpiresAt: d.ExpiresAt, CreatedBy: identity.ActorRef{Kind: identity.ActorKind(d.CreatedBy.Kind), ID: d.CreatedBy.ID},
			LastMutationID: d.LastMutationID, LastAuditEventID: d.LastAuditEventID,
		}.Canonical()
		if err := g.Validate(); err != nil {
			return nil, corrupt("grant record")
		}
		if _, dup := s.grants[g.ID]; dup {
			return nil, corrupt("duplicate grant")
		}
		s.grants[g.ID] = g
	}
	for _, d := range doc.Delegations {
		c, err := fromConstraintsDTO(d.Constraints)
		if err != nil {
			return nil, err
		}
		dl := Delegation{
			ID: d.ID, OrgID: d.OrgID, OwnerTeamID: d.OwnerTeamID, ServicePrincipal: fromPrincipalDTO(d.ServicePrincipal),
			Actions: fromActionsDTO(d.Actions), Targets: fromScopeDTO(d.Targets), Constraints: c,
			State: DelegationState(d.State), Revision: d.Revision, ExpiresAt: d.ExpiresAt,
			CreatedBy:      identity.ActorRef{Kind: identity.ActorKind(d.CreatedBy.Kind), ID: d.CreatedBy.ID},
			LastMutationID: d.LastMutationID, LastAuditEventID: d.LastAuditEventID,
		}.Canonical()
		if err := dl.Validate(); err != nil {
			return nil, corrupt("delegation record")
		}
		if _, dup := s.delegations[dl.ID]; dup {
			return nil, corrupt("duplicate delegation")
		}
		s.delegations[dl.ID] = dl
	}
	return s, nil
}
