// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/24DSO-labs/kubron/internal/secrets"
)

// Limits of a pending sign-in request.
const (
	// ChallengeTTL is how long a request lives (ADR-0018).
	ChallengeTTL = 10 * time.Minute
	// MaxAnswerAttempts is the number of wrong answers after which a request is destroyed.
	MaxAnswerAttempts = 5
	// claimTimeout bounds how long a crashed worker can keep a request claimed.
	claimTimeout = 30 * time.Second
	// tokenMargin: a cached token that expires sooner than this is refreshed.
	tokenMargin = 30 * time.Second
)

// ErrNotConfigured means that NewService was given an incomplete configuration.
var ErrNotConfigured = errors.New("the sign-in service is not configured completely")

// Config configures a Service.
type Config struct {
	Registry   *Registry
	Store      CredentialStore
	Challenges ChallengeStore
	// Envelope encrypts the private state of pending requests (descriptor, provider state).
	Envelope *secrets.Envelope
	// Env is passed to providers; Env.Now defaults to Now.
	Env Env
	Now func() time.Time
	// RefreshRecheck are the pauses before re-reading the saved data when a refresh token was rejected: the
	// rejection may be caused by another replica that has just rotated it. Default 50 ms, 150 ms, 400 ms.
	RefreshRecheck []time.Duration
	// Logger receives ids, kinds and outcomes only, never secrets. Defaults to slog.Default().
	Logger *slog.Logger
}

// Service implements Broker: it orchestrates providers, pending requests and token refresh.
// A Service is safe for concurrent use; several Services (replicas) may share the same stores.
type Service struct {
	reg     *Registry
	store   CredentialStore
	chal    ChallengeStore
	envp    *secrets.Envelope
	penv    Env
	now     func() time.Time
	log     *slog.Logger
	recheck []time.Duration
	cache   tokenCache
	flight  flightGroup
	live    liveSet // pending requests of providers that implement Canceller (this process only)
}

