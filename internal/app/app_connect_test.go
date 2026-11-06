// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/k8s"
	"github.com/24DSO-labs/kubron/internal/signin"
	"github.com/24DSO-labs/kubron/pkg/ext"
)

// fakeBroker is a scripted credential.Broker: each call pops the next prepared result.
type fakeBroker struct {
	mu       sync.Mutex
	desc     credential.Descriptor
	acquire  []credential.Result
	answer   []credential.Result
	poll     []credential.Result
	answerEr error
	subjects []credential.Subject
	cancels  []string
	revoked  []credential.RevokeFilter
	answers  []credential.Answer
}

func pop(q *[]credential.Result) credential.Result {
	if len(*q) == 0 {
		return credential.Result{}
	}
	r := (*q)[0]
	*q = (*q)[1:]
	return r
}

func (f *fakeBroker) Describe([]byte, string) (credential.Descriptor, error) { return f.desc, nil }
func (f *fakeBroker) Acquire(_ context.Context, s credential.Subject, _ credential.Descriptor) (credential.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subjects = append(f.subjects, s)
	return pop(&f.acquire), nil
}
func (f *fakeBroker) Answer(_ context.Context, s credential.Subject, _ string, a credential.Answer) (credential.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subjects = append(f.subjects, s)
	f.answers = append(f.answers, a)
	if f.answerEr != nil {
		return credential.Result{}, f.answerEr
	}
	return pop(&f.answer), nil
}
func (f *fakeBroker) Poll(_ context.Context, s credential.Subject, _ string) (credential.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subjects = append(f.subjects, s)
	return pop(&f.poll), nil
}
func (f *fakeBroker) Cancel(_ context.Context, _ credential.Subject, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels = append(f.cancels, id)
	return nil
}
func (f *fakeBroker) Token(context.Context, credential.Subject, credential.Descriptor) (credential.Token, error) {
	return credential.Token{}, credential.ErrChallengeRequired
}
func (f *fakeBroker) Revoke(_ context.Context, r credential.RevokeFilter) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, r)
	return nil
}
func (f *fakeBroker) List(context.Context, string) ([]credential.ConnectionInfo, error) {
	return nil, nil
}

type recPlatform struct {
	mu     sync.Mutex
	events []string
	data   []any
}

func (p *recPlatform) Emit(e string, d ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
	p.data = append(p.data, d...)
}
func (*recPlatform) PickFile(string) (string, error) { return "", nil }
func (*recPlatform) OpenURL(string)                  {}
func (*recPlatform) Capabilities() ext.Capabilities  { return ext.Capabilities{} }

func challenge(id string, k credential.ChallengeKind) credential.Result {
	return credential.Result{Challenge: &credential.Challenge{ID: id, Kind: k}}
}

// connectFixture returns a logged-in app with one stored cluster, a fake broker and a recorded "connected" hook.
func connectFixture(t *testing.T, b *fakeBroker) (*App, *recPlatform, string, *[]string) {
	t.Helper()
	k8s.SetKubronDir(t.TempDir())
	t.Cleanup(func() { k8s.SetKubronDir("") })
	a := newAuditedApp(t)
	a.auth.SetOnLogout(nil)
	if _, err := a.auth.Setup("admin", testPass); err != nil {
		t.Fatal(err)
	}
	c, err := k8s.AddClusterFromText(kubeconfigForTest, k8s.ClusterSettings{DisplayName: "c", ContextName: "ctx"})
	if err != nil {
		t.Fatal(err)
	}
	pl := &recPlatform{}
	a.platform = pl
	a.conn = signin.NewState()
	a.broker = b
	var connected []string
	a.finish = func(id string) error { connected = append(connected, id); return nil }
	return a, pl, c.ID, &connected
}

func TestWithWebOIDCParamsRequiresPublicClientAndRedirectPair(t *testing.T) {
	base := credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{
		"client-id":        "kubeconfig-client",
		"web.client-id":    "untrusted-client",
		"web.redirect-url": "https://evil.example/callback",
	}}
	for _, tc := range []struct {
		name, clientID, redirectURL string
		wantClient, wantRedirect    string
	}{
		{name: "no admin client", redirectURL: "https://kubron.example/auth/cluster/callback"},
		{name: "no public URL", clientID: "admin-public-client"},
		{name: "both configured", clientID: "admin-public-client", redirectURL: "https://kubron.example/auth/cluster/callback",
			wantClient: "admin-public-client", wantRedirect: "https://kubron.example/auth/cluster/callback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := signin.WithWebOIDCParams(base, tc.clientID, tc.redirectURL)
			if got.Params["web.client-id"] != tc.wantClient || got.Params["web.redirect-url"] != tc.wantRedirect {
				t.Fatalf("web OIDC params = %#v", got.Params)
			}
			if got.Params["client-id"] != "kubeconfig-client" {
				t.Fatalf("the kubeconfig client id was changed: %#v", got.Params)
			}
		})
	}
}

