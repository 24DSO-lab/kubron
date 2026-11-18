// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incidenttest

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// NewProblem строит проблему для тестов: id и время обновления задаёт вызывающий.
func NewProblem(scope tenant.Scope, id string, updated time.Time) *incident.Problem {
	return &incident.Problem{
		ID: id, Scope: scope, Key: "shared_node:/Node//node-1", Kind: "shared_node",
		Source: diagnose.ResourceRef{Kind: "Node", Name: "node-1"},
		Status: incident.ProblemOpen, Origin: incident.ProblemAuto,
		Members: []incident.ProblemMember{
			{IncidentID: "i1", AddedAt: updated, AddedBy: incident.AutoMember},
			{IncidentID: "i2", AddedAt: updated.Add(time.Second), AddedBy: "alice"},
		},
		CreatedAt: updated, UpdatedAt: updated,
	}
}

// RunProblems выполняет контрактные тесты хранилища проблем. newBackend возвращает ЧИСТОЕ хранилище для каждого
// подтеста.
func RunProblems(t *testing.T, newBackend func(t *testing.T) incident.ProblemBackend) {
	t.Helper()
	cases := []struct {
		name string
		fn   func(t *testing.T, b incident.ProblemBackend)
	}{
		{"problem roundtrip", problemRoundtrip},
		{"problem optimistic version", problemVersions},
		{"problem scope isolation", problemIsolation},
		{"problem invalid scope fails closed", problemInvalidScope},
		{"problem unsafe identifiers are rejected", problemUnsafeIDs},
		{"problem invalid status is rejected", problemInvalidStatus},
		{"problem list paging and filter", problemPaging},
		{"problem delete", problemDelete},
		{"problem purge by retention", problemPurge},
		{"problem concurrent puts", problemConcurrent},
		{"problem with the maximum number of members", problemMaxMembers},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.fn(t, newBackend(t)) })
	}
}

func problemRoundtrip(t *testing.T, b incident.ProblemBackend) {
	ctx := t.Context()
	p := NewProblem(Scope, "p1", base.Add(123456789*time.Nanosecond))
	p.Detached = []string{"i9"}
	must(t, b.PutProblem(ctx, p))
	if p.Version != 1 {
		t.Fatalf("version after create = %d", p.Version)
	}
	got, err := b.GetProblem(ctx, Scope, "p1")
	must(t, err)
	if got.Version != 1 || got.Key != p.Key || got.Kind != "shared_node" || got.Status != incident.ProblemOpen ||
		got.Origin != incident.ProblemAuto || got.Scope != Scope || got.Source.Name != "node-1" ||
		len(got.Members) != 2 || got.Members[1].AddedBy != "alice" || got.Members[0].AddedBy != incident.AutoMember ||
		len(got.Detached) != 1 || got.Detached[0] != "i9" {
		t.Errorf("problem read back = %+v", got)
	}
	if !got.UpdatedAt.Equal(incident.Micro(p.UpdatedAt)) || got.UpdatedAt.Location() != time.UTC {
		t.Errorf("time = %v, want microsecond UTC %v", got.UpdatedAt, incident.Micro(p.UpdatedAt))
	}
	if _, err := b.GetProblem(ctx, Scope, "missing"); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("missing problem: %v", err)
	}
	// Пустой состав сохраняется как пустой список, а не null.
	e := NewProblem(Scope, "p2", base)
	e.Members = nil
	must(t, b.PutProblem(ctx, e))
	if g, err := b.GetProblem(ctx, Scope, "p2"); err != nil || g.Members == nil || len(g.Members) != 0 {
		t.Errorf("empty members = %+v, %v", g, err)
	}
}

