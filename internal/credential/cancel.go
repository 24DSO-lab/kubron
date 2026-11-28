// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"sync"
	"time"
)

// Canceller is an optional interface of a Provider. A provider that keeps something outside of Pending (the
// loopback listener of the desktop) implements it; the Broker calls Cancel with the last Pending of a request when
// the request is cancelled, expires, is swept, completes or is revoked. Cancel must be idempotent and quick.
type Canceller interface {
	Cancel(pending []byte)
}

// Closer is an optional interface of a Provider: Close releases everything the provider holds, and the Broker calls
// it when it stops (Service.Close).
type Closer interface {
	Close()
}

// liveSet remembers, in the memory of this process, the pending requests of providers that implement Canceller. It
// exists because what a Canceller holds (a listening socket) lives in this process only: a replica that does not
// know the request cannot release it, and one that does know it is this one.
type liveSet struct {
	mu sync.Mutex
	m  map[string]liveRequest
}

type liveRequest struct {
	id           string
	p            Canceller
	pending      []byte
	key          Key
	expires      time.Time
	provisioning bool // technical provisioning requests must be audited before provider cancellation
	prov         *provState
}

func (l *liveSet) track(id string, r liveRequest) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]liveRequest{}
	}
	r.pending = append([]byte(nil), r.pending...)
	if old, ok := l.m[id]; ok {
		clear(old.pending)
	}
	l.m[id] = r
}

// take removes and returns the request.
func (l *liveSet) take(id string) (liveRequest, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.m[id]
	delete(l.m, id)
	return r, ok
}

// takeIf removes and returns the requests for which match is true.
func (l *liveSet) takeIf(match func(liveRequest) bool) []liveRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []liveRequest
	for id, r := range l.m {
		if match(r) {
			out = append(out, r)
			delete(l.m, id)
		}
	}
	return out
}

// listIf snapshots matching requests without removing them. Provisioning cleanup uses this so a failed audit
// leaves the local provider resource registered and available for a later retry.
func (l *liveSet) listIf(match func(liveRequest) bool) []liveRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []liveRequest
	for _, r := range l.m {
		if match(r) {
			out = append(out, r)
		}
	}
	return out
}

func (r liveRequest) cancel() {
	r.p.Cancel(r.pending)
	clear(r.pending)
}

// track remembers a pending request whose provider can be cancelled.
func (s *Service) track(id string, sub Subject, d Descriptor, pending []byte, exp time.Time, provisioning ...*provState) {
	p, err := s.reg.Resolve(d)
	if err != nil {
		return
	}
	if c, ok := p.(Canceller); ok {
		r := liveRequest{id: id, p: c, pending: pending, key: sub.key(), expires: exp, provisioning: sub.IsPrincipal()}
		if len(provisioning) > 0 {
			r.prov = provisioning[0]
		}
		s.live.track(id, r)
	}
}

// release tells the provider that a request is gone (cancelled, expired, completed, replaced).
func (s *Service) dropLive(id string) {
	if r, ok := s.live.take(id); ok {
		r.cancel()
	}
}

// Close implements io.Closer for the Service: it cancels every pending request this process holds and closes the
// providers that have something to release (the loopback listener of the desktop). The Service must not be used
// afterwards. Close is safe to call twice.
func (s *Service) Close() error {
	for _, r := range s.live.takeIf(func(liveRequest) bool { return true }) {
		r.cancel()
	}
	for _, p := range s.reg.all() {
		if c, ok := p.(Closer); ok {
			c.Close()
		}
	}
	return nil
}

	return out, nil
}

// abandonProvisioning destroys a provisioning challenge whose setup can no longer continue and records the failure.
// abandonProvisioning terminates a provisioning only after its failed outcome is durable.
func (s *Service) abandonProvisioning(ctx context.Context, id string, prov *provState, cause error) error {
	return s.finishProvisioningFailure(ctx, prov, reasonFor(cause), cause, id)
}

