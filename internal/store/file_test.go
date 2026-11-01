// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/24DSO-labs/kubron/internal/audit"
	"github.com/24DSO-labs/kubron/internal/auth"
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

	// токены Argo: хранилище не nil
	if s.ArgoTokens() == nil {
		t.Fatal("ArgoTokens() returned nil")
	}
}
