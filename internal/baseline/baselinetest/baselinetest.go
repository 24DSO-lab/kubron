// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package baselinetest — общий набор контрактных тестов baseline.Backend (ADR-0026 §4). Его запускают против обеих
// реализаций: файловой (desktop) и PostgreSQL (server). Расхождение семантики — ошибка теста, а не «особенность режима».
package baselinetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/baseline"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Env — окружение одного подтеста: пустое хранилище и фабрика областей. Каждый вызов NewScope даёт новый кластер
// (для PostgreSQL — со строкой в каталоге clusters); команда и организация у областей одной фабрики общие.
type Env struct {
	Backend  baseline.Backend
	NewScope func(t *testing.T) tenant.Scope
}

var t0 = time.Date(2026, 11, 12, 10, 0, 0, 123456000, time.UTC)

// Run выполняет все контрактные тесты. newEnv вызывается для каждого подтеста и обязан вернуть пустое хранилище.
func Run(t *testing.T, newEnv func(t *testing.T) Env) {
	t.Helper()
	for _, c := range []struct {
		name string
		fn   func(t *testing.T, e Env)
	}{
		{"BaselineLifecycle", testBaselineLifecycle},
		{"TenantIsolation", testTenantIsolation},
		{"InvalidScopeFailsClosed", testInvalidScope},
		{"VersionsAreImmutable", testVersionsImmutable},
		{"VersionPaging", testVersionPaging},
		{"Exceptions", testExceptions},
		{"Results", testResults},
		{"ResultPaging", testResultPaging},
		{"DeleteCascades", testDeleteCascades},
		{"RetentionResults", testRetentionResults},
		{"RetentionVersions", testRetentionVersions},
		{"ConcurrentSameVersionNumber", testConcurrentSameSeq},
		{"ConcurrentDistinctVersions", testConcurrentDistinctSeq},
		{"ConcurrentResults", testConcurrentResults},
		{"CreateWithVersion", testCreateWithVersion},
		{"CommitVersion", testCommitVersion},
		{"SetActiveVersion", testSetActiveVersion},
		{"UpdateActiveExceptions", testUpdateActiveExceptions},
		{"ConcurrentExceptionEdits", testConcurrentExceptionEdits},
		{"ConcurrentCommitAndExceptionEdits", testConcurrentCommitAndEdits},
		{"ConcurrentPruneAndSetActive", testConcurrentPruneAndSetActive},
		{"ContextCancelled", testContextCancelled},
		{"MaliciousIdentifiers", testMaliciousIDs},
	} {
		t.Run(c.name, func(t *testing.T) { c.fn(t, newEnv(t)) })
	}
}

// --- помощники -------------------------------------------------------------------------------------------------

func newBaseline(s tenant.Scope, name string, at time.Time) baseline.Baseline {
	return baseline.Baseline{
		ID: baseline.NewID(), Scope: s, Name: name, Namespaces: []string{"prod", "stage"}, Selector: "app=web",
		Refs: []baseline.Ref{
			{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "prod", Name: "web"},
			{Version: "v1", Kind: "ConfigMap", Namespace: "prod", Name: "web-config"},
		},
		CreatedBy: "alice", CreatedAt: at,
	}
}

func newVersion(baselineID string, seq int, at time.Time) baseline.Version {
	return baseline.Version{
		ID: baseline.NewID(), BaselineID: baselineID, Seq: seq, RulesVersion: 1, Source: baseline.SourceLiveSnapshot,
		Author: "alice", Comment: fmt.Sprintf("version %d", seq), CreatedAt: at,
		Salt: []byte("0123456789abcdef0123456789abcdef"),
		Objects: []baseline.Object{{
			Ref: baseline.Ref{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "prod", Name: "web"},
			Fields: map[string]baseline.Field{
				"spec.replicas": {Plain: "3"},
				"spec.template.spec.containers[name=web].image":                 {Plain: `"nginx:1.27"`},
				"spec.template.spec.containers[name=web].env[name=TOKEN].value": {Hash: &baseline.HashedValue{HMAC: []byte{1, 2, 3, 4}}},
			},
		}},
	}
}

func newResult(baselineID, versionID string, at time.Time) baseline.CheckResult {
	return baseline.CheckResult{
		ID: baseline.NewID(), BaselineID: baselineID, VersionID: versionID, RulesVersion: 1, VersionRulesVersion: 1,
		CheckedAt: at, Initiator: "bob", CorrelationID: "req-1",
		Summary: baseline.Summary{Total: 2, Matched: 1, Changed: 1, Checked: 2, SuppressedChanges: 1,
			UncheckedByReason: map[baseline.Reason]int{}},
		Items: []baseline.ItemResult{{
			Ref:    baseline.Ref{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "prod", Name: "web"},
			Status: baseline.StatusChanged, Priority: baseline.PriorityHigh,
			Changes: []baseline.Change{{
				Path: "spec.template.spec.containers[name=web].image", Op: baseline.OpChanged, Old: `"nginx:1.27"`, New: `"nginx:1.28"`,
				Priority: baseline.PriorityHigh,
				Source:   &baseline.ChangeSource{Kind: baseline.SourceManagedFields, Manager: "kubectl", At: at.Add(-time.Minute)},
			}},
			Suppressed: []string{"exc-1"},
		}},
		Exceptions: baseline.ExceptionReport{Expired: []string{"exc-0"}},
	}
}

func newException(path string, at time.Time) baseline.Exception {
	exp := at.Add(24 * time.Hour)
	return baseline.Exception{
		ID: baseline.NewID(), Scope: baseline.ExceptionKind,
		Ref:  baseline.Ref{Group: "apps", Kind: "Deployment", Namespace: "prod"},
		Path: path, Comment: "managed by the HPA", ExpiresAt: &exp, CreatedBy: "alice", CreatedAt: at,
	}
}

