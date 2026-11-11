// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/incident/incidenttest"
)

func TestFileBackendContract(t *testing.T) {
	incidenttest.Run(t, func(t *testing.T) incident.Backend {
		return incident.NewFileBackend(t.TempDir())
	})
}

// Права файлов не шире, чем у users.json и audit.log (0600/0700); временных файлов не остаётся.
func TestFileBackendPermissionsAndAtomicity(t *testing.T) {
	root := t.TempDir()
	b := incident.NewFileBackend(root)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(b.PutIncident(t.Context(), incidenttest.NewIncident(incidenttest.Scope, "inc1", incidenttest.NewIncident(incidenttest.Scope, "x", testBase).OpenedAt)))
	must(b.PutFragment(t.Context(), incidenttest.NewFragment(incidenttest.Scope, "inc1", "fr1", testBase, "text")))
	must(b.PutSettings(t.Context(), "org1", incident.DefaultSettings()))
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(p)[0] == '.' && p != root {
			t.Errorf("temporary file left behind: %s", p)
		}
		if runtime.GOOS == "windows" || p == root {
			return nil
		}
		info, _ := d.Info()
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("%s has mode %v: group/other access", p, perm)
		}
		return nil
	})
}

func TestFileBackendCorruptedRecordDoesNotHideHistory(t *testing.T) {
	root := t.TempDir()
	b := incident.NewFileBackend(root)
	sc := incidenttest.Scope
	if err := b.PutIncident(t.Context(), incidenttest.NewIncident(sc, "good", testBase)); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(root, sc.OrgID, sc.TeamID, sc.ClusterID, "incidents", "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := b.ListIncidents(t.Context(), sc, incident.ListQuery{})
	if err != nil || len(p.Items) != 1 || p.Items[0].ID != "good" {
		t.Errorf("page = %+v, %v", p, err)
	}
	if _, err := b.GetIncident(t.Context(), sc, "bad"); err == nil {
		t.Error("a corrupted record must be an error, not an empty incident")
	}
}