func TestWebOIDCClientIDIsAdminOnly(t *testing.T) {
	a, _, _, _ := connectFixture(t, &fakeBroker{})
	if err := a.SetWebOIDCClientID("public-client"); err != nil {
		t.Fatal(err)
	}
	if got, err := a.GetWebOIDCClientID(); err != nil || got != "public-client" {
		t.Fatalf("admin read: %q %v", got, err)
	}
	bob, err := a.auth.CreateUser("bob", testPass, auth.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	a.auth.Logout()
	if _, err := a.auth.Login(bob.Username, testPass); err != nil {
		t.Fatal(err)
	}
	if err := a.auth.ChangePassword(testPass, "another-pass-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.GetWebOIDCClientID(); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("non-admin read: %v", err)
	}
	if err := a.SetWebOIDCClientID("attacker-client"); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("non-admin write: %v", err)
	}
}

func TestConnectClusterConnectedWithoutChallenge(t *testing.T) {
	b := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindStatic}}
	a, _, id, connected := connectFixture(t, b)
	res, err := a.ConnectCluster(id)
	if err != nil || res.Status != "connected" || res.Challenge != nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(*connected) != 1 || (*connected)[0] != id {
		t.Fatalf("the cluster was not connected: %v", *connected)
	}
	if got := b.subjects[0]; got.Scope != credential.ScopeTeam || got.ClusterID != id {
		t.Fatalf("static credentials belong to the team, got %+v", got)
	}
}

func TestConnectClusterChallengeAnswerCompletes(t *testing.T) {
	b := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindOIDC},
		acquire: []credential.Result{challenge("c1", credential.ChallengeCredentials)}}
	a, pl, id, connected := connectFixture(t, b)
	res, err := a.ConnectCluster(id)
	if err != nil || res.Status != "challenge" || res.Challenge.ID != "c1" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(*connected) != 0 {
		t.Fatal("the cluster must not connect while a challenge is open")
	}
	if b.subjects[0].Scope != credential.ScopeUser || b.subjects[0].OwnerID == "" {
		t.Fatalf("interactive sign-in is personal: %+v", b.subjects[0])
	}
	res, err = a.AnswerChallenge("c1", map[string]string{"username": "u", "password": "p"})
	if err != nil || res.Status != "connected" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(*connected) != 1 {
		t.Fatal("the cluster was not connected after the answer")
	}
	if len(pl.events) != 1 || pl.events[0] != EventAuthCompleted {
		t.Fatalf("events: %v", pl.events)
	}
	if _, err := a.AnswerChallenge("c1", nil); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("a completed challenge is single-use, got %v", err)
	}
}

func TestAnswerRejectedKeepsChallengeForRetry(t *testing.T) {
	b := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindOIDC},
		acquire: []credential.Result{challenge("c1", credential.ChallengeCredentials)}}
	a, _, id, connected := connectFixture(t, b)
	if _, err := a.ConnectCluster(id); err != nil {
		t.Fatal(err)
	}
	b.answerEr = credential.ErrAnswerRejected
	if _, err := a.AnswerChallenge("c1", map[string]string{"password": "bad"}); !errors.Is(err, credential.ErrAnswerRejected) {
		t.Fatalf("got %v", err)
	}
	b.answerEr = nil
	if res, err := a.AnswerChallenge("c1", map[string]string{"password": "good"}); err != nil || res.Status != "connected" {
		t.Fatalf("retry: res=%+v err=%v", res, err)
	}
	if len(*connected) != 1 {
		t.Fatal("not connected after the retry")
	}
}

