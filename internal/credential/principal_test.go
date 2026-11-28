// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/credential"
	"github.com/24DSO-lab/kubron/internal/credential/filestore"
	"github.com/24DSO-lab/kubron/internal/identity"
)

// ---------------------------------------------------------------------------------------------------------------
// fixtures: the real identity domain (file backend), the real critical audit journal (file recorder) and the real file
// credential store, so the tests prove the integration, not a mock of it.

type toggleAuthz struct{ deny atomic.Bool }

func (a *toggleAuthz) AuthorizeIdentityAdmin(context.Context, identity.ActorRef, identity.AdminAction, identity.OwnerScope) error {
	if a.deny.Load() {
		return identity.ErrForbidden
	}
	return nil
}

func (a *toggleAuthz) AuthorizeProvisioning(context.Context, credential.ProvisioningContext, identity.PrincipalRef) error {
	if a.deny.Load() {
		return errors.New("role lost")
	}
	return nil
}

type anyClusterOwner struct{ org, team string }

func (o anyClusterOwner) ResolveClusterOwner(context.Context, string) (identity.OwnerScope, error) {
	return identity.OwnerScope{Kind: identity.OwnerTeam, OrgID: o.org, TeamID: o.team}, nil
}

// flakyRec wraps the real recorder so a test can make the journal fail.
type flakyRec struct {
	audit.CriticalRecorder
	failIntent  atomic.Bool
	failOutcome atomic.Bool
}

func (f *flakyRec) AppendIntent(ctx context.Context, in audit.CriticalIntent) error {
	if f.failIntent.Load() {
		return errors.New("journal down")
	}
	return f.CriticalRecorder.AppendIntent(ctx, in)
}

func (f *flakyRec) AppendOutcome(ctx context.Context, out audit.CriticalOutcome) error {
	if f.failOutcome.Load() {
		return errors.New("journal down")
	}
	return f.CriticalRecorder.AppendOutcome(ctx, out)
}

var admin = identity.ActorRef{Kind: identity.ActorUser, ID: "admin-1"}

type techRig struct {
	*rig
	ids     *identity.Service
	rec     *flakyRec
	authz   *toggleAuthz
	svc     *credential.Service
	auditDr string
	idDir   string
	events  atomic.Int64
}

func newTechRig(t *testing.T) *techRig {
	t.Helper()
	r := newRig(t)
	r.idp.AddUser("bob", "bob-pw")
	tr := &techRig{rig: r, authz: &toggleAuthz{}, auditDr: filepath.Join(r.dir, "audit"), idDir: filepath.Join(r.dir, "identity")}
	jr, err := audit.OpenCriticalFile(tr.auditDr)
	if err != nil {
		t.Fatal(err)
	}
	tr.rec = &flakyRec{CriticalRecorder: jr}
	be, err := identity.OpenFile(tr.idDir, identity.WithFileClock(r.clk.Now))
	if err != nil {
		t.Fatal(err)
	}
	tr.ids, err = identity.NewService(be, tr.rec, tr.authz, anyClusterOwner{org: "o1", team: "t1"}, identity.WithClock(r.clk.Now))
	if err != nil {
		t.Fatal(err)
	}
	tr.svc = tr.service(r.store)
	return tr
}

func (tr *techRig) service(store credential.CredentialStore) *credential.Service {
	reg := credential.NewRegistry(false)
	if err := reg.Register(tr.prov); err != nil {
		tr.t.Fatal(err)
	}
	svc, err := credential.NewService(credential.Config{Registry: reg, Store: store, Challenges: tr.chal, Envelope: tr.env,
		Now: tr.clk.Now, Logger: slog.New(slog.NewTextHandler(tr.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Identities: tr.ids, Audit: tr.rec, Provisioning: tr.authz})
	if err != nil {
		tr.t.Fatal(err)
	}
	return svc
}

func (tr *techRig) nextID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, tr.events.Add(1))
}

// principal creates an active technical identity and a pending binding to cluster c1.
func (tr *techRig) principal(kind identity.PrincipalKind, id string) (credential.Subject, identity.CredentialBinding) {
	tr.t.Helper()
	req := identity.CreateRequest{Actor: admin, Kind: kind, OrgID: "o1", DisplayName: id, MutationID: tr.nextID("mk")}
	if kind == identity.PrincipalTeam {
		req.TeamID = "t1"
	}
	ident, err := tr.ids.Create(bg, req)
	if err != nil {
		tr.t.Fatal(err)
	}
	b, err := tr.ids.AssignBinding(bg, identity.AssignBindingRequest{Actor: admin, Principal: ident.Ref, ClusterID: "c1", MutationID: tr.nextID("mb")})
	if err != nil {
		tr.t.Fatal(err)
	}
	return credential.Subject{ClusterID: "c1", Scope: credential.ScopePrincipal, Principal: ident.Ref, BindingID: b.ID}, b
}

func (tr *techRig) by(session string) credential.ProvisioningContext {
	return credential.ProvisioningContext{Actor: admin, SessionID: session, EventID: tr.nextID("ev")}
}

// provision runs the whole interactive setup with the given IdP user and returns the event id.
func (tr *techRig) provision(sub credential.Subject, user, pw string) credential.ProvisioningContext {
	tr.t.Helper()
	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil {
		tr.t.Fatalf("BeginProvisioning = %+v, %v", res, err)
	}
	res, err = tr.svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer(user, pw))
	if err != nil {
		tr.t.Fatalf("AnswerProvisioning: %v", err)
	}
	if res.Token != nil || res.Challenge != nil {
		tr.t.Fatalf("provisioning must complete without handing a token to the administrator: %+v", res)
	}
	return by
}

func (tr *techRig) state(sub credential.Subject) identity.BindingState {
	tr.t.Helper()
	all, err := tr.ids.ListBindings(bg, sub.Principal)
	if err != nil {
		tr.t.Fatal(err)
	}
	for _, b := range all {
		if b.ID == sub.BindingID {
			return b.State
		}
	}
	tr.t.Fatal("binding not found")
	return ""
}

func (tr *techRig) token(sub credential.Subject) (credential.Token, error) {
	return tr.svc.Token(bg, sub, desc)
}

// ---------------------------------------------------------------------------------------------------------------
// keys and subjects: an exclusive tagged union

