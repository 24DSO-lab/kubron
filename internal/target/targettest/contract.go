// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package targettest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/target"
)

var (
	orgA   = target.OrganizationOwner("org-a")
	teamA1 = target.TeamOwner("org-a", "team-1")
	orgB   = target.OrganizationOwner("org-b")
	user   = target.Actor{Kind: target.ActorUser, ID: "u1"}
	t0     = time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
)

func viewerOf(s ...target.OwnerScope) target.Viewer { return target.Viewer{Scopes: s} }

// Snapshot строит валидный snapshot для группы g (одна цель в кластере c1).
func Snapshot(id string, g target.Group) target.TargetSnapshot {
	return target.TargetSnapshot{
		ID: id, GroupID: g.ID, GroupRevision: g.Revision, Owner: g.Owner, RequestedBy: user, ResolvedAt: t0,
		Owners:   []target.OwnerObservation{{ClusterID: "c1", Owner: g.Owner}},
		Coverage: []target.ClusterCoverage{{ClusterID: "c1", Outcome: target.OutcomeComplete, Targets: 1}},
		Targets: []target.ResolvedTarget{{
			Identity:        target.TargetIdentity{ClusterID: "c1", Version: "v1", Kind: "ConfigMap", Namespace: "default", Name: "cm", UID: "uid-1"},
			ResourceVersion: "42",
		}},
	}
}