// Bad input and an outage keep the challenge too, and the errors reach the caller as the contract sentinels (the
// interface translates them): an outage is never reported as wrong credentials.
func TestInvalidAnswerAndOutageKeepTheChallenge(t *testing.T) {
	for _, tc := range []struct {
		err    error
		reason string
	}{
		{credential.ErrInvalidAnswer, "invalid-answer"},
		{credential.ErrServiceUnavailable, "unavailable"},
		{credential.ErrAnswerRejected, "rejected"},
	} {
		b := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindOIDC},
			acquire: []credential.Result{challenge("c1", credential.ChallengeCredentials)}}
		a, _, id, connected := connectFixture(t, b)
		if _, err := a.ConnectCluster(id); err != nil {
			t.Fatal(err)
		}
		b.answerEr = tc.err
		if _, err := a.AnswerChallenge("c1", map[string]string{"password": "x"}); !errors.Is(err, tc.err) {
			t.Fatalf("%v: got %v", tc.err, err)
		}
		if got := authFailureReason(tc.err); got != tc.reason {
			t.Errorf("authFailureReason(%v) = %q, want %q", tc.err, got, tc.reason)
		}
		b.answerEr = nil
		if res, err := a.AnswerChallenge("c1", map[string]string{"password": "good"}); err != nil || res.Status != "connected" || len(*connected) != 1 {
			t.Fatalf("%v: the challenge was lost: res=%+v err=%v", tc.err, res, err)
		}
	}
}

func TestPollChallengePendingThenCompleted(t *testing.T) {
	b := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindOIDC},
		acquire: []credential.Result{challenge("d1", credential.ChallengeDeviceCode)},
		poll:    []credential.Result{challenge("d1", credential.ChallengeDeviceCode), {}}}
	a, pl, id, connected := connectFixture(t, b)
	if _, err := a.ConnectCluster(id); err != nil {
		t.Fatal(err)
	}
	res, err := a.PollChallenge("d1")
	if err != nil || res.Status != "challenge" {
		t.Fatalf("first poll: res=%+v err=%v", res, err)
	}
	if len(*connected) != 0 || len(pl.events) != 0 {
		t.Fatal("nothing may happen while the sign-in is pending")
	}
	res, err = a.PollChallenge("d1")
	if err != nil || res.Status != "connected" || len(*connected) != 1 || len(pl.events) != 1 {
		t.Fatalf("second poll: res=%+v err=%v connected=%v events=%v", res, err, *connected, pl.events)
	}
}

func TestCancelChallenge(t *testing.T) {
	b := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindOIDC},
		acquire: []credential.Result{challenge("c1", credential.ChallengeCredentials)}}
	a, _, id, _ := connectFixture(t, b)
	if _, err := a.ConnectCluster(id); err != nil {
		t.Fatal(err)
	}
	if err := a.CancelChallenge("c1"); err != nil {
		t.Fatal(err)
	}
	if len(b.cancels) != 1 || b.cancels[0] != "c1" {
		t.Fatalf("the broker was not told: %v", b.cancels)
	}
	if _, err := a.AnswerChallenge("c1", nil); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("got %v", err)
	}
}

func TestExpiredChallengeIsForgotten(t *testing.T) {
	b := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindOIDC},
		acquire: []credential.Result{challenge("c1", credential.ChallengeCredentials)}}
	a, _, id, _ := connectFixture(t, b)
	if _, err := a.ConnectCluster(id); err != nil {
		t.Fatal(err)
	}
	b.answerEr = credential.ErrChallengeExpired
	if _, err := a.AnswerChallenge("c1", nil); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("got %v", err)
	}
	b.answerEr = nil
	n := len(b.answers)
	if _, err := a.AnswerChallenge("c1", nil); !errors.Is(err, credential.ErrChallengeExpired) || len(b.answers) != n {
		t.Fatal("an expired challenge must not reach the broker again")
	}
}

// A challenge started in one session cannot be completed from another view of the same user.
func TestChallengeBelongsToTheSessionThatStartedIt(t *testing.T) {
	b := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindOIDC},
		acquire: []credential.Result{challenge("c1", credential.ChallengeCredentials)}}
	a, _, id, connected := connectFixture(t, b)
	if _, err := a.ConnectCluster(id); err != nil {
		t.Fatal(err)
	}
	other := *a
	other.conn = signin.NewState()
	n := len(b.answers)
	if _, err := other.AnswerChallenge("c1", map[string]string{"password": "p"}); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("got %v", err)
	}
	if len(b.answers) != n || len(*connected) != 0 {
		t.Fatal("a foreign session reached the broker or connected the cluster")
	}
}

func TestDisconnectClusterRevokesOnlyThePersonalSignIn(t *testing.T) {
	b := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindOIDC}}
	a, _, id, _ := connectFixture(t, b)
	if err := a.DisconnectCluster(id); err != nil {
		t.Fatal(err)
	}
	r := b.revoked[0]
	if r.ClusterID != id || r.Scope != credential.ScopeUser || r.OwnerID == "" {
		t.Fatalf("filter %+v", r)
	}
}

