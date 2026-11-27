// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package identity

// File backend (Desktop, один процесс). Всё состояние — один JSON-файл identity.json в каталоге данных.
//
// Гарантии (Contract Freeze §8):
//   - read-modify-write сериализован мьютексом одного процесса; многопроцессный доступ не поддерживается
//     и не заявляется (нет файловой блокировки);
//   - запись — атомарная замена: временный файл в том же каталоге (0600, O_EXCL) → полная запись с проверкой
//     длины → fsync → rename → fsync каталога. Файл на диске всегда либо прежний, либо новый целиком;
//   - состояние в памяти меняется только после того, как запись подтверждена. Если исход неясен (сбой fsync
//     каталога после rename), backend «отравляется»: все операции отвечают ErrUnavailable до переоткрытия,
//     когда истина читается с диска (fail closed);
//   - при открытии файл читается строго (неизвестные поля, хвост, версия, инварианты). Повреждённый файл не
//     «лечится» и не отбрасывается молча: OpenFile возвращает ErrUnavailable. Брошенные временные файлы
//     прерванной записи удаляются: они никогда не авторитетны.

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
)

const (
	identityFileName = "identity.json"
	tempPrefix       = ".identity-"
	tempSuffix       = ".tmp"
	fileVersion      = 1
	// maxStateFileSize ограничивает чтение файла состояния (защита от раздутого/подменённого файла).
	maxStateFileSize = 64 << 20
)

// FileOption настраивает FileBackend.
type FileOption func(*FileBackend)

// WithFileClock задаёт часы Backend'а для UpdatedAt обновлений (по умолчанию time.Now).
func WithFileClock(now func() time.Time) FileOption {
	return func(b *FileBackend) {
		if now != nil {
			b.now = now
		}
	}
}

// withFS подменяет файловые операции (внутренние тесты: сбои записи/sync).
func withFS(fs stateFS) FileOption { return func(b *FileBackend) { b.fs = fs } }

// FileBackend — реализация Backend на файле. Безопасна для параллельных вызовов в одном процессе.
type FileBackend struct {
	mu     sync.RWMutex
	dir    string
	path   string
	fs     stateFS
	now    func() time.Time
	st     *snapshot
	poison error // не nil: исход записи неясен, всё закрыто до переоткрытия
}

var _ Backend = (*FileBackend)(nil)

// OpenFile открывает (или создаёт пустое) хранилище в каталоге dir.
func OpenFile(dir string, opts ...FileOption) (*FileBackend, error) {
	b := &FileBackend{dir: dir, path: filepath.Join(dir, identityFileName), fs: osFS{}, now: time.Now}
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

// removeStaleTemps удаляет временные файлы прерванной записи. Ошибки удаления не критичны: такие файлы
// никогда не читаются как состояние.
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
		return newSnapshot(), nil // ничего не было зафиксировано
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, "read state file")
	}
	st, err := decodeSnapshot(data)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// ---------- снимок состояния и доменные операции ----------

type idKey struct {
	kind PrincipalKind
	id   string
}

func keyOf(r PrincipalRef) idKey { return idKey{r.Kind, r.ID} }

type snapshot struct {
	identities map[idKey]Identity
	bindings   map[string]CredentialBinding
}

func newSnapshot() *snapshot {
	return &snapshot{identities: map[idKey]Identity{}, bindings: map[string]CredentialBinding{}}
}

func (s *snapshot) clone() *snapshot {
	c := &snapshot{
		identities: make(map[idKey]Identity, len(s.identities)),
		bindings:   make(map[string]CredentialBinding, len(s.bindings)),
	}
	for k, v := range s.identities {
		c.identities[k] = v
	}
	for k, v := range s.bindings {
		c.bindings[k] = v
	}
	return c
}

// identity возвращает запись, сопоставляя ссылку целиком: чужой org/team отвечает как отсутствующая.
func (s *snapshot) identity(ref PrincipalRef) (Identity, error) {
	if err := ref.validateTechnical(); err != nil {
		return Identity{}, err
	}
	cur, ok := s.identities[keyOf(ref)]
	if !ok || cur.Ref != ref {
		return Identity{}, ErrNotFound
	}
	return cur, nil
}

