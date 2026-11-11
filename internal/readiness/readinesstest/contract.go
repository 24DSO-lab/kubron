// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package readinesstest — контрактные тесты readiness.Backend (ADR-0026 §4). Один набор запускается против всех
// реализаций: файловой (desktop) и PostgreSQL (server), чтобы семантика совпадала.
package readinesstest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/readiness"
	"github.com/24DSO-lab/kubron/internal/readiness/apimatrix"
	"github.com/24DSO-lab/kubron/internal/readiness/catalog"
	"github.com/24DSO-lab/kubron/internal/readiness/kver"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Factory создаёт чистое хранилище для одного теста. Если реализации нужны внешние ресурсы
// (PostgreSQL), фабрика сама пропускает тест (t.Skip).
type Factory func(t *testing.T) readiness.Backend

// Scope возвращает область для тестов.
func Scope(cluster string) tenant.Scope {
	return tenant.Scope{OrgID: "org-1", TeamID: "team-1", ClusterID: cluster}
}

// Base — момент, от которого считаются времена отчётов в тестах.
var Base = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

// NewReport собирает валидный отчёт кластера cluster, созданный в момент at.
func NewReport(cluster, id string, at time.Time) *readiness.Report {
	am, err := apimatrix.Default()
	if err != nil {
		panic(err)
	}
	cat, err := catalog.Default()
	if err != nil {
		panic(err)
	}
	cur := kver.Minor{Major: 1, Minor: 35}
	return &readiness.Report{
		SchemaVersion: readiness.SchemaVersion,
		ID:            id,
		ClusterID:     cluster,
		CreatedAt:     at,
		Initiator:     "admin",
		CorrelationID: "corr-" + id,
		Current:       readiness.ClusterVersion{GitVersion: "v1.35.2", Minor: cur},
		Target:        kver.Minor{Major: 1, Minor: 36},
		Findings: []readiness.Finding{{
			Key: "api/autoscaling/v2beta2/HorizontalPodAutoscaler", Type: readiness.TypeAPI,
			Status: readiness.StatusRemoved, Severity: readiness.SeverityBlocker, Priority: 1,
			Explanation:    readiness.Message{Code: "api.removed", Params: map[string]string{"target": "1.36"}},
			Recommendation: &readiness.Message{Code: "api.migrate"},
			Source:         readiness.SourceLiveAPI, ObservedAt: at,
			API: &readiness.APIDetail{APIVersion: "autoscaling/v2beta2", Kind: "HorizontalPodAutoscaler",
				RemovedIn: "1.26", ResourceCount: 1,
				Resources: []readiness.ResourceRef{{Namespace: "web", Name: "front", Evidence: []string{readiness.EvidenceManagedFields}}}},
		}},
		Actions: []readiness.Action{{Phase: readiness.PhaseBefore, Priority: 1, Message: readiness.Message{Code: "action.migrateAPIs"},
			Related: []string{"api/autoscaling/v2beta2/HorizontalPodAutoscaler"}}},
		Coverage: readiness.Coverage{
			Mode: readiness.ModeGenericKubernetes, TargetSpecified: true, EvaluatedAgainst: kver.Minor{Major: 1, Minor: 36},
			Sources: []readiness.SourceCoverage{
				{Kind: readiness.SourceLiveAPI, Checked: true, ObservedAt: at, Methods: []string{readiness.MethodServerVersion},
					Limitations: []readiness.Message{{Code: readiness.LimitLiveOnly}}},
				{Kind: readiness.SourceGitOps, Reason: &readiness.Message{Code: readiness.LimitNotAnalyzed}},
				{Kind: readiness.SourceLocalFiles, Reason: &readiness.Message{Code: readiness.LimitNotAnalyzed}},
			},
			APIMatrix: am.Info(), Catalog: cat.Info(),
			Limitations: []readiness.Message{{Code: readiness.LimitProviderNotChecked}, {Code: readiness.LimitNotAGuarantee}},
		},
	}
}

