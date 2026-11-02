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
	"slices"
	"strings"
	"testing"
)

func TestResolveEnvWinsEverywhere(t *testing.T) {
	k, _ := GenerateKey()
	for _, m := range []Mode{ModeDesktop, ModeServerPG, ModeServerLocal} {
		sel, err := Resolve(context.Background(), Options{Mode: m, EnvKey: FormatKey(k), DataDir: t.TempDir(),
			Keyring: &KeyringStore{backend: &fakeKeyring{}}})
		if err != nil || sel.Source != SourceEnv || sel.Keyring.CurrentID() != k.ID || len(sel.Warnings) != 0 {
			t.Fatalf("mode %d: %+v, %v", m, sel, err)
		}
	}
}

func TestResolveServerPGRequiresKey(t *testing.T) {
	_, err := Resolve(context.Background(), Options{Mode: ModeServerPG, DataDir: t.TempDir()})
	if !errors.Is(err, ErrSecretKeyRequired) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveServerPGBadEnvKeyFails(t *testing.T) {
	_, err := Resolve(context.Background(), Options{Mode: ModeServerPG, EnvKey: "oops"})
	if err == nil {
		t.Fatal("a malformed KUBRON_SECRET_KEY is accepted")
	}
}

// D-02: a server without PostgreSQL creates the key file once and then keeps using it.
func TestResolveServerLocalAutogenerates(t *testing.T) {
	dir := t.TempDir()
	var logs bytes.Buffer
	o := Options{Mode: ModeServerLocal, DataDir: dir, Logger: slog.New(slog.NewTextHandler(&logs, nil))}
	first, err := Resolve(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Generated || first.Source != SourceFile || !slices.Contains(first.Warnings, WarnKeyFileGenerated) {
		t.Fatalf("first = %+v", first)
	}
	if !strings.Contains(logs.String(), "key file was created") {
		t.Fatal("the automatic key creation is not logged")
	}
	if _, err := os.Stat(filepath.Join(dir, "secret.key")); err != nil {
		t.Fatal(err)
	}
	second, err := Resolve(context.Background(), o)
	if err != nil || second.Generated || second.Keyring.CurrentID() != first.Keyring.CurrentID() {
		t.Fatalf("second = %+v, %v", second, err)
	}
}

func TestResolveDesktopUsesKeyring(t *testing.T) {
	fk := &fakeKeyring{}
	o := Options{Mode: ModeDesktop, DataDir: t.TempDir(), Keyring: &KeyringStore{Service: "s", User: "u", backend: fk}}
	first, err := Resolve(context.Background(), o)
	if err != nil || first.Source != SourceKeyring || !first.Generated || len(first.Warnings) != 0 {
		t.Fatalf("first = %+v, %v", first, err)
	}
	second, err := Resolve(context.Background(), o)
	if err != nil || second.Generated || second.Keyring.CurrentID() != first.Keyring.CurrentID() {
		t.Fatalf("second = %+v, %v", second, err)
	}
	if _, err := os.Stat(filepath.Join(o.DataDir, "master.key")); err == nil {
		t.Fatal("a key file was created although the keyring works")
	}
}

// The fallback to a file must not be silent: warning code in the result and a log line.
func TestResolveDesktopFallbackIsNotSilent(t *testing.T) {
	fk := &fakeKeyring{fail: errors.New("no secret service")}
	var logs bytes.Buffer
	o := Options{Mode: ModeDesktop, DataDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		Keyring: &KeyringStore{Service: "s", User: "u", backend: fk}}
	sel, err := Resolve(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if sel.Source != SourceFile || !slices.Contains(sel.Warnings, WarnKeyFileFallback) {
		t.Fatalf("sel = %+v", sel)
	}
	if !strings.Contains(logs.String(), "kept in a file") {
		t.Fatalf("the fallback is not logged: %q", logs.String())
	}
	if fi, err := os.Stat(filepath.Join(o.DataDir, "master.key")); err != nil || fi.Size() == 0 {
		t.Fatalf("key file: %v", err)
	}
}

// Master key bytes never reach the logs.
func TestResolveDoesNotLogKeyMaterial(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fk := &fakeKeyring{fail: errors.New("boom")}
	sel, err := Resolve(context.Background(), Options{Mode: ModeDesktop, DataDir: t.TempDir(), Logger: log,
		Keyring: &KeyringStore{backend: fk}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(filepath.Dir(sel.Store.(FileStore).Path), "master.key"))
	b64 := strings.TrimSpace(string(raw))
	if i := strings.IndexByte(b64, ':'); i >= 0 {
		b64 = b64[i+1:]
	}
	if b64 == "" || strings.Contains(logs.String(), b64) {
		t.Fatal("key material found in the log")
	}
}

func TestResolveNoMode(t *testing.T) {
	if _, err := Resolve(context.Background(), Options{}); !errors.Is(err, ErrNoMasterKey) {
		t.Fatalf("err = %v", err)
	}
}