func (s *snapshot) create(in Identity) (Identity, bool, error) {
	if err := in.validate(false); err != nil {
		return Identity{}, false, err
	}
	if in.CreatedAt.IsZero() {
		return Identity{}, false, invalid("Identity.CreatedAt")
	}
	if cur, ok := s.identities[keyOf(in.Ref)]; ok {
		if cur.Ref == in.Ref && cur.Revision == 1 && cur.LastMutationID == in.LastMutationID &&
			cur.Owner == in.Owner && cur.DisplayName == in.DisplayName && cur.CreatedBy == in.CreatedBy {
			return cur, false, nil // идемпотентный повтор
		}
		return Identity{}, false, fmt.Errorf("%w: %s", ErrConflict, "identity exists")
	}
	in.State = IdentityActive
	in.Revision = 1
	in.CreatedAt = canon(in.CreatedAt)
	in.UpdatedAt = in.CreatedAt
	s.identities[keyOf(in.Ref)] = in
	return in, true, nil
}

func (s *snapshot) setState(ref PrincipalRef, expected uint64, next IdentityState,
	mutationID, auditEventID string, now time.Time) (Identity, bool, error) {
	if expected == 0 || !knownIdentityState(next) || !validID(mutationID) || !validID(auditEventID) {
		return Identity{}, false, invalid("SetState arguments")
	}
	cur, err := s.identity(ref)
	if err != nil {
		return Identity{}, false, err
	}
	if cur.LastMutationID == mutationID {
		if cur.State == next && cur.Revision == expected+1 {
			return cur, false, nil // идемпотентный повтор
		}
		return Identity{}, false, fmt.Errorf("%w: %s", ErrConflict, "mutation id reused")
	}
	if cur.Revision != expected {
		return Identity{}, false, fmt.Errorf("%w: %s", ErrConflict, "stale revision")
	}
	if err := ValidateIdentityTransition(cur.State, next); err != nil {
		return Identity{}, false, err
	}
	cur.State = next
	cur.Revision++
	cur.UpdatedAt = laterOf(canon(now), cur.UpdatedAt)
	cur.LastMutationID = mutationID
	cur.LastAuditEventID = auditEventID
	s.identities[keyOf(ref)] = cur
	return cur, true, nil
}

func (s *snapshot) updateDisplayName(ref PrincipalRef, expected uint64, displayName,
	mutationID, auditEventID string, now time.Time) (Identity, bool, error) {
	if expected == 0 || !validDisplayName(displayName) || !validID(mutationID) || !validID(auditEventID) {
		return Identity{}, false, invalid("UpdateDisplayName arguments")
	}
	cur, err := s.identity(ref)
	if err != nil {
		return Identity{}, false, err
	}
	if cur.LastMutationID == mutationID {
		if cur.DisplayName == displayName && cur.Revision == expected+1 {
			return cur, false, nil
		}
		return Identity{}, false, fmt.Errorf("%w: %s", ErrConflict, "mutation id reused")
	}
	if cur.Revision != expected {
		return Identity{}, false, fmt.Errorf("%w: %s", ErrConflict, "stale revision")
	}
	cur.DisplayName = displayName
	cur.Revision++
	cur.UpdatedAt = laterOf(canon(now), cur.UpdatedAt)
	cur.LastMutationID = mutationID
	cur.LastAuditEventID = auditEventID
	s.identities[keyOf(ref)] = cur
	return cur, true, nil
}

func (s *snapshot) liveBinding(principal PrincipalRef, clusterID string) (live []CredentialBinding, revoked []CredentialBinding) {
	for _, b := range s.bindings {
		if b.Principal != principal || b.ClusterID != clusterID {
			continue
		}
		if b.State == BindingRevoked {
			revoked = append(revoked, b)
		} else {
			live = append(live, b)
		}
	}
	return live, revoked
}

