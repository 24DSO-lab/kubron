// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/24DSO-labs/kubron/internal/secretbox"
)

// MasterKeyStore is a source of the master key (KEK).
type MasterKeyStore interface {
	// Name is a short stable identifier: "env", "keyring" or "file".
	Name() string
	// Available checks that the store works on this machine. It must not create or change anything.
	Available(ctx context.Context) error
	// Load returns the key ring. ErrNoMasterKey means "works, but no key yet".
	Load(ctx context.Context) (*secretbox.Keyring, error)
	// Save stores a new key. It never overwrites an existing one (ErrKeyExists).
	Save(ctx context.Context, key secretbox.Key) error
}

// GenerateKey creates a random master key with a random identifier.
func GenerateKey() (secretbox.Key, error) {
	var idRaw [4]byte
	k := secretbox.Key{}
	if _, err := rand.Read(idRaw[:]); err != nil {
		return k, fmt.Errorf("random source unavailable: %w", err)
	}
	if _, err := rand.Read(k.Bytes[:]); err != nil {
		return k, fmt.Errorf("random source unavailable: %w", err)
	}
	k.ID = "local-" + hex.EncodeToString(idRaw[:])
	return k, nil
}

// FormatKey renders a key in the id:base64 form understood by secretbox.ParseKey.
func FormatKey(k secretbox.Key) string {
	return k.ID + ":" + base64.StdEncoding.EncodeToString(k.Bytes[:])
}

// EnvStore reads keys given by the operator (KUBRON_SECRET_KEY and KUBRON_SECRET_KEY_PREVIOUS).
type EnvStore struct {
	Current  string
	Previous []string
}

func (EnvStore) Name() string                    { return "env" }
func (EnvStore) Available(context.Context) error { return nil }

// Load parses the configured keys.
func (s EnvStore) Load(context.Context) (*secretbox.Keyring, error) {
	return secretbox.ParseKeyring(s.Current, s.Previous...)
}

// Save is not supported: the operator owns the key.
func (EnvStore) Save(context.Context, secretbox.Key) error { return ErrReadOnlyKeyStore }

// FileStore keeps one key in a file with mode 0600. It is the fallback on desktops without a system
// keyring and the automatic key storage of a server without PostgreSQL (decision D-02).
type FileStore struct {
	Path   string
	Logger *slog.Logger
}

func (FileStore) Name() string { return "file" }

func (s FileStore) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// Available checks that the directory of the key file exists or can be created.
func (s FileStore) Available(context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("%w: %v", ErrKeyStoreUnavailable, err)
	}
	return nil
}

// Load reads the key file. Too wide permissions are tightened to 0600; if that is impossible the key is
// not used.
func (s FileStore) Load(context.Context) (*secretbox.Keyring, error) {
	info, err := os.Stat(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoMasterKey
	}
	if err != nil {
		return nil, fmt.Errorf("read master key file: %w", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(s.Path, 0o600); err != nil {
			return nil, ErrKeyFileInsecure
		}
		s.logger().Warn("master key file permissions were too wide and have been set to 0600", "path", s.Path)
	}
	raw, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, fmt.Errorf("read master key file: %w", err)
	}
	return secretbox.ParseKeyring(strings.TrimSpace(string(raw)))
}

// Save creates the key file exclusively (0600). An existing file is never replaced.
func (s FileStore) Save(_ context.Context, key secretbox.Key) (err error) {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("%w: %v", ErrKeyStoreUnavailable, err)
	}
	f, err := os.OpenFile(s.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return ErrKeyExists
	}
	if err != nil {
		return fmt.Errorf("write master key file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(s.Path) // never leave a half-written key behind
		}
	}()
	if _, err = f.WriteString(FormatKey(key) + "\n"); err != nil {
		_ = f.Close()
		return fmt.Errorf("write master key file: %w", err)
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("write master key file: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("write master key file: %w", err)
	}
	return nil
}
