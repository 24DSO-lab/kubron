// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	gokeyring "github.com/zalando/go-keyring"
)

// fakeKeyring is an in-memory OS keyring.
type fakeKeyring struct {
	mu   sync.Mutex
	data map[string]string
	fail error // returned by every call when set
}

func (f *fakeKeyring) Get(s, u string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return "", f.fail
	}
	v, ok := f.data[s+"/"+u]
	if !ok {
		return "", gokeyring.ErrNotFound
	}
	return v, nil
}
func (f *fakeKeyring) Set(s, u, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	if f.data == nil {
		f.data = map[string]string{}
	}
	f.data[s+"/"+u] = v
	return nil
}
func (f *fakeKeyring) Delete(s, u string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, s+"/"+u)
	return nil
}

func TestFileStoreCreateLoad(t *testing.T) {
	ctx := context.Background()
	st := FileStore{Path: filepath.Join(t.TempDir(), "sub", "k.key")}
	if _, err := st.Load(ctx); !errors.Is(err, ErrNoMasterKey) {
		t.Fatalf("Load on empty = %v", err)
	}
	key, _ := GenerateKey()
	if err := st.Save(ctx, key); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(st.Path)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, err = %v", fi.Mode().Perm(), err)
		}
	}
	kr, err := st.Load(ctx)
	if err != nil || kr.CurrentID() != key.ID {
		t.Fatalf("Load = %v, %v", kr, err)
	}
	if err := st.Save(ctx, key); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("second Save = %v, want ErrKeyExists", err)
	}
}

func TestFileStoreTightensPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	ctx := context.Background()
	st := FileStore{Path: filepath.Join(t.TempDir(), "k.key")}
	key, _ := GenerateKey()
	if err := st.Save(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(st.Path, 0o644); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	st.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	if _, err := st.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(st.Path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
	if !strings.Contains(logs.String(), "permissions") {
		t.Fatal("the tightening is not reported")
	}
}

func TestFileStoreCorruptKeyFile(t *testing.T) {
	st := FileStore{Path: filepath.Join(t.TempDir(), "k.key")}
	if err := os.WriteFile(st.Path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Load(context.Background()); err == nil || errors.Is(err, ErrNoMasterKey) {
		t.Fatalf("a corrupt key file must be an error, got %v", err)
	}
}

func TestFileStoreConcurrentCreateOneWinner(t *testing.T) {
	ctx := context.Background()
	st := FileStore{Path: filepath.Join(t.TempDir(), "k.key")}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			k, _ := GenerateKey()
			results <- st.Save(ctx, k)
		}()
	}
	wg.Wait()
	close(results)
	ok := 0
	for err := range results {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ErrKeyExists):
			t.Fatalf("unexpected error %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d writers succeeded, want exactly 1", ok)
	}
	if _, err := st.Load(ctx); err != nil {
		t.Fatalf("the key file is unreadable after the race: %v", err)
	}
}

func TestKeyringStore(t *testing.T) {
	ctx := context.Background()
	fk := &fakeKeyring{}
	st := &KeyringStore{Service: "kubron-test", User: "k", backend: fk}
	if err := st.Available(ctx); err != nil {
		t.Fatalf("Available = %v", err)
	}
	if _, err := st.Load(ctx); !errors.Is(err, ErrNoMasterKey) {
		t.Fatalf("Load = %v", err)
	}
	key, _ := GenerateKey()
	if err := st.Save(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(ctx, key); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("second Save = %v", err)
	}
	if kr, err := st.Load(ctx); err != nil || kr.CurrentID() != key.ID {
		t.Fatalf("Load = %v, %v", kr, err)
	}
}

func TestKeyringStoreUnavailable(t *testing.T) {
	ctx := context.Background()
	fk := &fakeKeyring{fail: errors.New("dbus: no secret service")}
	st := &KeyringStore{Service: "kubron-test", User: "k", backend: fk}
	if err := st.Available(ctx); !errors.Is(err, ErrKeyStoreUnavailable) {
		t.Fatalf("Available = %v", err)
	}
	if _, err := st.Load(ctx); !errors.Is(err, ErrKeyStoreUnavailable) {
		t.Fatalf("Load = %v", err)
	}
	k, _ := GenerateKey()
	if err := st.Save(ctx, k); !errors.Is(err, ErrKeyStoreUnavailable) {
		t.Fatalf("Save = %v", err)
	}
}

func TestEnvStore(t *testing.T) {
	ctx := context.Background()
	k, _ := GenerateKey()
	k2, _ := GenerateKey()
	st := EnvStore{Current: FormatKey(k), Previous: []string{FormatKey(k2)}}
	kr, err := st.Load(ctx)
	if err != nil || kr.CurrentID() != k.ID {
		t.Fatalf("Load = %v, %v", kr, err)
	}
	if err := st.Save(ctx, k); !errors.Is(err, ErrReadOnlyKeyStore) {
		t.Fatalf("Save = %v", err)
	}
	if _, err := (EnvStore{Current: "bad"}).Load(ctx); err == nil {
		t.Fatal("a malformed key is accepted")
	}
}