// Run запускает контрактные тесты против реализации.
func Run(t *testing.T, newBackend Factory) {
	t.Helper()
	ctx := t.Context()

	t.Run("save and get round trip", func(t *testing.T) {
		b := newBackend(t)
		sc := Scope("c1")
		in := NewReport("c1", "rep-00000001", Base)
		if err := b.Save(ctx, sc, in); err != nil {
			t.Fatal(err)
		}
		got, err := b.Get(ctx, sc, "rep-00000001")
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != in.ID || got.Target != in.Target || got.Initiator != "admin" || !got.CreatedAt.Equal(Base) {
			t.Fatalf("report changed: %+v", got)
		}
		if got.Coverage.Mode != readiness.ModeGenericKubernetes || got.Coverage.APIMatrix.Version != in.Coverage.APIMatrix.Version {
			t.Fatalf("coverage was lost: %+v", got.Coverage)
		}
		if len(got.Findings) != 1 || got.Findings[0].API == nil || got.Findings[0].API.Resources[0].Name != "front" {
			t.Fatalf("findings were lost: %+v", got.Findings)
		}
		if err := got.Validate(); err != nil {
			t.Fatalf("a stored report must stay valid: %v", err)
		}
	})

	t.Run("save replaces by id", func(t *testing.T) {
		b := newBackend(t)
		sc := Scope("c1")
		r := NewReport("c1", "rep-00000001", Base)
		if err := b.Save(ctx, sc, r); err != nil {
			t.Fatal(err)
		}
		r.Initiator = "someone"
		if err := b.Save(ctx, sc, r); err != nil {
			t.Fatal(err)
		}
		page, err := b.List(ctx, sc, readiness.HistoryQuery{})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("list = %+v, %v; want one entry", page, err)
		}
		got, _ := b.Get(ctx, sc, r.ID)
		if got.Initiator != "someone" {
			t.Fatalf("report was not replaced: %q", got.Initiator)
		}
	})

	t.Run("report without coverage is rejected", func(t *testing.T) {
		b := newBackend(t)
		sc := Scope("c1")
		r := NewReport("c1", "rep-00000001", Base)
		r.Coverage = readiness.Coverage{}
		if err := b.Save(ctx, sc, r); !errors.Is(err, readiness.ErrInvalidReport) || !errors.Is(err, readiness.ErrCoverage) {
			t.Fatalf("err = %v, want ErrInvalidReport wrapping ErrCoverage", err)
		}
		if _, err := b.Get(ctx, sc, r.ID); !errors.Is(err, readiness.ErrNotFound) {
			t.Fatalf("an invalid report must not be stored: %v", err)
		}
	})

	t.Run("report of another cluster is rejected", func(t *testing.T) {
		b := newBackend(t)
		if err := b.Save(ctx, Scope("c1"), NewReport("c2", "rep-00000001", Base)); !errors.Is(err, readiness.ErrInvalidScope) {
			t.Fatalf("err = %v, want ErrInvalidScope", err)
		}
	})

	t.Run("incomplete scope fails closed", func(t *testing.T) {
		b := newBackend(t)
		r := NewReport("c1", "rep-00000001", Base)
		if err := b.Save(ctx, Scope("c1"), r); err != nil {
			t.Fatal(err)
		}
		for name, sc := range map[string]tenant.Scope{
			"no org":     {TeamID: "team-1", ClusterID: "c1"},
			"no team":    {OrgID: "org-1", ClusterID: "c1"},
			"no cluster": {OrgID: "org-1", TeamID: "team-1"},
			"empty":      {},
		} {
			if err := b.Save(ctx, sc, r); !errors.Is(err, readiness.ErrInvalidScope) {
				t.Errorf("%s: Save err = %v, want ErrInvalidScope", name, err)
			}
			if _, err := b.Get(ctx, sc, r.ID); !errors.Is(err, readiness.ErrInvalidScope) {
				t.Errorf("%s: Get err = %v, want ErrInvalidScope", name, err)
			}
			if _, err := b.List(ctx, sc, readiness.HistoryQuery{}); !errors.Is(err, readiness.ErrInvalidScope) {
				t.Errorf("%s: List err = %v, want ErrInvalidScope", name, err)
			}
		}
	})

	t.Run("other scope sees nothing", func(t *testing.T) {
		b := newBackend(t)
		if err := b.Save(ctx, Scope("c1"), NewReport("c1", "rep-00000001", Base)); err != nil {
			t.Fatal(err)
		}
		others := map[string]tenant.Scope{
			"other org":     {OrgID: "org-2", TeamID: "team-1", ClusterID: "c1"},
			"other team":    {OrgID: "org-1", TeamID: "team-2", ClusterID: "c1"},
			"other cluster": {OrgID: "org-1", TeamID: "team-1", ClusterID: "c2"},
		}
		for name, sc := range others {
			if _, err := b.Get(ctx, sc, "rep-00000001"); !errors.Is(err, readiness.ErrNotFound) {
				t.Errorf("%s: Get err = %v, want ErrNotFound", name, err)
			}
			page, err := b.List(ctx, sc, readiness.HistoryQuery{})
			if err != nil || len(page.Items) != 0 {
				t.Errorf("%s: List = %+v, %v; want empty", name, page, err)
			}
		}
	})

	t.Run("same id in another scope does not collide", func(t *testing.T) {
		b := newBackend(t)
		a, c := Scope("c1"), tenant.Scope{OrgID: "org-2", TeamID: "team-9", ClusterID: "c1"}
		ra, rc := NewReport("c1", "rep-00000001", Base), NewReport("c1", "rep-00000001", Base.Add(time.Hour))
		rc.Initiator = "other"
		if err := b.Save(ctx, a, ra); err != nil {
			t.Fatal(err)
		}
		if err := b.Save(ctx, c, rc); err != nil {
			t.Fatal(err)
		}
		ga, _ := b.Get(ctx, a, "rep-00000001")
		gc, _ := b.Get(ctx, c, "rep-00000001")
		if ga == nil || gc == nil || ga.Initiator != "admin" || gc.Initiator != "other" {
			t.Fatalf("scopes share a record: %+v / %+v", ga, gc)
		}
	})

	t.Run("get unknown or malformed id", func(t *testing.T) {
		b := newBackend(t)
		for _, id := range []string{"rep-99999999", "", "../etc/passwd", "a/b", "x"} {
			if _, err := b.Get(ctx, Scope("c1"), id); !errors.Is(err, readiness.ErrNotFound) {
				t.Errorf("Get(%q) err = %v, want ErrNotFound", id, err)
			}
		}
	})

	t.Run("history is paged newest first", func(t *testing.T) {
		b := newBackend(t)
		sc := Scope("c1")
		const n = 7
		for i := range n {
			id := fmt.Sprintf("rep-%08d", i)
			if err := b.Save(ctx, sc, NewReport("c1", id, Base.Add(time.Duration(i)*time.Minute))); err != nil {
				t.Fatal(err)
			}
		}
		// Отчёт другого кластера в историю не попадает.
		if err := b.Save(ctx, Scope("c2"), NewReport("c2", "rep-90000000", Base)); err != nil {
			t.Fatal(err)
		}
		var ids []string
		cursor := ""
		for range 10 {
			page, err := b.List(ctx, sc, readiness.HistoryQuery{Cursor: cursor, Limit: 3})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) > 3 {
				t.Fatalf("page has %d items, limit is 3", len(page.Items))
			}
			for _, s := range page.Items {
				ids = append(ids, s.ID)
				if s.ClusterID != "c1" || s.CurrentVersion != "1.35" || s.TargetVersion != "1.36" || s.Blockers != 1 {
					t.Fatalf("summary = %+v", s)
				}
			}
			if page.Next == "" {
				break
			}
			cursor = page.Next
		}
		if len(ids) != n {
			t.Fatalf("got %d entries (%v), want %d", len(ids), ids, n)
		}
		for i, id := range ids {
			if want := fmt.Sprintf("rep-%08d", n-1-i); id != want {
				t.Fatalf("order: position %d = %s, want %s (all: %v)", i, id, want, ids)
			}
		}
	})

	t.Run("equal timestamps are ordered by id without gaps", func(t *testing.T) {
		b := newBackend(t)
		sc := Scope("c1")
		for i := range 5 {
			if err := b.Save(ctx, sc, NewReport("c1", fmt.Sprintf("rep-%08d", i), Base)); err != nil {
				t.Fatal(err)
			}
		}
		seen := map[string]bool{}
		cursor := ""
		for range 10 {
			page, err := b.List(ctx, sc, readiness.HistoryQuery{Cursor: cursor, Limit: 2})
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range page.Items {
				if seen[s.ID] {
					t.Fatalf("%s listed twice", s.ID)
				}
				seen[s.ID] = true
			}
			if page.Next == "" {
				break
			}
			cursor = page.Next
		}
		if len(seen) != 5 {
			t.Fatalf("listed %d of 5", len(seen))
		}
	})

	t.Run("bad cursor", func(t *testing.T) {
		b := newBackend(t)
		for _, c := range []string{"%%%", "bm90LWEtY3Vyc29y", "MTI6Li4v"} {
			if _, err := b.List(ctx, Scope("c1"), readiness.HistoryQuery{Cursor: c}); !errors.Is(err, readiness.ErrBadCursor) {
				t.Errorf("cursor %q: err = %v, want ErrBadCursor", c, err)
			}
		}
	})

	t.Run("prune by age", func(t *testing.T) {
		b := newBackend(t)
		now := Base.Add(200 * 24 * time.Hour)
		old := NewReport("c1", "rep-00000001", now.Add(-181*24*time.Hour))
		fresh := NewReport("c1", "rep-00000002", now.Add(-10*24*time.Hour))
		for _, r := range []*readiness.Report{old, fresh} {
			if err := b.Save(ctx, Scope("c1"), r); err != nil {
				t.Fatal(err)
			}
		}
		n, err := b.Prune(ctx, now, readiness.Retention{MaxAge: 180 * 24 * time.Hour})
		if err != nil || n != 1 {
			t.Fatalf("Prune = %d, %v; want 1", n, err)
		}
		if _, err := b.Get(ctx, Scope("c1"), old.ID); !errors.Is(err, readiness.ErrNotFound) {
			t.Fatalf("old report survived: %v", err)
		}
		if _, err := b.Get(ctx, Scope("c1"), fresh.ID); err != nil {
			t.Fatalf("fresh report was removed: %v", err)
		}
	})

	t.Run("prune keeps the newest per cluster", func(t *testing.T) {
		b := newBackend(t)
		now := Base.Add(time.Hour)
		for _, c := range []string{"c1", "c2"} {
			for i := range 6 {
				id := fmt.Sprintf("rep-%s-%04d", c, i)
				if err := b.Save(ctx, Scope(c), NewReport(c, id, Base.Add(time.Duration(i)*time.Minute))); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := b.Save(ctx, Scope("c3"), NewReport("c3", "rep-c3-0000", Base)); err != nil {
			t.Fatal(err)
		}
		n, err := b.Prune(ctx, now, readiness.Retention{KeepPerCluster: 4})
		if err != nil || n != 4 {
			t.Fatalf("Prune = %d, %v; want 4 (two per cluster)", n, err)
		}
		for _, c := range []string{"c1", "c2"} {
			page, _ := b.List(ctx, Scope(c), readiness.HistoryQuery{Limit: 100})
			if len(page.Items) != 4 || page.Items[0].ID != fmt.Sprintf("rep-%s-0005", c) || page.Items[3].ID != fmt.Sprintf("rep-%s-0002", c) {
				t.Fatalf("%s: kept %+v", c, page.Items)
			}
		}
		if _, err := b.Get(ctx, Scope("c3"), "rep-c3-0000"); err != nil {
			t.Fatalf("a small cluster lost its report: %v", err)
		}
	})

	t.Run("prune with an empty policy removes nothing", func(t *testing.T) {
		b := newBackend(t)
		if err := b.Save(ctx, Scope("c1"), NewReport("c1", "rep-00000001", Base.Add(-1000*24*time.Hour))); err != nil {
			t.Fatal(err)
		}
		if n, err := b.Prune(ctx, Base, readiness.Retention{}); err != nil || n != 0 {
			t.Fatalf("Prune = %d, %v; want 0", n, err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		b := newBackend(t)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if err := b.Save(cctx, Scope("c1"), NewReport("c1", "rep-00000001", Base)); err == nil {
			t.Fatal("Save with a canceled context succeeded")
		}
	})
}