func TestKeyAndSubjectAreAnExclusiveUnion(t *testing.T) {
	team := identity.PrincipalRef{Kind: identity.PrincipalTeam, ID: "ti-1", OrgID: "o1", TeamID: "t1"}
	svc := identity.PrincipalRef{Kind: identity.PrincipalService, ID: "sv-1", OrgID: "o1"}
	user := identity.PrincipalRef{Kind: identity.PrincipalUser, ID: "alice", OrgID: "o1"}

	keys := []struct {
		name  string
		k     credential.Key
		legal bool // ValidateAny
	}{
		{"legacy user", credential.Key{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1"}, true},
		{"legacy team", credential.Key{ClusterID: "c1", Scope: credential.ScopeTeam, OwnerID: "t1"}, true},
		{"team principal", credential.Key{ClusterID: "c1", Scope: credential.ScopePrincipal, Principal: team, BindingID: "b1"}, true},
		{"service principal", credential.Key{ClusterID: "c1", Scope: credential.ScopePrincipal, Principal: svc, BindingID: "b1"}, true},
		{"user principal", credential.Key{ClusterID: "c1", Scope: credential.ScopePrincipal, Principal: user, BindingID: "b1"}, false},
		{"no binding", credential.Key{ClusterID: "c1", Scope: credential.ScopePrincipal, Principal: team}, false},
		{"no principal", credential.Key{ClusterID: "c1", Scope: credential.ScopePrincipal, BindingID: "b1"}, false},
		{"principal with owner id", credential.Key{ClusterID: "c1", Scope: credential.ScopePrincipal, OwnerID: "u1", Principal: team, BindingID: "b1"}, false},
		{"legacy scope with principal", credential.Key{ClusterID: "c1", Scope: credential.ScopeTeam, OwnerID: "t1", Principal: team}, false},
		{"legacy scope with binding", credential.Key{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1", BindingID: "b1"}, false},
		{"team principal in a service shape", credential.Key{ClusterID: "c1", Scope: credential.ScopePrincipal, Principal: identity.PrincipalRef{Kind: identity.PrincipalService, ID: "x", OrgID: "o1", TeamID: "t1"}, BindingID: "b1"}, false},
		{"bad cluster", credential.Key{ClusterID: "c 1", Scope: credential.ScopePrincipal, Principal: team, BindingID: "b1"}, false},
		{"bad binding", credential.Key{ClusterID: "c1", Scope: credential.ScopePrincipal, Principal: team, BindingID: "b/1"}, false},
	}
	for _, tc := range keys {
		err := tc.k.ValidateAny()
		if tc.legal != (err == nil) || (err != nil && !errors.Is(err, credential.ErrInvalidKey)) {
			t.Errorf("%s: ValidateAny = %v, want legal=%v", tc.name, err, tc.legal)
		}
		// Validate is legacy-only: a store that predates technical records can never accept a technical key.
		legacy := tc.k.Scope == credential.ScopeUser || tc.k.Scope == credential.ScopeTeam
		if want := tc.legal && legacy; want != (tc.k.Validate() == nil) {
			t.Errorf("%s: Validate (legacy only) = %v", tc.name, tc.k.Validate())
		}
	}

	for _, tc := range keys {
		sub := credential.Subject{ClusterID: tc.k.ClusterID, Scope: tc.k.Scope, OwnerID: tc.k.OwnerID, Principal: tc.k.Principal, BindingID: tc.k.BindingID}
		if tc.legal != (sub.Validate() == nil) {
			t.Errorf("subject %s: Validate = %v, want legal=%v", tc.name, sub.Validate(), tc.legal)
		}
	}
	// A technical subject never has a user session.
	withSession := credential.Subject{ClusterID: "c1", Scope: credential.ScopePrincipal, Principal: team, BindingID: "b1", SessionID: "s1"}
	if withSession.Validate() == nil {
		t.Error("a technical subject with a session must be refused")
	}
}

// A mixed subject is never silently handled as a legacy subject.
func TestMixedSubjectIsRefusedByEveryEntryPoint(t *testing.T) {
	tr := newTechRig(t)
	team := identity.PrincipalRef{Kind: identity.PrincipalTeam, ID: "ti-1", OrgID: "o1", TeamID: "t1"}
	mixed := credential.Subject{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1", Principal: team, BindingID: "b1"}
	if _, err := tr.svc.Token(bg, mixed, desc); !errors.Is(err, credential.ErrInvalidKey) {
		t.Errorf("Token: %v", err)
	}
	if _, err := tr.svc.Acquire(bg, mixed, desc); !errors.Is(err, credential.ErrInvalidKey) {
		t.Errorf("Acquire: %v", err)
	}
	if _, err := tr.svc.Answer(bg, mixed, "any", nil); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Errorf("Answer: %v", err)
	}
}

// ---------------------------------------------------------------------------------------------------------------
// encryption: the associated data binds every field of a technical key

func TestTechnicalRecordIsBoundToEveryKeyField(t *testing.T) {
	sealer := credential.NewSealer(newRig(t).env)
	base := credential.Key{ClusterID: "c1", Scope: credential.ScopePrincipal,
		Principal: identity.PrincipalRef{Kind: identity.PrincipalTeam, ID: "ti-1", OrgID: "o1", TeamID: "t1"}, BindingID: "b1"}
	blob, keyID, err := sealer.Seal(base, []byte("refresh-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := sealer.Open(base, blob, keyID, credential.EncEnvelopeV1); err != nil || string(got) != "refresh-secret" {
		t.Fatalf("round trip: %q %v", got, err)
	}
	mutate := map[string]func(k *credential.Key){
		"cluster":      func(k *credential.Key) { k.ClusterID = "c2" },
		"principal id": func(k *credential.Key) { k.Principal.ID = "ti-2" },
		"org":          func(k *credential.Key) { k.Principal.OrgID = "o2" },
		"team":         func(k *credential.Key) { k.Principal.TeamID = "t2" },
		"binding":      func(k *credential.Key) { k.BindingID = "b2" },
		"kind": func(k *credential.Key) {
			k.Principal = identity.PrincipalRef{Kind: identity.PrincipalService, ID: "ti-1", OrgID: "o1"}
		},
		"moved boundary": func(k *credential.Key) { k.Principal.ID, k.BindingID = "ti-1b", "1" },
	}
	for name, f := range mutate {
		k := base
		f(&k)
		if _, err := sealer.Open(k, blob, keyID, credential.EncEnvelopeV1); err == nil {
			t.Errorf("a record moved to another %s must not decrypt", name)
		}
	}
	// A legacy key with the same textual ids cannot open it either, and a technical record is never legacy-format.
	legacy := credential.Key{ClusterID: "c1", Scope: credential.ScopeTeam, OwnerID: "ti-1"}
	if _, err := sealer.Open(legacy, blob, keyID, credential.EncEnvelopeV1); err == nil {
		t.Error("a technical record must not open as a legacy one")
	}
	if _, err := sealer.Open(base, blob, keyID, credential.EncLegacyV1); err == nil {
		t.Error("a technical record must never be read in the legacy format")
	}
}

// ---------------------------------------------------------------------------------------------------------------
// provisioning

func TestProvisioningStoresTheCredentialOnlyUnderTheTechnicalSubject(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")
	by := tr.provision(sub, "alice", pwCanary)

	metas, err := tr.svc.ListPrincipal(bg, sub.Principal)
	if err != nil || len(metas) != 1 {
		t.Fatalf("ListPrincipal = %+v, %v", metas, err)
	}
	m := metas[0]
	if m.Key != (credential.Key{ClusterID: "c1", Scope: credential.ScopePrincipal, Principal: sub.Principal, BindingID: sub.BindingID}) ||
		m.LastAuditEventID != by.EventID || m.EncFormat != credential.EncEnvelopeV1 || m.Version != 1 {
		t.Fatalf("stored meta: %+v", m)
	}
	// Nothing under the administrator, nor any legacy scope.
	for _, owner := range []string{admin.ID, "t1", sub.Principal.ID, "alice"} {
		if l, err := tr.svc.List(bg, owner); err != nil || len(l) != 0 {
			t.Errorf("legacy List(%q) = %+v, %v", owner, l, err)
		}
	}
	if _, err := tr.store.Get(bg, credential.Key{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: admin.ID}); !errors.Is(err, credential.ErrNotFound) {
		t.Errorf("the administrator's user scope must stay empty: %v", err)
	}
	if got := tr.state(sub); got != identity.BindingActive {
		t.Fatalf("the binding must be active after the credential is stored: %s", got)
	}
	out, err := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if err != nil || out.Result != audit.CriticalSucceeded {
		t.Fatalf("outcome: %+v %v", out, err)
	}
	tok, err := tr.token(sub)
	if err != nil || tok.Principal != "alice" || tok.Value == "" {
		t.Fatalf("Token = %+v, %v", tok, err)
	}
}

func TestProvisioningIntentIsDurableBeforeTheProviderIsCalled(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")
	tr.rec.failIntent.Store(true)
	_, err := tr.svc.BeginProvisioning(bg, sub, tr.by("sess-1"), desc)
	if !errors.Is(err, credential.ErrAuditUnavailable) {
		t.Fatalf("got %v", err)
	}
	if b, c := tr.prov.Calls(); b+c != 0 {
		t.Fatalf("the identity provider must not be contacted without a durable intent: %d/%d", b, c)
	}
}

func TestProvisioningRefusesBadActorsAndBindings(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")
	other, _ := tr.principal(identity.PrincipalTeam, "other")

	by := tr.by("sess-1")
	byService := by
	byService.Actor = identity.ActorRef{Kind: identity.ActorSystem, ID: "scheduler"}
	if _, err := tr.svc.BeginProvisioning(bg, sub, byService, desc); !errors.Is(err, credential.ErrProvisioningDenied) {
		t.Errorf("a system actor must not provision: %v", err)
	}
	noSession := by
	noSession.SessionID = ""
	if _, err := tr.svc.BeginProvisioning(bg, sub, noSession, desc); err == nil {
		t.Error("a setup without a session must be refused")
	}
	// The binding must be the subject's: a foreign binding id on this principal is not found.
	foreign := sub
	foreign.BindingID = other.BindingID
	if _, err := tr.svc.BeginProvisioning(bg, foreign, by, desc); !errors.Is(err, credential.ErrBindingUnusable) {
		t.Errorf("foreign binding: %v", err)
	}
	// Static descriptors have no technical identity.
	if _, err := tr.svc.BeginProvisioning(bg, sub, by, credential.Descriptor{Kind: credential.KindStatic}); !errors.Is(err, credential.ErrUnsupported) {
		t.Errorf("static: %v", err)
	}
	// Denied authority: nothing starts.
	tr.authz.deny.Store(true)
	if _, err := tr.svc.BeginProvisioning(bg, sub, by, desc); !errors.Is(err, credential.ErrProvisioningDenied) {
		t.Errorf("denied: %v", err)
	}
	if b, c := tr.prov.Calls(); b+c != 0 {
		t.Fatalf("no provider call without authority: %d/%d", b, c)
	}
}

func TestProvisioningChallengeIsBoundToSubjectActorSessionAndEvent(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")
	other, _ := tr.principal(identity.PrincipalTeam, "other")

	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("%+v %v", res, err)
	}
	id := res.Challenge.ID
	if !strings.HasPrefix(id, "p.") {
		t.Fatalf("provisioning challenge ids are marked: %q", id)
	}

	wrongSession := by
	wrongSession.SessionID = "sess-2"
	wrongEvent := by
	wrongEvent.EventID = tr.nextID("ev")
	wrongActor := by
	wrongActor.Actor = identity.ActorRef{Kind: identity.ActorUser, ID: "admin-2"}
	cases := map[string]struct {
		sub credential.Subject
		by  credential.ProvisioningContext
	}{
		"other session": {sub, wrongSession},
		"other event":   {sub, wrongEvent},
		"other actor":   {sub, wrongActor},
		"other binding": {other, by},
	}
	for name, c := range cases {
		if _, err := tr.svc.AnswerProvisioning(bg, c.sub, c.by, id, answer("alice", pwCanary)); !errors.Is(err, credential.ErrChallengeExpired) {
			t.Errorf("%s: %v", name, err)
		}
		if err := tr.svc.CancelProvisioning(bg, c.sub, c.by, id); !errors.Is(err, credential.ErrChallengeExpired) {
			t.Errorf("cancel by %s: %v", name, err)
		}
	}
	// The ordinary user path can never continue, poll, cancel or call back a provisioning challenge: it would store the
	// credential under the administrator.
	adminUser := credential.Subject{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: admin.ID, SessionID: "sess-1"}
	if _, err := tr.svc.Answer(bg, adminUser, id, answer("alice", pwCanary)); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Errorf("legacy Answer: %v", err)
	}
	if _, err := tr.svc.Poll(bg, adminUser, id); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Errorf("legacy Poll: %v", err)
	}
	if err := tr.svc.Cancel(bg, adminUser, id); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Errorf("legacy Cancel: %v", err)
	}
	if _, _, err := tr.svc.CallbackSubject(bg, "sess-1", id); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Errorf("CallbackSubject: %v", err)
	}
	if l, _ := tr.svc.List(bg, admin.ID); len(l) != 0 {
		t.Fatalf("nothing under the administrator: %+v", l)
	}
	// The right caller still can.
	if _, err := tr.svc.AnswerProvisioning(bg, sub, by, id, answer("alice", pwCanary)); err != nil {
		t.Fatalf("the owner of the challenge: %v", err)
	}
}