// continueProvisioning turns a provider result into the next step: another challenge, or the final storage of the
// credential. old is the claimed challenge being continued, if any.
func (s *Service) continueProvisioning(ctx context.Context, ps PrincipalStore, sub Subject, by ProvisioningContext, d Descriptor,
		c, err := s.storeProvisioningChallenge(ctx, sub, by, d, prov, res, old)
		if err != nil {
			ids := []string{}
			if old != nil {
				ids = append(ids, old.ID)
			}
			if c.ID != "" {
				ids = append(ids, c.ID)
			}
			ferr := s.finishProvisioningFailure(ctx, prov, reasonFor(err), err, ids...)
			if !errors.Is(ferr, audit.ErrOutcomeUnknown) {
				s.cancelPending(d, res.Pending)
			if old != nil {
				out, readErr := s.crit.GetOutcome(context.WithoutCancel(ctx), sub.Principal.OrgID, by.EventID)
				if readErr == nil && (out.Result == audit.CriticalFailed || out.Result == audit.CriticalSucceeded) {
					if delErr := s.deleteProvisioningChallenges(ctx, old.ID); delErr != nil {
						return Result{}, errors.Join(err, delErr)
					}
				}
			}
			return Result{}, err
		}
		if old != nil {
			if err := s.deleteProvisioningChallenges(ctx, old.ID); err != nil {
				return Result{}, err
			}
		}
		return Result{}, nil
	}
	cause := ErrUnsupported
	var ids []string
	if old != nil {
		ids = append(ids, old.ID)
	}
	return Result{}, s.finishProvisioningFailure(ctx, prov, "nothing_to_store", cause, ids...)
}

// deleteProvisioningChallenges is called only after a terminal outcome has been confirmed.
func (s *Service) deleteProvisioningChallenges(ctx context.Context, ids ...string) error {
	for _, id := range ids {
		if id == "" {
			continue
		}
		if err := s.chal.Delete(ctx, id); err != nil && !errors.Is(err, ErrChallengeNotFound) {
			return err
		}
		s.dropLive(id)
	}
	return nil
}

// failedProvisioning mirrors the ordinary failed() policy but keeps the provisioning audit invariant:
// terminal errors are recorded before deleting the challenge; retryable errors merely release the claim.
	}
	now := s.now()
	exp := now.Add(ChallengeTTL)
	if c := res.Challenge; !c.ExpiresAt.IsZero() && c.ExpiresAt.Before(exp) {
		exp = c.ExpiresAt
	}
	if old != nil && old.ID != "" {
		prev, err := s.open(*old)
		if err == nil && prev.Challenge.Kind == res.Challenge.Kind && res.Challenge.Poll {
			pub := publicChallenge(*res.Challenge, old.ID, old.ExpiresAt)
			blob, keyID, err := s.seal(old.ID, pendingState{Descriptor: d, Pending: res.Pending, Challenge: pub, Prov: prov})
			if err != nil {
				return Challenge{}, err
			}
			next := *old
			next.Status, next.LockedUntil, next.State, next.KeyID = ChallengeActive, time.Time{}, blob, keyID
			if _, err := s.chal.Swap(ctx, next, old.Version); err != nil {
				return Challenge{}, mapChallengeErr(err)
			}
			s.track(old.ID, sub, d, res.Pending, old.ExpiresAt, prov)
			return pub, nil
		}
	}
	id, err := newProvisioningChallengeID()
	if err != nil {
		return Challenge{}, err
	}
	pub := publicChallenge(*res.Challenge, id, exp)
	blob, keyID, err := s.seal(id, pendingState{Descriptor: d, Pending: res.Pending, Challenge: pub, Prov: prov})
	if err != nil {
		return Challenge{}, err
	}
	err = s.chal.Create(ctx, ChallengeRecord{ID: id, ClusterID: sub.ClusterID, Scope: ScopeUser, OwnerID: by.Actor.ID,
		SessionID: by.SessionID, Status: ChallengeActive, ExpiresAt: exp, CreatedAt: now, KeyID: keyID, State: blob})
	if err != nil {
		return Challenge{}, err
	}
	s.track(id, sub, d, res.Pending, exp, prov)
	// Never remove the old challenge before the replacement is durable. If cleanup fails, return the new ID too:
	// the caller will persist CriticalFailed first, then retry cleanup of both records.
	return pub, nil
}

