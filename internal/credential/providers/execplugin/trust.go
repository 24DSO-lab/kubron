// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package execplugin

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// TrustFileName is the file in Env.DataDir that holds the "always" decisions.
const TrustFileName = "exec-trust.json"

const trustVersion = 1

// blockKey is the identity of an exec block for confirmation: sha256 over (command, args, env, apiVersion,
// clusterId). Every part is length-prefixed, so no two different blocks share a key by concatenation.
func blockKey(x *credential.ExecSpec, clusterID string) string {
	h := sha256.New()
	put := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	put(x.Command)
	put(strconv.Itoa(len(x.Args)))
	for _, a := range x.Args {
		put(a)
	}
	names := make([]string, 0, len(x.Env))
	for k := range x.Env {
		names = append(names, k)
	}
	sort.Strings(names)
	put(strconv.Itoa(len(names)))
	for _, k := range names {
		put(k)
		put(x.Env[k])
	}
	put(x.APIVersion)
	put(clusterID)
	return hex.EncodeToString(h.Sum(nil))
}

type trustFile struct {
	Version int                   `json:"version"`
	Trusted map[string]trustEntry `json:"trusted"`
}

type trustEntry struct {
	At time.Time `json:"at"`
}

// trustStore reads and writes the trust file. It holds no cache: the file is the single source of truth, so
// a deleted file immediately withdraws every "always".
type trustStore struct {
	mu  sync.Mutex
	now func() time.Time
}

func (s *trustStore) path(dir string) string { return filepath.Join(dir, TrustFileName) }

// load returns the stored keys. A missing, unreadable or malformed file means "nothing is trusted".
func (s *trustStore) load(dir string) trustFile {
	empty := trustFile{Version: trustVersion, Trusted: map[string]trustEntry{}}
	if dir == "" {
		return empty
	}
	raw, err := os.ReadFile(s.path(dir))
	if err != nil {
		return empty
	}
	var f trustFile
	if err := json.Unmarshal(raw, &f); err != nil || f.Version != trustVersion || f.Trusted == nil {
		return empty
	}
	return f
}

func (s *trustStore) isTrusted(dir, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.load(dir).Trusted[key]
	return ok
}

// add records the key atomically (temp file in the same directory, then rename).
func (s *trustStore) add(dir, key string) error {
	if dir == "" {
		return errNoTrustDir
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.load(dir)
	f.Trusted[key] = trustEntry{At: s.now().UTC()}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return errTrustSave
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errTrustSave
	}
	tmp, err := os.CreateTemp(dir, TrustFileName+".*")
	if err != nil {
		return errTrustSave
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, os.ErrPermission) {
		_ = tmp.Close()
		return errTrustSave
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return errTrustSave
	}
	if err := tmp.Close(); err != nil {
		return errTrustSave
	}
	if err := os.Rename(name, s.path(dir)); err != nil {
		return errTrustSave
	}
	ok = true
	return nil
}