func TestContinuationRechecksAuthorityAndDestroysTheChallenge(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")
	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil {
		t.Fatal(err)
	}
	tr.authz.deny.Store(true) // the role or the session is gone
	if _, err := tr.svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", pwCanary)); !errors.Is(err, credential.ErrProvisioningDenied) {
		t.Fatalf("got %v", err)
	}
	tr.authz.deny.Store(false) // regained: the old challenge must stay dead
	if _, err := tr.svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", pwCanary)); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("got %v", err)
	}
	if m, _ := tr.svc.ListPrincipal(bg, sub.Principal); len(m) != 0 {
		t.Fatalf("nothing may be stored: %+v", m)
	}
	out, err := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if err != nil || out.Result != audit.CriticalFailed || out.ReasonCode != "authority_lost" {
		t.Fatalf("outcome: %+v %v", out, err)
	}
	if st := tr.state(sub); st != identity.BindingPending {
		t.Fatalf("the binding stays pending: %s", st)
	}
}

func TestCancelAndExpiryAreRecorded(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")

	by := tr.by("sess-1")
	res, _ := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err := tr.svc.CancelProvisioning(bg, sub, by, res.Challenge.ID); err != nil {
		t.Fatal(err)
	}
	if out, err := tr.rec.GetOutcome(bg, "o1", by.EventID); err != nil || out.Result != audit.CriticalFailed || out.ReasonCode != "cancelled" {
		t.Fatalf("cancel: %+v %v", out, err)
	}
	if _, err := tr.svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", pwCanary)); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("a cancelled challenge cannot be answered: %v", err)
	}

	by2 := tr.by("sess-1")
	res, _ = tr.svc.BeginProvisioning(bg, sub, by2, desc)
	tr.clk.Advance(credential.ChallengeTTL + time.Second)
	if _, err := tr.svc.AnswerProvisioning(bg, sub, by2, res.Challenge.ID, answer("alice", pwCanary)); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("expired: %v", err)
	}
	if out, err := tr.rec.GetOutcome(bg, "o1", by2.EventID); err != nil || out.Result != audit.CriticalFailed || out.ReasonCode != "expired" {
		t.Fatalf("expiry: %+v %v", out, err)
	}
}

