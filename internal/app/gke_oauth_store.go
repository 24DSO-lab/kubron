// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/24DSO-labs/kubron/internal/credential/providers/gke"
	"github.com/24DSO-labs/kubron/internal/secrets"
)

// Errors of the desktop settings of the Google OAuth client. The texts are fixed and never hold a value that the
// user typed.
var (
	errGKEOAuthInvalid     = errors.New("the Google OAuth client id or secret is not valid")
	errGKEOAuthUnavailable = errors.New("the storage for the Google OAuth client is not available")
	errGKEOAuthUnreadable  = errors.New("the saved Google OAuth client cannot be read")
)

// gkeOAuthAAD binds the sealed secret to its purpose: the same blob cannot be opened as anything else.
var gkeOAuthAAD = []byte("kubron/gke-oauth/v1")

// GKEOAuthStore keeps the Google OAuth client of the desktop (the one used for the sign-in of a Google user to
// GKE) in one file of the data directory. The client id is stored as it is; the client secret is sealed with the
// envelope of the master key, like every saved sign-in. The file is written with mode 0600 and replaced
// atomically. The secret is never returned by any method: the caller can learn only that one is set.
type GKEOAuthStore struct {
	mu   sync.Mutex
	path string
	env  *secrets.Envelope
}

// NewGKEOAuthStore returns the store for the file at path.
func NewGKEOAuthStore(path string, env *secrets.Envelope) *GKEOAuthStore {
	return &GKEOAuthStore{path: path, env: env}
}

type gkeOAuthFile struct {
	V        int    `json:"v"`
	ClientID string `json:"clientId"`
	Secret   string `json:"secret,omitempty"` // base64 of the sealed envelope
}

// Load returns the saved client, or nil if there is none.
func (s *GKEOAuthStore) Load() (*gke.UserOAuth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *GKEOAuthStore) load() (*gke.UserOAuth, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errGKEOAuthUnreadable
	}
	var f gkeOAuthFile
	if json.Unmarshal(raw, &f) != nil || f.V != 1 || f.ClientID == "" {
		return nil, errGKEOAuthUnreadable
	}
	u := &gke.UserOAuth{ClientID: f.ClientID}
	if f.Secret != "" {
		blob, err := base64.StdEncoding.DecodeString(f.Secret)
		if err != nil {
			return nil, errGKEOAuthUnreadable
		}
		plain, err := s.env.Open(blob, gkeOAuthAAD)
		if err != nil {
			return nil, errGKEOAuthUnreadable
		}
		u.ClientSecret = string(plain)
	}
	return u, nil
}

// Save stores the client. An empty secret keeps the saved one if the client id is the same, so that the user does
// not have to type it again; for another client id an empty secret means "none".
func (s *GKEOAuthStore) Save(clientID, clientSecret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.env == nil {
		return errGKEOAuthUnavailable
	}
	if clientSecret == "" {
		if old, err := s.load(); err == nil && old != nil && old.ClientID == clientID {
			clientSecret = old.ClientSecret
		}
	}
	// The same checks as the provider makes when it is built; the redirect address is not part of this setting.
	if _, err := gke.NewWithUserOAuth(gke.UserOAuth{ClientID: clientID, ClientSecret: clientSecret}); err != nil {
		return errGKEOAuthInvalid
	}
	f := gkeOAuthFile{V: 1, ClientID: clientID}
	if clientSecret != "" {
		blob, _, err := s.env.Seal([]byte(clientSecret), gkeOAuthAAD)
		if err != nil {
			return errGKEOAuthUnavailable
		}
		f.Secret = base64.StdEncoding.EncodeToString(blob)
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return errGKEOAuthUnavailable
	}
	return writeFileAtomic(s.path, raw)
}

// Clear removes the saved client. A missing file is not an error.
func (s *GKEOAuthStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errGKEOAuthUnavailable
	}
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errGKEOAuthUnavailable
	}
	tmp, err := os.CreateTemp(dir, ".gke-oauth-*")
	if err != nil {
		return errGKEOAuthUnavailable
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return errGKEOAuthUnavailable
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return errGKEOAuthUnavailable
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return errGKEOAuthUnavailable
	}
	if err := tmp.Close(); err != nil {
		return errGKEOAuthUnavailable
	}
	if err := os.Rename(name, path); err != nil {
		return errGKEOAuthUnavailable
	}
	ok = true
	return nil
}

// GKEOAuthStatus is what the settings screen learns about the Google OAuth client of the desktop. The client
// secret is never part of it.
type GKEOAuthStatus struct {
	Configured bool   `json:"configured"`
	ClientID   string `json:"clientId"`
	SecretSet  bool   `json:"secretSet"`
	// RestartRequired: the saved client differs from the one the running sign-in broker was built with. The broker
	// is built at start, so a change takes effect after a restart.
	RestartRequired bool `json:"restartRequired"`
}

// gkeOAuthStatus describes the saved client for the settings screen; active is the client id the running broker was
// built with ("" if none).
func gkeOAuthStatus(saved *gke.UserOAuth, active string) GKEOAuthStatus {
	st := GKEOAuthStatus{}
	if saved != nil {
		st.Configured, st.ClientID, st.SecretSet = true, saved.ClientID, saved.ClientSecret != ""
	}
	st.RestartRequired = st.ClientID != active
	return st
}
