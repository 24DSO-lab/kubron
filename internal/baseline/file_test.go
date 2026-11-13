// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package baseline_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/baseline"
	"github.com/24DSO-lab/kubron/internal/baseline/baselinetest"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

func newFileEnv(t *testing.T) baselinetest.Env {
	t.Helper()
	var n atomic.Int64
	return baselinetest.Env{
		Backend: baseline.NewFileBackend(filepath.Join(t.TempDir(), "baselines")),
		NewScope: func(t *testing.T) tenant.Scope {
			return tenant.Scope{OrgID: "local", TeamID: "default", ClusterID: "cluster-" + strconv.FormatInt(n.Add(1), 10)}
		},
	}
}

// Контракт Backend (общий для файловой и PostgreSQL реализаций).
func TestFileBackendContract(t *testing.T) { baselinetest.Run(t, newFileEnv) }

// Файлы создаются с правами не шире, чем у users.json и audit.log, без временных остатков.
func TestFileBackendPermissionsAndNoTempFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	root := filepath.Join(t.TempDir(), "baselines")
	b := baseline.NewFileBackend(root)
	s := tenant.Scope{OrgID: "local", TeamID: "default", ClusterID: "c1"}
	bl := baseline.Baseline{ID: "b1", Scope: s, Name: "x", CreatedBy: "a", CreatedAt: time.Now()}
	if err := b.PutBaseline(context.Background(), bl); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		perm := info.Mode().Perm()
		if d.IsDir() && perm&0o077 != 0 {
			t.Errorf("directory %s has permissions %o", path, perm)
		}
		if !d.IsDir() && perm&0o077 != 0 {
			t.Errorf("file %s has permissions %o", path, perm)
		}
		if !d.IsDir() && filepath.Base(path)[0] == '.' {
			t.Errorf("temporary file left behind: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Повреждённый файл не превращается в «нет данных» и не роняет процесс.
func TestFileBackendCorruptedFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "baselines")
	b := baseline.NewFileBackend(root)
	s := tenant.Scope{OrgID: "local", TeamID: "default", ClusterID: "c1"}
	bl := baseline.Baseline{ID: "b1", Scope: s, Name: "x", CreatedBy: "a", CreatedAt: time.Now()}
	if err := b.PutBaseline(context.Background(), bl); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "local", "default", "c1", "b1", "baseline.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := b.GetBaseline(context.Background(), s, "b1")
	if err == nil || err == baseline.ErrNotFound {
		t.Fatalf("a corrupted file must be an error, not a missing record: %v", err)
	}
}

// Документ, подложенный в каталог чужой области, не отдаётся (защита в глубину).
func TestFileBackendForeignDocumentInOwnDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "baselines")
	b := baseline.NewFileBackend(root)
	owner := tenant.Scope{OrgID: "local", TeamID: "default", ClusterID: "c1"}
	other := tenant.Scope{OrgID: "local", TeamID: "default", ClusterID: "c2"}
	bl := baseline.Baseline{ID: "b1", Scope: other, Name: "x", CreatedBy: "a", CreatedAt: time.Now()}
	if err := b.PutBaseline(context.Background(), bl); err != nil {
		t.Fatal(err)
	}
	// Копируем документ чужого кластера в каталог своего.
	raw, err := os.ReadFile(filepath.Join(root, "local", "default", "c2", "b1", "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "local", "default", "c1", "b1")
	if err := os.MkdirAll(dst, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "baseline.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.GetBaseline(context.Background(), owner, "b1"); err != baseline.ErrNotFound {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}