// Sign-in is audited: challenge, failure and success, with the Kubernetes subject next to the application user,
// and no secret reaches the journal.
func TestSignInIsAudited(t *testing.T) {
	const pw, tok = "CANARY-PASSWORD-1f9e", "CANARY-TOKEN-6c2d"
	b := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindOIDC},
		acquire: []credential.Result{challenge("c1", credential.ChallengeCredentials)},
		answer:  []credential.Result{{Token: &credential.Token{Value: tok, Principal: "alice@idp"}}}}
	a, _, id, _ := connectFixture(t, b)
	if _, err := a.ConnectCluster(id); err != nil {
		t.Fatal(err)
	}
	b.answerEr = credential.ErrAnswerRejected
	if _, err := a.AnswerChallenge("c1", map[string]string{"username": "u", "password": pw}); err == nil {
		t.Fatal("expected the rejection")
	}
	b.answerEr = nil
	if _, err := a.AnswerChallenge("c1", map[string]string{"username": "u", "password": pw}); err != nil {
		t.Fatal(err)
	}
	if err := a.DisconnectCluster(id); err != nil {
		t.Fatal(err)
	}

	var got []string // newest first
	var all string
	for _, e := range events(t, a) {
		if strings.HasPrefix(e.Action, "cluster.auth.") {
			got = append(got, e.Action+"|"+e.Result+"|"+e.Detail+"|"+e.User)
		}
		all += fmt.Sprintf("%+v", e)
	}
	want := []string{
		"cluster.auth.revoked|ok|cluster=" + id + " reason=disconnect owner=" + a.currentUserID() + "|admin",
		"cluster.auth.success|ok|cluster=" + id + " principal=alice@idp|admin",
		"cluster.auth.failure|failed|cluster=" + id + " reason=rejected|admin",
		"cluster.auth.challenge|ok|cluster=" + id + " kind=credentials|admin",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("journal:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if strings.Contains(all, pw) || strings.Contains(all, tok) {
		t.Fatal("a secret reached the audit journal")
	}
}

// openView returns another view of the same signed-in user with its own manager, connected to the cluster.
func openView(t *testing.T, a *App, clusterID string) *App {
	t.Helper()
	v := *a
	v.k8sMgr = a.k8sMgr.ForSession()
	v.pfMgr = k8s.NewSessionPortForwardManager(v.k8sMgr)
	v.conn = signin.NewState()
	if err := v.k8sMgr.ConnectToCluster(clusterID); err != nil {
		t.Fatal(err)
	}
	return &v
}

func lifecycleFixture(t *testing.T) (*App, *fakeBroker, string, *recPlatform) {
	t.Helper()
	b := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindOIDC}}
	a, pl, id, _ := connectFixture(t, b)
	a.pfMgr = k8s.NewPortForwardManager(a.k8sMgr)
	a.rev = &revocations{}
	if err := a.k8sMgr.ConnectToCluster(id); err != nil { // builds a client offline; nothing is dialled
		t.Fatal(err)
	}
	return a, b, id, pl
}

// Disconnect revokes the personal sign-in, closes the user's active cluster at once and tells the client.
func TestDisconnectClusterClosesTheViewAtOnce(t *testing.T) {
	a, b, id, pl := lifecycleFixture(t)
	if err := a.DisconnectCluster(id); err != nil {
		t.Fatal(err)
	}
	if len(b.revoked) != 1 || b.revoked[0].ClusterID != id {
		t.Fatalf("revoked: %+v", b.revoked)
	}
	if a.k8sMgr.GetActiveContext() != "" {
		t.Fatal("the active cluster must be closed")
	}
	if len(pl.events) == 0 || pl.events[len(pl.events)-1] != EventAuthRequired {
		t.Fatalf("events: %v", pl.events)
	}
}

// A revoke made in one session is applied to the other views of the same user by EnforceAccess.
func TestRevokeIsAppliedToOtherViewsOfTheUser(t *testing.T) {
	a, _, id, _ := lifecycleFixture(t)
	other := openView(t, a, id)
	if other.k8sMgr.GetActiveContext() != id {
		t.Fatal("the second view must be connected")
	}
	if EnforceAccess(other) {
		t.Fatal("nothing was revoked yet")
	}
	if err := a.revokePersonal(a.currentUserID(), id, "test"); err != nil {
		t.Fatal(err)
	}
	if !EnforceAccess(other) || other.k8sMgr.GetActiveContext() != "" {
		t.Fatal("the other view of the user must be closed")
	}
	if EnforceAccess(other) {
		t.Fatal("a revoke is applied once")
	}
}