// sameJSON сравнивает записи по каноническому JSON (nil и пустые контейнеры различимы, время — после Normalized).
func sameJSON(t *testing.T, want, got any) {
	t.Helper()
	w, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	g, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(w) != string(g) {
		t.Fatalf("records differ\nwant: %s\n got: %s", w, g)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

// seed создаёт baseline и n версий (Seq 1..n) в области s; активна последняя.
func seed(t *testing.T, e Env, s tenant.Scope, n int) (baseline.Baseline, []baseline.Version) {
	t.Helper()
	ctx := context.Background()
	b := newBaseline(s, "web", t0)
	must(t, e.Backend.PutBaseline(ctx, b))
	var vs []baseline.Version
	for i := 1; i <= n; i++ {
		v := newVersion(b.ID, i, t0.Add(time.Duration(i)*time.Minute))
		must(t, e.Backend.PutVersion(ctx, s, v))
		vs = append(vs, v)
	}
	if n > 0 {
		b.ActiveVersionID = vs[n-1].ID
		must(t, e.Backend.PutBaseline(ctx, b))
	}
	return b, vs
}

// --- тесты -----------------------------------------------------------------------------------------------------

func testBaselineLifecycle(t *testing.T, e Env) {
	ctx := context.Background()
	s := e.NewScope(t)

	got, err := e.Backend.ListBaselines(ctx, s)
	must(t, err)
	if len(got) != 0 {
		t.Fatalf("a new cluster has baselines: %v", got)
	}

	b1 := newBaseline(s, "first", t0)
	b2 := newBaseline(s, "second", t0.Add(time.Hour))
	must(t, e.Backend.PutBaseline(ctx, b2))
	must(t, e.Backend.PutBaseline(ctx, b1))

	gb, err := e.Backend.GetBaseline(ctx, s, b1.ID)
	must(t, err)
	sameJSON(t, b1.Normalized(), gb)

	// Замена по идентификатору.
	b1.Name = "renamed"
	b1.Kinds = []string{"Deployment"}
	must(t, e.Backend.PutBaseline(ctx, b1))
	gb, err = e.Backend.GetBaseline(ctx, s, b1.ID)
	must(t, err)
	sameJSON(t, b1.Normalized(), gb)

	list, err := e.Backend.ListBaselines(ctx, s)
	must(t, err)
	if len(list) != 2 || list[0].ID != b1.ID || list[1].ID != b2.ID {
		t.Fatalf("list must be ordered by creation time: %v", list)
	}

	_, err = e.Backend.GetBaseline(ctx, s, baseline.NewID())
	wantErr(t, err, baseline.ErrNotFound)

	must(t, e.Backend.DeleteBaseline(ctx, s, b1.ID))
	_, err = e.Backend.GetBaseline(ctx, s, b1.ID)
	wantErr(t, err, baseline.ErrNotFound)
	wantErr(t, e.Backend.DeleteBaseline(ctx, s, b1.ID), baseline.ErrNotFound)

	// Невалидный документ отклоняется.
	bad := newBaseline(s, " ", t0)
	wantErr(t, e.Backend.PutBaseline(ctx, bad), baseline.ErrInvalid)
}

func testTenantIsolation(t *testing.T, e Env) {
	ctx := context.Background()
	own := e.NewScope(t)
	other := e.NewScope(t)            // другой кластер
	otherTeam := own                  // та же организация и кластер, другая команда
	otherTeam.TeamID = "another-team" //
	otherOrg := own                   //
	otherOrg.OrgID = "another-org"    //
	for name, foreign := range map[string]tenant.Scope{"cluster": other, "team": otherTeam, "org": otherOrg} {
		t.Run(name, func(t *testing.T) {
			b, vs := seed(t, e, own, 2)
			exc := newException("spec.replicas", t0)
			must(t, e.Backend.PutExceptions(ctx, own, b.ID, vs[0].ID, []baseline.Exception{exc}))
			res := newResult(b.ID, vs[0].ID, t0)
			must(t, e.Backend.AppendResult(ctx, own, res))

			_, err := e.Backend.GetBaseline(ctx, foreign, b.ID)
			wantErr(t, err, baseline.ErrNotFound)
			list, err := e.Backend.ListBaselines(ctx, foreign)
			must(t, err)
			if len(list) != 0 {
				t.Fatalf("a foreign scope sees baselines: %v", list)
			}
			wantErr(t, e.Backend.DeleteBaseline(ctx, foreign, b.ID), baseline.ErrNotFound)
			_, err = e.Backend.GetVersion(ctx, foreign, b.ID, vs[0].ID)
			wantErr(t, err, baseline.ErrNotFound)
			_, err = e.Backend.ListVersions(ctx, foreign, b.ID, baseline.Page{})
			wantErr(t, err, baseline.ErrNotFound)
			wantErr(t, e.Backend.PutVersion(ctx, foreign, newVersion(b.ID, 9, t0)), baseline.ErrNotFound)
			_, err = e.Backend.GetExceptions(ctx, foreign, b.ID, vs[0].ID)
			wantErr(t, err, baseline.ErrNotFound)
			wantErr(t, e.Backend.PutExceptions(ctx, foreign, b.ID, vs[0].ID, nil), baseline.ErrNotFound)
			_, err = e.Backend.GetResult(ctx, foreign, b.ID, res.ID)
			wantErr(t, err, baseline.ErrNotFound)
			_, err = e.Backend.ListResults(ctx, foreign, b.ID, baseline.Page{})
			wantErr(t, err, baseline.ErrNotFound)
			wantErr(t, e.Backend.AppendResult(ctx, foreign, newResult(b.ID, vs[0].ID, t0)), baseline.ErrNotFound)

			// Хозяин по-прежнему всё видит.
			if _, err := e.Backend.GetBaseline(ctx, own, b.ID); err != nil {
				t.Fatalf("the owner lost the baseline: %v", err)
			}
		})
	}
}

func testInvalidScope(t *testing.T, e Env) {
	ctx := context.Background()
	for name, s := range map[string]tenant.Scope{
		"empty":      {},
		"no org":     {TeamID: "t", ClusterID: "c"},
		"no team":    {OrgID: "o", ClusterID: "c"},
		"no cluster": {OrgID: "o", TeamID: "t"},
		"traversal":  {OrgID: "..", TeamID: "t", ClusterID: "c"},
		"slash":      {OrgID: "o", TeamID: "t/x", ClusterID: "c"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := e.Backend.GetBaseline(ctx, s, baseline.NewID())
			wantErr(t, err, baseline.ErrInvalidScope)
			_, err = e.Backend.ListBaselines(ctx, s)
			wantErr(t, err, baseline.ErrInvalidScope)
			b := newBaseline(s, "x", t0)
			wantErr(t, e.Backend.PutBaseline(ctx, b), baseline.ErrInvalidScope)
			wantErr(t, e.Backend.DeleteBaseline(ctx, s, b.ID), baseline.ErrInvalidScope)
			_, err = e.Backend.ListResults(ctx, s, b.ID, baseline.Page{})
			wantErr(t, err, baseline.ErrInvalidScope)
		})
	}
}

func testVersionsImmutable(t *testing.T, e Env) {
	ctx := context.Background()
	s := e.NewScope(t)
	b, vs := seed(t, e, s, 1)

	got, err := e.Backend.GetVersion(ctx, s, b.ID, vs[0].ID)
	must(t, err)
	sameJSON(t, vs[0].Normalized(), got) // соль, поля и hash доезжают без потерь
	if got.Objects[0].Fields["spec.template.spec.containers[name=web].env[name=TOKEN].value"].Hash == nil {
		t.Fatal("the hashed value lost its hash")
	}

	// Повтор того же ID — ErrExists, содержимое не перезаписывается.
	dup := vs[0]
	dup.Comment = "overwritten"
	wantErr(t, e.Backend.PutVersion(ctx, s, dup), baseline.ErrExists)
	// Тот же номер под другим ID — тоже ErrExists.
	sameSeq := newVersion(b.ID, 1, t0)
	wantErr(t, e.Backend.PutVersion(ctx, s, sameSeq), baseline.ErrExists)
	got, err = e.Backend.GetVersion(ctx, s, b.ID, vs[0].ID)
	must(t, err)
	if got.Comment != vs[0].Comment {
		t.Fatalf("an immutable version was changed: %q", got.Comment)
	}

	// Версия несуществующего baseline.
	wantErr(t, e.Backend.PutVersion(ctx, s, newVersion(baseline.NewID(), 1, t0)), baseline.ErrNotFound)
	_, err = e.Backend.GetVersion(ctx, s, b.ID, baseline.NewID())
	wantErr(t, err, baseline.ErrNotFound)

	// Версия без соли или с неизвестным источником не принимается.
	noSalt := newVersion(b.ID, 5, t0)
	noSalt.Salt = nil
	wantErr(t, e.Backend.PutVersion(ctx, s, noSalt), baseline.ErrInvalid)
	badSrc := newVersion(b.ID, 6, t0)
	badSrc.Source = "gitops"
	wantErr(t, e.Backend.PutVersion(ctx, s, badSrc), baseline.ErrInvalid)
}

func testVersionPaging(t *testing.T, e Env) {
	ctx := context.Background()
	s := e.NewScope(t)
	b, vs := seed(t, e, s, 5)

	var seqs []int
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("paging does not terminate")
		}
		p, err := e.Backend.ListVersions(ctx, s, b.ID, baseline.Page{Cursor: cursor, Limit: 2})
		must(t, err)
		for _, it := range p.Items {
			seqs = append(seqs, it.Seq)
			if it.ObjectCount != 1 {
				t.Fatalf("ObjectCount = %d", it.ObjectCount)
			}
		}
		if p.Next == "" {
			break
		}
		cursor = p.Next
	}
	if fmt.Sprint(seqs) != "[5 4 3 2 1]" {
		t.Fatalf("order = %v, want newest first", seqs)
	}
	_ = vs

	_, err := e.Backend.ListVersions(ctx, s, b.ID, baseline.Page{Cursor: "not-a-cursor!"})
	wantErr(t, err, baseline.ErrInvalid)

	// Пустая история — пустая страница, а не ошибка.
	empty := newBaseline(s, "empty", t0)
	must(t, e.Backend.PutBaseline(ctx, empty))
	p, err := e.Backend.ListVersions(ctx, s, empty.ID, baseline.Page{})
	must(t, err)
	if len(p.Items) != 0 || p.Next != "" {
		t.Fatalf("page = %+v", p)
	}
}

