// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func masterKeyString(id string, fill byte) string {
	raw := bytes.Repeat([]byte{fill}, 32)
	return id + ":" + base64.StdEncoding.EncodeToString(raw)
}

func TestDerivePreviewKey(t *testing.T) {
	t.Parallel()
	master := bytes.Repeat([]byte{7}, 32)
	a, err := derivePreviewKey(master)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := derivePreviewKey(master)
	if !bytes.Equal(a, b) || len(a) != previewKeySize {
		t.Fatalf("derivation must be deterministic and %d bytes long", previewKeySize)
	}
	if bytes.Equal(a, master) {
		t.Fatal("derived key must differ from the master key")
	}
	other, _ := derivePreviewKey(bytes.Repeat([]byte{8}, 32))
	if bytes.Equal(a, other) {
		t.Fatal("different master keys must give different preview keys")
	}
	if _, err := derivePreviewKey(nil); err == nil {
		t.Fatal("empty master key must be rejected")
	}
}

// Две «реплики» с одним мастер-ключом получают один ключ предпросмотра; идентификатор ключа на него не влияет.
func TestSetBaselinePreviewKey(t *testing.T) {
	t.Cleanup(func() { derivedPreviewKey.Store(nil) })

	derivedPreviewKey.Store(nil)
	if err := SetBaselinePreviewKey("  "); err != nil {
		t.Fatalf("empty key keeps the process random key: %v", err)
	}
	r1 := baselinePreviewKey()
	if !bytes.Equal(r1, baselinePreviewKey()) || len(r1) == 0 {
		t.Fatal("random fallback key must be stable within a process")
	}

	if err := SetBaselinePreviewKey(masterKeyString("k1", 1)); err != nil {
		t.Fatal(err)
	}
	first := bytes.Clone(baselinePreviewKey())
	if bytes.Equal(first, r1) {
		t.Fatal("derived key must replace the random one")
	}
	derivedPreviewKey.Store(nil)
	if err := SetBaselinePreviewKey(masterKeyString("k1", 1)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, baselinePreviewKey()) {
		t.Fatal("replicas with the same master key must share the preview key")
	}
	if err := SetBaselinePreviewKey(masterKeyString("k2", 2)); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, baselinePreviewKey()) {
		t.Fatal("a rotated master key must change the preview key")
	}
	if err := SetBaselinePreviewKey("not-a-key"); err == nil {
		t.Fatal("a malformed master key must be an error")
	}
}
