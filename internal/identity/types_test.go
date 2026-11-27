// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/identity"
)

func TestPrincipalRef_ValidationMatrix(t *testing.T) {
	long := strings.Repeat("x", 129)
	cases := []struct {
		name string
		ref  identity.PrincipalRef
		ok   bool
	}{
		{"user minimal (personal desktop)", identity.PrincipalRef{Kind: "user", ID: "u1"}, true},
		{"user with org", identity.PrincipalRef{Kind: "user", ID: "u1", OrgID: "o1"}, true},
		{"user with team", identity.PrincipalRef{Kind: "user", ID: "u1", OrgID: "o1", TeamID: "t1"}, false},
		{"user without id", identity.PrincipalRef{Kind: "user"}, false},
		{"team full", identity.PrincipalRef{Kind: "team", ID: "p1", OrgID: "o1", TeamID: "t1"}, true},
		{"team without org", identity.PrincipalRef{Kind: "team", ID: "p1", TeamID: "t1"}, false},
		{"team without team", identity.PrincipalRef{Kind: "team", ID: "p1", OrgID: "o1"}, false},
		{"team without id", identity.PrincipalRef{Kind: "team", OrgID: "o1", TeamID: "t1"}, false},
		{"service full", identity.PrincipalRef{Kind: "service", ID: "s1", OrgID: "o1"}, true},
		{"service with team", identity.PrincipalRef{Kind: "service", ID: "s1", OrgID: "o1", TeamID: "t1"}, false},
		{"service without org", identity.PrincipalRef{Kind: "service", ID: "s1"}, false},
		{"unknown kind", identity.PrincipalRef{Kind: "robot", ID: "r1", OrgID: "o1"}, false},
		{"empty kind", identity.PrincipalRef{ID: "r1", OrgID: "o1"}, false},
		{"id too long", identity.PrincipalRef{Kind: "service", ID: long, OrgID: "o1"}, false},
		{"org too long", identity.PrincipalRef{Kind: "service", ID: "s1", OrgID: long}, false},
		{"id with space", identity.PrincipalRef{Kind: "service", ID: "s 1", OrgID: "o1"}, false},
		{"id with unicode", identity.PrincipalRef{Kind: "service", ID: "сервис", OrgID: "o1"}, false},
		{"id with nul", identity.PrincipalRef{Kind: "service", ID: "s\x001", OrgID: "o1"}, false},
		{"id with slash (path safety)", identity.PrincipalRef{Kind: "service", ID: "s/1", OrgID: "o1"}, false},
		{"id with dot-dot", identity.PrincipalRef{Kind: "service", ID: "..", OrgID: "o1"}, false},
		{"uuid-like", identity.PrincipalRef{Kind: "service", ID: "0b6e1c2a-8f1d-4c1e-9a77-3d6f5f1a2b3c", OrgID: "o1"}, true},
		{"hex id as produced by auth", identity.PrincipalRef{Kind: "user", ID: "9f86d081884c7d659a2feaa0c55ad015"}, true},
	}
	for _, c := range cases {
		err := c.ref.Validate()
		if c.ok != (err == nil) {
			t.Errorf("%s: ok=%v err=%v", c.name, c.ok, err)
		}
		if err != nil && !errors.Is(err, identity.ErrInvalid) {
			t.Errorf("%s: error must be ErrInvalid: %v", c.name, err)
		}
		if err != nil && strings.Contains(err.Error(), c.ref.ID) && c.ref.ID != "" && len(c.ref.ID) > 3 {
			t.Errorf("%s: error text leaks a field value: %v", c.name, err)
		}
	}
}