func (s *snapshot) putBinding(in CredentialBinding, expected uint64, mutationID, auditEventID string) (CredentialBinding, bool, error) {
	if !validID(mutationID) || !validID(auditEventID) ||
		(in.LastMutationID != "" && in.LastMutationID != mutationID) ||
		(in.LastAuditEventID != "" && in.LastAuditEventID != auditEventID) {
		return CredentialBinding{}, false, invalid("PutBinding arguments")
	}
	if expected != 0 {
		return CredentialBinding{}, false, invalid("PutBinding expected revision")
	}
	if err := in.validate(false); err != nil {
		return CredentialBinding{}, false, err
	}
	if in.CreatedAt.IsZero() {
		return CredentialBinding{}, false, invalid("CredentialBinding.CreatedAt")
	}
	ident, err := s.identity(in.Principal)
	if err != nil {
		return CredentialBinding{}, false, err
	}
	if cur, ok := s.bindings[in.ID]; ok {
		if cur.Principal == in.Principal && cur.ClusterID == in.ClusterID && cur.Revision == 1 &&
			cur.LastMutationID == mutationID && cur.CreatedBy == in.CreatedBy {
			return cur, false, nil // идемпотентный повтор
		}
		return CredentialBinding{}, false, fmt.Errorf("%w: %s", ErrConflict, "binding id reused")
	}
	switch ident.State {
	case IdentityRevoked:
		return CredentialBinding{}, false, fmt.Errorf("%w: %s", ErrRevoked, "identity")
	case IdentityActive:
	default:
		return CredentialBinding{}, false, invalid("Identity.State")
	}
	if live, _ := s.liveBinding(in.Principal, in.ClusterID); len(live) > 0 {
		return CredentialBinding{}, false, fmt.Errorf("%w: %s", ErrConflict, "binding exists")
	}
	in.State = BindingPending
	in.Revision = 1
	in.CreatedAt = canon(in.CreatedAt)
	in.UpdatedAt = in.CreatedAt
	in.LastMutationID = mutationID
	in.LastAuditEventID = auditEventID
	s.bindings[in.ID] = in
	return in, true, nil
}

func (s *snapshot) getBinding(principal PrincipalRef, clusterID string) (CredentialBinding, error) {
	if !validID(clusterID) {
		return CredentialBinding{}, invalid("CredentialBinding.ClusterID")
	}
	if _, err := s.identity(principal); err != nil {
		return CredentialBinding{}, err
	}
	live, revoked := s.liveBinding(principal, clusterID)
	switch {
	case len(live) > 1:
		return CredentialBinding{}, fmt.Errorf("%w: %s", ErrConflict, "ambiguous binding")
	case len(live) == 1:
		return live[0], nil
	case len(revoked) > 0:
		sort.Slice(revoked, func(i, j int) bool {
			if !revoked[i].CreatedAt.Equal(revoked[j].CreatedAt) {
				return revoked[i].CreatedAt.After(revoked[j].CreatedAt)
			}
			return revoked[i].ID > revoked[j].ID
		})
		return revoked[0], fmt.Errorf("%w: %s", ErrRevoked, "binding")
	}
	return CredentialBinding{}, ErrNotFound
}

