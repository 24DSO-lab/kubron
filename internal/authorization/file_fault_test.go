// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package authorization

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/identity"
)

var (
	fctx   = context.Background()
	fbase  = time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	factor = identity.ActorRef{Kind: identity.ActorUser, ID: "admin-1"}
)

func fGrant(id, mut string) Grant {
	return Grant{
		ID: id, Principal: identity.PrincipalRef{Kind: identity.PrincipalTeam, ID: "tp-1", OrgID: "org-a", TeamID: "team-a"},
		Actions: []OperationAction{{OperationID: "workload", ActionID: "restart"}},
		Targets: TargetScope{OrgID: "org-a", ClusterIDs: []string{"c-1"}}, State: GrantActive,
		ExpiresAt: fbase.Add(time.Hour), CreatedBy: factor, LastMutationID: mut, LastAuditEventID: "ev-" + mut,
	}
}

func fDelegation(id, mut string) Delegation {
	sp := identity.PrincipalRef{Kind: identity.PrincipalService, ID: "sp-1", OrgID: "org-a"}
	return Delegation{
		ID: id, OrgID: "org-a", OwnerTeamID: "team-a", ServicePrincipal: sp,
		Actions: []OperationAction{{OperationID: "workload", ActionID: "restart"}},
		Targets: TargetScope{OrgID: "org-a", TeamIDs: []string{"team-a"}}, State: DelegationActive,
		ExpiresAt: fbase.Add(time.Hour), CreatedBy: factor, LastMutationID: mut, LastAuditEventID: "ev-" + mut,
	}
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
	data, err := os.ReadFile(filepath.Join(dir, authFileName))
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

// Сбои записи до rename: диск и память не меняются, temp удаляется, backend остаётся пригодным.
func TestFile_WriteFailuresLeaveStateUnchanged(t *testing.T) {
	cases := map[string]func(*faultFS){
		"create temp": func(f *faultFS) { f.createErr = syscall.EACCES },
		"short write": func(f *faultFS) { f.shortWrite = true },
		"disk full":   func(f *faultFS) { f.writeErr = syscall.ENOSPC },
		"sync":        func(f *faultFS) { f.syncErr = syscall.EIO },
		"close":       func(f *faultFS) { f.closeErr = syscall.EIO },
		"rename":      func(f *faultFS) { f.renameErr = syscall.EXDEV },
	}
	for name, inject := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			fs := &faultFS{}
			b, err := OpenFile(dir, withFS(fs))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.CreateGrant(fctx, fGrant("g-ok", "m-1")); err != nil {
				t.Fatal(err)
			}
			before := readState(t, dir)

			inject(fs)
			if _, err := b.CreateGrant(fctx, fGrant("g-2", "m-2")); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("want ErrUnavailable, got %v", err)
			}
			if _, err := b.CreateDelegation(fctx, fDelegation("d-1", "m-3")); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("want ErrUnavailable, got %v", err)
			}
			if !bytes.Equal(before, readState(t, dir)) {
				t.Fatal("state file changed by a failed write")
			}
			noTemps(t, dir)
			if _, err := b.GetGrant(fctx, "g-2"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("memory must not change on failed write: %v", err)
			}

			*fs = faultFS{} // сбой прошёл: backend работает дальше
			if _, err := b.CreateGrant(fctx, fGrant("g-2", "m-2")); err != nil {
				t.Fatalf("backend must recover: %v", err)
			}
			re, err := OpenFile(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := re.GetGrant(fctx, "g-2"); err != nil {
				t.Fatalf("durable after recovery: %v", err)
			}
		})
	}
}

