// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package identitytest — переиспользуемый контрактный набор тестов для реализаций identity.Backend
// (File backend; PostgreSQL-адаптер интегратора подключает тот же набор — Contract Freeze §10).
package identitytest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/identity"
)

// Harness — тестируемый backend и (необязательно) способ «перезапуска».
type Harness struct {
	Backend identity.Backend
	// Reopen закрывает и заново открывает то же хранилище, возвращая backend над сохранённым состоянием.
	// nil — кейсы с перезапуском пропускаются.
	Reopen func(t *testing.T) identity.Backend
}

// Factory создаёт чистый Harness для одного теста.
type Factory func(t *testing.T) Harness

var (
	base    = time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	counter atomic.Uint64
	actor   = identity.ActorRef{Kind: identity.ActorUser, ID: "admin-1"}
)

const (
	orgA  = "org-a"
	orgB  = "org-b"
	teamA = "team-a"
	teamB = "team-b"
)

func next(prefix string) string { return fmt.Sprintf("%s-%d", prefix, counter.Add(1)) }

// TeamRef и ServiceRef строят ссылки технических principal'ов.
func TeamRef(org, team, id string) identity.PrincipalRef {
	return identity.PrincipalRef{Kind: identity.PrincipalTeam, ID: id, OrgID: org, TeamID: team}
}

// ServiceRef строит ссылку ServicePrincipal.
func ServiceRef(org, id string) identity.PrincipalRef {
	return identity.PrincipalRef{Kind: identity.PrincipalService, ID: id, OrgID: org}
}

// NewIdentity строит валидную identity на создание для ссылки (владелец выводится из ссылки).
func NewIdentity(ref identity.PrincipalRef) identity.Identity {
	owner, err := ref.Owner()
	if err != nil {
		panic(err)
	}
	return identity.Identity{
		Ref: ref, Owner: owner, DisplayName: "ci-runner", State: identity.IdentityActive,
		CreatedBy: actor, CreatedAt: base, LastMutationID: next("mut"), LastAuditEventID: next("ev"),
	}
}

// NewBinding строит валидную pending-привязку на создание.
func NewBinding(p identity.PrincipalRef, cluster string) identity.CredentialBinding {
	return identity.CredentialBinding{
		ID: next("bnd"), Principal: p, ClusterID: cluster, State: identity.BindingPending,
		CreatedBy: actor, CreatedAt: base,
	}
}

func mustCreate(t *testing.T, b identity.Backend, ref identity.PrincipalRef) identity.Identity {
	t.Helper()
	got, err := b.Create(context.Background(), NewIdentity(ref))
	if err != nil {
		t.Fatalf("create %v: %v", ref, err)
	}
	return got
}

func mustPut(t *testing.T, b identity.Backend, p identity.PrincipalRef, cluster string) identity.CredentialBinding {
	t.Helper()
	got, err := b.PutBinding(context.Background(), NewBinding(p, cluster), 0, next("mut"), next("ev"))
	if err != nil {
		t.Fatalf("put binding: %v", err)
	}
	return got
}

func wantErr(t *testing.T, what string, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Errorf("%s: want %v, got %v", what, want, got)
	}
}

func sameIdentity(a, b identity.Identity) bool {
	return a.Ref == b.Ref && a.Owner == b.Owner && a.DisplayName == b.DisplayName && a.State == b.State &&
		a.Revision == b.Revision && a.CreatedBy == b.CreatedBy && a.CreatedAt.Equal(b.CreatedAt) &&
		a.UpdatedAt.Equal(b.UpdatedAt) && a.LastMutationID == b.LastMutationID && a.LastAuditEventID == b.LastAuditEventID
}

func sameBinding(a, b identity.CredentialBinding) bool {
	return a.ID == b.ID && a.Principal == b.Principal && a.ClusterID == b.ClusterID && a.State == b.State &&
		a.Revision == b.Revision && a.CreatedBy == b.CreatedBy && a.CreatedAt.Equal(b.CreatedAt) &&
		a.UpdatedAt.Equal(b.UpdatedAt) && a.LastMutationID == b.LastMutationID && a.LastAuditEventID == b.LastAuditEventID
}

