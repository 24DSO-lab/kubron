// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

var (
	tbase = time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	tact  = ActorRef{Kind: ActorUser, ID: "admin-1"}
	tctx  = context.Background()
)

func tIdentity(ref PrincipalRef, mut string) Identity {
	o, _ := ref.Owner()
	return Identity{Ref: ref, Owner: o, DisplayName: "runner", State: IdentityActive, CreatedBy: tact,
		CreatedAt: tbase, LastMutationID: mut, LastAuditEventID: "ev-" + mut}
}

func tTeam(id string) PrincipalRef {
	return PrincipalRef{Kind: PrincipalTeam, ID: id, OrgID: "org-a", TeamID: "team-a"}
}

// faultFS подменяет отдельные файловые операции ошибками.
type faultFS struct {
	osFS
	createErr, syncErr, closeErr, renameErr, syncDirErr error
	shortWrite                                          bool
	writeErr                                            error
}

type faultFile struct {
	*os.File
	fs *faultFS
}

func (f *faultFile) Write(p []byte) (int, error) {
	if f.fs.writeErr != nil {
		n, _ := f.File.Write(p[:len(p)/2])
		return n, f.fs.writeErr
	}
	if f.fs.shortWrite {
		return f.File.Write(p[:len(p)/2]) // n < len(p), err == nil
	}
	return f.File.Write(p)
}
func (f *faultFile) Sync() error {
	if f.fs.syncErr != nil {
		return f.fs.syncErr
	}
	return f.File.Sync()
}
func (f *faultFile) Close() error {
	err := f.File.Close()
	if f.fs.closeErr != nil {
		return f.fs.closeErr
	}
	return err
}

func (fs *faultFS) CreateTemp(dir string) (stateFile, error) {
	if fs.createErr != nil {
		return nil, fs.createErr
	}
	f, err := os.CreateTemp(dir, tempPrefix+"*"+tempSuffix)
	if err != nil {
		return nil, err
	}
	return &faultFile{File: f, fs: fs}, nil
}
func (fs *faultFS) Rename(a, b string) error {
	if fs.renameErr != nil {
		return fs.renameErr
	}
	return os.Rename(a, b)
}
func (fs *faultFS) SyncDir(dir string) error {
	if fs.syncDirErr != nil {
		return fs.syncDirErr
	}
	return syncDir(dir)
}

func readState(t *testing.T, dir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, identityFileName))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return data
}

func noTemps(t *testing.T, dir string) {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), tempPrefix) {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}

// Сбои записи до rename: состояние на диске и в памяти не меняется, temp удаляется, backend не отравлен.
func TestFile_WriteFailuresLeaveStateUnchanged(t *testing.T) {
	cases := map[string]func(*faultFS){
		"create temp": func(f *faultFS) { f.createErr = syscall.EACCES },
		"short write": func(f *faultFS) { f.shortWrite = true },
		"disk full":   func(f *faultFS) { f.writeErr = syscall.ENOSPC },
		"fsync file":  func(f *faultFS) { f.syncErr = syscall.EIO },
		"close file":  func(f *faultFS) { f.closeErr = syscall.EIO },
		"rename":      func(f *faultFS) { f.renameErr = syscall.EPERM },
	}
	for name, inject := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			fs := &faultFS{}
			b, err := OpenFile(dir, withFS(fs))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.Create(tctx, tIdentity(tTeam("tid-1"), "m1")); err != nil {
				t.Fatal(err)
			}
			before := readState(t, dir)

			inject(fs)
			_, err = b.Create(tctx, tIdentity(tTeam("tid-2"), "m2"))
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("want ErrUnavailable, got %v", err)
			}
			if !bytes.Equal(before, readState(t, dir)) {
				t.Fatal("state file changed after failed write")
			}
			noTemps(t, dir)
			if _, err := b.Get(tctx, tTeam("tid-2")); !errors.Is(err, ErrNotFound) {
				t.Fatalf("failed create visible in memory: %v", err)
			}

			// Не отравлен: после снятия сбоя запись проходит, прежнее состояние цело.
			*fs = faultFS{}
			if _, err := b.Create(tctx, tIdentity(tTeam("tid-2"), "m2")); err != nil {
				t.Fatalf("recovery: %v", err)
			}
			b2, err := OpenFile(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"tid-1", "tid-2"} {
				if _, err := b2.Get(tctx, tTeam(id)); err != nil {
					t.Fatalf("%s after restart: %v", id, err)
				}
			}
		})
	}
}