// The other user is not affected by a revoke for someone else.
func TestRevokeOfAnotherUserDoesNotCloseThisView(t *testing.T) {
	a, _, id, _ := lifecycleFixture(t)
	if err := a.revokePersonal("someone-else", id, "test"); err != nil {
		t.Fatal(err)
	}
	if EnforceAccess(a) || a.k8sMgr.GetActiveContext() != id {
		t.Fatal("the view of another user must stay open")
	}
}

func TestPasswordChangeLogoutAndUserDeletionRevoke(t *testing.T) {
	a, b, _, _ := lifecycleFixture(t)
	uid := a.currentUserID()
	if err := a.ChangePassword(testPass, "another-pass-2"); err != nil {
		t.Fatal(err)
	}
	a.Logout()
	last := b.revoked
	if len(last) != 2 || last[0].OwnerID != uid || last[1].OwnerID != uid || last[0].ClusterID != "" {
		t.Fatalf("revokes: %+v", last)
	}
	for _, r := range last {
		if r.Scope != credential.ScopeUser {
			t.Fatalf("only personal sign-ins are revoked: %+v", r)
		}
	}
}

func TestResetPasswordAndUserDeletionRevokeTheirSignIns(t *testing.T) {
	a, b, _, _ := lifecycleFixture(t)
	bob, err := a.auth.CreateUser("bob", testPass, auth.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ResetUserPassword(bob.ID, "fresh-password-3"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteUser(bob.ID); err != nil {
		t.Fatal(err)
	}
	if len(b.revoked) != 2 || b.revoked[0].OwnerID != bob.ID || b.revoked[1].OwnerID != bob.ID {
		t.Fatalf("revokes: %+v", b.revoked)
	}
}

// scriptLimiter allows the first n attempts and then refuses with a fixed wait.
type scriptLimiter struct {
	left int
	keys []string
}

func (l *scriptLimiter) Allow(key string) (bool, time.Duration) {
	l.keys = append(l.keys, key)
	if l.left > 0 {
		l.left--
		return true, 0
	}
	return false, 1500 * time.Millisecond
}

// Answers are limited per user and per identity provider; a refused answer never reaches the broker, and the
// challenge stays open for a later attempt.
func TestAnswerChallengeIsRateLimited(t *testing.T) {
	b := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{"issuer-url": "https://IdP.example/realms/x"}},
		acquire: []credential.Result{challenge("c1", credential.ChallengeCredentials)}}
	a, _, id, connected := connectFixture(t, b)
	user, idp := &scriptLimiter{left: 1}, &scriptLimiter{left: 100}
	SetAnswerLimiters(a, user, idp)
	if _, err := a.ConnectCluster(id); err != nil {
		t.Fatal(err)
	}
	b.answerEr = credential.ErrAnswerRejected
	if _, err := a.AnswerChallenge("c1", map[string]string{"password": "p1"}); !errors.Is(err, credential.ErrAnswerRejected) {
		t.Fatalf("the first attempt reaches the broker: %v", err)
	}
	n := len(b.answers)
	_, err := a.AnswerChallenge("c1", map[string]string{"password": "p2"})
	var rl *RateLimitedError
	if !errors.As(err, &rl) || rl.RetryAfterSeconds() != 2 {
		t.Fatalf("expected a rate-limit error with a 2 s wait, got %v", err)
	}
	if len(b.answers) != n {
		t.Fatal("a limited answer must not reach the broker")
	}
	if !strings.Contains(strings.Join(idp.keys, ","), "answer:idp:idp.example") {
		t.Fatalf("the IdP key is the lower-cased host of the issuer: %v", idp.keys)
	}
	user.left, b.answerEr = 1, nil // the allowance is back: the same challenge can still be answered
	if res, err := a.AnswerChallenge("c1", map[string]string{"password": "p3"}); err != nil || res.Status != "connected" || len(*connected) != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestIdPLimitAppliesAcrossUsers(t *testing.T) {
	b := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{"issuer-url": "https://idp.example"}},
		acquire: []credential.Result{challenge("c1", credential.ChallengeCredentials)}}
	a, _, id, _ := connectFixture(t, b)
	SetAnswerLimiters(a, &scriptLimiter{left: 100}, &scriptLimiter{left: 0})
	if _, err := a.ConnectCluster(id); err != nil {
		t.Fatal(err)
	}
	var rl *RateLimitedError
	if _, err := a.AnswerChallenge("c1", nil); !errors.As(err, &rl) {
		t.Fatalf("an exhausted IdP allowance limits every user: %v", err)
	}
	if len(b.answers) != 0 {
		t.Fatal("the broker must not be reached")
	}
}