// storeProvisioned stores the finished credential under the technical subject and completes the audit. Order: the
// binding is checked, the record is written conditionally on the version read at the start, the binding is checked
// again (a revocation in between removes the record), the outcome is recorded, and only then a pending binding is
// cancellation; anything else is ErrChallengeExpired. Cancelling is always allowed to the actor who started the setup
// (it cannot widen anything), even after the actor lost the authority to continue.
func (s *Service) CancelProvisioning(ctx context.Context, sub Subject, by ProvisioningContext, challengeID string) error {
	if _, err := s.provisioningStore(); err != nil {
		return err
	}
	if !sub.IsPrincipal() || sub.Validate() != nil || by.validate() != nil {
		return ErrChallengeExpired
	}
	if !isProvisioningID(challengeID) {
		return ErrChallengeExpired
	}
	rec, err := s.chal.Get(ctx, challengeID)
	if errors.Is(err, ErrChallengeNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, st, err := s.loadProvisioning(ctx, sub, by, rec.ID)
	if err != nil {
		return err
	}
	if err := s.provisioningOutcomeBlocksContinuation(ctx, challengeID, st.Prov); err != nil {
		return err
	}
	// Claim before auditing so a concurrent Answer/Poll on another replica cannot race cancellation.
	if rec.Status == ChallengeBusy && s.now().Before(rec.LockedUntil) {
		return ErrChallengeExpired
	}
	claim := rec
	claim.Status, claim.LockedUntil = ChallengeBusy, s.now().Add(claimTimeout)
	claimed, err := s.chal.Swap(ctx, claim, rec.Version)
	if err != nil {
		return mapChallengeErr(err)
	}
	err = s.finishProvisioningFailure(ctx, st.Prov, "cancelled", nil, challengeID)
	if errors.Is(err, audit.ErrOutcomeUnknown) {
		s.release(ctx, claimed)
	}
	return err
}

// ---------------------------------------------------------------------------------------------------------------
// revoke
	}
}

// deleteFailChallengeStore injects a one-shot failure into challenge deletion while retaining the shared store
// and attempt limiter, as happens with a transient PostgreSQL failure.
type deleteFailChallengeStore struct {

func (tr *techRig) serviceWithProvider(store credential.CredentialStore, challenges credential.ChallengeStore, provider credential.Provider) *credential.Service {
	reg := credential.NewRegistry(false)
	if err := reg.Register(provider); err != nil {
		tr.t.Fatal(err)
	}
	svc, err := credential.NewService(credential.Config{Registry: reg, Store: store, Challenges: challenges, Envelope: tr.env,
		Now: tr.clk.Now, Logger: slog.New(slog.NewTextHandler(tr.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Identities: tr.ids, Audit: tr.rec, Provisioning: tr.authz})
	if err != nil {
		tr.t.Fatal(err)
	}
	return svc
}

	sub, _ := tr.principal(identity.PrincipalTeam, "cancel-audit-failure")
	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("BeginProvisioning = %+v, %v", res, err)
	}

	tr.rec.failOutcome.Store(true)
	err = tr.svc.CancelProvisioning(bg, sub, by, res.Challenge.ID)
	if !errors.Is(err, audit.ErrOutcomeUnknown) {
		t.Fatalf("CancelProvisioning = %v, want ErrOutcomeUnknown", err)
	}
	if _, err := tr.chal.Get(bg, res.Challenge.ID); err != nil {
		t.Fatalf("challenge must remain recoverable: %v", err)
	}

	tr.rec.failOutcome.Store(false)
	if err := tr.svc.CancelProvisioning(bg, sub, by, res.Challenge.ID); err != nil {
	store := &deleteFailChallengeStore{ChallengeStore: tr.chal, AttemptLimiter: tr.chal}
	svc := tr.serviceWithChallenges(tr.store, store)
	res, err := svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("BeginProvisioning = %+v, %v", res, err)
	}

	store.fail.Store(true)
	if err := svc.CancelProvisioning(bg, sub, by, res.Challenge.ID); err == nil {
		t.Fatal("expected the injected delete failure")
	}
	out, err := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if err != nil || out.Result != audit.CriticalFailed {
		t.Fatalf("outcome must precede deletion: %+v %v", out, err)
	}
	if _, err := tr.chal.Get(bg, res.Challenge.ID); err != nil {
		t.Fatalf("challenge must remain after failed delete: %v", err)
	}

	// The retry sees the immutable CriticalFailed, does not append a conflicting outcome, and completes cleanup.
	if err := svc.CancelProvisioning(bg, sub, by, res.Challenge.ID); !errors.Is(err, credential.ErrChallengeExpired) {
	sub, _ := tr.principal(identity.PrincipalTeam, "advance-expiry")
	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("BeginProvisioning = %+v, %v", res, err)
	}
	tr.clk.Advance(credential.ChallengeTTL + time.Second)
	tr.rec.failOutcome.Store(true)
	_, err = tr.svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", pwCanary))
	if !errors.Is(err, audit.ErrOutcomeUnknown) {
		t.Fatalf("expiry = %v, want ErrOutcomeUnknown", err)
	}
	if _, err := tr.chal.Get(bg, res.Challenge.ID); err != nil {
		t.Fatalf("expired challenge must remain after audit failure: %v", err)
	}
	tr.rec.failOutcome.Store(false)
	_, err = tr.svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", pwCanary))
	if !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("retry expiry = %v", err)
	}
	if _, err := tr.chal.Get(bg, res.Challenge.ID); !errors.Is(err, credential.ErrChallengeNotFound) {
		t.Fatalf("challenge remains: %v", err)
	}
}