// Run выполняет контрактный набор.
func Run(t *testing.T, newHarness Factory) {
	sub := func(name string, fn func(t *testing.T, h Harness)) {
		t.Run(name, func(t *testing.T) { fn(t, newHarness(t)) })
	}
	ctx := context.Background()

	sub("create/validation matrix", func(t *testing.T, h Harness) {
		b := h.Backend
		team := TeamRef(orgA, teamA, "tid-1")
		svc := ServiceRef(orgA, "sid-1")
		mut := map[string]func(*identity.Identity){
			"user kind is not stored": func(i *identity.Identity) { i.Ref = identity.PrincipalRef{Kind: identity.PrincipalUser, ID: "u-1"} },
			"unknown kind":            func(i *identity.Identity) { i.Ref.Kind = "robot" },
			"empty kind":              func(i *identity.Identity) { i.Ref.Kind = "" },
			"empty id":                func(i *identity.Identity) { i.Ref.ID = "" },
			"id with space":           func(i *identity.Identity) { i.Ref.ID = "a b" },
			"id with slash":           func(i *identity.Identity) { i.Ref.ID = "a/b" },
			"id with newline":         func(i *identity.Identity) { i.Ref.ID = "a\nb" },
			"id with leading dash":    func(i *identity.Identity) { i.Ref.ID = "-a" },
			"id too long":             func(i *identity.Identity) { i.Ref.ID = strings.Repeat("x", 129) },
			"team without org":        func(i *identity.Identity) { i.Ref.OrgID = ""; i.Owner.OrgID = "" },
			"team without team":       func(i *identity.Identity) { i.Ref.TeamID = ""; i.Owner.TeamID = "" },
			"team with organization owner": func(i *identity.Identity) {
				i.Owner = identity.OwnerScope{Kind: identity.OwnerOrganization, OrgID: orgA}
			},
			"team owner other team":     func(i *identity.Identity) { i.Owner.TeamID = teamB },
			"team owner other org":      func(i *identity.Identity) { i.Owner.OrgID = orgB },
			"empty owner":               func(i *identity.Identity) { i.Owner = identity.OwnerScope{} },
			"unknown owner kind":        func(i *identity.Identity) { i.Owner.Kind = "galaxy" },
			"empty display name":        func(i *identity.Identity) { i.DisplayName = "" },
			"display name control char": func(i *identity.Identity) { i.DisplayName = "a\x00b" },
			"display name edge space":   func(i *identity.Identity) { i.DisplayName = " a" },
			"display name too long":     func(i *identity.Identity) { i.DisplayName = strings.Repeat("я", 129) },
			"revision preset":           func(i *identity.Identity) { i.Revision = 1 },
			"suspended on create":       func(i *identity.Identity) { i.State = identity.IdentitySuspended },
			"revoked on create":         func(i *identity.Identity) { i.State = identity.IdentityRevoked },
			"unknown state":             func(i *identity.Identity) { i.State = "bogus" },
			"empty mutation id":         func(i *identity.Identity) { i.LastMutationID = "" },
			"empty audit event id":      func(i *identity.Identity) { i.LastAuditEventID = "" },
			"bad creator kind":          func(i *identity.Identity) { i.CreatedBy.Kind = "robot" },
			"empty creator id":          func(i *identity.Identity) { i.CreatedBy.ID = "" },
			"zero created at":           func(i *identity.Identity) { i.CreatedAt = time.Time{} },
		}
		for name, m := range mut {
			in := NewIdentity(team)
			m(&in)
			if _, err := b.Create(ctx, in); !errors.Is(err, identity.ErrInvalid) {
				t.Errorf("team/%s: want ErrInvalid, got %v", name, err)
			}
		}
		// Service: владелец — ровно одна Organization.
		for name, m := range map[string]func(*identity.Identity){
			"service with team id": func(i *identity.Identity) { i.Ref.TeamID = teamA },
			"service with team owner": func(i *identity.Identity) {
				i.Owner = identity.OwnerScope{Kind: identity.OwnerTeam, OrgID: orgA, TeamID: teamA}
			},
			"service owner other org":    func(i *identity.Identity) { i.Owner.OrgID = orgB },
			"service owner with team id": func(i *identity.Identity) { i.Owner.TeamID = teamA },
		} {
			in := NewIdentity(svc)
			m(&in)
			if _, err := b.Create(ctx, in); !errors.Is(err, identity.ErrInvalid) {
				t.Errorf("service/%s: want ErrInvalid, got %v", name, err)
			}
		}
		for _, o := range []identity.OwnerScope{{Kind: identity.OwnerTeam, OrgID: orgA, TeamID: teamA}, {Kind: identity.OwnerOrganization, OrgID: orgA}} {
			if l, err := b.List(ctx, o); err != nil || len(l) != 0 {
				t.Fatalf("invalid creates must store nothing: %d %v", len(l), err)
			}
		}
		// Граничная длина ID допустима.
		if _, err := b.Create(ctx, NewIdentity(ServiceRef(orgA, strings.Repeat("x", 128)))); err != nil {
			t.Errorf("128-char id: %v", err)
		}
	})

	sub("create/get roundtrip and isolation", func(t *testing.T, h Harness) {
		b := h.Backend
		ref := TeamRef(orgA, teamA, "tid-1")
		got := mustCreate(t, b, ref)
		if got.Revision != 1 || got.State != identity.IdentityActive || !got.UpdatedAt.Equal(got.CreatedAt) {
			t.Fatalf("new identity: %+v", got)
		}
		if got.Owner != (identity.OwnerScope{Kind: identity.OwnerTeam, OrgID: orgA, TeamID: teamA}) || got.CreatedBy != actor {
			t.Fatalf("owner/provenance: %+v", got)
		}
		again, err := b.Get(ctx, ref)
		if err != nil || !sameIdentity(got, again) {
			t.Fatalf("get: %+v %v", again, err)
		}
		// Чужая организация/команда/другой вид с тем же ID неотличимы от отсутствующей записи.
		for name, other := range map[string]identity.PrincipalRef{
			"other org":  TeamRef(orgB, teamA, "tid-1"),
			"other team": TeamRef(orgA, teamB, "tid-1"),
			"other kind": ServiceRef(orgA, "tid-1"),
			"absent":     TeamRef(orgA, teamA, "tid-nope"),
		} {
			if _, err := b.Get(ctx, other); !errors.Is(err, identity.ErrNotFound) {
				t.Errorf("%s: want ErrNotFound, got %v", name, err)
			}
		}
		if _, err := b.Get(ctx, identity.PrincipalRef{Kind: identity.PrincipalUser, ID: "u-1"}); !errors.Is(err, identity.ErrInvalid) {
			t.Errorf("user principal get: %v", err)
		}
		// Тот же текст ID, но другой вид — независимая запись.
		if _, err := b.Create(ctx, NewIdentity(ServiceRef(orgA, "tid-1"))); err != nil {
			t.Errorf("same id different kind must coexist: %v", err)
		}
		// Один и тот же (Kind, ID) не переиспользуется ни в другой организации, ни в другой команде.
		_, err = b.Create(ctx, NewIdentity(TeamRef(orgB, teamB, "tid-1")))
		wantErr(t, "id reuse across tenants", err, identity.ErrConflict)
	})

	sub("duplicate create and idempotent replay", func(t *testing.T, h Harness) {
		b := h.Backend
		in := NewIdentity(ServiceRef(orgA, "sid-1"))
		first, err := b.Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		// Тот же mutation id и содержимое (иное время/аудит-событие попытки) — идемпотентный повтор.
		retry := in
		retry.CreatedAt = base.Add(time.Hour)
		retry.LastAuditEventID = next("ev")
		got, err := b.Create(ctx, retry)
		if err != nil || !sameIdentity(first, got) {
			t.Fatalf("replay must return stored record: %+v %v", got, err)
		}
		// Новый mutation id — это уже дубликат.
		dup := in
		dup.LastMutationID = next("mut")
		_, err = b.Create(ctx, dup)
		wantErr(t, "duplicate create", err, identity.ErrConflict)
		// Тот же mutation id, но другое содержимое — конфликт.
		diff := in
		diff.DisplayName = "other"
		_, err = b.Create(ctx, diff)
		wantErr(t, "same mutation different payload", err, identity.ErrConflict)
		// Повтор после последующей мутации — уже не идемпотентный.
		if _, err := b.SetState(ctx, in.Ref, 1, identity.IdentitySuspended, next("mut"), next("ev")); err != nil {
			t.Fatal(err)
		}
		_, err = b.Create(ctx, in)
		wantErr(t, "create replay after later mutation", err, identity.ErrConflict)
	})

	sub("list is exact-owner and does not inherit", func(t *testing.T, h Harness) {
		b := h.Backend
		mustCreate(t, b, TeamRef(orgA, teamA, "tid-2"))
		mustCreate(t, b, TeamRef(orgA, teamA, "tid-1"))
		mustCreate(t, b, TeamRef(orgA, teamB, "tid-3"))
		mustCreate(t, b, TeamRef(orgB, teamA, "tid-4"))
		mustCreate(t, b, ServiceRef(orgA, "sid-1"))
		mustCreate(t, b, ServiceRef(orgB, "sid-2"))
		ids := func(o identity.OwnerScope) string {
			l, err := b.List(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			var s []string
			for _, i := range l {
				if i.Owner != o {
					t.Fatalf("foreign owner in list: %+v", i)
				}
				s = append(s, i.Ref.ID)
			}
			return strings.Join(s, ",")
		}
		if got := ids(identity.OwnerScope{Kind: identity.OwnerTeam, OrgID: orgA, TeamID: teamA}); got != "tid-1,tid-2" {
			t.Errorf("team A: %q", got)
		}
		if got := ids(identity.OwnerScope{Kind: identity.OwnerTeam, OrgID: orgA, TeamID: teamB}); got != "tid-3" {
			t.Errorf("team B: %q", got)
		}
		// Organization видит только свои Service identity, без Team identity своих команд.
		if got := ids(identity.OwnerScope{Kind: identity.OwnerOrganization, OrgID: orgA}); got != "sid-1" {
			t.Errorf("org A: %q", got)
		}
		if got := ids(identity.OwnerScope{Kind: identity.OwnerOrganization, OrgID: "org-none"}); got != "" {
			t.Errorf("unknown org: %q", got)
		}
		for name, o := range map[string]identity.OwnerScope{
			"empty":         {},
			"team no team":  {Kind: identity.OwnerTeam, OrgID: orgA},
			"org with team": {Kind: identity.OwnerOrganization, OrgID: orgA, TeamID: teamA},
		} {
			if _, err := b.List(ctx, o); !errors.Is(err, identity.ErrInvalid) {
				t.Errorf("list %s: want ErrInvalid, got %v", name, err)
			}
		}
	})

	sub("state transition matrix", func(t *testing.T, h Harness) {
		b := h.Backend
		type tc struct {
			from, to identity.IdentityState
			want     error // nil — допустимо
		}
		cases := []tc{
			{identity.IdentityActive, identity.IdentitySuspended, nil},
			{identity.IdentityActive, identity.IdentityRevoked, nil},
			{identity.IdentityActive, identity.IdentityActive, identity.ErrInvalid},
			{identity.IdentitySuspended, identity.IdentityActive, nil},
			{identity.IdentitySuspended, identity.IdentityRevoked, nil},
			{identity.IdentitySuspended, identity.IdentitySuspended, identity.ErrInvalid},
			{identity.IdentityRevoked, identity.IdentityActive, identity.ErrRevoked},
			{identity.IdentityRevoked, identity.IdentitySuspended, identity.ErrRevoked},
			{identity.IdentityRevoked, identity.IdentityRevoked, identity.ErrRevoked},
		}
		for i, c := range cases {
			ref := ServiceRef(orgA, fmt.Sprintf("sid-%d", i))
			cur := mustCreate(t, b, ref)
			switch c.from {
			case identity.IdentitySuspended:
				cur, _ = b.SetState(ctx, ref, cur.Revision, identity.IdentitySuspended, next("mut"), next("ev"))
			case identity.IdentityRevoked:
				cur, _ = b.SetState(ctx, ref, cur.Revision, identity.IdentityRevoked, next("mut"), next("ev"))
			}
			if cur.State != c.from {
				t.Fatalf("setup %d: state %s", i, cur.State)
			}
			got, err := b.SetState(ctx, ref, cur.Revision, c.to, next("mut"), next("ev"))
			if c.want == nil {
				if err != nil || got.State != c.to || got.Revision != cur.Revision+1 {
					t.Errorf("%s→%s: %+v %v", c.from, c.to, got, err)
				}
				continue
			}
			wantErr(t, fmt.Sprintf("%s→%s", c.from, c.to), err, c.want)
			if after, _ := b.Get(ctx, ref); after.State != c.from || after.Revision != cur.Revision {
				t.Errorf("%s→%s rejected transition mutated state: %+v", c.from, c.to, after)
			}
		}
		if _, err := b.SetState(ctx, ServiceRef(orgA, "sid-0"), 1, "bogus", next("mut"), next("ev")); !errors.Is(err, identity.ErrInvalid) {
			t.Errorf("unknown target state: %v", err)
		}
		if _, err := b.SetState(ctx, ServiceRef(orgA, "absent"), 1, identity.IdentitySuspended, next("mut"), next("ev")); !errors.Is(err, identity.ErrNotFound) {
			t.Errorf("absent: %v", err)
		}
		if _, err := b.SetState(ctx, ServiceRef(orgB, "sid-0"), 1, identity.IdentitySuspended, next("mut"), next("ev")); !errors.Is(err, identity.ErrNotFound) {
			t.Errorf("foreign org: %v", err)
		}
	})

	sub("CAS, idempotency and immutable fields", func(t *testing.T, h Harness) {
		b := h.Backend
		ref := TeamRef(orgA, teamA, "tid-1")
		created := mustCreate(t, b, ref)

		for _, bad := range []struct {
			name string
			rev  uint64
		}{{"zero is not a wildcard", 0}} {
			_, err := b.SetState(ctx, ref, bad.rev, identity.IdentitySuspended, next("mut"), next("ev"))
			wantErr(t, bad.name, err, identity.ErrInvalid)
		}
		_, err := b.SetState(ctx, ref, 7, identity.IdentitySuspended, next("mut"), next("ev"))
		wantErr(t, "stale/future revision", err, identity.ErrConflict)
		_, err = b.SetState(ctx, ref, 1, identity.IdentitySuspended, "bad id", next("ev"))
		wantErr(t, "bad mutation id", err, identity.ErrInvalid)
		_, err = b.SetState(ctx, ref, 1, identity.IdentitySuspended, next("mut"), "")
		wantErr(t, "empty audit event", err, identity.ErrInvalid)

		mutID, evID := next("mut"), next("ev")
		s1, err := b.SetState(ctx, ref, 1, identity.IdentitySuspended, mutID, evID)
		if err != nil {
			t.Fatal(err)
		}
		if s1.Revision != 2 || s1.LastMutationID != mutID || s1.LastAuditEventID != evID || s1.UpdatedAt.Before(created.UpdatedAt) {
			t.Fatalf("after suspend: %+v", s1)
		}
		// Владелец, создатель, имя и время создания неизменны; creator — только provenance.
		if s1.Owner != created.Owner || s1.CreatedBy != created.CreatedBy || s1.DisplayName != created.DisplayName ||
			!s1.CreatedAt.Equal(created.CreatedAt) || s1.Ref != created.Ref {
			t.Fatalf("immutable fields changed: %+v vs %+v", s1, created)
		}
		// Идемпотентный повтор: тот же mutation id, то же ожидание — запись без изменений, revision не растёт.
		again, err := b.SetState(ctx, ref, 1, identity.IdentitySuspended, mutID, next("ev"))
		if err != nil || !sameIdentity(s1, again) {
			t.Fatalf("replay: %+v %v", again, err)
		}
		// Тот же mutation id с другим содержимым/ожиданием — конфликт.
		_, err = b.SetState(ctx, ref, 1, identity.IdentityRevoked, mutID, next("ev"))
		wantErr(t, "same mutation, other target", err, identity.ErrConflict)
		_, err = b.SetState(ctx, ref, 2, identity.IdentitySuspended, mutID, next("ev"))
		wantErr(t, "same mutation, shifted revision", err, identity.ErrConflict)
		// Устаревший писатель получает ErrConflict, а не last-write-wins.
		_, err = b.SetState(ctx, ref, 1, identity.IdentityRevoked, next("mut"), next("ev"))
		wantErr(t, "stale writer", err, identity.ErrConflict)
		if cur, _ := b.Get(ctx, ref); cur.State != identity.IdentitySuspended || cur.Revision != 2 {
			t.Fatalf("stale writer changed state: %+v", cur)
		}
	})

	sub("display name update: CAS, idempotency and immutable fields", func(t *testing.T, h Harness) {
		b := h.Backend
		ref := TeamRef(orgA, teamA, "tid-display")
		created := mustCreate(t, b, ref)
		for _, tc := range []struct {
			name, displayName, mutationID string
			revision                      uint64
			want                          error
		}{
			{"zero revision", "new-name", next("mut"), 0, identity.ErrInvalid},
			{"invalid name", "", next("mut"), 1, identity.ErrInvalid},
			{"invalid mutation", "new-name", "bad/id", 1, identity.ErrInvalid},
			{"stale revision", "new-name", next("mut"), 9, identity.ErrConflict},
		} {
			_, err := b.UpdateDisplayName(ctx, ref, tc.revision, tc.displayName, tc.mutationID, next("ev"))
			wantErr(t, tc.name, err, tc.want)
		}
		mutID := next("mut")
		updated, err := b.UpdateDisplayName(ctx, ref, 1, "new-name", mutID, next("ev"))
		if err != nil || updated.DisplayName != "new-name" || updated.Revision != 2 {
			t.Fatalf("update: %+v %v", updated, err)
		}
		if updated.Ref != created.Ref || updated.Owner != created.Owner || updated.State != created.State ||
			updated.CreatedBy != created.CreatedBy || !updated.CreatedAt.Equal(created.CreatedAt) {
			t.Fatalf("immutable fields changed: before=%+v after=%+v", created, updated)
		}
		again, err := b.UpdateDisplayName(ctx, ref, 1, "new-name", mutID, next("ev"))
		if err != nil || !sameIdentity(updated, again) {
			t.Fatalf("idempotent replay: %+v %v", again, err)
		}
		_, err = b.UpdateDisplayName(ctx, ref, 1, "different-name", mutID, next("ev"))
		wantErr(t, "same mutation with different payload", err, identity.ErrConflict)
		_, err = b.UpdateDisplayName(ctx, ref, 1, "stale-name", next("mut"), next("ev"))
		wantErr(t, "stale writer", err, identity.ErrConflict)
		if got, err := b.Get(ctx, ref); err != nil || !sameIdentity(updated, got) {
			t.Fatalf("stored identity: %+v %v", got, err)
		}
	})

	sub("revoked is terminal", func(t *testing.T, h Harness) {
		b := h.Backend
		ref := TeamRef(orgA, teamA, "tid-1")
		mustCreate(t, b, ref)
		mutID := next("mut")
		rev, err := b.SetState(ctx, ref, 1, identity.IdentityRevoked, mutID, next("ev"))
		if err != nil || rev.State != identity.IdentityRevoked || rev.Revision != 2 {
			t.Fatalf("revoke: %+v %v", rev, err)
		}
		// Повтор того же revoke — идемпотентен; новый revoke/resume — ErrRevoked.
		if again, err := b.SetState(ctx, ref, 1, identity.IdentityRevoked, mutID, next("ev")); err != nil || again.Revision != 2 {
			t.Fatalf("revoke replay: %+v %v", again, err)
		}
		for _, to := range []identity.IdentityState{identity.IdentityActive, identity.IdentitySuspended, identity.IdentityRevoked} {
			_, err := b.SetState(ctx, ref, 2, to, next("mut"), next("ev"))
			wantErr(t, "from revoked to "+string(to), err, identity.ErrRevoked)
		}
		// Повторное включение возможно только новой identity с новым ID; старый ID не переиспользуется.
		_, err = b.Create(ctx, NewIdentity(ref))
		wantErr(t, "re-create revoked id", err, identity.ErrConflict)
		if _, err := b.Create(ctx, NewIdentity(TeamRef(orgA, teamA, "tid-2"))); err != nil {
			t.Fatalf("new identity: %v", err)
		}
		// Revoked остаётся читаемым как данные.
		if got, err := b.Get(ctx, ref); err != nil || got.State != identity.IdentityRevoked {
			t.Fatalf("get revoked: %+v %v", got, err)
		}
	})

	sub("binding: create, uniqueness, isolation", func(t *testing.T, h Harness) {
		b := h.Backend
		p1 := TeamRef(orgA, teamA, "tid-1")
		p2 := TeamRef(orgA, teamA, "tid-2")
		svc := ServiceRef(orgA, "sid-1")
		for _, r := range []identity.PrincipalRef{p1, p2, svc} {
			mustCreate(t, b, r)
		}
		bd := mustPut(t, b, p1, "cl-1")
		if bd.State != identity.BindingPending || bd.Revision != 1 || bd.Principal != p1 || bd.ClusterID != "cl-1" ||
			!bd.UpdatedAt.Equal(bd.CreatedAt) {
			t.Fatalf("new binding: %+v", bd)
		}
		// Pending резервирует кортеж.
		_, err := b.PutBinding(ctx, NewBinding(p1, "cl-1"), 0, next("mut"), next("ev"))
		wantErr(t, "second pending for tuple", err, identity.ErrConflict)
		// Тот же кластер другому principal — независимая привязка; другой кластер тому же — тоже.
		mustPut(t, b, p2, "cl-1")
		mustPut(t, b, svc, "cl-1")
		mustPut(t, b, p1, "cl-2")
		// Привязки не протекают между principal'ами.
		for _, c := range []struct {
			p    identity.PrincipalRef
			want int
		}{{p1, 2}, {p2, 1}, {svc, 1}} {
			l, err := b.ListBindings(ctx, c.p)
			if err != nil || len(l) != c.want {
				t.Fatalf("list %v: %d %v", c.p, len(l), err)
			}
			for _, x := range l {
				if x.Principal != c.p {
					t.Fatalf("foreign binding in list: %+v", x)
				}
			}
		}
		got, err := b.GetBinding(ctx, p1, "cl-1")
		if err != nil || !sameBinding(got, bd) {
			t.Fatalf("get binding: %+v %v", got, err)
		}
		// Чужой/несуществующий principal и кластер без привязки — ErrNotFound.
		if _, err := b.GetBinding(ctx, TeamRef(orgB, teamA, "tid-1"), "cl-1"); !errors.Is(err, identity.ErrNotFound) {
			t.Errorf("foreign principal: %v", err)
		}
		if _, err := b.GetBinding(ctx, p1, "cl-none"); !errors.Is(err, identity.ErrNotFound) {
			t.Errorf("no binding: %v", err)
		}
		if _, err := b.ListBindings(ctx, TeamRef(orgA, teamB, "tid-1")); !errors.Is(err, identity.ErrNotFound) {
			t.Errorf("list foreign principal: %v", err)
		}
		// ID привязки не переиспользуется — ни тем же, ни другим principal.
		reuse := NewBinding(p2, "cl-9")
		reuse.ID = bd.ID
		_, err = b.PutBinding(ctx, reuse, 0, next("mut"), next("ev"))
		wantErr(t, "cross-principal binding id reuse", err, identity.ErrConflict)
	})

	sub("binding: validation and principal state", func(t *testing.T, h Harness) {
		b := h.Backend
		p := TeamRef(orgA, teamA, "tid-1")
		cur := mustCreate(t, b, p)
		mut := map[string]func(*identity.CredentialBinding){
			"user principal": func(x *identity.CredentialBinding) {
				x.Principal = identity.PrincipalRef{Kind: identity.PrincipalUser, ID: "u-1"}
			},
			"empty id":          func(x *identity.CredentialBinding) { x.ID = "" },
			"bad id":            func(x *identity.CredentialBinding) { x.ID = "a b" },
			"empty cluster":     func(x *identity.CredentialBinding) { x.ClusterID = "" },
			"bad cluster":       func(x *identity.CredentialBinding) { x.ClusterID = "c/1" },
			"active on create":  func(x *identity.CredentialBinding) { x.State = identity.BindingActive },
			"revoked on create": func(x *identity.CredentialBinding) { x.State = identity.BindingRevoked },
			"unknown state":     func(x *identity.CredentialBinding) { x.State = "bogus" },
			"revision preset":   func(x *identity.CredentialBinding) { x.Revision = 3 },
			"bad creator":       func(x *identity.CredentialBinding) { x.CreatedBy = identity.ActorRef{} },
			"zero created at":   func(x *identity.CredentialBinding) { x.CreatedAt = time.Time{} },
			"foreign mutation":  func(x *identity.CredentialBinding) { x.LastMutationID = "something-else" },
		}
		for name, m := range mut {
			in := NewBinding(p, "cl-1")
			m(&in)
			if _, err := b.PutBinding(ctx, in, 0, "mut-x", "ev-x"); !errors.Is(err, identity.ErrInvalid) {
				t.Errorf("%s: want ErrInvalid, got %v", name, err)
			}
		}
		_, err := b.PutBinding(ctx, NewBinding(p, "cl-1"), 4, "mut-x", "ev-x")
		wantErr(t, "put with non-zero expected revision", err, identity.ErrInvalid)
		_, err = b.PutBinding(ctx, NewBinding(p, "cl-1"), 0, "", "ev-x")
		wantErr(t, "empty mutation id", err, identity.ErrInvalid)
		_, err = b.PutBinding(ctx, NewBinding(TeamRef(orgA, teamA, "tid-ghost"), "cl-1"), 0, "mut-x", "ev-x")
		wantErr(t, "unknown principal", err, identity.ErrNotFound)
		_, err = b.PutBinding(ctx, NewBinding(TeamRef(orgB, teamA, "tid-1"), "cl-1"), 0, "mut-x", "ev-x")
		wantErr(t, "foreign org principal", err, identity.ErrNotFound)
		if l, _ := b.ListBindings(ctx, p); len(l) != 0 {
			t.Fatalf("invalid puts must store nothing: %d", len(l))
		}

		// Приостановленная identity: новая привязка недопустима; отозванная: ErrRevoked.
		cur, _ = b.SetState(ctx, p, cur.Revision, identity.IdentitySuspended, next("mut"), next("ev"))
		_, err = b.PutBinding(ctx, NewBinding(p, "cl-1"), 0, next("mut"), next("ev"))
		wantErr(t, "suspended principal", err, identity.ErrInvalid)
		cur, _ = b.SetState(ctx, p, cur.Revision, identity.IdentityRevoked, next("mut"), next("ev"))
		_, err = b.PutBinding(ctx, NewBinding(p, "cl-1"), 0, next("mut"), next("ev"))
		wantErr(t, "revoked principal", err, identity.ErrRevoked)
	})

	sub("binding: lifecycle and terminal revoke", func(t *testing.T, h Harness) {
		b := h.Backend
		p := ServiceRef(orgA, "sid-1")
		mustCreate(t, b, p)
		bd := mustPut(t, b, p, "cl-1")

		// pending → active; CAS.
		_, err := b.ActivateBinding(ctx, bd.ID, 0, next("mut"), next("ev"))
		wantErr(t, "activate zero revision", err, identity.ErrInvalid)
		_, err = b.ActivateBinding(ctx, bd.ID, 9, next("mut"), next("ev"))
		wantErr(t, "activate stale", err, identity.ErrConflict)
		_, err = b.ActivateBinding(ctx, "bnd-none", 1, next("mut"), next("ev"))
		wantErr(t, "activate unknown id", err, identity.ErrNotFound)
		actMut := next("mut")
		act, err := b.ActivateBinding(ctx, bd.ID, 1, actMut, next("ev"))
		if err != nil || act.State != identity.BindingActive || act.Revision != 2 || act.LastMutationID != actMut {
			t.Fatalf("activate: %+v %v", act, err)
		}
		if act.Principal != bd.Principal || act.ClusterID != bd.ClusterID || act.CreatedBy != bd.CreatedBy || act.ID != bd.ID {
			t.Fatalf("binding identity fields changed: %+v", act)
		}
		// Идемпотентный повтор; активация активной — недопустимый переход.
		if again, err := b.ActivateBinding(ctx, bd.ID, 1, actMut, next("ev")); err != nil || !sameBinding(act, again) {
			t.Fatalf("activate replay: %+v %v", again, err)
		}
		_, err = b.ActivateBinding(ctx, bd.ID, 2, next("mut"), next("ev"))
		wantErr(t, "active → active", err, identity.ErrInvalid)
		got, err := b.GetBinding(ctx, p, "cl-1")
		if err != nil || got.State != identity.BindingActive {
			t.Fatalf("get active: %+v %v", got, err)
		}

		// active → revoked, терминально.
		revMut := next("mut")
		rev, err := b.RevokeBinding(ctx, bd.ID, 2, revMut, next("ev"))
		if err != nil || rev.State != identity.BindingRevoked || rev.Revision != 3 {
			t.Fatalf("revoke: %+v %v", rev, err)
		}
		if again, err := b.RevokeBinding(ctx, bd.ID, 2, revMut, next("ev")); err != nil || !sameBinding(rev, again) {
			t.Fatalf("revoke replay: %+v %v", again, err)
		}
		_, err = b.ActivateBinding(ctx, bd.ID, 3, next("mut"), next("ev"))
		wantErr(t, "activate revoked", err, identity.ErrRevoked)
		_, err = b.RevokeBinding(ctx, bd.ID, 3, next("mut"), next("ev"))
		wantErr(t, "revoke revoked", err, identity.ErrRevoked)

		// Для кортежа без живой привязки GetBinding отдаёт отозванную вместе с ErrRevoked.
		got, err = b.GetBinding(ctx, p, "cl-1")
		wantErr(t, "get revoked-only tuple", err, identity.ErrRevoked)
		if got.ID != bd.ID || got.State != identity.BindingRevoked {
			t.Fatalf("revoked-only tuple must return the revoked binding: %+v", got)
		}
		// После отзыва кортеж свободен, но ID прежней привязки не возвращается: новая получает новый ID.
		again := mustPut(t, b, p, "cl-1")
		if again.ID == bd.ID || again.State != identity.BindingPending {
			t.Fatalf("re-assignment: %+v", again)
		}
		if l, _ := b.ListBindings(ctx, p); len(l) != 2 {
			t.Fatalf("history must be retained: %d", len(l))
		}
		if cur, err := b.GetBinding(ctx, p, "cl-1"); err != nil || cur.ID != again.ID {
			t.Fatalf("live binding must win over revoked: %+v %v", cur, err)
		}
		// pending можно отозвать без активации.
		if r, err := b.RevokeBinding(ctx, again.ID, 1, next("mut"), next("ev")); err != nil || r.State != identity.BindingRevoked {
			t.Fatalf("revoke pending: %+v %v", r, err)
		}
	})

	sub("binding: principal state gates activation but not revoke", func(t *testing.T, h Harness) {
		b := h.Backend
		p := TeamRef(orgA, teamA, "tid-1")
		cur := mustCreate(t, b, p)
		bd := mustPut(t, b, p, "cl-1")
		cur, _ = b.SetState(ctx, p, cur.Revision, identity.IdentitySuspended, next("mut"), next("ev"))
		_, err := b.ActivateBinding(ctx, bd.ID, 1, next("mut"), next("ev"))
		wantErr(t, "activate under suspended principal", err, identity.ErrInvalid)
		cur, _ = b.SetState(ctx, p, cur.Revision, identity.IdentityRevoked, next("mut"), next("ev"))
		_, err = b.ActivateBinding(ctx, bd.ID, 1, next("mut"), next("ev"))
		wantErr(t, "activate under revoked principal", err, identity.ErrRevoked)
		// Отзыв привязки возможен всегда: администратор должен уметь снять назначение.
		if r, err := b.RevokeBinding(ctx, bd.ID, 1, next("mut"), next("ev")); err != nil || r.State != identity.BindingRevoked {
			t.Fatalf("revoke under revoked principal: %+v %v", r, err)
		}
		_ = cur
	})

	sub("no implicit grants or cross-scope rights", func(t *testing.T, h Harness) {
		// Backend не хранит grants/delegation: identity и binding — единственные записи, а создание принципала
		// и назначение кластера не создают чужих записей и не меняют чужих владельцев.
		b := h.Backend
		svc := mustCreate(t, b, ServiceRef(orgA, "sid-1"))
		team := mustCreate(t, b, TeamRef(orgA, teamA, "tid-1"))
		mustPut(t, b, svc.Ref, "cl-of-team-a")
		if l, _ := b.List(ctx, identity.OwnerScope{Kind: identity.OwnerTeam, OrgID: orgA, TeamID: teamA}); len(l) != 1 || l[0].Ref != team.Ref {
			t.Fatalf("service binding must not touch team ownership: %+v", l)
		}
		if l, _ := b.ListBindings(ctx, team.Ref); len(l) != 0 {
			t.Fatalf("service binding leaked into team principal: %+v", l)
		}
		if after, _ := b.Get(ctx, svc.Ref); after.Revision != 1 || after.Owner != svc.Owner {
			t.Fatalf("assignment changed identity: %+v", after)
		}
	})

	sub("concurrent writers: exactly one CAS wins", func(t *testing.T, h Harness) {
		b := h.Backend
		ref := TeamRef(orgA, teamA, "tid-1")
		mustCreate(t, b, ref)
		const n = 24
		var wg sync.WaitGroup
		var ok, conflict atomic.Int32
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				to := identity.IdentitySuspended
				if i%2 == 1 {
					to = identity.IdentityRevoked
				}
				_, err := b.SetState(ctx, ref, 1, to, fmt.Sprintf("mut-race-%d", i), fmt.Sprintf("ev-race-%d", i))
				switch {
				case err == nil:
					ok.Add(1)
				case errors.Is(err, identity.ErrConflict):
					conflict.Add(1)
				default:
					t.Errorf("unexpected: %v", err)
				}
			}(i)
		}
		wg.Wait()
		if ok.Load() != 1 || conflict.Load() != n-1 {
			t.Fatalf("want 1 winner and %d conflicts, got %d/%d", n-1, ok.Load(), conflict.Load())
		}
		if got, _ := b.Get(ctx, ref); got.Revision != 2 {
			t.Fatalf("revision after race: %d", got.Revision)
		}

		// Параллельное создание одной ссылки: победитель один.
		dup := ServiceRef(orgA, "sid-race")
		var created, rejected atomic.Int32
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := b.Create(ctx, NewIdentity(dup)) // у каждой попытки свой mutation id
				switch {
				case err == nil:
					created.Add(1)
				case errors.Is(err, identity.ErrConflict):
					rejected.Add(1)
				default:
					t.Errorf("unexpected: %v", err)
				}
			}()
		}
		wg.Wait()
		if created.Load() != 1 || rejected.Load() != n-1 {
			t.Fatalf("create race: %d/%d", created.Load(), rejected.Load())
		}

		// Параллельное назначение кортежа: одна живая привязка.
		var put, held atomic.Int32
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := b.PutBinding(ctx, NewBinding(ref, "cl-race"), 0, next("mut"), next("ev"))
				switch {
				case err == nil:
					put.Add(1)
				case errors.Is(err, identity.ErrConflict), errors.Is(err, identity.ErrRevoked), errors.Is(err, identity.ErrInvalid):
					held.Add(1)
				default:
					t.Errorf("unexpected: %v", err)
				}
			}()
		}
		wg.Wait()
		// ref к этому моменту suspended/revoked (победитель гонки выше): привязка допустима только active.
		if put.Load() != 0 {
			t.Fatalf("binding under non-active principal must be rejected, created %d", put.Load())
		}
		live := ServiceRef(orgA, "sid-live")
		mustCreate(t, b, live)
		put.Store(0)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := b.PutBinding(ctx, NewBinding(live, "cl-race"), 0, next("mut"), next("ev")); err == nil {
					put.Add(1)
				} else if !errors.Is(err, identity.ErrConflict) {
					t.Errorf("unexpected: %v", err)
				}
			}()
		}
		wg.Wait()
		if put.Load() != 1 {
			t.Fatalf("exactly one live binding per tuple, got %d", put.Load())
		}
	})

	sub("canceled context changes nothing", func(t *testing.T, h Harness) {
		b := h.Backend
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := b.Create(cctx, NewIdentity(ServiceRef(orgA, "sid-1")))
		wantErr(t, "create with canceled ctx", err, identity.ErrUnavailable)
		if _, err := b.Get(ctx, ServiceRef(orgA, "sid-1")); !errors.Is(err, identity.ErrNotFound) {
			t.Fatalf("canceled create must not store: %v", err)
		}
	})

	sub("restart keeps state, terminal revoke and idempotency", func(t *testing.T, h Harness) {
		if h.Reopen == nil {
			t.Skip("backend has no restart semantics")
		}
		b := h.Backend
		team := TeamRef(orgA, teamA, "tid-1")
		svc := ServiceRef(orgA, "sid-1")
		mustCreate(t, b, team)
		mustCreate(t, b, svc)
		revMut := next("mut")
		revoked, err := b.SetState(ctx, team, 1, identity.IdentityRevoked, revMut, next("ev"))
		if err != nil {
			t.Fatal(err)
		}
		bd := mustPut(t, b, svc, "cl-1")
		actMut := next("mut")
		act, err := b.ActivateBinding(ctx, bd.ID, 1, actMut, next("ev"))
		if err != nil {
			t.Fatal(err)
		}
		pending := mustPut(t, b, svc, "cl-2")

		b2 := h.Reopen(t)
		if got, err := b2.Get(ctx, team); err != nil || !sameIdentity(revoked, got) {
			t.Fatalf("revoked identity after restart: %+v %v", got, err)
		}
		if _, err := b2.SetState(ctx, team, 2, identity.IdentityActive, next("mut"), next("ev")); !errors.Is(err, identity.ErrRevoked) {
			t.Fatalf("revoked must stay terminal after restart: %v", err)
		}
		if got, err := b2.GetBinding(ctx, svc, "cl-1"); err != nil || !sameBinding(act, got) {
			t.Fatalf("active binding after restart: %+v %v", got, err)
		}
		if got, err := b2.GetBinding(ctx, svc, "cl-2"); err != nil || !sameBinding(pending, got) {
			t.Fatalf("pending binding after restart: %+v %v", got, err)
		}
		// Pending по-прежнему резервирует кортеж после перезапуска.
		_, err = b2.PutBinding(ctx, NewBinding(svc, "cl-2"), 0, next("mut"), next("ev"))
		wantErr(t, "pending reserves tuple after restart", err, identity.ErrConflict)
		// Идемпотентные повторы пережили перезапуск; revision не растёт.
		if again, err := b2.SetState(ctx, team, 1, identity.IdentityRevoked, revMut, next("ev")); err != nil || again.Revision != 2 {
			t.Fatalf("revoke replay after restart: %+v %v", again, err)
		}
		if again, err := b2.ActivateBinding(ctx, bd.ID, 1, actMut, next("ev")); err != nil || !sameBinding(act, again) {
			t.Fatalf("activate replay after restart: %+v %v", again, err)
		}
		// Изоляция по владельцу сохраняется.
		if l, _ := b2.List(ctx, identity.OwnerScope{Kind: identity.OwnerTeam, OrgID: orgA, TeamID: teamA}); len(l) != 1 {
			t.Fatalf("list after restart: %d", len(l))
		}
	})
}