func problemVersions(t *testing.T, b incident.ProblemBackend) {
	ctx := t.Context()
	p := NewProblem(Scope, "p1", base)
	must(t, b.PutProblem(ctx, p))
	// Повторное создание уже существующей записи — конфликт.
	if err := b.PutProblem(ctx, NewProblem(Scope, "p1", base)); !errors.Is(err, incident.ErrConflict) {
		t.Errorf("duplicate create: %v", err)
	}
	// Замена несуществующей записи с ненулевой версией — конфликт.
	ghost := NewProblem(Scope, "ghost", base)
	ghost.Version = 4
	if err := b.PutProblem(ctx, ghost); !errors.Is(err, incident.ErrConflict) {
		t.Errorf("replace of a missing problem: %v", err)
	}
	stale := *p // версия 1
	p.Status = incident.ProblemClosed
	must(t, b.PutProblem(ctx, p))
	if p.Version != 2 {
		t.Fatalf("version after replace = %d", p.Version)
	}
	stale.Status = incident.ProblemOpen
	if err := b.PutProblem(ctx, &stale); !errors.Is(err, incident.ErrConflict) {
		t.Errorf("stale replace: %v", err)
	}
	if got, _ := b.GetProblem(ctx, Scope, "p1"); got.Status != incident.ProblemClosed || got.Version != 2 {
		t.Errorf("stored = %+v", got)
	}
}

func problemIsolation(t *testing.T, b incident.ProblemBackend) {
	ctx := t.Context()
	other := tenant.Scope{OrgID: "org1", TeamID: "team1", ClusterID: "cl2"}
	foreign := tenant.Scope{OrgID: "org2", TeamID: "team1", ClusterID: "cl1"}
	must(t, b.PutProblem(ctx, NewProblem(Scope, "p1", base)))
	// Один и тот же id в разных областях — разные записи.
	must(t, b.PutProblem(ctx, NewProblem(other, "p1", base.Add(time.Hour))))
	for _, sc := range []tenant.Scope{other, foreign} {
		if _, err := b.GetProblem(ctx, sc, "nope"); !errors.Is(err, incident.ErrNotFound) {
			t.Errorf("%v: %v", sc, err)
		}
	}
	if _, err := b.GetProblem(ctx, foreign, "p1"); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("foreign organization sees a problem: %v", err)
	}
	if err := b.DeleteProblem(ctx, foreign, "p1"); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("foreign delete: %v", err)
	}
	page, err := b.ListProblems(ctx, foreign, incident.ProblemQuery{})
	must(t, err)
	if len(page.Items) != 0 {
		t.Errorf("foreign list = %+v", page.Items)
	}
	page, err = b.ListProblems(ctx, Scope, incident.ProblemQuery{})
	must(t, err)
	if len(page.Items) != 1 || page.Items[0].Scope != Scope {
		t.Errorf("list leaked another cluster: %+v", page.Items)
	}
	if _, err := b.GetProblem(ctx, Scope, "p1"); err != nil {
		t.Errorf("own problem lost after foreign delete attempt: %v", err)
	}
}

func problemInvalidScope(t *testing.T, b incident.ProblemBackend) {
	ctx := t.Context()
	for _, sc := range []tenant.Scope{{}, {OrgID: "org1"}, {OrgID: "org1", TeamID: "team1"}, {TeamID: "t", ClusterID: "c"}} {
		if err := b.PutProblem(ctx, NewProblem(sc, "p1", base)); !errors.Is(err, incident.ErrInvalidScope) {
			t.Errorf("put %+v: %v", sc, err)
		}
		if _, err := b.GetProblem(ctx, sc, "p1"); !errors.Is(err, incident.ErrInvalidScope) {
			t.Errorf("get %+v: %v", sc, err)
		}
		if _, err := b.ListProblems(ctx, sc, incident.ProblemQuery{}); !errors.Is(err, incident.ErrInvalidScope) {
			t.Errorf("list %+v: %v", sc, err)
		}
		if err := b.DeleteProblem(ctx, sc, "p1"); !errors.Is(err, incident.ErrInvalidScope) {
			t.Errorf("delete %+v: %v", sc, err)
		}
	}
}