func TestProvisioningAttemptExhaustionIsAuditedBeforeDelete(t *testing.T) {
	sub, _ := tr.principal(identity.PrincipalTeam, "attempt-exhaustion")
	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("BeginProvisioning = %+v, %v", res, err)
	}
	tr.prov.OnContinue = func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
		return credential.Result{}, credential.ErrAnswerRejected
	}
	for i := 0; i < credential.MaxAnswerAttempts; i++ {
		_, err = tr.svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", "wrong"))
	}
	if !errors.Is(err, credential.ErrAnswerRejected) {
		t.Fatalf("last answer = %v", err)
	}
	out, err := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if err != nil || out.Result != audit.CriticalFailed || out.ReasonCode != "rejected" {
		t.Fatalf("outcome = %+v, %v", out, err)
	store := &deleteFailChallengeStore{ChallengeStore: tr.chal, AttemptLimiter: tr.chal}
	svc := tr.serviceWithChallenges(tr.store, store)
	res, err := svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("BeginProvisioning = %+v, %v", res, err)
	}
	tr.prov.OnContinue = func(_ context.Context, _ credential.Env, req credential.Request) (credential.Result, error) {
		return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeDeviceCode, Poll: true}, Pending: []byte("next")}, nil
	}
	store.fail.Store(true)
	_, err = svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", pwCanary))
	if err == nil {
		t.Fatal("expected old challenge deletion failure")
	}
	out, auditErr := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if auditErr != nil || out.Result != audit.CriticalFailed {
		t.Fatalf("transition failure must be audited before cleanup: %+v %v", out, auditErr)
	sub, _ := tr.principal(identity.PrincipalTeam, "concurrent-cancel")
	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("BeginProvisioning = %+v, %v", res, err)
	}
	replica := tr.service(tr.store)

	var wg sync.WaitGroup
	wg.Wait()
	success, refused := 0, 0
	for _, err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, credential.ErrChallengeExpired) {
			refused++
		} else {
			t.Errorf("unexpected concurrent result: %v", err)
		}
	}
	if success != 1 || refused != 1 {
		t.Fatalf("concurrent results = %v; want one success and one CAS refusal", errs)
	}
	out, err := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if err != nil || out.Result != audit.CriticalFailed {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
}

func TestAbandonProvisioningAuditsBeforeDelete(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "abandon-order")
	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("BeginProvisioning = %+v, %v", res, err)
	}
	tr.authz.deny.Store(true)
	tr.rec.failOutcome.Store(true)
	_, err = tr.svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", pwCanary))
	if !errors.Is(err, audit.ErrOutcomeUnknown) {
		t.Fatalf("authority loss = %v, want ErrOutcomeUnknown", err)
	}
	if _, err := tr.chal.Get(bg, res.Challenge.ID); err != nil {
		t.Fatalf("challenge must remain: %v", err)
	}
	tr.rec.failOutcome.Store(false)
	_, err = tr.svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", pwCanary))
	if !errors.Is(err, credential.ErrProvisioningDenied) {
		t.Fatalf("retry after authority loss = %v", err)
	}
	out, err := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if err != nil || out.Result != audit.CriticalFailed || out.ReasonCode != "authority_lost" {
		t.Fatalf("outcome = %+v, %v", out, err)
	sub, _ := tr.principal(identity.PrincipalTeam, "concurrent-local")
	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("BeginProvisioning = %+v, %v", res, err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
	wg.Wait()
	success, refused := 0, 0
	for _, err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, credential.ErrChallengeExpired) {
			refused++
		} else {
			t.Errorf("unexpected result: %v", err)
		}
	}
	if success != 1 || refused != 1 {
		t.Fatalf("results = %v; want one success and one CAS refusal", errs)
	}
	out, err := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if err != nil || out.Result != audit.CriticalFailed {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
}