func TestPrincipalRef_OwnerIsDerivedFromRef(t *testing.T) {
	team := identity.PrincipalRef{Kind: "team", ID: "p1", OrgID: "o1", TeamID: "t1"}
	if o, err := team.Owner(); err != nil || o != (identity.OwnerScope{Kind: identity.OwnerTeam, OrgID: "o1", TeamID: "t1"}) {
		t.Fatalf("team owner: %+v %v", o, err)
	}
	svc := identity.PrincipalRef{Kind: "service", ID: "s1", OrgID: "o1"}
	if o, err := svc.Owner(); err != nil || o != (identity.OwnerScope{Kind: identity.OwnerOrganization, OrgID: "o1"}) {
		t.Fatalf("service owner: %+v %v", o, err)
	}
	if _, err := (identity.PrincipalRef{Kind: "user", ID: "u1"}).Owner(); !errors.Is(err, identity.ErrInvalid) {
		t.Fatalf("user has no technical owner: %v", err)
	}
	if !team.IsTechnical() || !svc.IsTechnical() || (identity.PrincipalRef{Kind: "user", ID: "u"}).IsTechnical() {
		t.Fatal("IsTechnical")
	}
}

func TestOwnerScope_ValidationMatrix(t *testing.T) {
	cases := []struct {
		name string
		o    identity.OwnerScope
		ok   bool
	}{
		{"org", identity.OwnerScope{Kind: "organization", OrgID: "o1"}, true},
		{"team", identity.OwnerScope{Kind: "team", OrgID: "o1", TeamID: "t1"}, true},
		{"org with team", identity.OwnerScope{Kind: "organization", OrgID: "o1", TeamID: "t1"}, false},
		{"org without id", identity.OwnerScope{Kind: "organization"}, false},
		{"team without team", identity.OwnerScope{Kind: "team", OrgID: "o1"}, false},
		{"team without org", identity.OwnerScope{Kind: "team", TeamID: "t1"}, false},
		{"unknown kind", identity.OwnerScope{Kind: "user", OrgID: "o1"}, false},
		{"zero", identity.OwnerScope{}, false},
	}
	for _, c := range cases {
		if err := c.o.Validate(); c.ok != (err == nil) {
			t.Errorf("%s: ok=%v err=%v", c.name, c.ok, err)
		}
	}
}

func TestActorRef_Validate(t *testing.T) {
	for _, k := range []identity.ActorKind{identity.ActorUser, identity.ActorSystem, identity.ActorScheduler} {
		if err := (identity.ActorRef{Kind: k, ID: "a1"}).Validate(); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
	for _, a := range []identity.ActorRef{{}, {Kind: "user"}, {ID: "a1"}, {Kind: "robot", ID: "a1"}, {Kind: "user", ID: "a 1"}} {
		if err := a.Validate(); !errors.Is(err, identity.ErrInvalid) {
			t.Errorf("%+v: want ErrInvalid, got %v", a, err)
		}
	}
}

func TestIdentityTransitionMatrix(t *testing.T) {
	a, s, r := identity.IdentityActive, identity.IdentitySuspended, identity.IdentityRevoked
	cases := []struct {
		from, to identity.IdentityState
		want     error
	}{
		{a, s, nil}, {s, a, nil}, {a, r, nil}, {s, r, nil},
		{a, a, identity.ErrInvalid}, {s, s, identity.ErrInvalid},
		{r, a, identity.ErrRevoked}, {r, s, identity.ErrRevoked}, {r, r, identity.ErrRevoked},
		{"", a, identity.ErrInvalid}, {a, "", identity.ErrInvalid}, {a, "bogus", identity.ErrInvalid},
	}
	for _, c := range cases {
		err := identity.ValidateIdentityTransition(c.from, c.to)
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%q→%q: want %v, got %v", c.from, c.to, c.want, err)
		}
	}
}

func TestBindingTransitionMatrix(t *testing.T) {
	p, a, r := identity.BindingPending, identity.BindingActive, identity.BindingRevoked
	cases := []struct {
		from, to identity.BindingState
		want     error
	}{
		{p, a, nil}, {p, r, nil}, {a, r, nil},
		{a, p, identity.ErrInvalid}, {p, p, identity.ErrInvalid}, {a, a, identity.ErrInvalid},
		{r, p, identity.ErrRevoked}, {r, a, identity.ErrRevoked}, {r, r, identity.ErrRevoked},
		{"", a, identity.ErrInvalid}, {p, "bogus", identity.ErrInvalid},
	}
	for _, c := range cases {
		err := identity.ValidateBindingTransition(c.from, c.to)
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%q→%q: want %v, got %v", c.from, c.to, c.want, err)
		}
	}
}