// NewService validates the configuration and returns a Service.
func NewService(cfg Config) (*Service, error) {
	if cfg.Registry == nil || cfg.Store == nil || cfg.Challenges == nil || cfg.Envelope == nil {
		return nil, ErrNotConfigured
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	env := cfg.Env
	if env.Now == nil {
		env.Now = now
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	recheck := cfg.RefreshRecheck
	if recheck == nil {
		recheck = []time.Duration{50 * time.Millisecond, 150 * time.Millisecond, 400 * time.Millisecond}
	}
	return &Service{reg: cfg.Registry, store: cfg.Store, chal: cfg.Challenges, envp: cfg.Envelope, penv: env, now: now, log: log,
		recheck: recheck, cache: tokenCache{m: map[string]Token{}}}, nil
}

// Describe implements Broker.
func (s *Service) Describe(kubeconfig []byte, contextName string) (Descriptor, error) {
	return Describe(kubeconfig, contextName)
}

func (sub Subject) key() Key {
	return Key{ClusterID: sub.ClusterID, Scope: sub.Scope, OwnerID: sub.OwnerID}
}

// pendingState is the private, encrypted part of a pending request.
type pendingState struct {
	Descriptor Descriptor `json:"descriptor"`
	Pending    []byte     `json:"pending,omitempty"`
	Challenge  Challenge  `json:"challenge"`
}

func newChallengeID() (string, error) {
	var b [16]byte // 128 bits
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("random source unavailable: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func stateAAD(id string) []byte { return []byte("chal/" + id) }

func (s *Service) seal(id string, st pendingState) ([]byte, string, error) {
	raw, err := json.Marshal(st)
	if err != nil {
		return nil, "", err
	}
	defer clear(raw)
	return s.envp.Seal(raw, stateAAD(id))
}

func (s *Service) open(rec ChallengeRecord) (pendingState, error) {
	raw, err := s.envp.Open(rec.State, stateAAD(rec.ID))
	if err != nil {
		return pendingState{}, err
	}
	defer clear(raw)
	var st pendingState
	if err := json.Unmarshal(raw, &st); err != nil {
		return pendingState{}, err
	}
	return st, nil
}

// publicChallenge is the form of a challenge that leaves the Service: ID and expiry are set by the Service,
// and the environment values of an exec block are cleared (they may hold secrets).
func publicChallenge(c Challenge, id string, exp time.Time) Challenge {
	c.ID, c.ExpiresAt = id, exp
	if c.Exec != nil {
		e := *c.Exec
		if len(e.Env) > 0 {
			env := make(map[string]string, len(e.Env))
			for k := range e.Env {
				env[k] = ""
			}
			e.Env = env
		}
		c.Exec = &e
	}
	return c
}

func (s *Service) validSubject(sub Subject, d *Descriptor) error {
	if err := sub.key().Validate(); err != nil {
		return err
	}
	if d != nil && d.Kind == "" {
		return ErrUnsupported
	}
	return nil
}

// Acquire implements Broker: it tries to obtain a token without asking the user (saved data, refresh) and,
// if that is impossible, creates a pending request and returns its challenge. For KindStatic nothing is
// needed and the result is empty.
func (s *Service) Acquire(ctx context.Context, sub Subject, d Descriptor) (Result, error) {
	if err := s.validSubject(sub, &d); err != nil {
		return Result{}, err
	}
	if d.Kind == KindStatic {
		return Result{}, nil
	}
	rec, saved := s.loadSaved(ctx, sub.key())
	resolved, res, err := s.begin(ctx, sub, d, saved)
	if err != nil {
		return Result{}, scrub(err, resolved, nil, saved)
	}
	return s.complete(ctx, sub, resolved, rec, res, true, nil)
}

// begin tries the native provider first. If it explicitly declines a mode and the kubeconfig has an exec
// block, route the same descriptor through KindExec: manual-token on the web, or the confirmed desktop
// exec plugin. Store KindExec in the encrypted challenge descriptor so Answer/Poll continue with the same
// provider rather than resolving back to the native provider.
func (s *Service) begin(ctx context.Context, sub Subject, d Descriptor, saved []byte) (Descriptor, Result, error) {
	p, err := s.reg.Resolve(d)
	if err != nil {
		return d, Result{}, err
	}
rec, saved := s.loadSaved(ctx, sub.key())
	req := Request{Descriptor: d, Subject: sub, Saved: saved}

	res, err := p.Begin(ctx, s.penv, req)
	if err == nil || !errors.Is(err, ErrUnsupported) || p.Kind() == KindExec || d.Exec == nil {
		if err != nil {
			return Result{}, scrub(err, d, nil, saved)
		}
		return res, nil
	}

	fallback := d
	fallback.Kind = KindExec

	fp, resolveErr := s.reg.Resolve(fallback)
	if resolveErr != nil || fp.Kind() != KindExec {
		return Result{}, scrub(err, d, nil, saved)
	}

	fallbackReq := Request{
		Descriptor: fallback,
		Subject:    sub,
	}

	res, err = fp.Begin(ctx, s.penv, fallbackReq)
	if err != nil {
		return Result{}, scrub(err, fallback, nil, saved)
	}
	}
	return fallback, res, nil
}

// loadSaved reads the stored sign-in data of a subject. A missing or unreadable record means "nothing saved":
// the provider then starts a fresh sign-in, which is the safe outcome.
func (s *Service) loadSaved(ctx context.Context, k Key) (Record, []byte) {
	rec, err := s.store.Get(ctx, k)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.log.Warn("saved sign-in data could not be read", "cluster", k.ClusterID, "scope", k.Scope, "error", scrubText(err.Error()))
		}
		return Record{}, nil
	}
	return rec, rec.Secret
}

// complete turns a provider result into what the caller gets: persists Save, caches the token, or stores
// the new challenge. force makes a Save overwrite a record changed by someone else meanwhile (a fresh
// interactive sign-in is authoritative).
func (s *Service) complete(ctx context.Context, sub Subject, d Descriptor, base Record, res Result, force bool, old *ChallengeRecord) (Result, error) {
	if res.Save != nil {
		if err := s.persist(ctx, sub, d, base, res, force); err != nil {
			return Result{}, err
		}
	}
	switch {
	case res.Token != nil:
		s.cache.put(sub.key(), *res.Token)
		if old != nil {
			_ = s.chal.Delete(ctx, old.ID)
			s.dropLive(old.ID)
		}
		return Result{Token: res.Token}, nil
	case res.Challenge != nil:
		c, err := s.storeChallenge(ctx, sub, d, res, old)
		if err != nil {
			return Result{}, err
		}
		return Result{Challenge: &c}, nil
	}
	if old != nil {
		_ = s.chal.Delete(ctx, old.ID)
		s.dropLive(old.ID)
	}
	return Result{}, nil
}

// persist writes Result.Save: nil is "no change" (not called), an empty slice erases, anything else replaces.
func (s *Service) persist(ctx context.Context, sub Subject, d Descriptor, base Record, res Result, force bool) error {
	k := sub.key()
	if len(res.Save) == 0 {
		return s.store.Delete(ctx, k)
	}
	rec := Record{Meta: Meta{Key: k, Kind: d.Kind}, Secret: res.Save}
	if res.Token != nil {
		rec.Principal = res.Token.Principal
		if !res.Token.Expiry.IsZero() {
			exp := res.Token.Expiry
			rec.ExpiresAt = &exp
		}
	}
	expected := base.Version
	if force {
		expected = AnyVersion
	}
	_, err := s.store.Put(ctx, rec, expected)
	return err
}

// storeChallenge stores a challenge returned by a provider. A continuation of the same polling request keeps
// its ID; anything else gets a new ID, and the previous request (if any) is removed.
func (s *Service) storeChallenge(ctx context.Context, sub Subject, d Descriptor, res Result, old *ChallengeRecord) (Challenge, error) {
	now := s.now()
	exp := now.Add(ChallengeTTL)
	if c := res.Challenge; !c.ExpiresAt.IsZero() && c.ExpiresAt.Before(exp) {
		exp = c.ExpiresAt
	}
	if old != nil && old.ID != "" {
		prev, err := s.open(*old)
		if err == nil && prev.Challenge.Kind == res.Challenge.Kind && res.Challenge.Poll {
			// still waiting: same request, new private state
			pub := publicChallenge(*res.Challenge, old.ID, old.ExpiresAt)
			blob, keyID, err := s.seal(old.ID, pendingState{Descriptor: d, Pending: res.Pending, Challenge: pub})
			if err != nil {
				return Challenge{}, err
			}
			next := *old
			next.Status, next.LockedUntil, next.State, next.KeyID = ChallengeActive, time.Time{}, blob, keyID
			if _, err := s.chal.Swap(ctx, next, old.Version); err != nil {
				return Challenge{}, mapChallengeErr(err)
			}
			s.track(old.ID, sub, d, res.Pending, old.ExpiresAt)
			return pub, nil
		}
		_ = s.chal.Delete(ctx, old.ID)
		s.dropLive(old.ID)
	}
	id, err := newChallengeID()
	if err != nil {
		return Challenge{}, err
	}
	pub := publicChallenge(*res.Challenge, id, exp)
	blob, keyID, err := s.seal(id, pendingState{Descriptor: d, Pending: res.Pending, Challenge: pub})
	if err != nil {
		return Challenge{}, err
	}
	err = s.chal.Create(ctx, ChallengeRecord{ID: id, ClusterID: sub.ClusterID, Scope: sub.Scope, OwnerID: sub.OwnerID,
		SessionID: sub.SessionID, Status: ChallengeActive, ExpiresAt: exp, CreatedAt: now, KeyID: keyID, State: blob})
	if err != nil {
		return Challenge{}, err
	}
	s.track(id, sub, d, res.Pending, exp)
	s.log.Debug("sign-in request created", "cluster", sub.ClusterID, "kind", pub.Kind)
	return pub, nil
}

func mapChallengeErr(err error) error {
	if errors.Is(err, ErrChallengeNotFound) || errors.Is(err, ErrVersionConflict) {
		return ErrChallengeExpired
	}
	return err
}

// bound reports whether the caller owns the request. The cluster and the scope are checked only when the
// caller states them (the RPC carries just the request id).
func bound(rec ChallengeRecord, sub Subject) bool {
	if sub.OwnerID == "" || rec.OwnerID != sub.OwnerID || rec.SessionID != sub.SessionID {
		return false
	}
	return (sub.ClusterID == "" || sub.ClusterID == rec.ClusterID) && (sub.Scope == "" || sub.Scope == rec.Scope)
}

// load fetches a request for a caller; every failure to get it is ErrChallengeExpired, so that a caller
// cannot learn whether somebody else's request exists.
func (s *Service) load(ctx context.Context, sub Subject, id string) (ChallengeRecord, error) {
	rec, err := s.chal.Get(ctx, id)
	if errors.Is(err, ErrChallengeNotFound) {
		return ChallengeRecord{}, ErrChallengeExpired
	}
	if err != nil {
		return ChallengeRecord{}, err
	}
	if !bound(rec, sub) {
		return ChallengeRecord{}, ErrChallengeExpired
	}
	if !s.now().Before(rec.ExpiresAt) {
		_ = s.chal.Delete(ctx, id)
		s.dropLive(id)
		return ChallengeRecord{}, ErrChallengeExpired
	}
	return rec, nil
}

// Answer implements Broker: the user's answer to a challenge.
func (s *Service) Answer(ctx context.Context, sub Subject, challengeID string, a Answer) (Result, error) {
	return s.advance(ctx, sub, challengeID, a, true)
}

// Poll implements Broker: asks whether an out-of-band sign-in (device code, browser) has finished.
func (s *Service) Poll(ctx context.Context, sub Subject, challengeID string) (Result, error) {
	return s.advance(ctx, sub, challengeID, nil, false)
}

func (s *Service) advance(ctx context.Context, sub Subject, id string, a Answer, isAnswer bool) (Result, error) {
	rec, err := s.load(ctx, sub, id)
	if err != nil {
		return Result{}, err
	}
	now := s.now()
	st, err := s.open(rec)
	if err != nil {
		_ = s.chal.Delete(ctx, id) // unreadable state can never be completed
		s.dropLive(id)
		return Result{}, ErrChallengeExpired
	}

	// Nothing to poll for a challenge that waits for an answer: report it as it is (no provider call, no attempt).
	if !isAnswer && !st.Challenge.Poll {
		c := publicChallenge(st.Challenge, rec.ID, rec.ExpiresAt)
		return Result{Challenge: &c}, nil
	}

	// Claim the request: only one caller (on any replica) can move it from active to busy.
	if rec.Status == ChallengeBusy && now.Before(rec.LockedUntil) {
		return s.busyResult(rec, st, isAnswer)
	}
	claim := rec
	claim.Status, claim.LockedUntil = ChallengeBusy, now.Add(claimTimeout)
	claimed, err := s.chal.Swap(ctx, claim, rec.Version)
	if err != nil {
		if errors.Is(err, ErrVersionConflict) {
			return s.busyResult(rec, st, isAnswer)
		}
		return Result{}, mapChallengeErr(err)
	}

	d := st.Descriptor
	p, err := s.reg.Resolve(d)
	if err != nil {
		s.release(ctx, claimed)
		return Result{}, err
	}
	subject := Subject{ClusterID: rec.ClusterID, Scope: rec.Scope, OwnerID: rec.OwnerID, SessionID: rec.SessionID}
	base, saved := s.loadSaved(ctx, subject.key())
	req := Request{Descriptor: d, Subject: subject, Saved: saved, Pending: st.Pending,
		Challenge: publicChallenge(st.Challenge, rec.ID, rec.ExpiresAt), Answer: a}
	res, perr := p.Continue(ctx, s.penv, req)
	if perr != nil {
		return s.failed(ctx, claimed, d, a, saved, perr)
	}
	out, err := s.complete(ctx, subject, d, base, res, true, &claimed)
	if err != nil {
		s.release(ctx, claimed)
		return Result{}, err
	}
	if out.Token != nil {
		s.log.Debug("sign-in completed", "cluster", rec.ClusterID, "kind", d.Kind)
	}
	return out, nil
}

// busyResult answers a caller that lost the race for the claim: a poll sees "still waiting", an answer is
// rejected because the request is already being completed.
func (s *Service) busyResult(rec ChallengeRecord, st pendingState, isAnswer bool) (Result, error) {
	if isAnswer {
		return Result{}, ErrChallengeExpired
	}
	c := publicChallenge(st.Challenge, rec.ID, rec.ExpiresAt)
	return Result{Challenge: &c}, nil
}

// release returns a claimed request to the active state (the work failed for a reason that is not the
// user's fault).
func (s *Service) release(ctx context.Context, claimed ChallengeRecord) {
	claimed.Status, claimed.LockedUntil = ChallengeActive, time.Time{}
	_, _ = s.chal.Swap(ctx, claimed, claimed.Version)
}

// failed handles an error from Continue.
func (s *Service) failed(ctx context.Context, claimed ChallengeRecord, d Descriptor, a Answer, saved []byte, perr error) (Result, error) {
	switch {
	case errors.Is(perr, ErrAnswerRejected):
		claimed.Attempts++
		if claimed.Attempts >= MaxAnswerAttempts {
			_ = s.chal.Delete(ctx, claimed.ID)
			s.dropLive(claimed.ID)
			s.log.Info("sign-in request destroyed after too many wrong answers", "cluster", claimed.ClusterID)
			return Result{}, ErrAnswerRejected
		}
		claimed.Status, claimed.LockedUntil = ChallengeActive, time.Time{}
		_, _ = s.chal.Swap(ctx, claimed, claimed.Version)
		return Result{}, ErrAnswerRejected
	case errors.Is(perr, ErrInvalidAnswer):
		// Not a wrong guess (an empty field, a value of the wrong shape): no attempt is used up, the request stays
		// open and the caller tells the user what could not be used.
		s.release(ctx, claimed)
		return Result{}, ErrInvalidAnswer
	case errors.Is(perr, ErrChallengeExpired), errors.Is(perr, ErrInvalidGrant), errors.Is(perr, ErrUnsupported):
		_ = s.chal.Delete(ctx, claimed.ID)
		s.dropLive(claimed.ID)
		return Result{}, scrub(perr, d, a, saved)
	}
	s.release(ctx, claimed)
	return Result{}, scrub(perr, d, a, saved)
}

// Cancel implements Broker. It removes the caller's own request; anything else is ErrChallengeExpired
// (the same answer as for an unknown id).
func (s *Service) Cancel(ctx context.Context, sub Subject, challengeID string) error {
	rec, err := s.chal.Get(ctx, challengeID)
	if errors.Is(err, ErrChallengeNotFound) {
		return nil // cancelling something that is gone is not an error
	}
	if err != nil {
		return err
	}
	if !bound(rec, sub) {
		return ErrChallengeExpired
	}
	s.dropLive(challengeID) // release what the provider holds (the loopback listener) at once
	return s.chal.Delete(ctx, challengeID)
}

// Sweep removes expired requests. The server calls it periodically.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	now := s.now()
	for _, r := range s.live.takeIf(func(r liveRequest) bool { return !now.Before(r.expires) }) {
		r.cancel()
	}
	return s.chal.DeleteExpired(ctx, now)
}