func TestSweepProvisioningExpiryRecordsCriticalFailed(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "sweep-success")
	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("BeginProvisioning = %+v, %v", res, err)
	}

	tr.clk.Advance(credential.ChallengeTTL + time.Second)
	n, err := tr.svc.Sweep(bg)
	if err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v", n, err)
	}
	out, err := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if err != nil || out.Result != audit.CriticalFailed || out.ReasonCode != "expired" {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
	if _, err := tr.chal.Get(bg, res.Challenge.ID); !errors.Is(err, credential.ErrChallengeNotFound) {
		t.Fatalf("challenge must be deleted only after its outcome is durable: %v", err)
	}
}

func TestSweepProvisioningExpiryKeepsChallengeWhenOutcomeFails(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "sweep-failure")
	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("BeginProvisioning = %+v, %v", res, err)
	}

	tr.clk.Advance(credential.ChallengeTTL + time.Second)
	tr.rec.failOutcome.Store(true)
	n, err := tr.svc.Sweep(bg)
	if n != 0 || !errors.Is(err, audit.ErrOutcomeUnknown) || errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("Sweep = %d, %v; want ErrOutcomeUnknown only", n, err)
	}
	if _, err := tr.chal.Get(bg, res.Challenge.ID); err != nil {
		t.Fatalf("challenge must remain for retry after outcome failure: %v", err)
	}

	tr.rec.failOutcome.Store(false)
	n, err = tr.svc.Sweep(bg)
	if err != nil || n != 1 {
		t.Fatalf("retry Sweep = %d, %v", n, err)
	}
	out, err := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if err != nil || out.Result != audit.CriticalFailed || out.ReasonCode != "expired" {
		t.Fatalf("retry outcome = %+v, %v", out, err)
	}
}

func TestSweepProvisioningExpiryIsIdempotent(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "sweep-idempotent")
	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("BeginProvisioning = %+v, %v", res, err)
	}

	tr.clk.Advance(credential.ChallengeTTL + time.Second)
	if n, err := tr.svc.Sweep(bg); err != nil || n != 1 {
		t.Fatalf("first Sweep = %d, %v", n, err)
	}
	if n, err := tr.svc.Sweep(bg); err != nil || n != 0 {
		t.Fatalf("second Sweep = %d, %v; want no-op", n, err)
	}
	out, err := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if err != nil || out.Result != audit.CriticalFailed || out.ReasonCode != "expired" {
		t.Fatalf("outcome after repeated Sweep = %+v, %v", out, err)
	}
}

func TestWrongPasswordsEndTheSetupAndAreRecorded(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")
	by := tr.by("sess-1")
	res, _ := tr.svc.BeginProvisioning(bg, sub, by, desc)
	var last error
	for i := 0; i < credential.MaxAnswerAttempts+1; i++ {
		_, last = tr.svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", "wrong"))
		if errors.Is(last, credential.ErrChallengeExpired) {
			break
		}
	}
	if m, _ := tr.svc.ListPrincipal(bg, sub.Principal); len(m) != 0 {
		t.Fatalf("nothing may be stored: %+v", m)
	}
	if out, err := tr.rec.GetOutcome(bg, "o1", by.EventID); err == nil && out.Result == audit.CriticalSucceeded {
		t.Fatalf("a failed setup must never be recorded as succeeded: %+v", out)
	}
}

func TestOutcomeFailureLeavesTheCredentialUnusableUntilConfirmed(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")
	by := tr.by("sess-1")
	res, _ := tr.svc.BeginProvisioning(bg, sub, by, desc)

	tr.rec.failOutcome.Store(true)
	_, err := tr.svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", pwCanary))
	if !errors.Is(err, audit.ErrOutcomeUnknown) {
		t.Fatalf("got %v", err)
	}
	tr.rec.failOutcome.Store(false)
	if st := tr.state(sub); st != identity.BindingPending {
		t.Fatalf("the binding must not be activated without a confirmed outcome: %s", st)
	}
	if _, err := tr.token(sub); !errors.Is(err, credential.ErrBindingUnusable) {
		t.Fatalf("Token on a pending binding: %v", err)
	}
	// Even an active binding does not make an unconfirmed version usable (rotation below).
}

func TestRotationIsGatedByItsOwnOutcome(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")
	tr.provision(sub, "alice", pwCanary)
	if _, err := tr.token(sub); err != nil {
		t.Fatal(err)
	}

	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc) // an active binding: rotation
	if err != nil || res.Challenge == nil {
		t.Fatalf("%+v %v", res, err)
	}
	tr.rec.failOutcome.Store(true)
	if _, err := tr.svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("bob", "bob-pw")); !errors.Is(err, audit.ErrOutcomeUnknown) {
		t.Fatalf("got %v", err)
	}
	tr.rec.failOutcome.Store(false)
	// The new version is stored with an unconfirmed event: it must not be served, and the old token must not be either.
	if _, err := tr.token(sub); !errors.Is(err, credential.ErrCredentialUnconfirmed) {
		t.Fatalf("an unconfirmed rotation must block the token: %v", err)
	}
}

