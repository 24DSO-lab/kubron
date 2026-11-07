// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/credential/providers/gke"
	"github.com/24DSO-lab/kubron/internal/secretbox"
	"github.com/24DSO-lab/kubron/internal/secrets"
)

const (
	storeID     = "1-a.apps.googleusercontent.com"
	storeSecret = "GOCSPX-store-secret-canary-4411"
)

func testEnvelope(t *testing.T) *secrets.Envelope {
	t.Helper()
	key, err := secrets.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	kr, err := secretbox.NewKeyring(key)
	if err != nil {
		t.Fatal(err)
	}
	env, err := secrets.NewEnvelope(kr)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func newTestStore(t *testing.T) (*GKEOAuthStore, string, *secrets.Envelope) {
	t.Helper()
	env := testEnvelope(t)
	path := filepath.Join(t.TempDir(), "sub", "gke-oauth.json")
	return NewGKEOAuthStore(path, env), path, env
}

func TestGKEOAuthStoreRoundTripSealsTheSecret(t *testing.T) {
	s, path, _ := newTestStore(t)
	if got, err := s.Load(); got != nil || err != nil {
		t.Fatalf("empty store: %v %v", got, err)
	}
	if err := s.Save(storeID, storeSecret); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(storeSecret)) || bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString([]byte(storeSecret)))) {
		t.Fatal("the secret is in the file as it is")
	}
	if !bytes.Contains(raw, []byte(storeID)) {
		t.Fatal("the client id is stored as it is")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v", st.Mode().Perm())
		}
	}
	got, err := s.Load()
	if err != nil || got == nil || got.ClientID != storeID || got.ClientSecret != storeSecret || got.WebRedirectURL != "" {
		t.Fatalf("loaded = %v %v", got, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temporary files are left behind: %v", entries)
	}
}

func TestGKEOAuthStoreEmptySecretKeepsTheSavedOneForTheSameClient(t *testing.T) {
	s, _, _ := newTestStore(t)
	if err := s.Save(storeID, storeSecret); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(storeID, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Load(); got == nil || got.ClientSecret != storeSecret {
		t.Fatal("the secret must be kept for the same client id")
	}
	if err := s.Save("2-b.apps.googleusercontent.com", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Load(); got == nil || got.ClientID != "2-b.apps.googleusercontent.com" || got.ClientSecret != "" {
		t.Fatalf("another client must not inherit the secret: %v", got)
	}
}

func TestGKEOAuthStoreRefusesInvalidValuesWithoutEchoingThem(t *testing.T) {
	s, path, _ := newTestStore(t)
	for name, tc := range map[string][2]string{
		"empty id":       {"", storeSecret},
		"id with space":  {"a b", storeSecret},
		"secret newline": {storeID, "x\n" + storeSecret},
		"id too long":    {strings.Repeat("a", 300), "x"},
	} {
		err := s.Save(tc[0], tc[1])
		if !errors.Is(err, errGKEOAuthInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
		if err != nil && (strings.Contains(err.Error(), storeSecret) || strings.Contains(err.Error(), tc[0]+"x")) {
			t.Errorf("%s: the error echoes the input", name)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("nothing may be written for invalid values")
	}
}

func TestGKEOAuthStoreUnreadableData(t *testing.T) {
	s, path, env := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(v any) {
		b, _ := json.Marshal(v)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A blob sealed for another purpose cannot be opened as the client secret.
	other, _, err := env.Seal([]byte(storeSecret), []byte("another purpose"))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(){
		"not JSON":      func() { _ = os.WriteFile(path, []byte("{"), 0o600) },
		"no id":         func() { write(map[string]any{"v": 1}) },
		"wrong version": func() { write(map[string]any{"v": 2, "clientId": storeID}) },
		"bad base64":    func() { write(map[string]any{"v": 1, "clientId": storeID, "secret": "%%%"}) },
		"not a blob": func() {
			write(map[string]any{"v": 1, "clientId": storeID, "secret": base64.StdEncoding.EncodeToString([]byte(storeSecret))})
		},
		"other purpose": func() {
			write(map[string]any{"v": 1, "clientId": storeID, "secret": base64.StdEncoding.EncodeToString(other)})
		},
	}
	for name, setup := range cases {
		setup()
		got, err := s.Load()
		if got != nil || !errors.Is(err, errGKEOAuthUnreadable) {
			t.Errorf("%s: %v %v", name, got, err)
		}
		if err != nil && strings.Contains(err.Error(), storeSecret) {
			t.Errorf("%s: the error echoes the secret", name)
		}
	}
	// Sealed with another master key: also unreadable.
	s2 := NewGKEOAuthStore(path, testEnvelope(t))
	if err := s.Save(storeID, storeSecret); err != nil {
		t.Fatal(err)
	}
	if got, err := s2.Load(); got != nil || !errors.Is(err, errGKEOAuthUnreadable) {
		t.Fatalf("another master key: %v %v", got, err)
	}
}

func TestGKEOAuthStoreClear(t *testing.T) {
	s, _, _ := newTestStore(t)
	if err := s.Clear(); err != nil {
		t.Fatalf("clearing an empty store: %v", err)
	}
	_ = s.Save(storeID, storeSecret)
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Load(); got != nil || err != nil {
		t.Fatalf("after Clear: %v %v", got, err)
	}
}

func TestGKEOAuthStoreWithoutEnvelope(t *testing.T) {
	s := NewGKEOAuthStore(filepath.Join(t.TempDir(), "x.json"), nil)
	if err := s.Save(storeID, storeSecret); !errors.Is(err, errGKEOAuthUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestGKEOAuthStatusNeverCarriesTheSecret(t *testing.T) {
	st := gkeOAuthStatus(&gke.UserOAuth{ClientID: storeID, ClientSecret: storeSecret}, storeID)
	if !st.Configured || st.ClientID != storeID || !st.SecretSet || st.RestartRequired {
		t.Fatalf("status = %+v", st)
	}
	b, _ := json.Marshal(st)
	if strings.Contains(string(b), storeSecret) {
		t.Fatal("the secret is in the status")
	}
	if st := gkeOAuthStatus(&gke.UserOAuth{ClientID: storeID}, ""); !st.RestartRequired || st.SecretSet {
		t.Fatalf("status = %+v", st)
	}
	if st := gkeOAuthStatus(nil, ""); st.Configured || st.RestartRequired {
		t.Fatalf("status = %+v", st)
	}
	if st := gkeOAuthStatus(nil, storeID); !st.RestartRequired {
		t.Fatal("removing the client of a running broker needs a restart")
	}
}
