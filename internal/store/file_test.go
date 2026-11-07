// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
)

// Файловое хранилище должно давать рабочие реализации всех интерфейсов.
func TestFileStoreBackendsWork(t *testing.T) {
	dir := t.TempDir()
	s := NewFile(dir)

	// пользователи: пустое хранилище → сохранение → чтение
	svc, err := auth.NewServiceWithBackend(s.Users())
	if err != nil {
		t.Fatal(err)
	}
	if svc.Status().NeedsSetup != true {
		t.Fatal("an empty store must require initial setup")
	}

	// журнал: запись и выборка
	l := audit.New(s.Audit())
	l.Record(audit.Event{User: "u", Action: "Test", Result: audit.ResultOK})
	page, err := l.Query(audit.Filter{})
	if err != nil || len(page.Events) != 1 || page.Events[0].Action != "Test" {
		t.Fatalf("journal: %+v %v", page, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "audit.log")); err != nil {
		t.Fatalf("audit.log was not created: %v", err)
	}

	// The web OIDC public client and its issuer are persisted by the file backend.
	if got, err := s.WebOIDC(); err != nil || got != (WebOIDC{}) {
		t.Fatalf("empty web OIDC client: %+v %v", got, err)
	}
	webWant := WebOIDC{ClientID: "kubron-public-client", IssuerURL: "https://idp.example.test/realms/kubron"}
	if err := s.SetWebOIDC(webWant); err != nil {
		t.Fatal(err)
	}
	if got, err := s.WebOIDC(); err != nil || got != webWant {
		t.Fatalf("web OIDC client: %+v %v", got, err)
	}
	// A file of the first form has a client id and no issuer: it is read as unbound (never applied).
	if err := os.WriteFile(filepath.Join(dir, "web-oidc.json"), []byte("{\"clientId\":\"old-client\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := s.WebOIDC(); err != nil || got != (WebOIDC{ClientID: "old-client"}) {
		t.Fatalf("web OIDC client of the first form: %+v %v", got, err)
	}
	if err := s.SetWebOIDC(WebOIDC{}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.WebOIDC(); err != nil || got != (WebOIDC{}) {
		t.Fatalf("cleared web OIDC client: %+v %v", got, err)
	}

	// токены Argo: хранилище не nil
	if s.ArgoTokens() == nil {
		t.Fatal("ArgoTokens() returned nil")
	}
}