func TestStaleRotationDoesNotOverwriteANewerVersion(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")
	tr.provision(sub, "alice", pwCanary)

	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil {
		t.Fatal(err)
	}
	// Meanwhile the access token expires and a routine refresh rotates the refresh token: version 2.
	tr.clk.Advance(6 * time.Minute) // past the access token's lifetime, inside the challenge's
	if _, err := tr.token(sub); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("bob", "bob-pw")); !errors.Is(err, credential.ErrVersionConflict) {
		t.Fatalf("a stale setup must lose: %v", err)
	}
	if out, err := tr.rec.GetOutcome(bg, "o1", by.EventID); err != nil || out.Result != audit.CriticalFailed || out.ReasonCode != "conflict" {
		t.Fatalf("outcome: %+v %v", out, err)
	}
	tok, err := tr.token(sub)
	if err != nil || tok.Principal != "alice" {
		t.Fatalf("the newer version (alice) must still be in use: %+v %v", tok, err)
	}
}

// ---------------------------------------------------------------------------------------------------------------
// execution

func TestTokenResolvesIdentityAndBindingOnEveryCall(t *testing.T) {
	tr := newTechRig(t)
	sub, b := tr.principal(identity.PrincipalTeam, "ci")

	if _, err := tr.token(sub); !errors.Is(err, credential.ErrBindingUnusable) {
		t.Fatalf("a pending binding: %v", err)
	}
	tr.provision(sub, "alice", pwCanary)
	if _, err := tr.token(sub); err != nil {
		t.Fatal(err)
	}
	// The cached token is not served once the identity is suspended.
	ident, _ := tr.ids.Get(bg, sub.Principal)
	if _, err := tr.ids.Suspend(bg, identity.TransitionRequest{Actor: admin, Ref: sub.Principal, ExpectedRevision: ident.Revision, MutationID: tr.nextID("ms")}); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.token(sub); !errors.Is(err, credential.ErrBindingUnusable) || !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("a suspended identity: %v", err)
	}
	ident, _ = tr.ids.Get(bg, sub.Principal)
	if _, err := tr.ids.Resume(bg, identity.TransitionRequest{Actor: admin, Ref: sub.Principal, ExpectedRevision: ident.Revision, MutationID: tr.nextID("mr")}); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.token(sub); err != nil {
		t.Fatalf("resumed: %v", err)
	}

	// A revoked binding blocks issuance at once, without any provider call.
	begins, _ := tr.prov.Calls()
	cur, _ := tr.ids.ResolveBinding(bg, sub.Principal, "c1")
	if _, err := tr.ids.RevokeBinding(bg, identity.BindingTransitionRequest{Actor: admin, Principal: sub.Principal, ClusterID: "c1", ExpectedRevision: cur.Revision, MutationID: tr.nextID("mv")}); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.token(sub); !errors.Is(err, credential.ErrBindingUnusable) {
		t.Fatalf("a revoked binding: %v", err)
	}
	if after, _ := tr.prov.Calls(); after != begins {
		t.Fatalf("no provider call for a blocked subject: %d -> %d", begins, after)
	}
	_ = b
}

func TestExecutionNeverCreatesAChallengeAndNeverFallsBack(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")
	tr.provision(sub, "alice", pwCanary)

	// A user credential and the legacy team account exist on the same cluster: neither may be used.
	userSub := credential.Subject{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1", SessionID: "s1"}
	res, _ := tr.svc.Acquire(bg, userSub, desc)
	if _, err := tr.svc.Answer(bg, userSub, res.Challenge.ID, answer("bob", "bob-pw")); err != nil {
		t.Fatal(err)
	}
	teamSub := credential.Subject{ClusterID: "c1", Scope: credential.ScopeTeam, OwnerID: "t1"}
	res, _ = tr.svc.Acquire(bg, teamSub, desc)
	if _, err := tr.svc.Answer(bg, teamSub, res.Challenge.ID, answer("bob", "bob-pw")); err != nil {
		t.Fatal(err)
	}

	// The IdP refuses the technical refresh token: a human would have to sign in, so execution is blocked.
	tr.idp.RevokeRefresh(tr.idp.LatestRefresh("alice"))
	tr.clk.Advance(10 * time.Minute)
	res, err := tr.svc.Acquire(bg, sub, desc)
	if !errors.Is(err, credential.ErrChallengeRequired) || res.Challenge != nil || res.Token != nil {
		t.Fatalf("Acquire = %+v, %v", res, err)
	}
	if _, err := tr.token(sub); !errors.Is(err, credential.ErrChallengeRequired) {
		t.Fatalf("Token: %v", err)
	}
	// No ordinary challenge was stored for the technical subject, and nothing of the user's was touched.
	if _, err := tr.svc.Poll(bg, sub, "anything"); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("Poll: %v", err)
	}
	if got, err := tr.svc.Token(bg, userSub, desc); err != nil || got.Principal != "bob" {
		t.Fatalf("the user's own sign-in is unaffected: %+v %v", got, err)
	}
	// No credential at all for a second binding: blocked, not borrowed.
	other, _ := tr.principal(identity.PrincipalTeam, "other")
	tr.provision(other, "bob", "bob-pw")
	third, _ := tr.principal(identity.PrincipalService, "svc")
	if _, err := tr.token(third); !errors.Is(err, credential.ErrBindingUnusable) {
		t.Fatalf("a pending binding with no credential: %v", err)
	}
}

func TestTechnicalSubjectsAreIsolated(t *testing.T) {
	tr := newTechRig(t)
	teamSub, _ := tr.principal(identity.PrincipalTeam, "same-name")
	svcSub, _ := tr.principal(identity.PrincipalService, "same-name")
	tr.provision(teamSub, "alice", pwCanary)
	tr.provision(svcSub, "bob", "bob-pw")

	a, err := tr.token(teamSub)
	if err != nil {
		t.Fatal(err)
	}
	b, err := tr.token(svcSub)
	if err != nil {
		t.Fatal(err)
	}
	if a.Principal != "alice" || b.Principal != "bob" || a.Value == b.Value {
		t.Fatalf("tokens crossed: %+v %+v", a, b)
	}
	// Even a swapped binding id on the other principal is not a credential.
	swapped := teamSub
	swapped.BindingID = svcSub.BindingID
	if _, err := tr.token(swapped); !errors.Is(err, credential.ErrBindingUnusable) {
		t.Fatalf("a foreign binding: %v", err)
	}
	if m, _ := tr.svc.ListPrincipal(bg, teamSub.Principal); len(m) != 1 || m[0].BindingID != teamSub.BindingID {
		t.Fatalf("ListPrincipal must show only its own principal: %+v", m)
	}
}