func testExceptions(t *testing.T, e Env) {
	ctx := context.Background()
	s := e.NewScope(t)
	b, vs := seed(t, e, s, 2)

	got, err := e.Backend.GetExceptions(ctx, s, b.ID, vs[0].ID)
	must(t, err)
	if got == nil || len(got) != 0 {
		t.Fatalf("a version without exceptions must return an empty list, got %#v", got)
	}

	a := newException("spec.replicas", t0)
	c := newException("metadata.annotations", t0.Add(time.Minute))
	c.Scope = baseline.ExceptionResource
	c.Ref = baseline.Ref{Group: "apps", Kind: "Deployment", Namespace: "prod", Name: "web"}
	c.ExpiresAt = nil
	must(t, e.Backend.PutExceptions(ctx, s, b.ID, vs[0].ID, []baseline.Exception{a, c}))
	got, err = e.Backend.GetExceptions(ctx, s, b.ID, vs[0].ID)
	must(t, err)
	sameJSON(t, baseline.NormalizedExceptions([]baseline.Exception{a, c}), got)

	// Исключения привязаны к версии.
	other, err := e.Backend.GetExceptions(ctx, s, b.ID, vs[1].ID)
	must(t, err)
	if len(other) != 0 {
		t.Fatalf("exceptions leaked to another version: %v", other)
	}

	// Замена списка целиком.
	must(t, e.Backend.PutExceptions(ctx, s, b.ID, vs[0].ID, []baseline.Exception{c}))
	got, err = e.Backend.GetExceptions(ctx, s, b.ID, vs[0].ID)
	must(t, err)
	sameJSON(t, baseline.NormalizedExceptions([]baseline.Exception{c}), got)

	// Пустой список очищает.
	must(t, e.Backend.PutExceptions(ctx, s, b.ID, vs[0].ID, nil))
	got, err = e.Backend.GetExceptions(ctx, s, b.ID, vs[0].ID)
	must(t, err)
	if len(got) != 0 {
		t.Fatalf("exceptions were not cleared: %v", got)
	}

	// Неизвестная версия и невалидное исключение.
	wantErr(t, e.Backend.PutExceptions(ctx, s, b.ID, baseline.NewID(), []baseline.Exception{a}), baseline.ErrNotFound)
	noComment := newException("spec.replicas", t0)
	noComment.Comment = "  "
	wantErr(t, e.Backend.PutExceptions(ctx, s, b.ID, vs[0].ID, []baseline.Exception{noComment}), baseline.ErrInvalid)
}