func is(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got error %v, want %v", err, want)
	}
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Run — контрактный набор target.Backend (ADR-0054, ADR-0055). Каждый подтест получает чистый backend.
// File и PostgreSQL реализации Lane C обязаны проходить его целиком.
func Run(t *testing.T, newBackend func(t *testing.T) target.Backend) {
	ctx := context.Background()
	sub := func(name string, f func(t *testing.T, b target.Backend)) {
		t.Run(name, func(t *testing.T) { f(t, newBackend(t)) })
	}

	sub("create get and collision", func(t *testing.T, b target.Backend) {
		g, err := b.CreateGroup(ctx, ValidGroup("g1", orgA))
		ok(t, err)
		if g.Revision != 1 || !g.UpdatedAt.Equal(g.CreatedAt) || g.State != target.GroupActive {
			t.Fatalf("unexpected created group: %+v", g)
		}
		_, err = b.CreateGroup(ctx, ValidGroup("g1", orgB))
		is(t, err, target.ErrAlreadyExists)
		got, err := b.GetGroup(ctx, viewerOf(orgA), "g1")
		ok(t, err)
		if got.Owner != orgA {
			t.Fatal("create collision overwrote the owner")
		}
		got.Name = "mutated"
		got.Selector.Members[0].Name = "mutated"
		again, _ := b.GetGroup(ctx, viewerOf(orgA), "g1")
		if again.Name == "mutated" || again.Selector.Members[0].Name == "mutated" {
			t.Fatal("backend returned memory shared with storage")
		}
	})

	sub("rejects invalid aggregates", func(t *testing.T, b target.Backend) {
		bad := ValidGroup("g1", target.OwnerScope{Kind: target.OwnerTeam, OrgID: "org-a"}) // Team без ID
		_, err := b.CreateGroup(ctx, bad)
		is(t, err, target.ErrInvalid)
		bad = ValidGroup("g1", orgA)
		bad.Revision = 5 // клиент не задаёт revision
		_, err = b.CreateGroup(ctx, bad)
		is(t, err, target.ErrInvalid)
		bad = ValidGroup("g1", orgA)
		bad.GroupContent.Selector = target.SelectorDefinition{}
		_, err = b.CreateGroup(ctx, bad)
		is(t, err, target.ErrInvalid)
	})

	sub("owner is immutable and update bumps revision exactly once", func(t *testing.T, b target.Backend) {
		_, err := b.CreateGroup(ctx, ValidGroup("g1", teamA1))
		ok(t, err)
		for rev := uint64(1); rev <= 5; rev++ {
			c := ValidContent(fmt.Sprintf("name %d", rev))
			g, err := b.UpdateGroup(ctx, target.UpdateGroupRequest{ID: "g1", ExpectedRevision: rev, Content: c, Actor: user, At: t0.Add(time.Duration(rev) * time.Second)})
			ok(t, err)
			if g.Revision != rev+1 {
				t.Fatalf("revision = %d, want %d", g.Revision, rev+1)
			}
			if g.Owner != teamA1 || g.CreatedBy != user || !g.CreatedAt.Equal(t0) {
				t.Fatalf("immutable fields changed: %+v", g)
			}
		}
	})

	sub("stale revision and zero revision", func(t *testing.T, b target.Backend) {
		_, err := b.CreateGroup(ctx, ValidGroup("g1", orgA))
		ok(t, err)
		req := target.UpdateGroupRequest{ID: "g1", ExpectedRevision: 1, Content: ValidContent("n2"), Actor: user, At: t0}
		_, err = b.UpdateGroup(ctx, req)
		ok(t, err)
		req.Content = ValidContent("n3")
		_, err = b.UpdateGroup(ctx, req) // тот же ExpectedRevision: не может перезаписать более новую версию
		is(t, err, target.ErrStaleRevision)
		req.ExpectedRevision = 0
		_, err = b.UpdateGroup(ctx, req)
		is(t, err, target.ErrInvalid) // 0 не wildcard
		got, _ := b.GetGroup(ctx, viewerOf(orgA), "g1")
		if got.Name != "n2" || got.Revision != 2 {
			t.Fatalf("stale writer changed state: %+v", got)
		}
		_, err = b.UpdateGroup(ctx, target.UpdateGroupRequest{ID: "nope", ExpectedRevision: 1, Content: ValidContent("x"), Actor: user, At: t0})
		is(t, err, target.ErrNotFound)
	})

	sub("concurrent updates have exactly one winner", func(t *testing.T, b target.Backend) {
		_, err := b.CreateGroup(ctx, ValidGroup("g1", orgA))
		ok(t, err)
		const n = 24
		var wg sync.WaitGroup
		errs := make([]error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, errs[i] = b.UpdateGroup(ctx, target.UpdateGroupRequest{ID: "g1", ExpectedRevision: 1,
					Content: ValidContent(fmt.Sprintf("w%d", i)), Actor: user, At: t0})
			}(i)
		}
		wg.Wait()
		wins := 0
		for _, e := range errs {
			switch {
			case e == nil:
				wins++
			case !errors.Is(e, target.ErrStaleRevision):
				t.Fatalf("loser got %v, want stale_revision", e)
			}
		}
		g, _ := b.GetGroup(ctx, viewerOf(orgA), "g1")
		if wins != 1 || g.Revision != 2 {
			t.Fatalf("wins=%d revision=%d, want exactly 1 and 2", wins, g.Revision)
		}
	})

	sub("deactivation is a tombstone", func(t *testing.T, b target.Backend) {
		_, err := b.CreateGroup(ctx, ValidGroup("g1", orgA))
		ok(t, err)
		_, err = b.CreateGroup(ctx, ValidGroup("g2", orgA))
		ok(t, err)
		_, err = b.DeactivateGroup(ctx, target.DeactivateGroupRequest{ID: "g1", ExpectedRevision: 9, Actor: user, At: t0})
		is(t, err, target.ErrStaleRevision)
		g, err := b.DeactivateGroup(ctx, target.DeactivateGroupRequest{ID: "g1", ExpectedRevision: 1, Actor: user, At: t0.Add(time.Minute)})
		ok(t, err)
		if g.State != target.GroupDeactivated || g.Revision != 2 || g.DeactivatedAt.IsZero() {
			t.Fatalf("unexpected tombstone: %+v", g)
		}
		got, err := b.GetGroup(ctx, viewerOf(orgA), "g1") // tombstone остаётся читаемым
		ok(t, err)
		if got.State != target.GroupDeactivated || got.Name != g.Name {
			t.Fatal("tombstone lost its content")
		}
		_, err = b.UpdateGroup(ctx, target.UpdateGroupRequest{ID: "g1", ExpectedRevision: 2, Content: ValidContent("x"), Actor: user, At: t0})
		is(t, err, target.ErrDeactivated)
		_, err = b.DeactivateGroup(ctx, target.DeactivateGroupRequest{ID: "g1", ExpectedRevision: 2, Actor: user, At: t0})
		is(t, err, target.ErrDeactivated)
		page, err := b.ListGroups(ctx, viewerOf(orgA), target.ListOptions{Limit: 10})
		ok(t, err)
		if len(page.Groups) != 1 || page.Groups[0].ID != "g2" {
			t.Fatalf("list must hide tombstones by default: %+v", page.Groups)
		}
		page, _ = b.ListGroups(ctx, viewerOf(orgA), target.ListOptions{Limit: 10, IncludeDeactivated: true})
		if len(page.Groups) != 2 {
			t.Fatal("IncludeDeactivated must return tombstones")
		}
	})

	sub("visibility and sharing isolation", func(t *testing.T, b target.Backend) {
		g := ValidGroup("g1", orgA)
		g.Sharing = target.Sharing{SharedWith: []target.OwnerScope{teamA1}}
		_, err := b.CreateGroup(ctx, g)
		ok(t, err)
		_, err = b.CreateGroup(ctx, ValidGroup("g2", orgA))
		ok(t, err)
		_, err = b.GetGroup(ctx, viewerOf(orgB), "g1") // чужой tenant: неотличимо от отсутствующего
		is(t, err, target.ErrNotFound)
		_, err = b.GetGroup(ctx, viewerOf(), "g1")
		is(t, err, target.ErrNotFound)
		_, err = b.GetGroup(ctx, viewerOf(teamA1), "g1") // расшарена команде
		ok(t, err)
		_, err = b.GetGroup(ctx, viewerOf(teamA1), "g2") // не расшарена
		is(t, err, target.ErrNotFound)
		missing, _ := b.GetGroup(ctx, viewerOf(orgB), "nope")
		_ = missing
		page, err := b.ListGroups(ctx, viewerOf(teamA1), target.ListOptions{Limit: 10})
		ok(t, err)
		if len(page.Groups) != 1 || page.Groups[0].ID != "g1" {
			t.Fatalf("list leaked or lost groups: %+v", page.Groups)
		}
		// Организация не включает свои Team: видимость — точное совпадение scope.
		_, err = b.GetGroup(ctx, viewerOf(orgA), "g1")
		ok(t, err)
		tg := ValidGroup("g3", teamA1)
		_, err = b.CreateGroup(ctx, tg)
		ok(t, err)
		_, err = b.GetGroup(ctx, viewerOf(orgA), "g3")
		is(t, err, target.ErrNotFound)
	})

	sub("list pagination is ordered and stable", func(t *testing.T, b target.Backend) {
		for _, id := range []string{"g3", "g1", "g5", "g2", "g4"} {
			_, err := b.CreateGroup(ctx, ValidGroup(id, orgA))
			ok(t, err)
		}
		var seen []string
		after := ""
		for i := 0; i < 10; i++ {
			page, err := b.ListGroups(ctx, viewerOf(orgA), target.ListOptions{Limit: 2, After: after})
			ok(t, err)
			for _, g := range page.Groups {
				seen = append(seen, g.ID)
			}
			if page.NextAfter == "" {
				break
			}
			after = page.NextAfter
		}
		want := []string{"g1", "g2", "g3", "g4", "g5"}
		if fmt.Sprint(seen) != fmt.Sprint(want) {
			t.Fatalf("pages = %v, want %v", seen, want)
		}
	})

	sub("snapshot publication is atomic with group revision", func(t *testing.T, b target.Backend) {
		g, err := b.CreateGroup(ctx, ValidGroup("g1", orgA))
		ok(t, err)
		pub, err := b.PublishSnapshot(ctx, Snapshot("s1", g))
		ok(t, err)
		if pub.GroupRevision != 1 {
			t.Fatal("snapshot must pin the group revision")
		}
		_, err = b.PublishSnapshot(ctx, Snapshot("s1", g))
		is(t, err, target.ErrAlreadyExists) // опубликованный snapshot неизменяем
		got, err := b.GetSnapshot(ctx, viewerOf(orgA), "s1")
		ok(t, err)
		got.Targets[0].Identity.UID = "mutated"
		again, _ := b.GetSnapshot(ctx, viewerOf(orgA), "s1")
		if again.Targets[0].Identity.UID != "uid-1" {
			t.Fatal("snapshot memory is shared with storage")
		}
		_, err = b.GetSnapshot(ctx, viewerOf(orgB), "s1")
		is(t, err, target.ErrNotFound)

		_, err = b.UpdateGroup(ctx, target.UpdateGroupRequest{ID: "g1", ExpectedRevision: 1, Content: ValidContent("n2"), Actor: user, At: t0})
		ok(t, err)
		_, err = b.PublishSnapshot(ctx, Snapshot("s2", g)) // группа уже ушла вперёд
		is(t, err, target.ErrGroupRevisionChanged)
		if _, err := b.GetSnapshot(ctx, viewerOf(orgA), "s2"); !errors.Is(err, target.ErrNotFound) {
			t.Fatal("snapshot was written despite stale group revision")
		}
		_, err = b.PublishSnapshot(ctx, Snapshot("s3", target.Group{ID: "nope", Owner: orgA, Revision: 1}))
		is(t, err, target.ErrNotFound)

		cur, _ := b.GetGroup(ctx, viewerOf(orgA), "g1")
		_, err = b.DeactivateGroup(ctx, target.DeactivateGroupRequest{ID: "g1", ExpectedRevision: cur.Revision, Actor: user, At: t0})
		ok(t, err)
		cur, _ = b.GetGroup(ctx, viewerOf(orgA), "g1")
		_, err = b.PublishSnapshot(ctx, Snapshot("s4", cur))
		is(t, err, target.ErrGroupRevisionChanged) // tombstone не публикует snapshots
	})

	sub("snapshot invariants are enforced by the backend", func(t *testing.T, b target.Backend) {
		g, err := b.CreateGroup(ctx, ValidGroup("g1", orgA))
		ok(t, err)
		noUID := Snapshot("s1", g)
		noUID.Targets[0].Identity.UID = ""
		_, err = b.PublishSnapshot(ctx, noUID)
		is(t, err, target.ErrInvalid)
		partial := Snapshot("s2", g)
		partial.Coverage[0].Outcome = target.OutcomePartial
		partial.Coverage[0].Reason = target.ReasonClusterUnavailable
		_, err = b.PublishSnapshot(ctx, partial)
		is(t, err, target.ErrInvalid) // partial никогда не публикуется
		foreign := Snapshot("s3", g)
		foreign.Owner = orgB
		_, err = b.PublishSnapshot(ctx, foreign)
		is(t, err, target.ErrInvalid)
		if n := len(mustList(t, b)); n != 1 {
			t.Fatalf("groups = %d", n)
		}
	})

	sub("concurrent publish of one snapshot ID has one winner", func(t *testing.T, b target.Backend) {
		g, err := b.CreateGroup(ctx, ValidGroup("g1", orgA))
		ok(t, err)
		const n = 16
		var wg sync.WaitGroup
		errs := make([]error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, errs[i] = b.PublishSnapshot(ctx, Snapshot("s1", g))
			}(i)
		}
		wg.Wait()
		wins := 0
		for _, e := range errs {
			if e == nil {
				wins++
			} else if !errors.Is(e, target.ErrAlreadyExists) {
				t.Fatalf("loser got %v", e)
			}
		}
		if wins != 1 {
			t.Fatalf("wins = %d, want 1", wins)
		}
	})

	sub("publish racing with update never leaves a snapshot for a stale revision", func(t *testing.T, b target.Backend) {
		for round := 0; round < 40; round++ {
			id := fmt.Sprintf("g%d", round)
			g, err := b.CreateGroup(ctx, ValidGroup(id, orgA))
			ok(t, err)
			var wg sync.WaitGroup
			var pubErr, updErr error
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, pubErr = b.PublishSnapshot(ctx, Snapshot("s-"+id, g))
			}()
			go func() {
				defer wg.Done()
				_, updErr = b.UpdateGroup(ctx, target.UpdateGroupRequest{ID: id, ExpectedRevision: 1, Content: ValidContent("n"), Actor: user, At: t0})
			}()
			wg.Wait()
			ok(t, updErr)
			if pubErr != nil && !errors.Is(pubErr, target.ErrGroupRevisionChanged) {
				t.Fatalf("publish got %v", pubErr)
			}
			// После завершения Update публикация для revision 1 обязана отказывать.
			_, err = b.PublishSnapshot(ctx, Snapshot("late-"+id, g))
			is(t, err, target.ErrGroupRevisionChanged)
		}
	})

	sub("cancelled context fails closed without changing state", func(t *testing.T, b target.Backend) {
		_, err := b.CreateGroup(ctx, ValidGroup("g1", orgA))
		ok(t, err)
		c, cancel := context.WithCancel(ctx)
		cancel()
		_, err = b.UpdateGroup(c, target.UpdateGroupRequest{ID: "g1", ExpectedRevision: 1, Content: ValidContent("x"), Actor: user, At: t0})
		is(t, err, target.ErrUnavailable)
		got, _ := b.GetGroup(ctx, viewerOf(orgA), "g1")
		if got.Revision != 1 {
			t.Fatal("cancelled update changed state")
		}
	})
}

func mustList(t *testing.T, b target.Backend) []target.Group {
	t.Helper()
	p, err := b.ListGroups(context.Background(), viewerOf(orgA), target.ListOptions{Limit: 100, IncludeDeactivated: true})
	ok(t, err)
	return p.Groups
}