// --- error scrubbing -------------------------------------------------------------------------------

// scrubbedError carries a provider's message with every known secret removed.
type scrubbedError struct{ msg string }

func (e *scrubbedError) Error() string { return e.msg }

// scrub makes an error from a provider safe to return. Contract errors pass through unchanged. Any other
// error keeps only its text, with every secret the Service knows about (answer values, descriptor
// parameters, plugin environment, saved data) replaced. Providers must not put secrets in errors; this is the
// second line of defense.
func scrub(err error, d Descriptor, a Answer, saved []byte) error {
	for _, sentinel := range []error{ErrChallengeRequired, ErrChallengeExpired, ErrAnswerRejected, ErrInvalidAnswer, ErrInvalidGrant, ErrUnsupported,
		ErrServiceUnavailable, ErrNotFound, ErrVersionConflict, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	var known []string
	for _, v := range a {
		known = append(known, v)
	}
	for _, v := range d.Params {
		known = append(known, v)
	}
	if d.Exec != nil {
		for _, v := range d.Exec.Env {
			known = append(known, v)
		}
		known = append(known, d.Exec.Args...)
	}
	if len(saved) > 0 {
		known = append(known, string(saved))
	}
	return &scrubbedError{msg: scrubText(err.Error(), known...)}
}

// scrubText replaces every secret (of at least 4 characters, shorter ones would mangle ordinary text) in s.
func scrubText(s string, secrets ...string) string {
	for _, sec := range secrets {
		if len(sec) >= 4 {
			s = strings.ReplaceAll(s, sec, "[redacted]")
		}
	}
	return s
}

// --- token cache and single flight -----------------------------------------------------------------

type tokenCache struct {
	mu sync.Mutex
	m  map[string]Token
}

func cacheKey(k Key) string { return k.ClusterID + "\x00" + string(k.Scope) + "\x00" + k.OwnerID }

// put stores a token. A token without expiry (an exec plugin that gives no expirationTimestamp) is kept as well,
// with the semantics of client-go: it lives in memory until the API server answers 401 and the caller invalidates it.
func (c *tokenCache) put(k Key, t Token) {
	c.mu.Lock()
	c.m[cacheKey(k)] = t
	c.mu.Unlock()
}

func (c *tokenCache) get(k Key, now time.Time) (Token, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.m[cacheKey(k)]
	if !ok {
		return Token{}, false
	}
	if t.Expiry.IsZero() {
		return t, true // no expiry: valid until Invalidate
	}
	if !t.Expiry.After(now.Add(tokenMargin)) {
		return Token{}, false
	}
	return t, true
}

func (c *tokenCache) remove(k Key) {
	c.mu.Lock()
	delete(c.m, cacheKey(k))
	c.mu.Unlock()
}

func (c *tokenCache) drop(match func(Key) bool, keys func(string) Key) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for ck := range c.m {
		if match(keys(ck)) {
			delete(c.m, ck)
		}
	}
}

func keyFromCache(ck string) Key {
	parts := strings.SplitN(ck, "\x00", 3)
	if len(parts) != 3 {
		return Key{}
	}
	return Key{ClusterID: parts[0], Scope: Scope(parts[1]), OwnerID: parts[2]}
}

type flight struct {
	done chan struct{}
	tok  Token
	err  error
}

// flightGroup lets one goroutine per key do the work while the others wait for its result. The work runs
// detached from the callers' contexts (a caller that gives up must not break the others), bounded by a timeout.
type flightGroup struct {
	mu sync.Mutex
	m  map[string]*flight
}

func (g *flightGroup) do(ctx context.Context, key string, fn func(context.Context) (Token, error)) (Token, error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = map[string]*flight{}
	}
	f, ok := g.m[key]
	if !ok {
		f = &flight{done: make(chan struct{})}
		g.m[key] = f
		go func() {
			wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
			defer cancel()
			f.tok, f.err = fn(wctx)
			g.mu.Lock()
			delete(g.m, key)
			g.mu.Unlock()
			close(f.done)
		}()
	}
	g.mu.Unlock()
	select {
	case <-f.done:
		return f.tok, f.err
	case <-ctx.Done():
		return Token{}, ctx.Err()
	}
}