func testResults(t *testing.T, e Env) {
	ctx := context.Background()
	s := e.NewScope(t)
	b, vs := seed(t, e, s, 1)

	r := newResult(b.ID, vs[0].ID, t0)
	must(t, e.Backend.AppendResult(ctx, s, r))
	got, err := e.Backend.GetResult(ctx, s, b.ID, r.ID)
	must(t, err)
	sameJSON(t, r.Normalized(), got)

	wantErr(t, e.Backend.AppendResult(ctx, s, r), baseline.ErrExists)
	_, err = e.Backend.GetResult(ctx, s, b.ID, baseline.NewID())
	wantErr(t, err, baseline.ErrNotFound)

	// Результат принадлежит своему baseline.
	b2 := newBaseline(s, "second", t0)
	must(t, e.Backend.PutBaseline(ctx, b2))
	_, err = e.Backend.GetResult(ctx, s, b2.ID, r.ID)
	wantErr(t, err, baseline.ErrNotFound)

	wantErr(t, e.Backend.AppendResult(ctx, s, newResult(baseline.NewID(), vs[0].ID, t0)), baseline.ErrNotFound)
	bad := newResult(b.ID, vs[0].ID, time.Time{})
	wantErr(t, e.Backend.AppendResult(ctx, s, bad), baseline.ErrInvalid)
}

func testResultPaging(t *testing.T, e Env) {
	ctx := context.Background()
	s := e.NewScope(t)
	b, vs := seed(t, e, s, 1)

	var want []string
	for i := 0; i < 5; i++ {
		r := newResult(b.ID, vs[0].ID, t0.Add(time.Duration(i)*time.Hour))
		must(t, e.Backend.AppendResult(ctx, s, r))
		want = append([]string{r.ID}, want...) // новые первыми
	}
	// Два результата в одну микросекунду: порядок определяется идентификатором, одинаково в обеих реализациях.
	same1 := newResult(b.ID, vs[0].ID, t0.Add(10*time.Hour))
	same2 := newResult(b.ID, vs[0].ID, t0.Add(10*time.Hour))
	must(t, e.Backend.AppendResult(ctx, s, same1))
	must(t, e.Backend.AppendResult(ctx, s, same2))
	hi, lo := same1.ID, same2.ID
	if lo > hi {
		hi, lo = lo, hi
	}
	want = append([]string{hi, lo}, want...)

	var got []string
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("paging does not terminate")
		}
		p, err := e.Backend.ListResults(ctx, s, b.ID, baseline.Page{Cursor: cursor, Limit: 3})
		must(t, err)
		for _, it := range p.Items {
			got = append(got, it.ID)
			if it.Summary.Total != 2 || it.Initiator != "bob" {
				t.Fatalf("summary = %+v", it)
			}
		}
		if p.Next == "" {
			break
		}
		cursor = p.Next
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order\n got %v\nwant %v", got, want)
	}
	_, err := e.Backend.ListResults(ctx, s, b.ID, baseline.Page{Cursor: "###"})
	wantErr(t, err, baseline.ErrInvalid)
}

func testDeleteCascades(t *testing.T, e Env) {
	ctx := context.Background()
	s := e.NewScope(t)
	b, vs := seed(t, e, s, 2)
	must(t, e.Backend.PutExceptions(ctx, s, b.ID, vs[0].ID, []baseline.Exception{newException("spec.replicas", t0)}))
	res := newResult(b.ID, vs[0].ID, t0)
	must(t, e.Backend.AppendResult(ctx, s, res))
	keep, _ := seed(t, e, s, 1) // соседний baseline не затрагивается

	must(t, e.Backend.DeleteBaseline(ctx, s, b.ID))

	for _, v := range vs {
		_, err := e.Backend.GetVersion(ctx, s, b.ID, v.ID)
		wantErr(t, err, baseline.ErrNotFound)
	}
	_, err := e.Backend.GetResult(ctx, s, b.ID, res.ID)
	wantErr(t, err, baseline.ErrNotFound)
	_, err = e.Backend.GetExceptions(ctx, s, b.ID, vs[0].ID)
	wantErr(t, err, baseline.ErrNotFound)

	// Тот же ID создан заново — старое содержимое не воскресает.
	again := b
	again.ActiveVersionID = ""
	must(t, e.Backend.PutBaseline(ctx, again))
	p, err := e.Backend.ListVersions(ctx, s, b.ID, baseline.Page{})
	must(t, err)
	rp, err := e.Backend.ListResults(ctx, s, b.ID, baseline.Page{})
	must(t, err)
	if len(p.Items) != 0 || len(rp.Items) != 0 {
		t.Fatalf("deleted history came back: %d versions, %d results", len(p.Items), len(rp.Items))
	}
	if _, err := e.Backend.GetBaseline(ctx, s, keep.ID); err != nil {
		t.Fatalf("a neighbouring baseline was deleted: %v", err)
	}
}