func TestRevokePrincipalIsAuditedAndFinal(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")
	tr.provision(sub, "alice", pwCanary)
	if _, err := tr.token(sub); err != nil {
		t.Fatal(err)
	}
	staleKey := credential.Key{ClusterID: "c1", Scope: credential.ScopePrincipal, Principal: sub.Principal, BindingID: sub.BindingID}
	stale, err := tr.store.Get(bg, staleKey)
	if err != nil {
		t.Fatal(err)
	}

	by := tr.by("sess-1")
	if err := tr.svc.RevokePrincipal(bg, sub, by); err != nil {
		t.Fatal(err)
	}
	if out, err := tr.rec.GetOutcome(bg, "o1", by.EventID); err != nil || out.Result != audit.CriticalSucceeded {
		t.Fatalf("outcome: %+v %v", out, err)
	}
	if _, err := tr.token(sub); !errors.Is(err, credential.ErrChallengeRequired) {
		t.Fatalf("after a revoke Token must not issue: %v", err)
	}
	// A stale writer (a refresh that began before the revoke) cannot bring the record back.
	if _, err := tr.store.Put(bg, stale, stale.Version); !errors.Is(err, credential.ErrVersionConflict) {
		t.Fatalf("a stale write after revoke: %v", err)
	}
	// Denied authority: nothing happens.
	tr.authz.deny.Store(true)
	if err := tr.svc.RevokePrincipal(bg, sub, tr.by("sess-1")); !errors.Is(err, credential.ErrProvisioningDenied) {
		t.Fatalf("got %v", err)
	}
}

func TestRevokeFilterForTechnicalCredentialsMustBeExact(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")
	tr.provision(sub, "alice", pwCanary)

	bad := []credential.RevokeFilter{
		{Scope: credential.ScopePrincipal},
		{Scope: credential.ScopePrincipal, ClusterID: "c1"},
		{Scope: credential.ScopePrincipal, ClusterID: "c1", Principal: sub.Principal},
		{Scope: credential.ScopePrincipal, ClusterID: "c1", BindingID: sub.BindingID},
		{ClusterID: "c1", Principal: sub.Principal, BindingID: sub.BindingID}, // technical fields without the scope
		{Scope: credential.ScopePrincipal, ClusterID: "c1", OwnerID: "x", Principal: sub.Principal, BindingID: sub.BindingID},
	}
	for i, f := range bad {
		if err := tr.svc.Revoke(bg, f); !errors.Is(err, credential.ErrInvalidKey) {
			t.Errorf("filter %d: %v", i, err)
		}
	}
	if m, _ := tr.svc.ListPrincipal(bg, sub.Principal); len(m) != 1 {
		t.Fatalf("nothing may be removed by a refused filter: %+v", m)
	}
	// An ordinary owner revoke does not touch technical credentials.
	if err := tr.svc.Revoke(bg, credential.RevokeFilter{OwnerID: "t1", Scope: credential.ScopeTeam}); err != nil {
		t.Fatal(err)
	}
	if m, _ := tr.svc.ListPrincipal(bg, sub.Principal); len(m) != 1 {
		t.Fatalf("a legacy team revoke must not remove a technical credential: %+v", m)
	}
	exact := credential.RevokeFilter{Scope: credential.ScopePrincipal, ClusterID: "c1", Principal: sub.Principal, BindingID: sub.BindingID}
	if err := tr.svc.Revoke(bg, exact); err != nil {
		t.Fatal(err)
	}
	if m, _ := tr.svc.ListPrincipal(bg, sub.Principal); len(m) != 0 {
		t.Fatalf("the exact filter removes it: %+v", m)
	}
}