func problemUnsafeIDs(t *testing.T, b incident.ProblemBackend) {
	ctx := t.Context()
	for _, id := range []string{"", "../x", "a/b", `a\b`, "a b", "..", "a\x00b"} {
		if err := b.PutProblem(ctx, NewProblem(Scope, id, base)); !errors.Is(err, incident.ErrInvalidID) {
			t.Errorf("put %q: %v", id, err)
		}
		if _, err := b.GetProblem(ctx, Scope, id); !errors.Is(err, incident.ErrInvalidID) {
			t.Errorf("get %q: %v", id, err)
		}
		if err := b.DeleteProblem(ctx, Scope, id); !errors.Is(err, incident.ErrInvalidID) {
			t.Errorf("delete %q: %v", id, err)
		}
	}
	if _, err := b.PurgeProblems(ctx, "../org", base); !errors.Is(err, incident.ErrInvalidID) {
		t.Errorf("purge with an unsafe organization: %v", err)
	}
}

func problemInvalidStatus(t *testing.T, b incident.ProblemBackend) {
	p := NewProblem(Scope, "p1", base)
	p.Status = "weird"
	if err := b.PutProblem(t.Context(), p); !errors.Is(err, incident.ErrInvalidProblem) {
		t.Errorf("invalid status: %v", err)
	}
	if _, err := b.GetProblem(t.Context(), Scope, "p1"); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("a rejected problem was stored: %v", err)
	}
}