func testRetentionResults(t *testing.T, e Env) {
	ctx := context.Background()
	s1, s2 := e.NewScope(t), e.NewScope(t)
	b1, v1 := seed(t, e, s1, 1)
	b2, v2 := seed(t, e, s2, 1)
	var keepIDs []string
	for i := 1; i <= 4; i++ {
		at := t0.Add(time.Duration(i) * 24 * time.Hour)
		r1, r2 := newResult(b1.ID, v1[0].ID, at), newResult(b2.ID, v2[0].ID, at)
		must(t, e.Backend.AppendResult(ctx, s1, r1))
		must(t, e.Backend.AppendResult(ctx, s2, r2))
		if i >= 3 {
			keepIDs = append(keepIDs, r1.ID)
		}
	}
	// Граница — день 3: результаты строго старше удаляются (по 2 в каждой области), день 3 и новее остаются.
	n, err := e.Backend.DeleteResultsBefore(ctx, t0.Add(3*24*time.Hour))
	must(t, err)
	if n != 4 {
		t.Fatalf("deleted %d results, want 4", n)
	}
	p, err := e.Backend.ListResults(ctx, s1, b1.ID, baseline.Page{})
	must(t, err)
	if len(p.Items) != 2 || p.Items[0].ID != keepIDs[1] || p.Items[1].ID != keepIDs[0] {
		t.Fatalf("wrong results left: %+v", p.Items)
	}
	// Версии и baseline не затронуты.
	if _, err := e.Backend.GetVersion(ctx, s1, b1.ID, v1[0].ID); err != nil {
		t.Fatal(err)
	}
	n, err = e.Backend.DeleteResultsBefore(ctx, t0.Add(3*24*time.Hour))
	must(t, err)
	if n != 0 {
		t.Fatalf("a repeated run deleted %d results", n)
	}
}

func testRetentionVersions(t *testing.T, e Env) {
	ctx := context.Background()
	s := e.NewScope(t)
	b := newBaseline(s, "long", t0)
	must(t, e.Backend.PutBaseline(ctx, b))
	var vs []baseline.Version
	for i := 1; i <= 25; i++ {
		v := newVersion(b.ID, i, t0.Add(time.Duration(i)*time.Minute))
		must(t, e.Backend.PutVersion(ctx, s, v))
		vs = append(vs, v)
	}
	// Активна старая версия №3: её удалять нельзя никогда.
	b.ActiveVersionID = vs[2].ID
	must(t, e.Backend.PutBaseline(ctx, b))
	must(t, e.Backend.PutExceptions(ctx, s, b.ID, vs[0].ID, []baseline.Exception{newException("spec.replicas", t0)}))
	must(t, e.Backend.PutExceptions(ctx, s, b.ID, vs[2].ID, []baseline.Exception{newException("spec.replicas", t0)}))

	// keep=1 поднимается до пола MinVersionsKept: остаются №6..25 и активная №3; удалены №1, 2, 4, 5.
	n, err := e.Backend.PruneVersions(ctx, 1)
	must(t, err)
	if n != 4 {
		t.Fatalf("pruned %d versions, want 4", n)
	}
	for i, v := range vs {
		_, err := e.Backend.GetVersion(ctx, s, b.ID, v.ID)
		deleted := i == 0 || i == 1 || i == 3 || i == 4
		if deleted {
			wantErr(t, err, baseline.ErrNotFound)
		} else if err != nil {
			t.Fatalf("version #%d must stay: %v", i+1, err)
		}
	}
	// Исключения активной версии остались, удалённой — исчезли вместе с ней.
	got, err := e.Backend.GetExceptions(ctx, s, b.ID, vs[2].ID)
	must(t, err)
	if len(got) != 1 {
		t.Fatalf("exceptions of the active version: %v", got)
	}
	_, err = e.Backend.GetExceptions(ctx, s, b.ID, vs[0].ID)
	wantErr(t, err, baseline.ErrNotFound)

	// Больший keep оставляет больше: повторный запуск с keep=100 ничего не удаляет.
	n, err = e.Backend.PruneVersions(ctx, 100)
	must(t, err)
	if n != 0 {
		t.Fatalf("pruned %d versions with a large keep", n)
	}
	// Новый номер после удаления не переиспользует старый: Seq 1 снова свободен, но решает это сервис, а не хранилище.
	p, err := e.Backend.ListVersions(ctx, s, b.ID, baseline.Page{Limit: 200})
	must(t, err)
	if len(p.Items) != 21 {
		t.Fatalf("%d versions left, want 21", len(p.Items))
	}
}

func testContextCancelled(t *testing.T, e Env) {
	s := e.NewScope(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := newBaseline(s, "x", t0)
	for name, err := range map[string]error{
		"PutBaseline":  e.Backend.PutBaseline(ctx, b),
		"DeleteBefore": func() error { _, err := e.Backend.DeleteResultsBefore(ctx, t0); return err }(),
		"Prune":        func() error { _, err := e.Backend.PruneVersions(ctx, 20); return err }(),
		"ListBaseline": func() error { _, err := e.Backend.ListBaselines(ctx, s); return err }(),
	} {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s: error = %v, want context.Canceled", name, err)
		}
	}
}

func testMaliciousIDs(t *testing.T, e Env) {
	ctx := context.Background()
	s := e.NewScope(t)
	b, vs := seed(t, e, s, 1)
	for _, id := range []string{"", "..", "../x", "a/b", `a\b`, "a b", "x\x00y", string(make([]byte, 100))} {
		if _, err := e.Backend.GetBaseline(ctx, s, id); !errors.Is(err, baseline.ErrNotFound) {
			t.Errorf("GetBaseline(%q) = %v, want ErrNotFound", id, err)
		}
		if _, err := e.Backend.GetVersion(ctx, s, b.ID, id); !errors.Is(err, baseline.ErrNotFound) {
			t.Errorf("GetVersion(%q) = %v, want ErrNotFound", id, err)
		}
		if _, err := e.Backend.GetResult(ctx, s, b.ID, id); !errors.Is(err, baseline.ErrNotFound) {
			t.Errorf("GetResult(%q) = %v, want ErrNotFound", id, err)
		}
		if _, err := e.Backend.GetExceptions(ctx, s, id, vs[0].ID); !errors.Is(err, baseline.ErrNotFound) {
			t.Errorf("GetExceptions(%q) = %v, want ErrNotFound", id, err)
		}
		bad := newBaseline(s, "x", t0)
		bad.ID = id
		if err := e.Backend.PutBaseline(ctx, bad); !errors.Is(err, baseline.ErrInvalid) {
			t.Errorf("PutBaseline(id=%q) = %v, want ErrInvalid", id, err)
		}
	}
}

// --- конкурентность --------------------------------------------------------------------------------------------

