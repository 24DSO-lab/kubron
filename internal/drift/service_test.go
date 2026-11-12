// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package drift

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/baseline"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

type svcEnv struct {
	svc  *Service
	c    *fakeCluster
	sess Session
	now  time.Time
}

func newSvcEnv(t *testing.T) *svcEnv {
	t.Helper()
	be := baseline.NewFileBackend(t.TempDir())
	env := &svcEnv{c: newCluster(), now: t0}
	env.svc = NewService(be, ServiceOptions{Now: func() time.Time { return env.now }})
	env.sess = Session{Scope: tenant.Scope{OrgID: "o1", TeamID: "t1", ClusterID: "c1"}, Actor: "alice", Reader: env.c, CorrelationID: "corr-1"}
	env.c.put(deployment("web", "nginx:1", canaryPW, 2))
	env.c.put(configMap("cfg", map[string]any{"a": "1"}))
	return env
}

func (e *svcEnv) create(t *testing.T) (baseline.Baseline, baseline.Version) {
	t.Helper()
	pv, err := e.svc.PreviewNew(t.Context(), e.sess, nsSpec())
	if err != nil {
		t.Fatal(err)
	}
	b, v, err := e.svc.Create(t.Context(), e.sess, CreateInput{Name: "prod", Spec: nsSpec(), Comment: "initial", Token: pv.Token, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	return b, v
}

func TestCreateRequiresIntent(t *testing.T) {
	t.Parallel()
	e := newSvcEnv(t)
	pv, err := e.svc.PreviewNew(t.Context(), e.sess, nsSpec())
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		in   CreateInput
		want error
	}{
		"no confirmation": {CreateInput{Name: "p", Spec: nsSpec(), Comment: "c", Token: pv.Token}, ErrConfirmationRequired},
		"no comment":      {CreateInput{Name: "p", Spec: nsSpec(), Comment: "  ", Token: pv.Token, Confirmed: true}, ErrCommentRequired},
		"no token":        {CreateInput{Name: "p", Spec: nsSpec(), Comment: "c", Confirmed: true}, ErrPreviewExpired},
		"no name":         {CreateInput{Spec: nsSpec(), Comment: "c", Token: pv.Token, Confirmed: true}, baseline.ErrInvalid},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := e.svc.Create(t.Context(), e.sess, tc.in); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCreateLeavesNothingOnFailure(t *testing.T) {
	t.Parallel()
	e := newSvcEnv(t)
	pv, _ := e.svc.PreviewNew(t.Context(), e.sess, nsSpec())
	e.c.put(configMap("cfg", map[string]any{"a": "changed-after-preview"}))
	_, _, err := e.svc.Create(t.Context(), e.sess, CreateInput{Name: "p", Spec: nsSpec(), Comment: "c", Token: pv.Token, Confirmed: true})
	if !errors.Is(err, ErrStalePreview) {
		t.Fatalf("err = %v", err)
	}
	if l, _ := e.svc.Baselines(t.Context(), e.sess); len(l) != 0 {
		t.Fatalf("baselines created on failure: %v", l)
	}
	empty := Spec{Namespaces: []string{"nowhere"}}
	pv, _ = e.svc.PreviewNew(t.Context(), e.sess, empty)
	if _, _, err := e.svc.Create(t.Context(), e.sess, CreateInput{Name: "p", Spec: empty, Comment: "c", Token: pv.Token, Confirmed: true}); !errors.Is(err, ErrEmptySnapshot) {
		t.Fatalf("empty: %v", err)
	}
}

func TestLifecycle(t *testing.T) {
	t.Parallel()
	e := newSvcEnv(t)
	b, v1 := e.create(t)
	if b.ActiveVersionID != v1.ID || v1.Seq != 1 || v1.Author != "alice" || v1.Comment != "initial" {
		t.Fatalf("baseline %+v version %+v", b, v1)
	}

	res, err := e.svc.Check(t.Context(), e.sess, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Matched != 2 || res.Initiator != "alice" || res.CorrelationID != "corr-1" || res.VersionID != v1.ID {
		t.Fatalf("result = %+v", res)
	}

	// Исключение, затем новая версия: исключение переносится.
	ex, err := e.svc.AddException(t.Context(), e.sess, b.ID, ExceptionInput{
		Scope: baseline.ExceptionResource, Ref: baseline.Ref{Kind: "ConfigMap", Namespace: "prod", Name: "cfg"}, Path: "data.a", Comment: "expected drift",
	})
	if err != nil {
		t.Fatal(err)
	}
	e.env(t, func() { e.c.put(configMap("cfg", map[string]any{"a": "2"})) })
	res, _ = e.svc.Check(t.Context(), e.sess, b.ID)
	if res.Summary.Changed != 0 || len(res.Exceptions.Applied) != 1 {
		t.Fatalf("exception must hide the change and stay visible: %+v", res)
	}

	pv, err := e.svc.PreviewVersion(t.Context(), e.sess, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !pv.HasPrevious || len(pv.Changed) != 1 {
		t.Fatalf("preview vs active = %+v", pv)
	}
	v2, err := e.svc.AddVersion(t.Context(), e.sess, VersionInput{BaselineID: b.ID, Comment: "re-snapshot", Token: pv.Token, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if v2.Seq != 2 {
		t.Fatalf("seq = %d", v2.Seq)
	}
	got, _ := e.svc.Baseline(t.Context(), e.sess, b.ID)
	if got.ActiveVersionID != v2.ID {
		t.Fatal("new version must become active")
	}
	if list, _ := e.svc.Exceptions(t.Context(), e.sess, b.ID); len(list) != 1 || list[0].ID != ex.ID {
		t.Fatalf("exceptions not carried: %v", list)
	}

	// Возврат к старой версии: содержимое не менялось.
	if _, err := e.svc.SetActive(t.Context(), e.sess, b.ID, v1.ID); err != nil {
		t.Fatal(err)
	}
	res, _ = e.svc.Check(t.Context(), e.sess, b.ID)
	// У старой версии остаётся её собственное исключение: оно скрывает отличие, но видно в результате.
	if res.VersionID != v1.ID || res.Summary.Changed != 0 || res.Summary.SuppressedChanges != 1 {
		t.Fatalf("old version keeps its own exception: %+v", res.Summary)
	}
	if _, err := e.svc.SetActive(t.Context(), e.sess, b.ID, "missing"); !errors.Is(err, baseline.ErrNotFound) {
		t.Fatalf("unknown version: %v", err)
	}

	vp, _ := e.svc.Versions(t.Context(), e.sess, b.ID, baseline.Page{})
	rp, _ := e.svc.Results(t.Context(), e.sess, b.ID, baseline.Page{})
	if len(vp.Items) != 2 || len(rp.Items) != 3 {
		t.Fatalf("history: %d versions, %d results", len(vp.Items), len(rp.Items))
	}
	if full, err := e.svc.Result(t.Context(), e.sess, b.ID, rp.Items[0].ID); err != nil || full.ID != rp.Items[0].ID {
		t.Fatalf("result: %v", err)
	}

	if err := e.svc.RemoveException(t.Context(), e.sess, b.ID, ex.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RemoveException(t.Context(), e.sess, b.ID, ex.ID); !errors.Is(err, baseline.ErrNotFound) {
		t.Fatalf("second removal: %v", err)
	}

	if err := e.svc.Delete(t.Context(), e.sess, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Check(t.Context(), e.sess, b.ID); !errors.Is(err, baseline.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func (e *svcEnv) env(t *testing.T, f func()) { t.Helper(); f() }

func TestExceptionValidation(t *testing.T) {
	t.Parallel()
	e := newSvcEnv(t)
	b, _ := e.create(t)
	_, err := e.svc.AddException(t.Context(), e.sess, b.ID, ExceptionInput{Scope: baseline.ExceptionKind, Ref: baseline.Ref{Kind: "ConfigMap"}, Path: "data.a"})
	if !errors.Is(err, baseline.ErrInvalid) {
		t.Fatalf("no comment: %v", err)
	}
	_, err = e.svc.AddException(t.Context(), e.sess, b.ID, ExceptionInput{Scope: "bogus", Path: "x", Comment: "c"})
	if !errors.Is(err, baseline.ErrInvalid) {
		t.Fatalf("bad scope: %v", err)
	}
}

func TestTenantIsolation(t *testing.T) {
	t.Parallel()
	e := newSvcEnv(t)
	b, _ := e.create(t)
	other := e.sess
	other.Scope.ClusterID = "c2"
	if _, err := e.svc.Check(t.Context(), other, b.ID); !errors.Is(err, baseline.ErrNotFound) {
		t.Fatalf("check from another cluster: %v", err)
	}
	if err := e.svc.Delete(t.Context(), other, b.ID); !errors.Is(err, baseline.ErrNotFound) {
		t.Fatalf("delete from another cluster: %v", err)
	}
	if l, _ := e.svc.Baselines(t.Context(), other); len(l) != 0 {
		t.Fatalf("list leaks: %v", l)
	}
}

func TestCheckCancelled(t *testing.T) {
	t.Parallel()
	e := newSvcEnv(t)
	b, _ := e.create(t)
	ctx, cancel := contextWithCancel(t)
	cancel()
	if _, err := e.svc.Check(ctx, e.sess, b.ID); err == nil {
		t.Fatal("cancelled check must fail")
	}
	if rp, _ := e.svc.Results(t.Context(), e.sess, b.ID, baseline.Page{}); len(rp.Items) != 0 {
		t.Fatal("cancelled check must not record a result")
	}
}

func contextWithCancel(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithCancel(t.Context())
}