func problemPaging(t *testing.T, b incident.ProblemBackend) {
	ctx := t.Context()
	// 5 проблем; у двух одинаковое время обновления — порядок задаёт ID по убыванию.
	times := map[string]time.Time{"p1": base, "p2": base.Add(time.Minute), "p3": base.Add(time.Minute),
		"p4": base.Add(2 * time.Minute), "p5": base.Add(3 * time.Minute)}
	for id, tm := range times {
		p := NewProblem(Scope, id, tm)
		if id == "p2" || id == "p5" {
			p.Status = incident.ProblemClosed
		}
		must(t, b.PutProblem(ctx, p))
	}
	want := []string{"p5", "p4", "p3", "p2", "p1"}
	var got []string
	cursor := ""
	for i := 0; i < 10; i++ {
		page, err := b.ListProblems(ctx, Scope, incident.ProblemQuery{Limit: 2, Cursor: cursor})
		must(t, err)
		for _, p := range page.Items {
			got = append(got, p.ID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("paged order = %v, want %v", got, want)
	}
	page, err := b.ListProblems(ctx, Scope, incident.ProblemQuery{Status: incident.ProblemOpen})
	must(t, err)
	got = nil
	for _, p := range page.Items {
		got = append(got, p.ID)
	}
	if fmt.Sprint(got) != "[p4 p3 p1]" || page.NextCursor != "" {
		t.Errorf("open problems = %v (next %q)", got, page.NextCursor)
	}
	if _, err := b.ListProblems(ctx, Scope, incident.ProblemQuery{Cursor: "%%%not-a-cursor"}); err == nil {
		t.Error("a malformed cursor must be rejected")
	}
	empty, err := b.ListProblems(ctx, tenant.Scope{OrgID: "org1", TeamID: "team1", ClusterID: "empty"}, incident.ProblemQuery{})
	if err != nil || empty.Items == nil || len(empty.Items) != 0 {
		t.Errorf("empty list = %+v, %v", empty, err)
	}
}

func problemDelete(t *testing.T, b incident.ProblemBackend) {
	ctx := t.Context()
	must(t, b.PutProblem(ctx, NewProblem(Scope, "p1", base)))
	must(t, b.DeleteProblem(ctx, Scope, "p1"))
	if _, err := b.GetProblem(ctx, Scope, "p1"); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("deleted problem still readable: %v", err)
	}
	if err := b.DeleteProblem(ctx, Scope, "p1"); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
	// После удаления тот же id можно создать заново с версии 0.
	must(t, b.PutProblem(ctx, NewProblem(Scope, "p1", base)))
}

func problemPurge(t *testing.T, b incident.ProblemBackend) {
	ctx := t.Context()
	cl2 := tenant.Scope{OrgID: "org1", TeamID: "team2", ClusterID: "cl2"}
	foreign := tenant.Scope{OrgID: "org2", TeamID: "team1", ClusterID: "cl1"}
	cut := base.Add(time.Hour)
	must(t, b.PutProblem(ctx, NewProblem(Scope, "old", base)))
	must(t, b.PutProblem(ctx, NewProblem(cl2, "old", base.Add(time.Minute))))
	must(t, b.PutProblem(ctx, NewProblem(Scope, "edge", cut))) // ровно на границе — остаётся
	must(t, b.PutProblem(ctx, NewProblem(Scope, "new", cut.Add(time.Minute))))
	must(t, b.PutProblem(ctx, NewProblem(foreign, "old", base))) // чужая организация — не трогается
	n, err := b.PurgeProblems(ctx, "org1", cut)
	must(t, err)
	if n != 2 {
		t.Errorf("purged = %d, want 2 (both clusters of the organization)", n)
	}
	for _, c := range []struct {
		sc  tenant.Scope
		id  string
		has bool
	}{{Scope, "old", false}, {cl2, "old", false}, {Scope, "edge", true}, {Scope, "new", true}, {foreign, "old", true}} {
		_, err := b.GetProblem(ctx, c.sc, c.id)
		if c.has != (err == nil) {
			t.Errorf("%v/%s: present = %v, want %v (%v)", c.sc, c.id, err == nil, c.has, err)
		}
	}
	if n, err := b.PurgeProblems(ctx, "org1", cut); err != nil || n != 0 {
		t.Errorf("second purge = %d, %v", n, err)
	}
	if n, err := b.PurgeProblems(ctx, "unknown", cut); err != nil || n != 0 {
		t.Errorf("purge of an unknown organization = %d, %v", n, err)
	}
}

// problemConcurrent: из N одновременных замен одной и той же версии удаётся ровно одна.
func problemConcurrent(t *testing.T, b incident.ProblemBackend) {
	ctx := t.Context()
	must(t, b.PutProblem(ctx, NewProblem(Scope, "p1", base)))
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := b.GetProblem(ctx, Scope, "p1")
			if err != nil {
				errs[i] = err
				return
			}
			p.Version = 1 // все читали версию 1
			p.Members = append(p.Members, incident.ProblemMember{IncidentID: fmt.Sprintf("n%d", i), AddedAt: base, AddedBy: "u"})
			errs[i] = b.PutProblem(ctx, p)
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, incident.ErrConflict):
			t.Errorf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Errorf("successful concurrent replaces = %d, want 1", wins)
	}
	if got, _ := b.GetProblem(ctx, Scope, "p1"); got.Version != 2 || len(got.Members) != 3 {
		t.Errorf("stored = version %d, %d members", got.Version, len(got.Members))
	}
}

// problemMaxMembers: документ с MaxProblemMembers участниками и списком отсоединённых пишется и читается целиком.
func problemMaxMembers(t *testing.T, b incident.ProblemBackend) {
	ctx := t.Context()
	p := NewProblem(Scope, "big", base)
	p.Members = p.Members[:0]
	for i := range incident.MaxProblemMembers {
		p.Members = append(p.Members, incident.ProblemMember{IncidentID: fmt.Sprintf("inc%03d", i), AddedAt: base.Add(time.Duration(i) * time.Second), AddedBy: "u"})
		p.Detached = append(p.Detached, fmt.Sprintf("det%03d", i))
	}
	must(t, b.PutProblem(ctx, p))
	got, err := b.GetProblem(ctx, Scope, "big")
	must(t, err)
	if len(got.Members) != incident.MaxProblemMembers || got.Members[99].IncidentID != "inc099" || len(got.Detached) != incident.MaxProblemMembers {
		t.Errorf("members %d, detached %d", len(got.Members), len(got.Detached))
	}
}