// concurrently запускает n вызовов одновременно (с общим стартом) и возвращает их ошибки по номерам.
func concurrently(n int, fn func(i int) error) []error {
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = fn(i)
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

// Два пользователя одновременно создают версию с одним номером: побеждает ровно один, остальные получают ErrExists,
// история не повреждается. Так drift.Service узнаёт, что номер занят, и берёт следующий.
func testConcurrentSameSeq(t *testing.T, e Env) {
	const n = 16
	ctx := context.Background()
	s := e.NewScope(t)
	b := newBaseline(s, "web", t0)
	must(t, e.Backend.PutBaseline(ctx, b))

	errs := concurrently(n, func(int) error {
		return e.Backend.PutVersion(ctx, s, newVersion(b.ID, 1, t0))
	})
	var won int
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, baseline.ErrExists):
			t.Fatalf("a losing writer got %v, want ErrExists", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d writers won the same version number, want exactly 1", won)
	}
	page, err := e.Backend.ListVersions(ctx, s, b.ID, baseline.Page{})
	must(t, err)
	if len(page.Items) != 1 || page.Items[0].Seq != 1 {
		t.Fatalf("history = %+v, want exactly version 1", page.Items)
	}
	// Победившая версия читается целиком: запись не «склеилась» из двух.
	got, err := e.Backend.GetVersion(ctx, s, b.ID, page.Items[0].ID)
	must(t, err)
	if len(got.Objects) != 1 || len(got.Salt) == 0 {
		t.Fatalf("the stored version is damaged: %d objects, %d salt bytes", len(got.Objects), len(got.Salt))
	}
}