// A store without technical support, or missing ports, fails closed instead of dropping the principal and binding.
func TestTechnicalOperationsFailClosedWithoutSupport(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")

	legacyOnly := struct{ credential.CredentialStore }{tr.store} // hides ListPrincipal
	svc := tr.service(legacyOnly)
	if _, err := svc.Token(bg, sub, desc); !errors.Is(err, credential.ErrNotConfigured) {
		t.Errorf("a store without ListPrincipal: %v", err)
	}
	if _, err := svc.BeginProvisioning(bg, sub, tr.by("sess-1"), desc); !errors.Is(err, credential.ErrNotConfigured) {
		t.Errorf("provisioning on such a store: %v", err)
	}
	// The legacy key validation rejects a technical key, so a store that only knows legacy keys cannot write one.
	key := credential.Key{ClusterID: "c1", Scope: credential.ScopePrincipal, Principal: sub.Principal, BindingID: sub.BindingID}
	if key.Validate() == nil {
		t.Error("Key.Validate must stay legacy-only")
	}

	reg := credential.NewRegistry(false)
	_ = reg.Register(tr.prov)
	noPorts, err := credential.NewService(credential.Config{Registry: reg, Store: tr.store, Challenges: tr.chal, Envelope: tr.env, Now: tr.clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := noPorts.Token(bg, sub, desc); !errors.Is(err, credential.ErrNotConfigured) {
		t.Errorf("no identity or audit port: %v", err)
	}
	if _, err := noPorts.BeginProvisioning(bg, sub, tr.by("sess-1"), desc); !errors.Is(err, credential.ErrNotConfigured) {
		t.Errorf("no authorizer: %v", err)
	}
	// The legacy behavior needs none of it.
	if res, err := noPorts.Acquire(bg, subj, desc); err != nil || res.Challenge == nil {
		t.Errorf("legacy Acquire: %+v %v", res, err)
	}
}

func TestActivateProvisionedBindingFinishesAnInterruptedSetup(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")
	by := tr.by("sess-1")
	res, _ := tr.svc.BeginProvisioning(bg, sub, by, desc)

	// The credential is stored and confirmed, but activation fails (the identity domain is unavailable for admins).
	// Simulate by making the admin authority of the identity domain fail only at activation time.
	tr.authz.deny.Store(false)
	failing := &activationFails{IdentityResolver: tr.ids}
	failing.fail.Store(true)
	reg := credential.NewRegistry(false)
	_ = reg.Register(tr.prov)
	svc, err := credential.NewService(credential.Config{Registry: reg, Store: tr.store, Challenges: tr.chal, Envelope: tr.env, Now: tr.clk.Now,
		Identities: failing, Audit: tr.rec, Provisioning: tr.authz})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", pwCanary))
	if !errors.Is(err, credential.ErrBindingNotActivated) {
		t.Fatalf("got %v", err)
	}
	if st := tr.state(sub); st != identity.BindingPending {
		t.Fatalf("still pending: %s", st)
	}
	if _, err := tr.token(sub); !errors.Is(err, credential.ErrBindingUnusable) {
		t.Fatalf("not usable before activation: %v", err)
	}
	failing.fail.Store(false)
	if err := svc.ActivateProvisionedBinding(bg, sub, tr.by("sess-1")); err != nil {
		t.Fatal(err)
	}
	if err := svc.ActivateProvisionedBinding(bg, sub, tr.by("sess-1")); err != nil {
		t.Fatalf("idempotent: %v", err)
	}
	if _, err := tr.token(sub); err != nil {
		t.Fatalf("usable after activation: %v", err)
	}
}

type activationFails struct {
	credential.IdentityResolver
	fail atomic.Bool
}

func (a *activationFails) ActivateBinding(ctx context.Context, req identity.BindingTransitionRequest) (identity.CredentialBinding, error) {
	if a.fail.Load() {
		return identity.CredentialBinding{}, identity.ErrUnavailable
	}
	return a.IdentityResolver.ActivateBinding(ctx, req)
}

// ---------------------------------------------------------------------------------------------------------------
// secrets

func TestNoSecretReachesLogsAuditOrDisk(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")
	tr.provision(sub, "alice", pwCanary)
	tok, err := tr.token(sub)
	if err != nil {
		t.Fatal(err)
	}
	refresh := tr.idp.LatestRefresh("alice")
	secrets := []string{pwCanary, tok.Value, refresh, csCanary}

	var files []string
	_ = filepath.WalkDir(tr.dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		t.Fatal("no files to scan")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range secrets {
			if s != "" && strings.Contains(string(raw), s) {
				t.Errorf("a secret is in %s", f)
			}
		}
	}
	for _, s := range secrets {
		if s != "" && strings.Contains(tr.logs.String(), s) {
			t.Errorf("a secret is in the logs: %q", s)
		}
	}
}

// ---------------------------------------------------------------------------------------------------------------
// the file store with technical records

func TestFileStoreFormatVersionAndPrincipalRecords(t *testing.T) {
	tr := newTechRig(t)
	path := tr.storeP
	readVersion := func() string {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), `"version":2`) {
			return "2"
		}
		if strings.Contains(string(raw), `"version":1`) {
			return "1"
		}
		return "?"
	}
	// A file with legacy records only stays in the format every earlier release reads.
	res, _ := tr.svc.Acquire(bg, subj, desc)
	if _, err := tr.svc.Answer(bg, subj, res.Challenge.ID, answer("alice", pwCanary)); err != nil {
		t.Fatal(err)
	}
	if v := readVersion(); v != "1" {
		t.Fatalf("legacy-only file version %s", v)
	}
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), "principalKind") || strings.Contains(string(raw), "bindingId") {
		t.Fatal("legacy records must not gain technical fields")
	}

	sub, _ := tr.principal(identity.PrincipalTeam, "ci")
	tr.provision(sub, "bob", "bob-pw")
	if v := readVersion(); v != "2" {
		t.Fatalf("a file with a technical record must be version 2 (older releases refuse it): %s", v)
	}

	// Reopen (a restart): both kinds are readable.
	st, err := filestore.New(path, credential.NewSealer(tr.env))
	if err != nil {
		t.Fatal(err)
	}
	key := credential.Key{ClusterID: "c1", Scope: credential.ScopePrincipal, Principal: sub.Principal, BindingID: sub.BindingID}
	rec, err := st.Get(bg, key)
	if err != nil || len(rec.Secret) == 0 || rec.LastAuditEventID == "" {
		t.Fatalf("reopened technical record: %v", err)
	}
	if _, err := st.Get(bg, credential.Key{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1"}); err != nil {
		t.Fatalf("legacy record: %v", err)
	}
	rep, err := st.Verify(bg)
	if err != nil || rep.Total != 2 || rep.Technical != 1 || len(rep.Unreadable) != 0 {
		t.Fatalf("Verify = %+v, %v", rep, err)
	}

	// Backup and restore: a byte copy opens with the same key ring and keeps the technical record intact.
	backup := filepath.Join(t.TempDir(), "restored.json")
	raw, _ := os.ReadFile(path)
	if err := os.WriteFile(backup, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	rst, err := filestore.New(backup, credential.NewSealer(tr.env))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := rst.Get(bg, key); err != nil || string(got.Secret) != string(rec.Secret) {
		t.Fatalf("restored: %v", err)
	}

	// Rekey covers technical records too.
	rr, err := st.Rekey(bg)
	if err != nil || rr.Total != 2 || len(rr.Failed) != 0 {
		t.Fatalf("Rekey = %+v, %v", rr, err)
	}

	// Removing the last technical record returns the file to the earlier format.
	if err := st.Delete(bg, key); err != nil {
		t.Fatal(err)
	}
	if v := readVersion(); v != "1" {
		t.Fatalf("version after the last technical record is gone: %s", v)
	}
}

func TestFileStoreListsAreSeparated(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "ci")
	tr.provision(sub, "alice", pwCanary)
	if l, err := tr.store.List(bg, ""); err != nil || len(l) != 0 {
		t.Fatalf("legacy List(\"\") must never return technical records: %+v %v", l, err)
	}
	if _, err := tr.store.(credential.PrincipalStore).ListPrincipal(bg, identity.PrincipalRef{Kind: identity.PrincipalUser, ID: "alice"}); !errors.Is(err, credential.ErrInvalidKey) {
		t.Fatalf("a user principal has no technical records: %v", err)
	}
}

func TestSwappedTechnicalCiphertextsDoNotOpen(t *testing.T) {
	tr := newTechRig(t)
	a, _ := tr.principal(identity.PrincipalTeam, "a")
	b, _ := tr.principal(identity.PrincipalTeam, "b")
	tr.provision(a, "alice", pwCanary)
	tr.provision(b, "bob", "bob-pw")

	raw, _ := os.ReadFile(tr.storeP)
	// Swap the ciphertexts of the two records (the secretEnc values) in the file.
	var encs []string
	for _, part := range strings.Split(string(raw), `"secretEnc":"`)[1:] {
		encs = append(encs, part[:strings.Index(part, `"`)])
	}
	if len(encs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(encs))
	}
	swapped := strings.NewReplacer(encs[0], "@@A@@", encs[1], encs[0]).Replace(string(raw))
	swapped = strings.ReplaceAll(swapped, "@@A@@", encs[1])
	path := filepath.Join(t.TempDir(), "swapped.json")
	if err := os.WriteFile(path, []byte(swapped), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := filestore.New(path, credential.NewSealer(tr.env))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := st.Verify(bg)
	if err != nil || len(rep.Unreadable) != 2 {
		t.Fatalf("both swapped records must be unreadable: %+v %v", rep, err)
	}
	if _, err := st.Get(bg, credential.Key{ClusterID: "c1", Scope: credential.ScopePrincipal, Principal: a.Principal, BindingID: a.BindingID}); err == nil {
		t.Fatal("a swapped ciphertext must not decrypt")
	}
}


// deleteFailChallengeStore injects a one-shot failure into challenge deletion while retaining the shared store
// and attempt limiter, as happens with a transient PostgreSQL failure.
type deleteFailChallengeStore struct {
	credential.ChallengeStore
	credential.AttemptLimiter
	fail atomic.Bool
}

func (s *deleteFailChallengeStore) Delete(ctx context.Context, id string) error {
	if s.fail.CompareAndSwap(true, false) {
		return errors.New("challenge delete unavailable")
	}
	return s.ChallengeStore.Delete(ctx, id)
}