func TestProvisioningSuccessOutcomeSurvivesChallengeDeleteFailure(t *testing.T) {
	tr := newTechRig(t)
	store := &deleteFailChallengeStore{ChallengeStore: tr.chal, AttemptLimiter: tr.chal}
	svc := tr.serviceWithChallenges(tr.store, store)
	res, err := svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("BeginProvisioning = %+v, %v", res, err)
	}

	store.fail.Store(true)
	_, err = svc.AnswerProvisioning(bg, sub, by, res.Challenge.ID, answer("alice", pwCanary))
	if err == nil {
		t.Fatal("expected challenge deletion failure after successful outcome")
	}
	out, err := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if err != nil || out.Result != audit.CriticalSucceeded {
		t.Fatalf("successful outcome must be durable before challenge deletion: %+v %v", out, err)

	// The retry recognizes CriticalSucceeded and performs cleanup without re-running the provider.
	_, err = svc.PollProvisioning(bg, sub, by, res.Challenge.ID)
	if !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("cleanup retry = %v", err)
	}
	if _, err := tr.chal.Get(bg, res.Challenge.ID); !errors.Is(err, credential.ErrChallengeNotFound) {
		t.Fatalf("challenge still exists: %v", err)
	}
	}
}

func TestConcurrentProvisioningAnswerAcrossServiceInstances(t *testing.T) {
	tr := newTechRig(t)
	sub, _ := tr.principal(identity.PrincipalTeam, "concurrent-answer")
	by := tr.by("sess-1")
	res, err := tr.svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("BeginProvisioning = %+v, %v", res, err)
	}
	replica := tr.service(tr.store)
	started := make(chan struct{})
	release := make(chan struct{})
		close(started)
		<-release
		grant, err := tr.idp.Login("alice", pwCanary)
		if err != nil {
			return credential.Result{}, err
		}
		return credential.Result{Token: &credential.Token{Value: grant.Access, Expiry: grant.Expiry, Principal: grant.User},
			Save: []byte(grant.Refresh)}, nil
	}
		t.Fatalf("concurrent replica must lose the claim: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("winning answer: %v", err)
	}
	_, continues := tr.prov.Calls()
	if continues != 1 {
		t.Fatalf("provider Continue called %d times, want exactly once", continues)
	}
	out, err := tr.rec.GetOutcome(bg, "o1", by.EventID)
	if err != nil || out.Result != audit.CriticalSucceeded {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
	if _, err := tr.chal.Get(bg, res.Challenge.ID); !errors.Is(err, credential.ErrChallengeNotFound) {
		t.Fatalf("completed challenge remains: %v", err)
	}
}

type auditOrderCancellerProvider struct {
	credential.Provider
	rec                         audit.CriticalRecorder
	chal                        credential.ChallengeStore
	orgID, eventID, challengeID string
	canceled                    atomic.Bool
	ordered                     atomic.Bool
}

func (p *auditOrderCancellerProvider) Cancel([]byte) {
	provider := &auditOrderCancellerProvider{Provider: tr.prov, rec: tr.rec, chal: tr.chal, orgID: "o1", eventID: by.EventID}
	svc := tr.serviceWithProvider(tr.store, tr.chal, provider)
	res, err := svc.BeginProvisioning(bg, sub, by, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("BeginProvisioning = %+v, %v", res, err)
	}
	provider.challengeID = res.Challenge.ID
	tr.clk.Advance(credential.ChallengeTTL + time.Second)
	if n, err := svc.Sweep(bg); err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v", n, err)
	}
	if !provider.canceled.Load() || !provider.ordered.Load() {
		t.Fatalf("provider Cancel must observe durable CriticalFailed and deleted challenge (canceled=%v ordered=%v)",
			provider.canceled.Load(), provider.ordered.Load())
	svc := tr.serviceWithProvider(tr.store, tr.chal, provider)
	setupBy := tr.by("sess-1")
	res, err := svc.BeginProvisioning(bg, sub, setupBy, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("BeginProvisioning = %+v, %v", res, err)
	}
	provider.eventID, provider.challengeID = setupBy.EventID, res.Challenge.ID

	if err := svc.RevokePrincipal(bg, sub, by); err != nil {
		t.Fatalf("RevokePrincipal: %v", err)
	}
	if !provider.canceled.Load() || !provider.ordered.Load() {
		t.Fatalf("provider Cancel must follow the provisioning outcome and challenge deletion (canceled=%v ordered=%v)",
			provider.canceled.Load(), provider.ordered.Load())
	}
	out, err := tr.rec.GetOutcome(bg, "o1", setupBy.EventID)
	if err != nil || out.Result != audit.CriticalFailed {
		t.Fatalf("provisioning outcome = %+v, %v", out, err)
	}
}