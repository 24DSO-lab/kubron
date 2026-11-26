// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/baseline"
	"github.com/24DSO-lab/kubron/internal/baseline/baselinetest"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

var baselineScopeSeq atomic.Int64

func baselineEnv(t *testing.T, s *Store) baselinetest.Env {
	t.Helper()
	return baselinetest.Env{
		Backend: s.Baselines(),
		NewScope: func(t *testing.T) tenant.Scope {
			n := baselineScopeSeq.Add(1)
			return tenant.Scope{OrgID: fmt.Sprintf("org%d", n), TeamID: fmt.Sprintf("team%d", n), ClusterID: fmt.Sprintf("cl%d", n)}
		},
	}
}

// Один и тот же контракт проверяется против файлового и PostgreSQL-хранилища.
func TestBaselineBackendContract(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	baselinetest.Run(t, func(t *testing.T) baselinetest.Env {
		// Retention работает по всем областям, поэтому каждый подтест начинает с пустых таблиц.
		if _, err := s.pool.Exec(context.Background(), `TRUNCATE baselines CASCADE`); err != nil {
			t.Fatal(err)
		}
		return baselineEnv(t, s)
	})
}

const baselineCanary = "canary-pg-baseline-8c2f1d"

func seedVersion(t *testing.T, b baseline.Backend, sc tenant.Scope) baseline.Version {
	t.Helper()
	ctx := context.Background()
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	if err := b.PutBaseline(ctx, baseline.Baseline{ID: "b1", Scope: sc, Name: "n", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	v := baseline.Version{
		ID: "v1", BaselineID: "b1", Seq: 1, RulesVersion: 1, Source: baseline.SourceLiveSnapshot, CreatedAt: at,
		Salt: bytes.Repeat([]byte{7}, 32),
		Objects: []baseline.Object{{
			Ref:    baseline.Ref{Version: "v1", Kind: "ConfigMap", Namespace: "ns", Name: "cm"},
			Fields: map[string]baseline.Field{"data.k": {Hash: &baseline.HashedValue{HMAC: bytes.Repeat([]byte{9}, 32)}}, "data.plain": {Plain: `"` + baselineCanary + `"`}},
		}},
	}
	if err := b.PutVersion(ctx, sc, v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestBaselineVersionIsEncryptedAtRest(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	sc := tenant.Scope{OrgID: "o", TeamID: "t", ClusterID: "c"}
	seedVersion(t, s.Baselines(), sc)
	var ct []byte
	if err := s.pool.QueryRow(context.Background(), `SELECT content_enc FROM baseline_versions`).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte(baselineCanary)) || bytes.Contains(ct, bytes.Repeat([]byte{7}, 32)) {
		t.Fatal("version content must be encrypted: plaintext or salt found in the column")
	}
}

// Подмена: строка версии, перенесённая в другую область или baseline, не расшифровывается (AAD).
func TestBaselineVersionBoundToScope(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	sc := tenant.Scope{OrgID: "o", TeamID: "t", ClusterID: "c"}
	b := s.Baselines()
	seedVersion(t, b, sc)
	other := tenant.Scope{OrgID: "o", TeamID: "t", ClusterID: "c2"}
	ctx := context.Background()
	if err := b.PutBaseline(ctx, baseline.Baseline{ID: "b1", Scope: other, Name: "n", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO baseline_versions (org_id, team_id, cluster_id, baseline_id, id, seq, rules_version, source, created_at, object_count, content_enc, key_id)
		SELECT org_id, team_id, 'c2', baseline_id, id, seq, rules_version, source, created_at, object_count, content_enc, key_id FROM baseline_versions`); err != nil {
		t.Fatal(err)
	}
	if _, err := b.GetVersion(ctx, other, "b1", "v1"); err == nil {
		t.Fatal("a version moved to another scope must not decrypt")
	}
}

func TestBaselineRekeyKeepsHashesComparable(t *testing.T) {
	oldK := keyString("old")
	s := newTestStore(t, oldK)
	sc := tenant.Scope{OrgID: "o", TeamID: "t", ClusterID: "c"}
	orig := seedVersion(t, s.Baselines(), sc)
	ctx := context.Background()

	n, err := Open(ctx, os.Getenv("KUBRON_TEST_DATABASE_URL"), mustKeyring(t, keyString("new"), oldK))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(n.Close)
	if err := n.checkBaselinesKey(ctx); err != nil {
		t.Fatalf("previous key must still read the data: %v", err)
	}
	cnt, err := n.rekeyBaselines(ctx, n.keys.CurrentID())
	if err != nil || cnt != 1 {
		t.Fatalf("rekey = %d, %v", cnt, err)
	}
	var kid string
	if err := n.pool.QueryRow(ctx, `SELECT key_id FROM baseline_versions`).Scan(&kid); err != nil || kid != "new" {
		t.Fatalf("version must move to the current key: %q %v", kid, err)
	}
	got, err := n.Baselines().GetVersion(ctx, sc, "b1", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Salt, orig.Salt) {
		t.Fatal("the salt must survive rekey: hashes would stop matching")
	}
	if !got.Objects[0].Fields["data.k"].Equal(orig.Objects[0].Fields["data.k"]) {
		t.Fatal("hashes must stay comparable after rekey")
	}
	// Связка без прежнего ключа читать данные не может: проверка ключа это ловит.
	onlyNew, err := Open(ctx, os.Getenv("KUBRON_TEST_DATABASE_URL"), mustKeyring(t, keyString("other")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(onlyNew.Close)
	if err := onlyNew.checkBaselinesKey(ctx); err == nil {
		t.Fatal("a foreign key must be detected")
	}
}

func TestBaselineCascadeAndRetention(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	sc := tenant.Scope{OrgID: "o", TeamID: "t", ClusterID: "c"}
	b := s.Baselines()
	seedVersion(t, b, sc)
	ctx := context.Background()
	if err := b.PutExceptions(ctx, sc, "b1", "v1", []baseline.Exception{{ID: "e1", Scope: baseline.ExceptionKind, Ref: baseline.Ref{Kind: "ConfigMap"}, Path: "data.k", Comment: "c"}}); err != nil {
		t.Fatal(err)
	}
	if err := b.DeleteBaseline(ctx, sc, "b1"); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"baseline_versions", "baseline_exceptions", "baseline_results"} {
		var n int
		if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: %d rows left, %v", tbl, n, err)
		}
	}
}