// Сбой fsync каталога после rename: исход неясен → backend закрыт (fail closed) до переоткрытия, где истина
// читается с диска.
func TestFile_DirSyncFailurePoisonsUntilReopen(t *testing.T) {
	dir := t.TempDir()
	fs := &faultFS{}
	b, err := OpenFile(dir, withFS(fs))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Create(tctx, tIdentity(tTeam("tid-1"), "m1")); err != nil {
		t.Fatal(err)
	}
	fs.syncDirErr = syscall.EIO
	if _, err := b.Create(tctx, tIdentity(tTeam("tid-2"), "m2")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	*fs = faultFS{}
	// Всё закрыто: и запись, и чтение, и List — даже когда сбой уже снят.
	if _, err := b.Create(tctx, tIdentity(tTeam("tid-3"), "m3")); !errors.Is(err, ErrUnavailable) {
		t.Errorf("write after poison: %v", err)
	}
	if _, err := b.Get(tctx, tTeam("tid-1")); !errors.Is(err, ErrUnavailable) {
		t.Errorf("read after poison: %v", err)
	}
	if _, err := b.List(tctx, OwnerScope{Kind: OwnerTeam, OrgID: "org-a", TeamID: "team-a"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("list after poison: %v", err)
	}
	// Переоткрытие: rename состоялся, запись на диске целая и согласована.
	b2, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b2.Get(tctx, tTeam("tid-2")); err != nil {
		t.Fatalf("renamed state must be readable after reopen: %v", err)
	}
	if _, err := b2.Get(tctx, tTeam("tid-3")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected write must be absent: %v", err)
	}
}

func seeded(t *testing.T) (dir string, data []byte) {
	t.Helper()
	dir = t.TempDir()
	b, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc := PrincipalRef{Kind: PrincipalService, ID: "sid-1", OrgID: "org-a"}
	for _, in := range []Identity{tIdentity(tTeam("tid-1"), "m1"), tIdentity(svc, "m2")} {
		if _, err := b.Create(tctx, in); err != nil {
			t.Fatal(err)
		}
	}
	bd := CredentialBinding{ID: "bnd-1", Principal: svc, ClusterID: "cl-1", State: BindingPending, CreatedBy: tact, CreatedAt: tbase}
	if _, err := b.PutBinding(tctx, bd, 0, "m3", "ev-m3"); err != nil {
		t.Fatal(err)
	}
	return dir, readState(t, dir)
}

// Повреждённый/усечённый файл при открытии — отказ (fail closed), без «лечения» и без изменения файла.
func TestFile_TruncatedAtEveryOffsetFailsClosed(t *testing.T) {
	dir, data := seeded(t)
	path := filepath.Join(dir, identityFileName)
	for n := 0; n < len(data); n++ {
		if err := os.WriteFile(path, data[:n], 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenFile(dir); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("truncated at %d/%d: want ErrUnavailable, got %v", n, len(data), err)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, data[:n]) {
			t.Fatalf("open modified the corrupt file at %d", n)
		}
	}
	// Целый файл читается.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFile(dir); err != nil {
		t.Fatalf("intact file: %v", err)
	}
}

func TestFile_CorruptVariantsFailClosed(t *testing.T) {
	dir, data := seeded(t)
	path := filepath.Join(dir, identityFileName)
	s := string(data)
	rep := func(old, new string) string {
		if !strings.Contains(s, old) {
			t.Fatalf("fixture lacks %q", old)
		}
		return strings.Replace(s, old, new, 1)
	}
	cases := map[string]string{
		"empty":                    "",
		"garbage":                  "\x00\x01not json",
		"null":                     "null",
		"trailing data":            s + "\n{}",
		"trailing garbage":         s + "x",
		"wrong version":            rep(`"version": 1`, `"version": 2`),
		"unknown field":            rep(`"version": 1`, `"version": 1, "extra": true`),
		"bad identity state":       rep(`"state": "active"`, `"state": "bogus"`),
		"owner mismatch":           replaceAfter(s, `"owner"`, `"team_id": "team-a"`, `"team_id": "team-b"`),
		"zero revision":            rep(`"revision": 1`, `"revision": 0`),
		"invalid id":               rep(`"id": "tid-1"`, `"id": "a b"`),
		"empty mutation id":        rep(`"last_mutation_id": "m1"`, `"last_mutation_id": ""`),
		"binding bad state":        rep(`"state": "pending"`, `"state": "bogus"`),
		"binding without identity": replaceAfter(s, `"bindings"`, `"id": "sid-1"`, `"id": "sid-9"`),
	}
	for name, content := range cases {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenFile(dir); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: want ErrUnavailable, got %v", name, err)
		}
	}
}