func (tr *techRig) serviceWithChallenges(store credential.CredentialStore, challenges credential.ChallengeStore) *credential.Service {
	reg := credential.NewRegistry(false)
	if err := reg.Register(tr.prov); err != nil { tr.t.Fatal(err) }
	svc, err := credential.NewService(credential.Config{Registry: reg, Store: store, Challenges: challenges, Envelope: tr.env,
		Now: tr.clk.Now, Logger: slog.New(slog.NewTextHandler(tr.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Identities: tr.ids, Audit: tr.rec, Provisioning: tr.authz})
	if err != nil { tr.t.Fatal(err) }
	return svc
}

func TestCancelProvisioningKeepsChallengeWhenOutcomeAppendFails(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "cancel-audit-failure")
	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil { t.Fatalf("BeginProvisioning = %+v, %v", res, err) }

	tr.rec.failOutcome.Store(true)
	err = tr.svc.CancelProvisioning(bg, sub, by, res.Challenge.ID)
	if !errors.Is(err, audit.ErrOutcomeUnknown) { t.Fatalf("CancelProvisioning = %v, want ErrOutcomeUnknown", err) }
	if _, err := tr.chal.Get(bg, res.Challenge.ID); err != nil { t.Fatalf("challenge must remain recoverable: %v", err) }

	tr.rec.failOutcome.Store(false)
	if err := tr.svc.CancelProvisioning(bg, sub, by, res.Challenge.ID); err != nil {
		t.Fatalf("retry cancellation: %v", err)
	}
	out, err := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if err != nil || out.Result != audit.CriticalFailed || out.ReasonCode != "cancelled" {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
}

func TestCancelProvisioningRetriesDeleteAfterDurableOutcome(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "cancel-delete-failure")
	by := tr.by("sess-1")
	store := &deleteFailChallengeStore{ChallengeStore: tr.chal, AttemptLimiter: tr.chal}
	svc := tr.serviceWithChallenges(tr.store, store)
	res, err := svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil { t.Fatalf("BeginProvisioning = %+v, %v", res, err) }

	store.fail.Store(true)
	if err := svc.CancelProvisioning(bg, sub, by, res.Challenge.ID); err == nil {
		t.Fatal("expected the injected delete failure")
	}
	out, err := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if err != nil || out.Result != audit.CriticalFailed { t.Fatalf("outcome must precede deletion: %+v %v", out, err) }
	if _, err := tr.chal.Get(bg, res.Challenge.ID); err != nil { t.Fatalf("challenge must remain after failed delete: %v", err) }

	// The retry sees the immutable CriticalFailed, does not append a conflicting outcome, and completes cleanup.
	if err := svc.CancelProvisioning(bg, sub, by, res.Challenge.ID); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("retry should clean up an already-terminal challenge: %v", err)
	}
	if _, err := tr.chal.Get(bg, res.Challenge.ID); !errors.Is(err, credential.ErrChallengeNotFound) {
		t.Fatalf("challenge still exists: %v", err)
	}
}

func TestAdvanceProvisioningExpiryAuditsBeforeDelete(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "advance-expiry")
	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil { t.Fatalf("BeginProvisioning = %+v, %v", res, err) }
	tr.clk.Advance(credential.ChallengeTTL + time.Second)
	tr.rec.failOutcome.Store(true)
	_, err = tr.svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", pwCanary))
	if !errors.Is(err, audit.ErrOutcomeUnknown) { t.Fatalf("expiry = %v, want ErrOutcomeUnknown", err) }
	if _, err := tr.chal.Get(bg, res.Challenge.ID); err != nil { t.Fatalf("expired challenge must remain after audit failure: %v", err) }
	tr.rec.failOutcome.Store(false)
	_, err = tr.svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", pwCanary))
	if !errors.Is(err, credential.ErrChallengeExpired) { t.Fatalf("retry expiry = %v", err) }
	if _, err := tr.chal.Get(bg, res.Challenge.ID); !errors.Is(err, credential.ErrChallengeNotFound) { t.Fatalf("challenge remains: %v", err) }
}

func TestProvisioningAttemptExhaustionIsAuditedBeforeDelete(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "attempt-exhaustion")
	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil { t.Fatalf("BeginProvisioning = %+v, %v", res, err) }
	tr.prov.OnContinue = func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
		return credential.Result{}, credential.ErrAnswerRejected
	}
	for i := 0; i < credential.MaxAnswerAttempts; i++ {
		_, err = tr.svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", "wrong"))
	}
	if !errors.Is(err, credential.ErrAnswerRejected) { t.Fatalf("last answer = %v", err) }
	out, err := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if err != nil || out.Result != audit.CriticalFailed || out.ReasonCode != "rejected" {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
	if _, err := tr.chal.Get(bg, res.Challenge.ID); !errors.Is(err, credential.ErrChallengeNotFound) {
		t.Fatalf("exhausted challenge remains: %v", err)
	}
}

func TestProvisioningChallengeTransitionDeleteFailureIsAudited(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "transition-delete-failure")
	by := tr.by("sess-1")
	store := &deleteFailChallengeStore{ChallengeStore: tr.chal, AttemptLimiter: tr.chal}
	svc := tr.serviceWithChallenges(tr.store, store)
	res, err := svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil { t.Fatalf("BeginProvisioning = %+v, %v", res, err) }
	tr.prov.OnContinue = func(_ context.Context, _ credential.Env, req credential.Request) (credential.Result, error) {
		return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeDeviceCode, Poll: true}, Pending: []byte("next")}, nil
	}
	store.fail.Store(true)
	_, err = svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", pwCanary))
	if err == nil { t.Fatal("expected old challenge deletion failure") }
	out, auditErr := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if auditErr != nil || out.Result != audit.CriticalFailed {
		t.Fatalf("transition failure must be audited before cleanup: %+v %v", out, auditErr)
	}
	if _, err := tr.chal.Get(bg, res.Challenge.ID); !errors.Is(err, credential.ErrChallengeNotFound) {
		t.Fatalf("old challenge not cleaned after durable outcome: %v", err)
	}
}

func TestConcurrentProvisioningCancelAcrossServiceInstances(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "concurrent-cancel")
	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil { t.Fatalf("BeginProvisioning = %+v, %v", res, err) }
	replica := tr.service(tr.store)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, svc := range []*credential.Service{tr.svc, replica} {
		wg.Add(1)
		go func(i int, svc *credential.Service) {
			defer wg.Done()
			errs[i] = svc.CancelProvisioning(bg, sub, by, res.Challenge.ID)
		}(i, svc)
	}
	wg.Wait()
	success, refused := 0, 0
	for _, err := range errs {
		if err == nil { success++ } else if errors.Is(err, credential.ErrChallengeExpired) { refused++ } else { t.Errorf("unexpected concurrent result: %v", err) }
	}
	if success != 1 || refused != 1 { t.Fatalf("concurrent results = %v; want one success and one CAS refusal", errs) }
	out, err := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if err != nil || out.Result != audit.CriticalFailed { t.Fatalf("outcome = %+v, %v", out, err) }
}
