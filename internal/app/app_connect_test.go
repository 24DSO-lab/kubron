// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/k8s"
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
	a.conn = newConnectState()
	a.broker = b
	var connected []string
	a.finish = func(id string) error { connected = append(connected, id); return nil }
	return a, pl, c.ID, &connected
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
	other.conn = newConnectState()
	n := len(b.answers)
	if _, err := other.AnswerChallenge("c1", map[string]string{"password": "p"}); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("got %v", err)
	}
	if len(b.answers) != n || len(*connected) != 0 {
		t.Fatal("a foreign session reached the broker or connected the cluster")
	}
}

// The old client gets a typed error, the pending request is cancelled, nothing is connected.
func TestLegacyConnectToClusterReturnsTypedErrorOnChallenge(t *testing.T) {
	b := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindOIDC},
		acquire: []credential.Result{challenge("c1", credential.ChallengeDeviceCode)}}
	a, _, id, connected := connectFixture(t, b)
	err := a.ConnectToCluster(id)
	var ce *ChallengeRequiredError
	if !errors.As(err, &ce) || !errors.Is(err, credential.ErrChallengeRequired) || ce.ClusterID != id {
		t.Fatalf("got %v", err)
	}
	if len(*connected) != 0 || len(b.cancels) != 1 {
		t.Fatalf("connected=%v cancels=%v", *connected, b.cancels)
	}
}

func TestLegacyConnectToClusterConnectsWhenNoChallenge(t *testing.T) {
	b := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindStatic}}
	a, _, id, connected := connectFixture(t, b)
	if err := a.ConnectToCluster(id); err != nil || len(*connected) != 1 {
		t.Fatalf("err=%v connected=%v", err, *connected)
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