// capturedLogs sends the default slog logger to a buffer for the duration of the test.
func capturedLogs(t *testing.T) *strings.Builder {
	t.Helper()
	var buf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// The control strings of a password, a token and a refresh token never appear in what this layer returns or
// writes: the results sent to the client (API), events, the audit journal and the logs. (Metrics are checked in
// the server package, errors in the broker's own tests.)
func TestSignInSecretsNeverLeaveTheApp(t *testing.T) {
	const pw, tok, refresh, secret = "CANARY-PASSWORD-a1", "CANARY-TOKEN-b2", "CANARY-REFRESH-c3", "CANARY-CLIENTSECRET-d4"
	logs := capturedLogs(t)
	b := &fakeBroker{
		desc: credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{
			"issuer-url": "https://idp.example", "client-secret": secret}},
		acquire: []credential.Result{challenge("c1", credential.ChallengeCredentials)},
		answer: []credential.Result{{Token: &credential.Token{Value: tok, Principal: "alice@idp"},
			Save: []byte(refresh)}},
	}
	a, pl, id, _ := connectFixture(t, b)

	var api []string // everything returned to the client
	res, err := a.ConnectCluster(id)
	if err != nil {
		t.Fatal(err)
	}
	j, _ := json.Marshal(res)
	api = append(api, string(j))
	b.answerEr = credential.ErrAnswerRejected
	_, err = a.AnswerChallenge("c1", map[string]string{"username": "u", "password": pw})
	api = append(api, fmt.Sprint(err))
	b.answerEr = nil
	res, err = a.AnswerChallenge("c1", map[string]string{"username": "u", "password": pw})
	if err != nil {
		t.Fatal(err)
	}
	j, _ = json.Marshal(res)
	api = append(api, string(j))
	list, _ := a.ListMyConnections()
	j, _ = json.Marshal(list)
	api = append(api, string(j))
	if err := a.DisconnectCluster(id); err != nil {
		t.Fatal(err)
	}

	var journal string
	for _, e := range events(t, a) {
		journal += fmt.Sprintf("%+v\n", e)
	}
	pl.mu.Lock()
	emitted := fmt.Sprintf("%v %+v", pl.events, pl.data)
	pl.mu.Unlock()

	for place, text := range map[string]string{
		"API": strings.Join(api, "\n"), "events": emitted, "audit journal": journal, "logs": logs.String(),
	} {
		for _, s := range []string{pw, tok, refresh, secret} {
			if strings.Contains(text, s) {
				t.Errorf("%s contains the control string %q", place, s)
			}
		}
	}
}

// The shared account of a team is entered by a cluster administrator only: another user is refused instead of
// being offered a form, and the pending request is cancelled.
func TestTeamChallengeIsOnlyForClusterAdministrators(t *testing.T) {
	b := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindStatic},
		acquire: []credential.Result{challenge("t1", credential.ChallengeCredentials), challenge("t2", credential.ChallengeCredentials)}}
	a, _, id, _ := connectFixture(t, b)
	// the administrator gets the challenge and may answer it
	res, err := a.ConnectCluster(id)
	if err != nil || res.Status != "challenge" {
		t.Fatalf("admin: res=%+v err=%v", res, err)
	}
	if _, err := a.AnswerChallenge("t1", map[string]string{"username": "svc", "password": "p"}); err != nil {
		t.Fatalf("admin answer: %v", err)
	}
	// an ordinary user does not
	if _, err := a.auth.CreateUser("bob", testPass, auth.RoleUser); err != nil {
		t.Fatal(err)
	}
	a.auth.Logout()
	if _, err := a.auth.Login("bob", testPass); err != nil {
		t.Fatal(err)
	}
	// bob входит по временному паролю: пока он не сменён, доступ закрыт с ErrPasswordChangeRequired
	if err := a.auth.ChangePassword(testPass, "another-pass-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConnectCluster(id); !errors.Is(err, credential.ErrChallengeRequired) {
		t.Fatalf("bob must be refused, got %v", err)
	}
	if len(b.cancels) != 1 || b.cancels[0] != "t2" {
		t.Fatalf("the pending request must be cancelled: %v", b.cancels)
	}
}
