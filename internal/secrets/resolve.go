// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"

	"github.com/24DSO-labs/kubron/internal/secretbox"
)

// Mode is the deployment mode that decides where the master key lives.
type Mode int

const (
	// ModeDesktop: system keyring, fallback to a 0600 file.
	ModeDesktop Mode = iota + 1
	// ModeServerPG: web server with PostgreSQL; KUBRON_SECRET_KEY is mandatory.
	ModeServerPG
	// ModeServerLocal: web server without PostgreSQL; KUBRON_SECRET_KEY or an automatically created key file.
	ModeServerLocal
)

// Warning codes. They are identifiers, not texts: the UI resolves them to localized messages.
const (
	// WarnKeyFileFallback: the system keyring is unavailable, the key is kept in a file.
	WarnKeyFileFallback = "key-file-fallback"
	// WarnKeyFileGenerated: a key file was created automatically (server without PostgreSQL).
	WarnKeyFileGenerated = "key-file-generated"
)

// Source names of Selection.Source.
const (
	SourceEnv     = "env"
	SourceKeyring = "keyring"
	SourceFile    = "file"
)

// Options configure Resolve.
type Options struct {
	Mode        Mode
	EnvKey      string   // KUBRON_SECRET_KEY
	EnvPrevious []string // KUBRON_SECRET_KEY_PREVIOUS
	DataDir     string
	// Keyring is the desktop keyring store; nil means the system keyring under service "kubron".
	Keyring MasterKeyStore
	Logger  *slog.Logger
}

// Selection is the outcome of Resolve.
type Selection struct {
	Keyring   *secretbox.Keyring
	Store     MasterKeyStore
	Source    string
	Generated bool // a new key was created during this call
	Warnings  []string
}

// Resolve picks the master key source, checks that it works, and creates a key where the policy allows it.
// A fallback to a file is never silent: it is logged and reported in Selection.Warnings.
func Resolve(ctx context.Context, o Options) (Selection, error) {
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}

	if o.EnvKey != "" {
		st := EnvStore{Current: o.EnvKey, Previous: o.EnvPrevious}
		kr, err := st.Load(ctx)
		if err != nil {
			return Selection{}, err
		}
		return Selection{Keyring: kr, Store: st, Source: SourceEnv}, nil
	}

	switch o.Mode {
	case ModeServerPG:
		return Selection{}, ErrSecretKeyRequired
	case ModeServerLocal:
		st := FileStore{Path: filepath.Join(o.DataDir, "secret.key"), Logger: log}
		sel, err := loadOrCreate(ctx, st, SourceFile)
		if err != nil {
			return Selection{}, err
		}
		if sel.Generated {
			sel.Warnings = append(sel.Warnings, WarnKeyFileGenerated)
			log.Warn("a master encryption key file was created; back it up, without it stored credentials cannot be read",
				"path", st.Path)
		}
		return sel, nil
	case ModeDesktop:
		return resolveDesktop(ctx, o, log)
	}
	return Selection{}, ErrNoMasterKey
}

func resolveDesktop(ctx context.Context, o Options, log *slog.Logger) (Selection, error) {
	var ks MasterKeyStore = o.Keyring
	if ks == nil {
		ks = NewKeyringStore("kubron", "master-key")
	}
	if err := ks.Available(ctx); err == nil {
		sel, err := loadOrCreate(ctx, ks, SourceKeyring)
		if err == nil {
			return sel, nil
		}
		if !errors.Is(err, ErrKeyStoreUnavailable) {
			return Selection{}, err
		}
		log.Warn("the system keyring failed", "error", sanitizeID(err.Error()))
	} else {
		log.Warn("the system keyring is unavailable", "error", sanitizeID(err.Error()))
	}

	st := FileStore{Path: filepath.Join(o.DataDir, "master.key"), Logger: log}
	sel, err := loadOrCreate(ctx, st, SourceFile)
	if err != nil {
		return Selection{}, err
	}
	sel.Warnings = append(sel.Warnings, WarnKeyFileFallback)
	log.Warn("the master encryption key is kept in a file because the system keyring is unavailable", "path", st.Path)
	return sel, nil
}

// loadOrCreate loads the key, creating it on first use. If two processes race to create it, the loser
// loads the winner's key.
func loadOrCreate(ctx context.Context, st MasterKeyStore, source string) (Selection, error) {
	kr, err := st.Load(ctx)
	if err == nil {
		return Selection{Keyring: kr, Store: st, Source: source}, nil
	}
	if !errors.Is(err, ErrNoMasterKey) {
		return Selection{}, err
	}
	key, err := GenerateKey()
	if err != nil {
		return Selection{}, err
	}
	switch err := st.Save(ctx, key); {
	case err == nil:
	case errors.Is(err, ErrKeyExists):
		kr, lerr := st.Load(ctx)
		if lerr != nil {
			return Selection{}, lerr
		}
		return Selection{Keyring: kr, Store: st, Source: source}, nil
	default:
		return Selection{}, err
	}
	kr, err = secretbox.NewKeyring(key)
	if err != nil {
		return Selection{}, err
	}
	return Selection{Keyring: kr, Store: st, Source: source, Generated: true}, nil
}
