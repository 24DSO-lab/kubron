// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/readiness"
	"github.com/24DSO-lab/kubron/internal/readiness/readinesstest"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

func TestFileBackendContract(t *testing.T) {
	t.Parallel()
	readinesstest.Run(t, func(t *testing.T) readiness.Backend {
		return readiness.NewFileBackend(t.TempDir())
	})
}

func TestFileBackendPermissionsAndLayout(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := t.TempDir()
	b := readiness.NewFileBackend(filepath.Join(dir, "readiness"))
	sc := readinesstest.Scope("c1")
	if err := b.Save(t.Context(), sc, readinesstest.NewReport("c1", "rep-00000001", readinesstest.Base)); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "readiness", "org-1", "team-1", "c1", "rep-00000001.json")
	st, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", st.Mode().Perm())
	}
	for _, d := range []string{"readiness", "readiness/org-1", "readiness/org-1/team-1/c1"} {
		st, err := os.Stat(filepath.Join(dir, d))
		if err != nil || st.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v, want 0700", d, st, err)
		}
	}
	// После записи не остаётся временных файлов.
	des, _ := os.ReadDir(filepath.Dir(file))
	for _, de := range des {
		if strings.HasPrefix(de.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", de.Name())
		}
	}
}

// Части области с символами пути не должны выводить запись за пределы каталога хранилища.
func TestFileBackendScopeCannotEscapeTheDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	root := filepath.Join(dir, "readiness")
	b := readiness.NewFileBackend(root)
	evil := tenant.Scope{OrgID: "../../evil", TeamID: "a/b", ClusterID: ".."}
	r := readinesstest.NewReport("..", "rep-00000001", readinesstest.Base)
	if err := b.Save(t.Context(), evil, r); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get(t.Context(), evil, r.ID); err != nil {
		t.Fatalf("Get after Save: %v", err)
	}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !strings.HasPrefix(p, root) && p != dir {
			t.Errorf("a file was created outside the storage directory: %s", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// «..» и «.» не совпадают ни с какой обычной областью.
	if _, err := b.Get(t.Context(), readinesstest.Scope("c1"), r.ID); !errors.Is(err, readiness.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestFileBackendSkipsBrokenFilesAndCleansTemps(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b := readiness.NewFileBackend(dir)
	sc := readinesstest.Scope("c1")
	if err := b.Save(t.Context(), sc, readinesstest.NewReport("c1", "rep-00000001", readinesstest.Base)); err != nil {
		t.Fatal(err)
	}
	cdir := filepath.Join(dir, "org-1", "team-1", "c1")
	if err := os.WriteFile(filepath.Join(cdir, "rep-00000002.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(cdir, ".tmp-123")
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(tmp, old, old); err != nil {
		t.Fatal(err)
	}
	page, err := b.List(t.Context(), sc, readiness.HistoryQuery{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("List = %+v, %v; want the intact report only", page, err)
	}
	if _, err := b.Prune(t.Context(), time.Now(), readiness.Retention{KeepPerCluster: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale temp file was not removed: %v", err)
	}
}