func (s *snapshot) listBindings(principal PrincipalRef) ([]CredentialBinding, error) {
	if _, err := s.identity(principal); err != nil {
		return nil, err
	}
	out := []CredentialBinding{}
	for _, b := range s.bindings {
		if b.Principal == principal {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ClusterID != out[j].ClusterID {
			return out[i].ClusterID < out[j].ClusterID
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *snapshot) transitionBinding(id string, expected uint64, next BindingState,
	mutationID, auditEventID string, now time.Time) (CredentialBinding, bool, error) {
	if expected == 0 || !validID(id) || !validID(mutationID) || !validID(auditEventID) {
		return CredentialBinding{}, false, invalid("binding transition arguments")
	}
	cur, ok := s.bindings[id]
	if !ok {
		return CredentialBinding{}, false, ErrNotFound
	}
	if cur.LastMutationID == mutationID {
		if cur.State == next && cur.Revision == expected+1 {
			return cur, false, nil // идемпотентный повтор
		}
		return CredentialBinding{}, false, fmt.Errorf("%w: %s", ErrConflict, "mutation id reused")
	}
	if cur.Revision != expected {
		return CredentialBinding{}, false, fmt.Errorf("%w: %s", ErrConflict, "stale revision")
	}
	if err := ValidateBindingTransition(cur.State, next); err != nil {
		return CredentialBinding{}, false, err
	}
	if next == BindingActive {
		ident, err := s.identity(cur.Principal)
		if err != nil {
			return CredentialBinding{}, false, err
		}
		switch ident.State {
		case IdentityRevoked:
			return CredentialBinding{}, false, fmt.Errorf("%w: %s", ErrRevoked, "identity")
		case IdentityActive:
		default:
			return CredentialBinding{}, false, invalid("Identity.State")
		}
	}
	cur.State = next
	cur.Revision++
	cur.UpdatedAt = laterOf(canon(now), cur.UpdatedAt)
	cur.LastMutationID = mutationID
	cur.LastAuditEventID = auditEventID
	s.bindings[id] = cur
	return cur, true, nil
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// ---------- Backend ----------

// guard проверяет ctx и отравленное состояние. Вызывается под мьютексом.
func (b *FileBackend) guard(ctx context.Context) error {
	if b.poison != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, "state file requires reopen")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return nil
}

// mutate выполняет fn над копией снимка и фиксирует результат на диске. changed=false (идемпотентный повтор)
// запись не вызывает. Память обновляется только после подтверждённой записи.
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

// persist: tmp → write(полностью) → fsync → close → rename → fsync каталога.
// Сбой до rename оставляет прежний файл (состояние не изменилось). Сбой fsync каталога после rename —
// исход неясен: backend отравляется.
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

// Create — см. Backend.
func (b *FileBackend) Create(ctx context.Context, in Identity) (Identity, error) {
	var out Identity
	err := b.mutate(ctx, func(s *snapshot) (bool, error) {
		got, changed, err := s.create(in)
		out = got
		return changed, err
	})
	if err != nil {
		return Identity{}, err
	}
	return out, nil
}

// Get — см. Backend.
func (b *FileBackend) Get(ctx context.Context, ref PrincipalRef) (Identity, error) {
	var out Identity
	err := b.read(ctx, func(s *snapshot) error {
		got, err := s.identity(ref)
		out = got
		return err
	})
	if err != nil {
		return Identity{}, err
	}
	return out, nil
}

// List — см. Backend.
func (b *FileBackend) List(ctx context.Context, owner OwnerScope) ([]Identity, error) {
	if err := owner.Validate(); err != nil {
		return nil, err
	}
	out := []Identity{}
	err := b.read(ctx, func(s *snapshot) error {
		for _, i := range s.identities {
			if i.Owner == owner {
				out = append(out, i)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Ref.Kind != out[j].Ref.Kind {
			return out[i].Ref.Kind < out[j].Ref.Kind
		}
		return out[i].Ref.ID < out[j].Ref.ID
	})
	return out, nil
}

// SetState — см. Backend.
func (b *FileBackend) SetState(ctx context.Context, ref PrincipalRef, expectedRevision uint64,
	next IdentityState, mutationID, auditEventID string) (Identity, error) {
	var out Identity
	err := b.mutate(ctx, func(s *snapshot) (bool, error) {
		got, changed, err := s.setState(ref, expectedRevision, next, mutationID, auditEventID, b.now())
		out = got
		return changed, err
	})
	if err != nil {
		return Identity{}, err
	}
	return out, nil
}

// UpdateDisplayName — см. Backend.
func (b *FileBackend) UpdateDisplayName(ctx context.Context, ref PrincipalRef, expectedRevision uint64,
	displayName, mutationID, auditEventID string) (Identity, error) {
	var out Identity
	err := b.mutate(ctx, func(s *snapshot) (bool, error) {
		got, changed, err := s.updateDisplayName(ref, expectedRevision, displayName, mutationID, auditEventID, b.now())
		out = got
		return changed, err
	})
	if err != nil {
		return Identity{}, err
	}
	return out, nil
}

// PutBinding — см. Backend.
func (b *FileBackend) PutBinding(ctx context.Context, in CredentialBinding,
	expectedRevision uint64, mutationID, auditEventID string) (CredentialBinding, error) {
	var out CredentialBinding
	err := b.mutate(ctx, func(s *snapshot) (bool, error) {
		got, changed, err := s.putBinding(in, expectedRevision, mutationID, auditEventID)
		out = got
		return changed, err
	})
	if err != nil {
		return CredentialBinding{}, err
	}
	return out, nil
}

// GetBinding — см. Backend (при ErrRevoked возвращает самую позднюю отозванную привязку).
func (b *FileBackend) GetBinding(ctx context.Context, principal PrincipalRef, clusterID string) (CredentialBinding, error) {
	var out CredentialBinding
	err := b.read(ctx, func(s *snapshot) error {
		got, err := s.getBinding(principal, clusterID)
		out = got
		return err
	})
	return out, err
}

// ListBindings — см. Backend.
func (b *FileBackend) ListBindings(ctx context.Context, principal PrincipalRef) ([]CredentialBinding, error) {
	var out []CredentialBinding
	err := b.read(ctx, func(s *snapshot) error {
		got, err := s.listBindings(principal)
		out = got
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ActivateBinding — см. Backend.
func (b *FileBackend) ActivateBinding(ctx context.Context, id string, expectedRevision uint64,
	mutationID, auditEventID string) (CredentialBinding, error) {
	return b.transitionBinding(ctx, id, expectedRevision, BindingActive, mutationID, auditEventID)
}

// RevokeBinding — см. Backend.
func (b *FileBackend) RevokeBinding(ctx context.Context, id string, expectedRevision uint64,
	mutationID, auditEventID string) (CredentialBinding, error) {
	return b.transitionBinding(ctx, id, expectedRevision, BindingRevoked, mutationID, auditEventID)
}

func (b *FileBackend) transitionBinding(ctx context.Context, id string, expected uint64, next BindingState,
	mutationID, auditEventID string) (CredentialBinding, error) {
	var out CredentialBinding
	err := b.mutate(ctx, func(s *snapshot) (bool, error) {
		got, changed, err := s.transitionBinding(id, expected, next, mutationID, auditEventID, b.now())
		out = got
		return changed, err
	})
	if err != nil {
		return CredentialBinding{}, err
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

type ownerDTO struct {
	Kind   string `json:"kind"`
	OrgID  string `json:"org_id"`
	TeamID string `json:"team_id,omitempty"`
}

type identityDTO struct {
	Ref              principalDTO `json:"ref"`
	Owner            ownerDTO     `json:"owner"`
	DisplayName      string       `json:"display_name"`
	State            string       `json:"state"`
	Revision         uint64       `json:"revision"`
	CreatedBy        actorDTO     `json:"created_by"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	LastMutationID   string       `json:"last_mutation_id"`
	LastAuditEventID string       `json:"last_audit_event_id"`
}

type bindingDTO struct {
	ID               string       `json:"id"`
	Principal        principalDTO `json:"principal"`
	ClusterID        string       `json:"cluster_id"`
	State            string       `json:"state"`
	Revision         uint64       `json:"revision"`
	CreatedBy        actorDTO     `json:"created_by"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	LastMutationID   string       `json:"last_mutation_id"`
	LastAuditEventID string       `json:"last_audit_event_id"`
}

type fileDoc struct {
	Version    int           `json:"version"`
	Identities []identityDTO `json:"identities"`
	Bindings   []bindingDTO  `json:"bindings"`
}

func toPrincipalDTO(r PrincipalRef) principalDTO {
	return principalDTO{Kind: string(r.Kind), ID: r.ID, OrgID: r.OrgID, TeamID: r.TeamID}
}

func fromPrincipalDTO(d principalDTO) PrincipalRef {
	return PrincipalRef{Kind: PrincipalKind(d.Kind), ID: d.ID, OrgID: d.OrgID, TeamID: d.TeamID}
}

// encodeSnapshot сериализует снимок детерминированно (порядок записей по ключам).
func encodeSnapshot(s *snapshot) ([]byte, error) {
	doc := fileDoc{Version: fileVersion, Identities: []identityDTO{}, Bindings: []bindingDTO{}}
	for _, i := range s.identities {
		doc.Identities = append(doc.Identities, identityDTO{
			Ref: toPrincipalDTO(i.Ref), Owner: ownerDTO{Kind: string(i.Owner.Kind), OrgID: i.Owner.OrgID, TeamID: i.Owner.TeamID},
			DisplayName: i.DisplayName, State: string(i.State), Revision: i.Revision,
			CreatedBy: actorDTO{Kind: string(i.CreatedBy.Kind), ID: i.CreatedBy.ID},
			CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt,
			LastMutationID: i.LastMutationID, LastAuditEventID: i.LastAuditEventID,
		})
	}
	for _, b := range s.bindings {
		doc.Bindings = append(doc.Bindings, bindingDTO{
			ID: b.ID, Principal: toPrincipalDTO(b.Principal), ClusterID: b.ClusterID, State: string(b.State),
			Revision:  b.Revision,
			CreatedBy: actorDTO{Kind: string(b.CreatedBy.Kind), ID: b.CreatedBy.ID},
			CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt,
			LastMutationID: b.LastMutationID, LastAuditEventID: b.LastAuditEventID,
		})
	}
	sort.Slice(doc.Identities, func(i, j int) bool {
		a, c := doc.Identities[i].Ref, doc.Identities[j].Ref
		if a.Kind != c.Kind {
			return a.Kind < c.Kind
		}
		return a.ID < c.ID
	})
	sort.Slice(doc.Bindings, func(i, j int) bool { return doc.Bindings[i].ID < doc.Bindings[j].ID })
	return json.MarshalIndent(doc, "", "  ")
}

// corrupt — единая ошибка повреждённого файла: без деталей содержимого, fail closed.
func corrupt(reason string) error {
	return fmt.Errorf("%w: %s", ErrUnavailable, "state file is corrupt: "+reason)
}

// decodeSnapshot строго разбирает файл и проверяет межзаписьные инварианты.
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
	for _, d := range doc.Identities {
		i := Identity{
			Ref:         fromPrincipalDTO(d.Ref),
			Owner:       OwnerScope{Kind: OwnerKind(d.Owner.Kind), OrgID: d.Owner.OrgID, TeamID: d.Owner.TeamID},
			DisplayName: d.DisplayName, State: IdentityState(d.State), Revision: d.Revision,
			CreatedBy: ActorRef{Kind: ActorKind(d.CreatedBy.Kind), ID: d.CreatedBy.ID},
			CreatedAt: canon(d.CreatedAt), UpdatedAt: canon(d.UpdatedAt),
			LastMutationID: d.LastMutationID, LastAuditEventID: d.LastAuditEventID,
		}
		if err := i.Validate(); err != nil {
			return nil, corrupt("identity record")
		}
		if _, dup := s.identities[keyOf(i.Ref)]; dup {
			return nil, corrupt("duplicate identity")
		}
		s.identities[keyOf(i.Ref)] = i
	}
	for _, d := range doc.Bindings {
		b := CredentialBinding{
			ID: d.ID, Principal: fromPrincipalDTO(d.Principal), ClusterID: d.ClusterID, State: BindingState(d.State),
			Revision:  d.Revision,
			CreatedBy: ActorRef{Kind: ActorKind(d.CreatedBy.Kind), ID: d.CreatedBy.ID},
			CreatedAt: canon(d.CreatedAt), UpdatedAt: canon(d.UpdatedAt),
			LastMutationID: d.LastMutationID, LastAuditEventID: d.LastAuditEventID,
		}
		if err := b.Validate(); err != nil {
			return nil, corrupt("binding record")
		}
		if _, dup := s.bindings[b.ID]; dup {
			return nil, corrupt("duplicate binding")
		}
		if ident, ok := s.identities[keyOf(b.Principal)]; !ok || ident.Ref != b.Principal {
			return nil, corrupt("binding without identity")
		}
		s.bindings[b.ID] = b
	}
	seen := map[string]struct{}{}
	for _, b := range s.bindings {
		if b.State == BindingRevoked {
			continue
		}
		k := string(b.Principal.Kind) + "\x00" + b.Principal.ID + "\x00" + b.ClusterID
		if _, dup := seen[k]; dup {
			return nil, corrupt("ambiguous binding")
		}
		seen[k] = struct{}{}
	}
	return s, nil
}
