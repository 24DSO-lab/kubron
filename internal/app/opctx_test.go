// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

func TestOpCtxIsBoundedAndEndsWithTheView(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	a := &App{base: base, cancel: cancel}

	ctx, done := a.opCtx(time.Minute)
	defer done()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("an operation context must have a deadline")
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("the context ended before the view: %v", err)
	}
	cancel()
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("releasing the view must cancel its operations, got %v", err)
	}
}

func TestOpCtxExpires(t *testing.T) {
	ctx, done := (&App{}).opCtx(10 * time.Millisecond)
	defer done()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the operation context did not expire")
	}
	if err := ctx.Err(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestDetachedCtxSurvivesTheViewButIsBounded(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	a := &App{base: base, cancel: cancel}

	ctx, done := a.detachedCtx(time.Minute)
	defer done()
	cancel()
	if err := ctx.Err(); err != nil {
		t.Fatalf("a detached context must outlive the view, got %v", err)
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("a detached context must still be bounded in time")
	}
}

func TestViewWithoutBaseHasNoCancellation(t *testing.T) {
	ctx, done := (&App{}).opCtx(time.Minute)
	defer done()
	if err := ctx.Err(); err != nil {
		t.Fatalf("got %v", err)
	}
}

func TestReleaseViewCancelsItsOperationsOnly(t *testing.T) {
	root := NewApp(nil)
	view := NewSessionView(root, "203.0.113.7")
	rootCtx, rootDone := root.opCtx(time.Minute)
	defer rootDone()
	viewCtx, viewDone := view.opCtx(time.Minute)
	defer viewDone()

	ReleaseView(view)

	if err := viewCtx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("an operation of the released view must be cancelled, got %v", err)
	}
	if err := rootCtx.Err(); err != nil {
		t.Fatalf("releasing a view must not touch the root: %v", err)
	}
}

func TestDeadlineIsAnOutageForTheAudit(t *testing.T) {
	if got := authFailureReason(context.DeadlineExceeded); got != "unavailable" {
		t.Fatalf("authFailureReason(deadline) = %q", got)
	}
}

// ctxCall is what the broker saw of the context of one call.
type ctxCall struct {
	err         error // ctx.Err() at the moment of the call
	hasDeadline bool
}

// ctxBroker records the context of every call and delegates to the scripted fakeBroker.
type ctxBroker struct {
	*fakeBroker
	ctxMu sync.Mutex
	calls map[string]ctxCall
}

func newCtxBroker(f *fakeBroker) *ctxBroker {
	return &ctxBroker{fakeBroker: f, calls: map[string]ctxCall{}}
}

func (b *ctxBroker) note(op string, ctx context.Context) {
	_, ok := ctx.Deadline()
	b.ctxMu.Lock()
	b.calls[op] = ctxCall{err: ctx.Err(), hasDeadline: ok}
	b.ctxMu.Unlock()
}

func (b *ctxBroker) call(op string) (ctxCall, bool) {
	b.ctxMu.Lock()
	defer b.ctxMu.Unlock()
	c, ok := b.calls[op]
	return c, ok
}

func (b *ctxBroker) Acquire(ctx context.Context, s credential.Subject, d credential.Descriptor) (credential.Result, error) {
	b.note("acquire", ctx)
	return b.fakeBroker.Acquire(ctx, s, d)
}

func (b *ctxBroker) Answer(ctx context.Context, s credential.Subject, id string, a credential.Answer) (credential.Result, error) {
	b.note("answer", ctx)
	return b.fakeBroker.Answer(ctx, s, id, a)
}

func (b *ctxBroker) Poll(ctx context.Context, s credential.Subject, id string) (credential.Result, error) {
	b.note("poll", ctx)
	return b.fakeBroker.Poll(ctx, s, id)
}

func (b *ctxBroker) Cancel(ctx context.Context, s credential.Subject, id string) error {
	b.note("cancel", ctx)
	return b.fakeBroker.Cancel(ctx, s, id)
}

func (b *ctxBroker) Revoke(ctx context.Context, r credential.RevokeFilter) error {
	b.note("revoke", ctx)
	return b.fakeBroker.Revoke(ctx, r)
}

func (b *ctxBroker) List(ctx context.Context, owner string) ([]credential.ConnectionInfo, error) {
	b.note("list", ctx)
	return b.fakeBroker.List(ctx, owner)
}

func TestSignInCallsAreBounded(t *testing.T) {
	fb := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindOIDC},
		acquire: []credential.Result{challenge("c1", credential.ChallengeCredentials)},
		poll:    []credential.Result{challenge("c1", credential.ChallengeCredentials)}}
	a, _, id, _ := connectFixture(t, fb)
	cb := newCtxBroker(fb)
	a.broker = cb

	if _, err := a.ConnectCluster(id); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PollChallenge("c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AnswerChallenge("c1", map[string]string{"password": "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ListMyConnections(); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"acquire", "poll", "answer", "list"} {
		c, ok := cb.call(op)
		if !ok {
			t.Fatalf("%s: the broker was not called", op)
		}
		if !c.hasDeadline || c.err != nil {
			t.Errorf("%s: want a live context with a deadline, got %+v", op, c)
		}
	}
}

// blockingBroker holds Answer until its context ends, like a slow identity provider.
type blockingBroker struct {
	*fakeBroker
	entered chan struct{}
}

func (b *blockingBroker) Answer(ctx context.Context, _ credential.Subject, _ string, _ credential.Answer) (credential.Result, error) {
	close(b.entered)
	<-ctx.Done()
	return credential.Result{}, ctx.Err()
}

func TestReleasingTheViewCancelsAnAnswerInFlight(t *testing.T) {
	fb := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindOIDC},
		acquire: []credential.Result{challenge("c1", credential.ChallengeCredentials)}}
	a, _, id, _ := connectFixture(t, fb)
	bb := &blockingBroker{fakeBroker: fb, entered: make(chan struct{})}
	a.broker = bb
	a.base, a.cancel = context.WithCancel(context.Background())
	if _, err := a.ConnectCluster(id); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := a.AnswerChallenge("c1", map[string]string{"password": "x"})
		result <- err
	}()
	select {
	case <-bb.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the broker was not called")
	}
	a.cancel() // the session ends while the identity provider is still thinking
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AnswerChallenge did not stop after the view was released")
	}
}

// A revoke and the withdrawal of a challenge are not left half-done because the view is released at that moment.
func TestRevokeAndCancelRunAfterTheViewIsReleased(t *testing.T) {
	fb := &fakeBroker{desc: credential.Descriptor{Kind: credential.KindOIDC},
		acquire: []credential.Result{challenge("c1", credential.ChallengeCredentials)}}
	a, _, id, _ := connectFixture(t, fb)
	cb := newCtxBroker(fb)
	a.broker = cb
	a.base, a.cancel = context.WithCancel(context.Background())
	if _, err := a.ConnectCluster(id); err != nil {
		t.Fatal(err)
	}

	a.cancel()

	if err := a.CancelChallenge("c1"); err != nil {
		t.Fatal(err)
	}
	if err := a.DisconnectCluster(id); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"cancel", "revoke"} {
		c, ok := cb.call(op)
		if !ok {
			t.Fatalf("%s: the broker was not called", op)
		}
		if !c.hasDeadline || c.err != nil {
			t.Errorf("%s: want a live context with a deadline after the view is released, got %+v", op, c)
		}
	}
}