// Разные номера, записанные одновременно, сохраняются все и без пропусков.
func testConcurrentDistinctSeq(t *testing.T, e Env) {
	const n = 16
	ctx := context.Background()
	s := e.NewScope(t)
	b := newBaseline(s, "web", t0)
	must(t, e.Backend.PutBaseline(ctx, b))

	for i, err := range concurrently(n, func(i int) error {
		return e.Backend.PutVersion(ctx, s, newVersion(b.ID, i+1, t0.Add(time.Duration(i)*time.Second)))
	}) {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	page, err := e.Backend.ListVersions(ctx, s, b.ID, baseline.Page{Limit: baseline.MaxPageLimit})
	must(t, err)
	if len(page.Items) != n {
		t.Fatalf("history has %d versions, want %d", len(page.Items), n)
	}
	for i, it := range page.Items { // от новых к старым
		if want := n - i; it.Seq != want {
			t.Fatalf("version %d of the page has seq %d, want %d", i, it.Seq, want)
		}
	}
}

// Одновременные проверки пишут свои результаты: ни один не теряется и не перезаписывает другой.
func testConcurrentResults(t *testing.T, e Env) {
	const n = 16
	ctx := context.Background()
	s := e.NewScope(t)
	b, vs := seed(t, e, s, 1)

	for i, err := range concurrently(n, func(i int) error {
		return e.Backend.AppendResult(ctx, s, newResult(b.ID, vs[0].ID, t0.Add(time.Duration(i)*time.Second)))
	}) {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	page, err := e.Backend.ListResults(ctx, s, b.ID, baseline.Page{Limit: baseline.MaxPageLimit})
	must(t, err)
	if len(page.Items) != n {
		t.Fatalf("history has %d results, want %d", len(page.Items), n)
	}
}

// --- атомарные операции ----------------------------------------------------------------------------------------

func exceptionIDs(list []baseline.Exception) map[string]bool {
	ids := make(map[string]bool, len(list))
	for _, e := range list {
		ids[e.ID] = true
	}
	return ids
}

func activeOf(t *testing.T, e Env, s tenant.Scope, id string) baseline.Baseline {
	t.Helper()
	b, err := e.Backend.GetBaseline(context.Background(), s, id)
	must(t, err)
	return b
}

func testCreateWithVersion(t *testing.T, e Env) {
	ctx := context.Background()
	s := e.NewScope(t)
	b := newBaseline(s, "web", t0)
	v := newVersion(b.ID, 1, t0)
	b.ActiveVersionID = "ignored-by-the-store" // хранилище ставит ID первой версии само
	must(t, e.Backend.CreateWithVersion(ctx, b, v))

	got := activeOf(t, e, s, b.ID)
	if got.ActiveVersionID != v.ID {
		t.Fatalf("active version = %q, want %q", got.ActiveVersionID, v.ID)
	}
	gv, err := e.Backend.GetVersion(ctx, s, b.ID, v.ID)
	must(t, err)
	sameJSON(t, v.Normalized(), gv)

	// Повтор ID baseline не заменяет существующий документ и не добавляет версию.
	other := newVersion(b.ID, 1, t0.Add(time.Hour))
	wantErr(t, e.Backend.CreateWithVersion(ctx, b, other), baseline.ErrExists)
	if again := activeOf(t, e, s, b.ID); again.ActiveVersionID != v.ID {
		t.Fatalf("a failed create changed the active version to %q", again.ActiveVersionID)
	}
	page, err := e.Backend.ListVersions(ctx, s, b.ID, baseline.Page{})
	must(t, err)
	if len(page.Items) != 1 {
		t.Fatalf("a failed create left %d versions, want 1", len(page.Items))
	}

	// Атомарность: негодная версия не оставляет baseline без версии.
	b2 := newBaseline(s, "api", t0)
	bad := newVersion(b2.ID, 1, t0)
	bad.Salt = nil
	wantErr(t, e.Backend.CreateWithVersion(ctx, b2, bad), baseline.ErrInvalid)
	wantErr(t, e.Backend.CreateWithVersion(ctx, b2, newVersion(baseline.NewID(), 1, t0)), baseline.ErrInvalid) // чужой baseline
	list, err := e.Backend.ListBaselines(ctx, s)
	must(t, err)
	if len(list) != 1 {
		t.Fatalf("a rejected create left %d baselines, want 1", len(list))
	}

	wantErr(t, e.Backend.CreateWithVersion(ctx, b, v), baseline.ErrExists)
	wantErr(t, e.Backend.CreateWithVersion(ctx, baseline.Baseline{ID: "x", Name: "x"}, v), baseline.ErrInvalidScope)
}

func testCommitVersion(t *testing.T, e Env) {
	ctx := context.Background()
	s := e.NewScope(t)
	b, vs := seed(t, e, s, 2)
	exc := newException("spec.replicas", t0)
	must(t, e.Backend.PutExceptions(ctx, s, b.ID, vs[1].ID, []baseline.Exception{exc}))

	v3 := newVersion(b.ID, 3, t0.Add(time.Hour))
	must(t, e.Backend.CommitVersion(ctx, s, b.ID, v3))
	if got := activeOf(t, e, s, b.ID); got.ActiveVersionID != v3.ID {
		t.Fatalf("active version = %q, want %q", got.ActiveVersionID, v3.ID)
	}
	carried, err := e.Backend.GetExceptions(ctx, s, b.ID, v3.ID)
	must(t, err)
	if len(carried) != 1 || carried[0].ID != exc.ID {
		t.Fatalf("exceptions of the new version = %+v, want the one carried over", carried)
	}
	// Исключения прежней версии остались при ней: версия неизменяема, история не переписывается.
	old, err := e.Backend.GetExceptions(ctx, s, b.ID, vs[1].ID)
	must(t, err)
	if len(old) != 1 {
		t.Fatalf("the previous version lost its exceptions: %+v", old)
	}

	// Неудача ничего не меняет: активная версия, число версий и исключения остаются прежними.
	wantErr(t, e.Backend.CommitVersion(ctx, s, b.ID, newVersion(b.ID, 3, t0.Add(2*time.Hour))), baseline.ErrExists) // номер занят
	dupID := newVersion(b.ID, 4, t0)
	dupID.ID = v3.ID
	wantErr(t, e.Backend.CommitVersion(ctx, s, b.ID, dupID), baseline.ErrExists) // ID занят
	bad := newVersion(b.ID, 4, t0)
	bad.Salt = nil
	wantErr(t, e.Backend.CommitVersion(ctx, s, b.ID, bad), baseline.ErrInvalid)
	wantErr(t, e.Backend.CommitVersion(ctx, s, b.ID, newVersion(baseline.NewID(), 4, t0)), baseline.ErrInvalid) // версия чужого baseline
	missing := baseline.NewID()
	wantErr(t, e.Backend.CommitVersion(ctx, s, missing, newVersion(missing, 1, t0)), baseline.ErrNotFound)
	if got := activeOf(t, e, s, b.ID); got.ActiveVersionID != v3.ID {
		t.Fatalf("a failed commit changed the active version to %q", got.ActiveVersionID)
	}
	page, err := e.Backend.ListVersions(ctx, s, b.ID, baseline.Page{})
	must(t, err)
	if len(page.Items) != 3 {
		t.Fatalf("a failed commit left %d versions, want 3", len(page.Items))
	}

	// Чужая область не видит baseline.
	wantErr(t, e.Backend.CommitVersion(ctx, e.NewScope(t), b.ID, newVersion(b.ID, 9, t0)), baseline.ErrNotFound)

	// У baseline без активной версии исключений переносить нечего: версия просто становится активной.
	empty := newBaseline(s, "empty", t0)
	must(t, e.Backend.PutBaseline(ctx, empty))
	first := newVersion(empty.ID, 1, t0)
	must(t, e.Backend.CommitVersion(ctx, s, empty.ID, first))
	if got := activeOf(t, e, s, empty.ID); got.ActiveVersionID != first.ID {
		t.Fatalf("active version = %q, want %q", got.ActiveVersionID, first.ID)
	}
	if ex, err := e.Backend.GetExceptions(ctx, s, empty.ID, first.ID); err != nil || len(ex) != 0 {
		t.Fatalf("exceptions = %+v, %v; want none", ex, err)
	}
}

func testSetActiveVersion(t *testing.T, e Env) {
	ctx := context.Background()
	s := e.NewScope(t)
	b, vs := seed(t, e, s, 3)

	got, err := e.Backend.SetActiveVersion(ctx, s, b.ID, vs[0].ID)
	must(t, err)
	if got.ActiveVersionID != vs[0].ID || got.ID != b.ID {
		t.Fatalf("returned %q of %q, want %q of %q", got.ActiveVersionID, got.ID, vs[0].ID, b.ID)
	}
	if stored := activeOf(t, e, s, b.ID); stored.ActiveVersionID != vs[0].ID {
		t.Fatalf("stored active version = %q, want %q", stored.ActiveVersionID, vs[0].ID)
	}

	wantErr(t, errOf(e.Backend.SetActiveVersion(ctx, s, b.ID, baseline.NewID())), baseline.ErrNotFound)
	wantErr(t, errOf(e.Backend.SetActiveVersion(ctx, s, baseline.NewID(), vs[0].ID)), baseline.ErrNotFound)
	wantErr(t, errOf(e.Backend.SetActiveVersion(ctx, s, b.ID, "../x")), baseline.ErrNotFound)
	wantErr(t, errOf(e.Backend.SetActiveVersion(ctx, e.NewScope(t), b.ID, vs[1].ID)), baseline.ErrNotFound)
	if stored := activeOf(t, e, s, b.ID); stored.ActiveVersionID != vs[0].ID {
		t.Fatalf("a failed switch changed the active version to %q", stored.ActiveVersionID)
	}
}

func errOf(_ baseline.Baseline, err error) error { return err }

func testUpdateActiveExceptions(t *testing.T, e Env) {
	ctx := context.Background()
	s := e.NewScope(t)

	// Без активной версии правки нет.
	empty := newBaseline(s, "empty", t0)
	must(t, e.Backend.PutBaseline(ctx, empty))
	called := false
	err := e.Backend.UpdateActiveExceptions(ctx, s, empty.ID, func(c []baseline.Exception) ([]baseline.Exception, error) {
		called = true
		return c, nil
	})
	wantErr(t, err, baseline.ErrNoActiveVersion)
	if called {
		t.Fatal("fn was called for a baseline without an active version")
	}
	wantErr(t, e.Backend.UpdateActiveExceptions(ctx, s, baseline.NewID(), func(c []baseline.Exception) ([]baseline.Exception, error) { return c, nil }), baseline.ErrNotFound)

	b, vs := seed(t, e, s, 2)
	a := newException("spec.replicas", t0)
	must(t, e.Backend.UpdateActiveExceptions(ctx, s, b.ID, func(c []baseline.Exception) ([]baseline.Exception, error) {
		if len(c) != 0 {
			t.Fatalf("current = %+v, want none", c)
		}
		return append(c, a), nil
	}))
	// Список лежит на активной версии, а не на других.
	if got, err := e.Backend.GetExceptions(ctx, s, b.ID, vs[1].ID); err != nil || len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("active exceptions = %+v, %v", got, err)
	}
	if got, err := e.Backend.GetExceptions(ctx, s, b.ID, vs[0].ID); err != nil || len(got) != 0 {
		t.Fatalf("a non-active version has exceptions: %+v, %v", got, err)
	}

	// Ошибка fn и негодный результат ничего не пишут.
	boom := errors.ErrUnsupported // маркер ошибки fn: своего текста нет, i18n-check не требует перевода
	wantErr(t, e.Backend.UpdateActiveExceptions(ctx, s, b.ID, func([]baseline.Exception) ([]baseline.Exception, error) { return nil, boom }), boom)
	noComment := newException("spec.x", t0)
	noComment.Comment = ""
	wantErr(t, e.Backend.UpdateActiveExceptions(ctx, s, b.ID, func(c []baseline.Exception) ([]baseline.Exception, error) {
		return append(c, noComment), nil
	}), baseline.ErrInvalid)
	if got, _ := e.Backend.GetExceptions(ctx, s, b.ID, vs[1].ID); len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("a failed update changed the list: %+v", got)
	}

	// Пустой результат очищает список.
	must(t, e.Backend.UpdateActiveExceptions(ctx, s, b.ID, func([]baseline.Exception) ([]baseline.Exception, error) { return nil, nil }))
	if got, err := e.Backend.GetExceptions(ctx, s, b.ID, vs[1].ID); err != nil || len(got) != 0 {
		t.Fatalf("exceptions after clearing = %+v, %v", got, err)
	}
	// Чужая область.
	wantErr(t, e.Backend.UpdateActiveExceptions(ctx, e.NewScope(t), b.ID, func(c []baseline.Exception) ([]baseline.Exception, error) { return c, nil }), baseline.ErrNotFound)
}