// После rename исход (дошла ли запись до диска) неясен: backend отравляется и отвечает ErrUnavailable.
func TestFile_SyncDirFailurePoisons(t *testing.T) {
	dir := t.TempDir()
	fs := &faultFS{}
	b, err := OpenFile(dir, withFS(fs))
	if err != nil {
		t.Fatal(err)
	}
	fs.syncDirErr = syscall.EIO
	if _, err := b.CreateGrant(fctx, fGrant("g-1", "m-1")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	fs.syncDirErr = nil
	p := fGrant("g-1", "m-1").Principal
	for name, call := range map[string]func() error{
		"get":    func() error { _, e := b.GetGrant(fctx, "g-1"); return e },
		"list":   func() error { _, e := b.ListGrants(fctx, p); return e },
		"create": func() error { _, e := b.CreateGrant(fctx, fGrant("g-2", "m-2")); return e },
		"revoke": func() error { _, e := b.RevokeGrant(fctx, "g-1", 1, "m-3", "ev-3"); return e },
		"deleg":  func() error { _, e := b.CreateDelegation(fctx, fDelegation("d-1", "m-4")); return e },
	} {
		if err := call(); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s on poisoned backend: want ErrUnavailable, got %v", name, err)
		}
	}
	// переоткрытие читает то, что реально лежит на диске: файл целый (старый либо новый)
	re, err := OpenFile(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := re.GetGrant(fctx, "g-1"); err != nil {
		t.Fatalf("rename completed, record must be readable after reopen: %v", err)
	}
}

func TestFile_CancelledContextChangesNothing(t *testing.T) {
	dir := t.TempDir()
	b, _ := OpenFile(dir)
	c, cancel := context.WithCancel(fctx)
	cancel()
	if _, err := b.CreateGrant(c, fGrant("g-1", "m-1")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	if _, err := b.GetGrant(c, "g-1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("read with cancelled ctx: %v", err)
	}
	if data := readState(t, dir); len(data) != 0 {
		t.Fatalf("nothing must be written: %s", data)
	}
	if _, err := b.GetGrant(fctx, "g-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("memory must not change: %v", err)
	}
}

// Повреждённый файл не «лечится», не стирается и не открывается: fail closed.
func TestFile_CorruptStateFailsClosed(t *testing.T) {
	good := func() []byte {
		dir := t.TempDir()
		b, _ := OpenFile(dir)
		g := fGrant("g-1", "m-1")
		g.Constraints = ConstraintSet{AllowedEnvironments: []string{"prod"}, DenyDestructive: true}
		if _, err := b.CreateGrant(fctx, g); err != nil {
			t.Fatal(err)
		}
		if _, err := b.CreateDelegation(fctx, fDelegation("d-1", "m-2")); err != nil {
			t.Fatal(err)
		}
		return readState(t, dir)
	}()
	rep := func(old, new string) []byte {
		if !bytes.Contains(good, []byte(old)) {
			t.Fatalf("fixture lacks %q", old)
		}
		return bytes.Replace(good, []byte(old), []byte(new), 1)
	}
	cases := map[string][]byte{
		"garbage":                  []byte("not json"),
		"truncated":                good[:len(good)/2],
		"trailing data":            append(append([]byte{}, good...), []byte(`{"x":1}`)...),
		"wrong file version":       rep(`"version": 1,`+"\n"+`  "grants"`, `"version": 2,`+"\n"+`  "grants"`),
		"unknown top field":        rep(`"grants"`, `"extra": 1, "grants"`),
		"unknown record field":     rep(`"state": "active"`, `"state": "active", "bonus": true`),
		"unknown constraint field": rep(`"deny_destructive": true`, `"deny_destructive": true, "max_concurrency": 3`),
		"unknown constraints ver":  rep(`"constraints": {`+"\n"+`        "version": 1`, `"constraints": {`+"\n"+`        "version": 2`),
		"zero revision":            rep(`"revision": 1`, `"revision": 0`),
		"unknown state":            rep(`"state": "active"`, `"state": "ghost"`),
		"scope org mismatch":       rep(`"org_id": "org-a",`+"\n"+`        "cluster_ids"`, `"org_id": "org-b",`+"\n"+`        "cluster_ids"`),
		"empty file":               {},
		"null":                     []byte("null"),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, authFileName)
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenFile(dir); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("want ErrUnavailable, got %v", err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(after, content) {
				t.Fatal("corrupt file must be left untouched for forensics")
			}
		})
	}
	// контроль: исходный файл читается
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, authFileName), good, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFile(dir); err != nil {
		t.Fatalf("control file must open: %v", err)
	}
}

func TestFile_DuplicateRecordsRejected(t *testing.T) {
	dir := t.TempDir()
	b, _ := OpenFile(dir)
	b.CreateGrant(fctx, fGrant("g-1", "m-1"))
	data := readState(t, dir)
	start := bytes.Index(data, []byte(`"grants": [`)) + len(`"grants": [`)
	end := bytes.Index(data, []byte("\n  ],\n  \"delegations\""))
	rec := data[start:end]
	dup := append(append(append(append([]byte{}, data[:end]...), ','), rec...), data[end:]...)
	d2 := t.TempDir()
	os.WriteFile(filepath.Join(d2, authFileName), dup, 0o600)
	if _, err := OpenFile(d2); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("duplicate IDs must fail closed: %v", err)
	}
}

func TestFile_StaleTempsRemovedAndPermissions(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, tempPrefix+"123"+tempSuffix)
	other := filepath.Join(dir, "keep.txt")
	os.WriteFile(stale, []byte("junk"), 0o600)
	os.WriteFile(other, []byte("mine"), 0o600)
	b, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale temp must be removed")
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("foreign files must not be touched")
	}
	if _, err := b.CreateGrant(fctx, fGrant("g-1", "m-1")); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(dir, authFileName))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("state file must be 0600: %v %v", st, err)
		}
	}
}

func TestFile_EncodingIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	b, _ := OpenFile(dir)
	g := fGrant("g-1", "m-1")
	g.Actions = []OperationAction{{"b", "y"}, {"a", "x"}}
	g.Targets.ClusterIDs = []string{"c-2", "c-1"}
	b.CreateGrant(fctx, g)
	b.CreateDelegation(fctx, fDelegation("d-1", "m-2"))
	first := readState(t, dir)
	re, _ := OpenFile(dir)
	s1, _ := encodeSnapshot(re.st)
	if !bytes.Equal(first, s1) {
		t.Fatal("encode(decode(file)) must reproduce the file byte-for-byte")
	}
}

// Fuzz: разбор произвольных байт не паникует; принятое состояние переживает round-trip без изменений.
func FuzzDecodeSnapshot(f *testing.F) {
	dir := f.TempDir()
	b, _ := OpenFile(dir)
	b.CreateGrant(fctx, fGrant("g-1", "m-1"))
	b.CreateDelegation(fctx, fDelegation("d-1", "m-2"))
	seed, _ := os.ReadFile(filepath.Join(dir, authFileName))
	f.Add(seed)
	f.Add([]byte(`{"version":1,"grants":[],"delegations":[]}`))
	f.Add([]byte(`{"version":1,"grants":[{"id":"x"}],"delegations":[]}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := decodeSnapshot(data)
		if err != nil {
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("decode error must be ErrUnavailable: %v", err)
			}
			return
		}
		enc, err := encodeSnapshot(s)
		if err != nil {
			t.Fatal(err)
		}
		s2, err := decodeSnapshot(enc)
		if err != nil {
			t.Fatalf("accepted state must round-trip: %v", err)
		}
		enc2, _ := encodeSnapshot(s2)
		if !bytes.Equal(enc, enc2) {
			t.Fatal("round-trip is not stable")
		}
	})
}