func TestFile_DuplicateRecordsAndAmbiguousBindingsFailClosed(t *testing.T) {
	svc := PrincipalRef{Kind: PrincipalService, ID: "sid-1", OrgID: "org-a"}
	i := tIdentity(svc, "m1")
	i.Revision, i.UpdatedAt = 1, tbase
	mk := func(ids []Identity, bs []CredentialBinding) []byte {
		s := newSnapshot()
		for _, x := range ids {
			s.identities[keyOf(x.Ref)] = x
		}
		for _, x := range bs {
			s.bindings[x.ID] = x
		}
		d, err := encodeSnapshot(s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	b := func(id string, st BindingState) CredentialBinding {
		return CredentialBinding{ID: id, Principal: svc, ClusterID: "cl-1", State: st, Revision: 1, CreatedBy: tact,
			CreatedAt: tbase, UpdatedAt: tbase, LastMutationID: "m", LastAuditEventID: "e"}
	}
	// Два живых binding одного кортежа — неоднозначность запрещена и при чтении.
	if _, err := decodeSnapshot(mk([]Identity{i}, []CredentialBinding{b("b1", BindingActive), b("b2", BindingPending)})); !errors.Is(err, ErrUnavailable) {
		t.Errorf("ambiguous live bindings: %v", err)
	}
	// Живой + отозванный — норма.
	if _, err := decodeSnapshot(mk([]Identity{i}, []CredentialBinding{b("b1", BindingRevoked), b("b2", BindingPending)})); err != nil {
		t.Errorf("revoked+live: %v", err)
	}
	// Дубликат identity в документе.
	doc := string(mk([]Identity{i}, nil))
	dup := strings.Replace(doc, `"identities": [`, `"identities": [`+extractFirstIdentity(t, doc)+`,`, 1)
	if _, err := decodeSnapshot([]byte(dup)); !errors.Is(err, ErrUnavailable) {
		t.Errorf("duplicate identity: %v", err)
	}
}

func extractFirstIdentity(t *testing.T, doc string) string {
	t.Helper()
	start := strings.Index(doc, `"identities": [`) + len(`"identities": [`)
	end := strings.Index(doc, `"bindings"`)
	body := strings.TrimSpace(doc[start:end])
	body = strings.TrimSuffix(body, ",")
	body = strings.TrimSuffix(strings.TrimSpace(body), "]")
	return strings.TrimSpace(body)
}

// Брошенные temp-файлы прерванной записи не авторитетны: удаляются, состояние берётся из identity.json.
func TestFile_InterruptedWriteLeavesOldStateAndTempIsDiscarded(t *testing.T) {
	dir, data := seeded(t)
	// «Процесс умер после записи temp, до rename»: temp содержит более новое, но не зафиксированное состояние.
	b0, _ := OpenFile(dir)
	if _, err := b0.Create(tctx, tIdentity(tTeam("tid-new"), "m9")); err != nil {
		t.Fatal(err)
	}
	newer := readState(t, dir)
	if err := os.WriteFile(filepath.Join(dir, identityFileName), data, 0o600); err != nil { // откат к старому
		t.Fatal(err)
	}
	stale := filepath.Join(dir, tempPrefix+"123"+tempSuffix)
	other := filepath.Join(dir, "keep.tmp")
	for _, p := range []string{stale, other} {
		if err := os.WriteFile(p, newer, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get(tctx, tTeam("tid-new")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("temp content must never be loaded: %v", err)
	}
	if _, err := b.Get(tctx, tTeam("tid-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Error("stale temp must be removed")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("foreign files must be left alone")
	}
}

func TestFile_MissingFileIsEmptyAndFirstWriteCreatesIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "state")
	b, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := readState(t, dir); got != nil {
		t.Fatal("open must not create the state file")
	}
	if _, err := b.Create(tctx, tIdentity(tTeam("tid-1"), "m1")); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, identityFileName))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("state file mode: %v %v", fi, err)
		}
		di, _ := os.Stat(dir)
		if di.Mode().Perm() != 0o700 {
			t.Fatalf("state dir mode: %v", di.Mode().Perm())
		}
	}
	noTemps(t, dir)
}

func TestFile_OpenFailures(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFile(f); !errors.Is(err, ErrUnavailable) {
		t.Errorf("dir is a file: %v", err)
	}
}

func TestOSFS_ReadFileLimit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), 11), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (osFS{}).ReadFile(p, 10); err == nil {
		t.Fatal("oversized state file must be rejected")
	}
	if got, err := (osFS{}).ReadFile(p, 11); err != nil || len(got) != 11 {
		t.Fatalf("at limit: %d %v", len(got), err)
	}
}

func TestFile_EncodingIsDeterministic(t *testing.T) {
	_, data := seeded(t)
	s, err := decodeSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, _ := encodeSnapshot(s)
		if !bytes.Equal(data, again) {
			t.Fatal("encoding is not deterministic / not a round trip")
		}
	}
}

// Short write реально портит temp, но не итоговый файл: проверяем, что полезная нагрузка не «просачивается».
func TestFile_ShortWriteNeverReachesStateFile(t *testing.T) {
	dir := t.TempDir()
	fs := &faultFS{shortWrite: true}
	b, err := OpenFile(dir, withFS(fs))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Create(tctx, tIdentity(tTeam("tid-1"), "m1")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
	if readState(t, dir) != nil {
		t.Fatal("partial write reached the state file")
	}
	if errors.Is(io.ErrShortWrite, ErrUnavailable) {
		t.Fatal("sentinel confusion")
	}
}

// replaceAfter заменяет первое вхождение old после маркера marker (независимо от форматирования JSON).
func replaceAfter(s, marker, old, new string) string {
	i := strings.Index(s, marker)
	if i < 0 || !strings.Contains(s[i:], old) {
		panic("fixture lacks " + old)
	}
	return s[:i] + strings.Replace(s[i:], old, new, 1)
}