// Параллельные добавления исключений не затирают друг друга: потеря записи была бы тихой, а исключения подавляют drift.
func testConcurrentExceptionEdits(t *testing.T, e Env) {
	const n = 12
	ctx := context.Background()
	s := e.NewScope(t)
	b, vs := seed(t, e, s, 1)

	added := make([]baseline.Exception, n)
	for i, err := range concurrently(n, func(i int) error {
		added[i] = newException(fmt.Sprintf("spec.field%d", i), t0)
		return e.Backend.UpdateActiveExceptions(ctx, s, b.ID, func(c []baseline.Exception) ([]baseline.Exception, error) {
			return append(c, added[i]), nil
		})
	}) {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	got, err := e.Backend.GetExceptions(ctx, s, b.ID, vs[0].ID)
	must(t, err)
	ids := exceptionIDs(got)
	for i, ex := range added {
		if !ids[ex.ID] {
			t.Errorf("exception %d was lost", i)
		}
	}
	if len(got) != n {
		t.Fatalf("%d exceptions stored, want %d", len(got), n)
	}
}

// Исключение, добавленное во время фиксации новой версии, оказывается на новой активной версии: не теряется ни до,
// ни после переключения.
func testConcurrentCommitAndEdits(t *testing.T, e Env) {
	const n = 8
	ctx := context.Background()
	s := e.NewScope(t)
	b, _ := seed(t, e, s, 1)

	added := make([]baseline.Exception, n)
	committed := make([]baseline.Version, n)
	errs := concurrently(2*n, func(i int) error {
		if i%2 == 0 {
			k := i / 2
			added[k] = newException(fmt.Sprintf("spec.field%d", k), t0)
			return e.Backend.UpdateActiveExceptions(ctx, s, b.ID, func(c []baseline.Exception) ([]baseline.Exception, error) {
				return append(c, added[k]), nil
			})
		}
		k := i / 2
		committed[k] = newVersion(b.ID, 100+k, t0.Add(time.Duration(k)*time.Second))
		return e.Backend.CommitVersion(ctx, s, b.ID, committed[k])
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("operation %d: %v", i, err)
		}
	}
	active := activeOf(t, e, s, b.ID)
	got, err := e.Backend.GetExceptions(ctx, s, b.ID, active.ActiveVersionID)
	must(t, err)
	ids := exceptionIDs(got)
	for i, ex := range added {
		if !ids[ex.ID] {
			t.Errorf("exception %d is missing on the active version", i)
		}
	}
}

// Очистка старых версий и смена активной версии на старую идут одновременно: указатель никогда не остаётся на
// удалённой версии.
func testConcurrentPruneAndSetActive(t *testing.T, e Env) {
	ctx := context.Background()
	s := e.NewScope(t)
	b, vs := seed(t, e, s, baseline.MinVersionsKept+10)
	for round := range 5 {
		target := vs[round] // самые старые версии: очистка хочет их удалить
		errs := concurrently(2, func(i int) error {
			if i == 0 {
				_, err := e.Backend.PruneVersions(ctx, baseline.MinVersionsKept)
				return err
			}
			_, err := e.Backend.SetActiveVersion(ctx, s, b.ID, target.ID)
			if errors.Is(err, baseline.ErrNotFound) {
				return nil // очистка успела раньше: версии уже нет, это допустимый исход
			}
			return err
		})
		for _, err := range errs {
			must(t, err)
		}
		active := activeOf(t, e, s, b.ID)
		if _, err := e.Backend.GetVersion(ctx, s, b.ID, active.ActiveVersionID); err != nil {
			t.Fatalf("round %d: the active version %q cannot be read: %v", round, active.ActiveVersionID, err)
		}
	}
}
