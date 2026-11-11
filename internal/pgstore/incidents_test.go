// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/incident/incidenttest"
	"github.com/24DSO-lab/kubron/internal/secretbox"
)

// Те же контрактные тесты, что и у файловой реализации (нужна KUBRON_TEST_DATABASE_URL).
func TestIncidentBackendContract(t *testing.T) {
	key := keyString("k1")
	incidenttest.Run(t, func(t *testing.T) incident.Backend { return newIncidentBackend(newTestStore(t, key)) })
}

func TestIncidentFragmentsAreEncryptedAtRest(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	b := newIncidentBackend(s)
	sc := incidenttest.Scope
	const text = "the redacted fragment text that must not be readable in the table"
	if err := b.PutFragment(t.Context(), incidenttest.NewFragment(sc, "inc1", "fr1", time.Now(), text)); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	var meta string
	if err := s.pool.QueryRow(t.Context(), `SELECT text_enc, meta::text FROM incident_fragments`).Scan(&raw, &meta); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "redacted fragment") || strings.Contains(meta, "redacted fragment") {
		t.Error("the fragment text is stored in clear")
	}
	// Шифротекст, перенесённый в другую строку, не расшифровывается (AAD).
	if err := b.PutFragment(t.Context(), incidenttest.NewFragment(sc, "inc1", "fr2", time.Now(), "other")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE incident_fragments SET text_enc=$1 WHERE id='fr2'`, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := b.GetFragment(t.Context(), sc, "inc1", "fr2"); !errors.Is(err, secretbox.ErrDecrypt) {
		t.Errorf("a moved ciphertext was accepted: %v", err)
	}
}

// После перешифровки фрагменты читаются новым ключом; проверка ключей видит таблицу.
func TestIncidentFragmentsSurviveRekey(t *testing.T) {
	old, cur := keyString("old"), keyString("cur")
	s := newTestStore(t, old)
	b := newIncidentBackend(s)
	sc := incidenttest.Scope
	if err := b.PutFragment(t.Context(), incidenttest.NewFragment(sc, "inc1", "fr1", time.Now(), "before the rotation")); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckIncidentKeys(t.Context()); err != nil {
		t.Fatal(err)
	}
	keys, err := secretbox.ParseKeyring(cur, old)
	if err != nil {
		t.Fatal(err)
	}
	s.keys = keys
	n, err := s.RekeyIncidentFragments(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("rekey = %d, %v", n, err)
	}
	var keyID string
	if err := s.pool.QueryRow(t.Context(), `SELECT key_id FROM incident_fragments`).Scan(&keyID); err != nil || keyID != keys.CurrentID() {
		t.Errorf("key id = %q, %v", keyID, err)
	}
	f, err := b.GetFragment(t.Context(), sc, "inc1", "fr1")
	if err != nil || f.Text != "before the rotation" {
		t.Fatalf("after rekey: %+v %v", f, err)
	}
	if n, _ := s.RekeyIncidentFragments(t.Context()); n != 0 {
		t.Errorf("second rekey changed %d rows", n)
	}
	// Без старого ключа в связке ключей проверка при старте падает ДО чтения данных.
	only, _ := secretbox.ParseKeyring(keyString("zzz"))
	s.keys = only
	if err := s.CheckIncidentKeys(t.Context()); !errors.Is(err, secretbox.ErrUnknownKey) {
		t.Errorf("check with a foreign key ring: %v", err)
	}
}
