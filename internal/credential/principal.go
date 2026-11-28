// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

// Subject-aware credential resolution for technical principals (Phase 1, Lane B, ADR-0036).
//
// A technical subject (ScopePrincipal: a TeamPrincipal or a ServicePrincipal bound to one cluster by a
// CredentialBinding) never has a user session and never asks a human during execution:
//
//   - Token / Acquire use the credential stored under the technical subject and nothing else. When a human would have to
//     answer a challenge, they return ErrChallengeRequired and the background operation is blocked. There is no
//     fallback to a user credential, a legacy Team account or a revoked credential.
//   - Every call resolves the principal and the binding through the identity domain (active identity, active binding,
//     confirmed critical audit) before the stored credential is read, and refuses a credential version whose critical
//     audit outcome is not CriticalSucceeded.
//   - Initial provisioning and rotation are the dedicated, session-bound interactive path (ProvisioningBroker). The
//     administrator authorizes and is the provenance of the setup; the secret is stored only under the technical
//     subject. No token is returned to the administrator.
//
// Provider calls, refresh, challenges and secret scrubbing stay in the one Broker (Service); nothing here calls a
// Provider outside of it.

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/identity"
)

// Errors of the technical path. Compare with errors.Is; the texts carry no identifiers or secrets.
var (
	// ErrBindingUnusable means that the principal or its credential binding cannot be used: the identity is not
	// active, the binding is pending, revoked, ambiguous, does not match the subject, or its audit is not confirmed.
	// The identity domain's own error (ErrForbidden, ErrRevoked, ErrUnavailable, ...) is wrapped and stays visible to
	// errors.Is. Fail closed: there is no fallback credential.
	ErrBindingUnusable = errors.New("the credential binding cannot be used")
	// ErrCredentialUnconfirmed means that the critical audit outcome of the stored credential version is missing,
	// failed or unknown. The version is unusable until the outcome is CriticalSucceeded.
	ErrCredentialUnconfirmed = errors.New("the credential assignment is not confirmed")
	// ErrProvisioningDenied means that the actor or the session may not (any longer) set up this credential.
	ErrProvisioningDenied = errors.New("the credential setup is not authorized")
	// ErrAuditUnavailable means that the critical audit journal could not record the intent or read an outcome.
	// Nothing was changed when it is returned before a mutation.
	ErrAuditUnavailable = errors.New("the critical audit is unavailable")
	// ErrBindingNotActivated means that the credential was stored and confirmed, but the pending binding could not be
	// activated. Call ActivateProvisionedBinding to finish; the credential is not usable meanwhile.
	ErrBindingNotActivated = errors.New("the credential is stored but the binding is not active")
)

// IdentityResolver is the part of the identity domain the credential layer consumes (implemented by *identity.Service).
// Resolve and ResolveBinding are the canonical "only active identity and active binding with a confirmed audit" path;
// ListBindings is a trusted read used to find the pending binding of a provisioning; ActivateBinding is called only
// after the credential has been stored durably. The credential layer owns no cluster catalog and no ownership
// resolver: ownership is checked by the identity domain when the binding is assigned.
type IdentityResolver interface {
	Resolve(ctx context.Context, ref identity.PrincipalRef) (identity.Identity, error)
	ResolveBinding(ctx context.Context, principal identity.PrincipalRef, clusterID string) (identity.CredentialBinding, error)
	ListBindings(ctx context.Context, principal identity.PrincipalRef) ([]identity.CredentialBinding, error)
	ActivateBinding(ctx context.Context, req identity.BindingTransitionRequest) (identity.CredentialBinding, error)
}

var _ IdentityResolver = (*identity.Service)(nil)

// ProvisioningAuthorizer is the injected authorization result of the use-case boundary: whether the authenticated actor,
// through the given active session, may currently assign, replace or revoke credentials of this principal
// (identity.AdminAssignCredential for its owner). It is asked at the start and again at every continuation of a
// challenge; an error (lost role, ended session, unavailable dependency) ends the setup. No policy lives here.
type ProvisioningAuthorizer interface {
	AuthorizeProvisioning(ctx context.Context, by ProvisioningContext, principal identity.PrincipalRef) error
}

// Audit actions of the credential use cases (CriticalIntent.Action) and the resource type.
const (
	auditActionProvision = "credential.provision"
	auditActionRevoke    = "credential.revoke"
	auditResourceType    = "credential"
)

// provisioningIDPrefix starts the id of every provisioning challenge. The alphabet of the ids of ordinary challenges
// (base64url) has no '.', so the two never clash, and the ordinary user path refuses a provisioning id.
const provisioningIDPrefix = "p."

func isProvisioningID(id string) bool { return strings.HasPrefix(id, provisioningIDPrefix) }

func newProvisioningChallengeID() (string, error) {
	id, err := newChallengeID()
	if err != nil {
		return "", err
	}
	return provisioningIDPrefix + id, nil
}

// IsPrincipal reports that the subject is meant as a technical one: any technical field is set. A subject that mixes
// legacy and technical fields is therefore routed to the technical validation, which rejects it; it is never
// handled as a legacy subject.
func (sub Subject) IsPrincipal() bool {
	return sub.Scope == ScopePrincipal || sub.Principal != (identity.PrincipalRef{}) || sub.BindingID != ""
}

// Validate checks the subject as the exclusive tagged union of the Phase 1 Contract Freeze: a legacy subject
// (ScopeUser/ScopeTeam with OwnerID, optional SessionID, no principal fields) or a technical one (ScopePrincipal with
// a Team/Service principal and a binding id, no OwnerID and no SessionID). Anything else is ErrInvalidKey.
func (sub Subject) Validate() error {
	if !sub.IsPrincipal() {
		return sub.key().Validate()
	}
	if sub.Scope != ScopePrincipal || sub.SessionID != "" {
		return ErrInvalidKey
	}
	return sub.key().ValidateAny()
}

// validate checks a provisioning context: a human actor (system and scheduler actors never provision), a session and a
// well-formed event id.
func (by ProvisioningContext) validate() error {
	if err := by.Actor.Validate(); err != nil || !identity.ValidID(by.EventID) || by.SessionID == "" || len(by.SessionID) > 512 {
		return ErrInvalidKey
	}
	if by.Actor.Kind != identity.ActorUser {
		return fmt.Errorf("%w: only an authenticated user sets up credentials", ErrProvisioningDenied)
	}
	return nil
}

// ---------------------------------------------------------------------------------------------------------------
// ports and readiness

func (s *Service) principalStore() (PrincipalStore, error) {
	ps, ok := s.store.(PrincipalStore)
	if !ok || s.ids == nil || s.crit == nil {
		return nil, ErrNotConfigured
	}
	return ps, nil
}

func (s *Service) provisioningStore() (PrincipalStore, error) {
	ps, err := s.principalStore()
	if err != nil {
		return nil, err
	}
	if s.authz == nil {
		return nil, ErrNotConfigured
	}
	return ps, nil
}

// ---------------------------------------------------------------------------------------------------------------
// execution: Token / Acquire

type cachedPrincipalToken struct {
	tok     Token
	version int64 // version of the stored record the token was issued from
}

// principalCache keeps tokens of technical subjects. Its key is the whole technical Key, so a token is never served to
// another principal kind, principal, cluster or binding. An entry is used only while the stored record still has the
// version it was issued from.
type principalCache struct {
	mu sync.Mutex
	m  map[Key]cachedPrincipalToken
}

func (c *principalCache) put(k Key, t Token, version int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[Key]cachedPrincipalToken{}
	}
	c.m[k] = cachedPrincipalToken{tok: t, version: version}
}

func (c *principalCache) get(k Key, version int64, now time.Time) (Token, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || e.version != version {
		return Token{}, false
	}
	if !e.tok.Expiry.IsZero() && !e.tok.Expiry.After(now.Add(tokenMargin)) {
		return Token{}, false
	}
	return e.tok, true
}

func (c *principalCache) remove(k Key) {
	c.mu.Lock()
	delete(c.m, k)
	c.mu.Unlock()
}

func (c *principalCache) drop(match func(Key) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.m {
		if match(k) {
			delete(c.m, k)
		}
	}
}

// principalFlightKey identifies one technical key in the single-flight group. Identifiers contain no NUL byte.
func principalFlightKey(k Key) string {
	return strings.Join([]string{k.ClusterID, string(k.Principal.Kind), k.Principal.ID, k.Principal.OrgID, k.Principal.TeamID, k.BindingID}, "\x00")
}

// usableBinding resolves the binding of an execution through the identity domain: the identity must be active, the
// binding active, and the audit of both confirmed. The result must be exactly the subject's binding.
func (s *Service) usableBinding(ctx context.Context, sub Subject) (identity.CredentialBinding, error) {
	b, err := s.ids.ResolveBinding(ctx, sub.Principal, sub.ClusterID)
	if err != nil {
		return identity.CredentialBinding{}, fmt.Errorf("%w: %w", ErrBindingUnusable, err)
	}
	if b.ID != sub.BindingID || b.ClusterID != sub.ClusterID || b.Principal != sub.Principal || b.State != identity.BindingActive {
		return identity.CredentialBinding{}, fmt.Errorf("%w: the binding does not belong to this subject", ErrBindingUnusable)
	}
	return b, nil
}

// confirmedEvent returns nil only when the critical audit outcome of the event is CriticalSucceeded. A missing,
// failed or unknown outcome is ErrCredentialUnconfirmed; an unreadable journal is ErrAuditUnavailable: fail closed.
func (s *Service) confirmedEvent(ctx context.Context, orgID, eventID string) error {
	if !identity.ValidID(eventID) {
		return fmt.Errorf("%w: no audit event", ErrCredentialUnconfirmed)
	}
	out, err := s.crit.GetOutcome(ctx, orgID, eventID)
	switch {
	case err == nil && out.Result == audit.CriticalSucceeded:
		return nil
	case err == nil:
		return fmt.Errorf("%w: the outcome is not succeeded", ErrCredentialUnconfirmed)
	case errors.Is(err, audit.ErrCriticalNotFound):
		return fmt.Errorf("%w: the outcome is not recorded", ErrCredentialUnconfirmed)
	default:
		return fmt.Errorf("%w: %w", ErrAuditUnavailable, err)
	}
}

// principalMeta finds the stored record of exactly this technical key. No record is ErrChallengeRequired (nothing
// to run with); an unreadable store is an error, never "absent".
func (s *Service) principalMeta(ctx context.Context, ps PrincipalStore, key Key) (Meta, error) {
	metas, err := ps.ListPrincipal(ctx, key.Principal)
	if err != nil {
		return Meta{}, err
	}
	for _, m := range metas {
		if m.Key == key {
			return m, nil
		}
	}
	return Meta{}, ErrChallengeRequired
}

// tokenPrincipal is Token for a technical subject. It never starts or stores a challenge.
func (s *Service) tokenPrincipal(ctx context.Context, sub Subject, d Descriptor) (Token, error) {
	if err := sub.Validate(); err != nil {
		return Token{}, err
	}
	// A static descriptor would use the identity written into the cluster's kubeconfig (the shared account): that is
	// a fallback credential, not the technical principal's own.
	if d.Kind == "" || d.Kind == KindStatic {
		return Token{}, ErrUnsupported
	}
	ps, err := s.principalStore()
	if err != nil {
		return Token{}, err
	}
	key := sub.key()
	if _, err := s.usableBinding(ctx, sub); err != nil {
		s.pcache.remove(key)
		return Token{}, err
	}
	meta, err := s.principalMeta(ctx, ps, key)
	if err != nil {
		return Token{}, err
	}
	if err := s.confirmedEvent(ctx, sub.Principal.OrgID, meta.LastAuditEventID); err != nil {
		s.pcache.remove(key)
		return Token{}, err
	}
	if t, ok := s.pcache.get(key, meta.Version, s.now()); ok {
		return t, nil
	}
	return s.pflight.do(ctx, principalFlightKey(key), func(fctx context.Context) (Token, error) {
		return s.refreshPrincipal(fctx, ps, sub, d)
	})
}

// acquirePrincipal is Acquire for a technical subject: a token from the stored credential or an error. A technical
// subject has no interactive challenge during execution.
func (s *Service) acquirePrincipal(ctx context.Context, sub Subject, d Descriptor) (Result, error) {
	tok, err := s.tokenPrincipal(ctx, sub, d)
	if err != nil {
		return Result{}, err
	}
	return Result{Token: &tok}, nil
}

func (s *Service) refreshPrincipal(ctx context.Context, ps PrincipalStore, sub Subject, d Descriptor) (Token, error) {
	p, err := s.reg.Resolve(d)
	if err != nil {
		return Token{}, err
	}
	key := sub.key()
	for round := 0; round < maxRefreshRounds; round++ {
		base, err := ps.Get(ctx, key)
		if errors.Is(err, ErrNotFound) {
			return Token{}, ErrChallengeRequired
		}
		if err != nil {
			return Token{}, err // the store is broken: not a reason to ask anybody to sign in again
		}
		if err := s.confirmedEvent(ctx, sub.Principal.OrgID, base.LastAuditEventID); err != nil {
			return Token{}, err
		}
		res, perr := p.Begin(ctx, s.penv, Request{Descriptor: d, Subject: sub, Saved: base.Secret})
		switch {
		case perr != nil && !errors.Is(perr, ErrInvalidGrant):
			return Token{}, scrub(perr, d, nil, base.Secret)
		case perr != nil || res.Challenge != nil || res.Token == nil || (res.Save != nil && len(res.Save) == 0):
			// A human would have to sign in (or the saved sign-in is no longer valid). Nothing is stored and no
			// challenge is created; whatever the provider holds for a challenge is released.
			if c, ok := p.(Canceller); ok && res.Challenge != nil {
				c.Cancel(res.Pending)
			}
			if len(base.Secret) > 0 && s.savedChanged(ctx, key, base.Version) {
				continue // another replica rotated or an administrator replaced it: read that version
			}
			return Token{}, ErrChallengeRequired
		}
		// The token is valid now; re-check the binding so that a revocation that happened meanwhile blocks its issue.
		if _, err := s.usableBinding(ctx, sub); err != nil {
			s.pcache.remove(key)
			return Token{}, err
		}
		version := base.Version
		if len(res.Save) > 0 {
			// A rotated refresh token continues the same assignment: LastAuditEventID is kept (a routine refresh is
			// not a new identity assignment) and the write is conditional on the version read, so a stale writer
			// can neither overwrite a newer rotation nor revive a revoked record.
			rec := Record{Meta: Meta{Key: key, Kind: d.Kind, Principal: res.Token.Principal, LastAuditEventID: base.LastAuditEventID}, Secret: res.Save}
			if !res.Token.Expiry.IsZero() {
				exp := res.Token.Expiry
				rec.ExpiresAt = &exp
			}
			m, err := ps.Put(ctx, rec, base.Version)
			switch {
			case err == nil:
				version = m.Version
			case errors.Is(err, ErrVersionConflict):
				version = 0 // a newer version exists: use the token, do not cache it
			default:
				s.log.Warn("the refreshed sign-in data of a technical identity could not be saved", "cluster", sub.ClusterID, "error", scrubText(err.Error()))
				version = 0
			}
		}
		if version > 0 {
			s.pcache.put(key, *res.Token, version)
		}
		return *res.Token, nil
	}
	return Token{}, ErrChallengeRequired
}

// ListPrincipal returns the stored sign-ins of one technical principal (no secrets).
func (s *Service) ListPrincipal(ctx context.Context, principal identity.PrincipalRef) ([]Meta, error) {
	if !principal.IsTechnical() || principal.Validate() != nil {
		return nil, ErrInvalidKey
	}
	ps, err := s.principalStore()
	if err != nil {
		return nil, err
	}
	return ps.ListPrincipal(ctx, principal)
}

// ---------------------------------------------------------------------------------------------------------------
// critical audit helpers (Contract Freeze 7.2)

func credDigest(parts ...string) string {
	h := sha256.New()
	var n [4]byte
	for _, p := range parts {
		binary.BigEndian.PutUint32(n[:], uint32(len(p)))
		h.Write(n[:])
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// intent builds the immutable critical intent of a credential use case. It holds identifiers, the action, the
// expected version and a digest: never a secret, a descriptor parameter or a token.
func (s *Service) intent(by ProvisioningContext, sub Subject, action string, expected int64, tag string) audit.CriticalIntent {
	p := sub.Principal
	dg := credDigest(action, string(p.Kind), p.ID, p.OrgID, p.TeamID, sub.ClusterID, sub.BindingID,
		string(by.Actor.Kind), by.Actor.ID, by.EventID, strconv.FormatInt(expected, 10), tag)
	return audit.CriticalIntent{
		EventID: by.EventID, MutationID: "cred." + dg[:40], RequestDigest: dg,
		ActorKind: string(by.Actor.Kind), ActorID: by.Actor.ID,
		PrincipalKind: string(p.Kind), PrincipalID: p.ID, OrgID: p.OrgID, TeamID: p.TeamID, ClusterID: sub.ClusterID,
		Action: action, ResourceType: auditResourceType, ResourceID: sub.BindingID,
		ExpectedRevision: uint64(max(expected, 0)), OccurredAt: s.now(),
	}
}

func (s *Service) appendIntent(ctx context.Context, in audit.CriticalIntent) error {
	if err := s.crit.AppendIntent(ctx, in); err != nil {
		return fmt.Errorf("%w: %w", ErrAuditUnavailable, err)
	}
	return nil
}

// appendOutcome records the outcome of an intent even when the caller's context has ended. A failure to record it is
// ErrOutcomeUnknown: the mutation may have happened; it is not repeated and the credential stays unusable until the
// outcome is confirmed.
func (s *Service) appendOutcome(ctx context.Context, eventID string, result audit.CriticalResult, revision int64, reason string) error {
	out := audit.CriticalOutcome{EventID: eventID, Result: result, ResourceRevision: uint64(max(revision, 0)), ReasonCode: reason, CompletedAt: s.now()}
	if err := s.crit.AppendOutcome(context.WithoutCancel(ctx), out); err != nil {
		return fmt.Errorf("%w: %w", audit.ErrOutcomeUnknown, err)
	}
	return nil
}

// failIntent records a failed outcome and returns the cause. If the outcome cannot be recorded the error says so and
// still carries the cause.
func (s *Service) failIntent(ctx context.Context, eventID, reason string, cause error) error {
	if err := s.appendOutcome(ctx, eventID, audit.CriticalFailed, 0, reason); err != nil {
		return errors.Join(err, cause)
	}
	return cause
}

// reasonFor maps an error to a safe reason code of a failed outcome.
func reasonFor(err error) string {
	switch {
	case errors.Is(err, ErrAnswerRejected):
		return "rejected"
	case errors.Is(err, ErrChallengeExpired):
		return "expired"
	case errors.Is(err, ErrInvalidGrant):
		return "invalid_grant"
	case errors.Is(err, ErrUnsupported):
		return "unsupported"
	case errors.Is(err, ErrServiceUnavailable):
		return "idp_unavailable"
	case errors.Is(err, ErrVersionConflict):
		return "conflict"
	case errors.Is(err, ErrBindingUnusable):
		return "binding_unusable"
	case errors.Is(err, ErrProvisioningDenied):
		return "authority_lost"
	default:
		return "provider_error"
	}
}

// ---------------------------------------------------------------------------------------------------------------
// provisioning

// provState is the binding of a provisioning challenge. It lives inside the encrypted state of the challenge, so it
// needs no column in any challenge store, and the ordinary user path cannot reach it.
type provState struct {
	Principal   identity.PrincipalRef `json:"principal"`
	BindingID   string                `json:"bindingId"`
	ClusterID   string                `json:"clusterId"`
	ActorKind   identity.ActorKind    `json:"actorKind"`
	ActorID     string                `json:"actorId"`
	SessionID   string                `json:"sessionId"`
	EventID     string                `json:"eventId"`
	BaseVersion int64                 `json:"baseVersion"` // version of the stored credential when the setup began; 0 = none
}

func newProvState(sub Subject, by ProvisioningContext, base int64) *provState {
	return &provState{Principal: sub.Principal, BindingID: sub.BindingID, ClusterID: sub.ClusterID,
		ActorKind: by.Actor.Kind, ActorID: by.Actor.ID, SessionID: by.SessionID, EventID: by.EventID, BaseVersion: base}
}

// matches reports whether the challenge belongs to exactly this subject, actor, session and event.
func (p *provState) matches(sub Subject, by ProvisioningContext) bool {
	return p != nil && p.Principal == sub.Principal && p.BindingID == sub.BindingID && p.ClusterID == sub.ClusterID &&
		p.ActorKind == by.Actor.Kind && p.ActorID == by.Actor.ID && p.SessionID == by.SessionID && p.EventID == by.EventID
}

// checkProvisioning validates the request and asks the injected authorizer. The authorizer's error is wrapped, never
// replaced by a default allow.
func (s *Service) checkProvisioning(ctx context.Context, sub Subject, by ProvisioningContext) error {
	if !sub.IsPrincipal() {
		return ErrInvalidKey
	}
	if err := sub.Validate(); err != nil {
		return err
	}
	if err := by.validate(); err != nil {
		return err
	}
	if err := s.authz.AuthorizeProvisioning(ctx, by, sub.Principal); err != nil {
		return fmt.Errorf("%w: %w", ErrProvisioningDenied, err)
	}
	return nil
}

// provisioningBinding finds the binding a setup is for. The identity must be active and the binding must be the
// subject's exactly, pending (initial assignment) or active (rotation), with a confirmed creation audit. A revoked,
// foreign or unknown binding is refused.
func (s *Service) provisioningBinding(ctx context.Context, sub Subject) (identity.CredentialBinding, error) {
	if _, err := s.ids.Resolve(ctx, sub.Principal); err != nil {
		return identity.CredentialBinding{}, fmt.Errorf("%w: %w", ErrBindingUnusable, err)
	}
	all, err := s.ids.ListBindings(ctx, sub.Principal)
	if err != nil {
		return identity.CredentialBinding{}, fmt.Errorf("%w: %w", ErrBindingUnusable, err)
	}
	for _, b := range all {
		if b.ID != sub.BindingID {
			continue
		}
		if b.ClusterID != sub.ClusterID || b.Principal != sub.Principal ||
			(b.State != identity.BindingPending && b.State != identity.BindingActive) {
			return identity.CredentialBinding{}, fmt.Errorf("%w: the binding cannot be set up", ErrBindingUnusable)
		}
		if err := s.confirmedEvent(ctx, sub.Principal.OrgID, b.LastAuditEventID); err != nil {
			return identity.CredentialBinding{}, fmt.Errorf("%w: %w", ErrBindingUnusable, err)
		}
		return b, nil
	}
	return identity.CredentialBinding{}, fmt.Errorf("%w: the binding was not found", ErrBindingUnusable)
}

// recordProvisioningFailureOutcome makes every terminal failure retry-safe. Critical outcomes are immutable:
// an already recorded CriticalFailed is success; any other existing outcome is a conflict, never overwritten.
func (s *Service) recordProvisioningFailureOutcome(ctx context.Context, prov *provState, reason string) error {
	if prov == nil || prov.EventID == "" || prov.Principal.OrgID == "" || s.crit == nil {
		return fmt.Errorf("%w: provisioning outcome cannot be identified", audit.ErrOutcomeUnknown)
	}
	out, err := s.crit.GetOutcome(ctx, prov.Principal.OrgID, prov.EventID)
	if err == nil {
		if out.Result == audit.CriticalFailed {
			return nil
		}
		return fmt.Errorf("%w: provisioning already has a non-failed outcome", audit.ErrOutcomeUnknown)
	}
	if !errors.Is(err, audit.ErrCriticalNotFound) {
		return fmt.Errorf("%w: %w", audit.ErrOutcomeUnknown, err)
	}
	if err := s.appendOutcome(ctx, prov.EventID, audit.CriticalFailed, 0, reason); err != nil {
		out, readErr := s.crit.GetOutcome(context.WithoutCancel(ctx), prov.Principal.OrgID, prov.EventID)
		if readErr == nil && out.Result == audit.CriticalFailed {
			return nil
		}
		return fmt.Errorf("%w: %w", audit.ErrOutcomeUnknown, err)
	}
	return nil
}

// finishProvisioningFailure persists the immutable outcome before touching any challenge. It is safe across
// replicas: concurrent writers converge on the same CriticalFailed and concurrent deletes treat not-found as success.
func (s *Service) finishProvisioningFailure(ctx context.Context, prov *provState, reason string, cause error, ids ...string) error {
	if err := s.recordProvisioningFailureOutcome(ctx, prov, reason); err != nil {
		return errors.Join(err, cause)
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if err := s.chal.Delete(ctx, id); err != nil && !errors.Is(err, ErrChallengeNotFound) {
			return errors.Join(fmt.Errorf("delete provisioning challenge: %w", err), cause)
		}
		s.dropLive(id)
	}
	return cause
}

// provisioningOutcomeBlocksContinuation prevents a leftover challenge from being processed after a terminal outcome.
// Failed/succeeded outcomes permit cleanup; unknown or unreadable outcomes preserve the challenge for recovery.
func (s *Service) provisioningOutcomeBlocksContinuation(ctx context.Context, id string, prov *provState) error {
	if prov == nil || prov.EventID == "" || s.crit == nil {
		return fmt.Errorf("%w: provisioning outcome cannot be identified", audit.ErrOutcomeUnknown)
	}
	out, err := s.crit.GetOutcome(ctx, prov.Principal.OrgID, prov.EventID)
	if errors.Is(err, audit.ErrCriticalNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: %w", audit.ErrOutcomeUnknown, err)
	}
	switch out.Result {
	case audit.CriticalFailed, audit.CriticalSucceeded:
		if err := s.chal.Delete(ctx, id); err != nil && !errors.Is(err, ErrChallengeNotFound) {
			return err
		}
		s.dropLive(id)
		return ErrChallengeExpired
	default:
		return fmt.Errorf("%w: provisioning outcome is not terminal", audit.ErrOutcomeUnknown)
	}
}

// BeginProvisioning implements ProvisioningBroker. The critical intent is durable before the identity provider is
// contacted; if it cannot be recorded, nothing happens. The result is a challenge, or an empty Result when the
// provider needed no answer and the credential was stored (completion: no token is returned either way).
func (s *Service) BeginProvisioning(ctx context.Context, sub Subject, by ProvisioningContext, d Descriptor) (Result, error) {
	ps, err := s.provisioningStore()
	if err != nil {
		return Result{}, err
	}
	if err := s.checkProvisioning(ctx, sub, by); err != nil {
		return Result{}, err
	}
	if d.Kind == "" || d.Kind == KindStatic {
		return Result{}, ErrUnsupported
	}
	b, err := s.provisioningBinding(ctx, sub)
	if err != nil {
		return Result{}, err
	}
	var base int64
	switch meta, err := s.principalMeta(ctx, ps, sub.key()); {
	case err == nil:
		base = meta.Version
	case !errors.Is(err, ErrChallengeRequired):
		return Result{}, err
	}
	if err := s.appendIntent(ctx, s.intent(by, sub, auditActionProvision, base, string(d.Kind))); err != nil {
		return Result{}, err // nothing was started
	}
	// A fresh sign-in: the existing credential (if any) is not offered to the provider.
	rd, res, perr := s.begin(ctx, sub, d, nil)
	if perr != nil {
		return Result{}, s.failIntent(ctx, by.EventID, reasonFor(perr), scrub(perr, rd, nil, nil))
	}
	return s.continueProvisioning(ctx, ps, sub, by, rd, newProvState(sub, by, base), b, res, nil)
}

// AnswerProvisioning implements ProvisioningBroker.
func (s *Service) AnswerProvisioning(ctx context.Context, sub Subject, by ProvisioningContext, challengeID string, a Answer) (Result, error) {
	return s.advanceProvisioning(ctx, sub, by, challengeID, a, true)
}

// PollProvisioning asks whether an out-of-band sign-in (device code, browser) of a provisioning has finished. It is not
// part of the frozen ProvisioningBroker interface, which has no polling; device-code setup needs it.
func (s *Service) PollProvisioning(ctx context.Context, sub Subject, by ProvisioningContext, challengeID string) (Result, error) {
	return s.advanceProvisioning(ctx, sub, by, challengeID, nil, false)
}

// loadProvisioning fetches the challenge of this exact subject, actor, session and event. Every failure is the
// same ErrChallengeExpired, so a caller cannot learn whether another actor's challenge exists.
func (s *Service) loadProvisioning(ctx context.Context, sub Subject, by ProvisioningContext, id string) (ChallengeRecord, pendingState, error) {
	if !isProvisioningID(id) {
		return ChallengeRecord{}, pendingState{}, ErrChallengeExpired
	}
	rec, err := s.chal.Get(ctx, id)
	if errors.Is(err, ErrChallengeNotFound) {
		return ChallengeRecord{}, pendingState{}, ErrChallengeExpired
	}
	if err != nil {
		return ChallengeRecord{}, pendingState{}, err
	}
	if rec.ClusterID != sub.ClusterID || rec.OwnerID != by.Actor.ID || rec.SessionID != by.SessionID {
		return ChallengeRecord{}, pendingState{}, ErrChallengeExpired
	}
	st, err := s.open(rec)
	if err != nil || !st.Prov.matches(sub, by) {
		return ChallengeRecord{}, pendingState{}, ErrChallengeExpired
	}
	return rec, st, nil
}

func (s *Service) advanceProvisioning(ctx context.Context, sub Subject, by ProvisioningContext, id string, a Answer, isAnswer bool) (Result, error) {
	ps, err := s.provisioningStore()
	if err != nil {
		return Result{}, err
	}
	if !sub.IsPrincipal() || sub.Validate() != nil || by.validate() != nil {
		return Result{}, ErrChallengeExpired
	}
	rec, st, err := s.loadProvisioning(ctx, sub, by, id)
	if err != nil {
		return Result{}, err
	}
	if err := s.provisioningOutcomeBlocksContinuation(ctx, id, st.Prov); err != nil {
		return Result{}, err
	}
	now := s.now()
	if !now.Before(rec.ExpiresAt) {
		return Result{}, s.finishProvisioningFailure(ctx, st.Prov, "expired", ErrChallengeExpired, id)
	}
	// Re-check authority and binding on every continuation. A terminal denial is audited before challenge deletion.
	if err := s.authz.AuthorizeProvisioning(ctx, by, sub.Principal); err != nil {
		cause := fmt.Errorf("%w: %w", ErrProvisioningDenied, err)
		return Result{}, s.abandonProvisioning(ctx, id, st.Prov, cause)
	}
	b, err := s.provisioningBinding(ctx, sub)
	if err != nil {
		return Result{}, s.abandonProvisioning(ctx, id, st.Prov, err)
	}
	if !isAnswer && !st.Challenge.Poll {
		c := publicChallenge(st.Challenge, rec.ID, rec.ExpiresAt)
		return Result{Challenge: &c}, nil
	}
	if isAnswer {
		userKey, idpKey := attemptKeys(Subject{Scope: rec.Scope, OwnerID: rec.OwnerID}, st.Descriptor)
		wait, err := s.attempts.Check(ctx, userKey, idpKey, now)
		if err != nil {
			return Result{}, err
		}
		if wait > 0 {
			return Result{}, &RateLimitedError{RetryAfter: wait}
		}
	}
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
	req := Request{Descriptor: d, Subject: sub, Pending: st.Pending,
		Challenge: publicChallenge(st.Challenge, rec.ID, rec.ExpiresAt), Answer: a}
	res, perr := p.Continue(ctx, s.penv, req)
	if perr != nil {
		return s.failedProvisioning(ctx, claimed, st.Prov, d, a, perr, isAnswer)
	}
	out, err := s.continueProvisioning(ctx, ps, sub, by, d, st.Prov, b, res, &claimed)
	if err != nil {
		// If the audit result is unknown the claimed record remains recoverable. Releasing it only changes
		// the lease; it never deletes or terminally completes the provisioning.
		s.release(ctx, claimed)
		return Result{}, err
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
	prov *provState, b identity.CredentialBinding, res Result, old *ChallengeRecord) (Result, error) {
	switch {
	case res.Challenge != nil:
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
			}
			return Result{}, ferr
		}
		return Result{Challenge: &c}, nil
	case res.Token != nil || len(res.Save) > 0:
		err := s.storeProvisioned(ctx, ps, sub, by, d, prov, b, res)
		if err != nil {
			// A failed outcome is terminal and can consume the old challenge. Unknown/unknown-result outcomes
			// deliberately leave it available for reconciliation.
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

// deleteProvisioningChallenges is called only after a terminal outcome has been confirmed; unknown audit results must retain every challenge.
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
func (s *Service) failedProvisioning(ctx context.Context, claimed ChallengeRecord, prov *provState, d Descriptor,
	a Answer, perr error, isAnswer bool) (Result, error) {
	switch {
	case errors.Is(perr, ErrAnswerRejected):
		if !isAnswer {
			err := s.finishProvisioningFailure(ctx, prov, "rejected", ErrAnswerRejected, claimed.ID)
			if errors.Is(err, audit.ErrOutcomeUnknown) {
				s.release(ctx, claimed)
			}
			return Result{}, err
		}
		userKey, idpKey := attemptKeys(Subject{Scope: claimed.Scope, OwnerID: claimed.OwnerID}, d)
		wait, limitErr := s.attempts.Fail(ctx, userKey, idpKey, s.now(), AttemptWindow,
			MaxUserAnswerFailures, MaxIDPAnswerFailures, AttemptLockout)
		if limitErr != nil {
			s.release(ctx, claimed)
			return Result{}, limitErr
		}
		claimed.Attempts++
		if claimed.Attempts >= MaxAnswerAttempts {
			err := s.finishProvisioningFailure(ctx, prov, "rejected", ErrAnswerRejected, claimed.ID)
			if errors.Is(err, audit.ErrOutcomeUnknown) {
				s.release(ctx, claimed)
			}
			if wait > 0 && err == nil {
				return Result{}, &RateLimitedError{RetryAfter: wait}
			}
			return Result{}, err
		}
		claimed.Status, claimed.LockedUntil = ChallengeActive, time.Time{}
		if _, err := s.chal.Swap(ctx, claimed, claimed.Version); err != nil {
			return Result{}, mapChallengeErr(err)
		}
		if wait > 0 {
			return Result{}, &RateLimitedError{RetryAfter: wait}
		}
		return Result{}, ErrAnswerRejected
	case errors.Is(perr, ErrInvalidAnswer):
		s.release(ctx, claimed)
		return Result{}, ErrInvalidAnswer
	case errors.Is(perr, ErrChallengeExpired), errors.Is(perr, ErrInvalidGrant), errors.Is(perr, ErrUnsupported):
		err := s.finishProvisioningFailure(ctx, prov, reasonFor(perr), scrub(perr, d, a, nil), claimed.ID)
		if errors.Is(err, audit.ErrOutcomeUnknown) {
			s.release(ctx, claimed)
		}
		return Result{}, err
	default:
		s.release(ctx, claimed)
		return Result{}, scrub(perr, d, a, nil)
	}
}

// cancelPending releases what a provider holds for a request that will not be stored.
func (s *Service) cancelPending(d Descriptor, pending []byte) {
	if p, err := s.reg.Resolve(d); err == nil {
		if c, ok := p.(Canceller); ok {
			c.Cancel(pending)
		}
	}
}

// storeProvisioningChallenge stores a challenge of a provisioning. The routing record is the administrator's own
// (actor and session, which is what binds the challenge to them); the technical subject, binding and event are in the
// encrypted state. A continuation of the same polling request keeps its ID.
func (s *Service) storeProvisioningChallenge(ctx context.Context, sub Subject, by ProvisioningContext, d Descriptor, prov *provState,
	res Result, old *ChallengeRecord) (Challenge, error) {
	if res.Challenge.Kind == ChallengeBrowserCallback {
		return Challenge{}, ErrUnsupported
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
	if old != nil && old.ID != "" {
		if err := s.chal.Delete(ctx, old.ID); err != nil && !errors.Is(err, ErrChallengeNotFound) {
			return pub, err
		}
		s.dropLive(old.ID)
	}
	return pub, nil
}

// storeProvisioned stores the finished credential under the technical subject and completes the audit. Order: the
// binding is checked, the record is written conditionally on the version read at the start, the binding is checked
// again (a revocation in between removes the record), the outcome is recorded, and only then a pending binding is
// activated. The new version is unusable until its outcome is CriticalSucceeded.
func (s *Service) storeProvisioned(ctx context.Context, ps PrincipalStore, sub Subject, by ProvisioningContext, d Descriptor,
	prov *provState, b identity.CredentialBinding, res Result) error {
	if len(res.Save) == 0 {
		return s.failIntent(ctx, by.EventID, "not_storable", ErrUnsupported)
	}
	if _, err := s.provisioningBinding(ctx, sub); err != nil {
		return s.failIntent(ctx, by.EventID, reasonFor(err), err)
	}
	key := sub.key()
	rec := Record{Meta: Meta{Key: key, Kind: d.Kind, LastAuditEventID: by.EventID}, Secret: res.Save}
	if res.Token != nil {
		rec.Principal = res.Token.Principal
		if !res.Token.Expiry.IsZero() {
			exp := res.Token.Expiry
			rec.ExpiresAt = &exp
		}
	}
	m, err := ps.Put(ctx, rec, prov.BaseVersion)
	switch {
	case errors.Is(err, ErrVersionConflict):
		// Somebody stored a newer version meanwhile (rotation, refresh): this stale setup does not overwrite it.
		return s.failIntent(ctx, by.EventID, "conflict", err)
	case err != nil:
		// Whether the write happened is not known: the intent stays resolvable by reconciliation.
		if aerr := s.appendOutcome(ctx, by.EventID, audit.CriticalUnknown, 0, "store_error"); aerr != nil {
			return errors.Join(aerr, scrubStoreErr(err))
		}
		return scrubStoreErr(err)
	}
	s.pcache.remove(key)
	if _, err := s.provisioningBinding(ctx, sub); err != nil {
		_ = ps.DeleteIfVersion(ctx, key, m.Version) // revoked while the setup ran: it must not leave a credential
		return s.failIntent(ctx, by.EventID, "binding_unusable", err)
	}
	if err := s.appendOutcome(ctx, by.EventID, audit.CriticalSucceeded, m.Version, ""); err != nil {
		return err // stored, but unusable until the outcome is confirmed
	}
	if b.State == identity.BindingPending {
		_, err := s.ids.ActivateBinding(ctx, identity.BindingTransitionRequest{Actor: by.Actor, Principal: sub.Principal,
			ClusterID: sub.ClusterID, ExpectedRevision: b.Revision, MutationID: activationMutationID(sub, by.EventID)})
		if err != nil {
			return fmt.Errorf("%w: %w", ErrBindingNotActivated, err)
		}
	}
	return nil
}

// scrubStoreErr keeps only the text of a store error with known secrets removed (a store should not hold any).
func scrubStoreErr(err error) error { return &scrubbedError{msg: scrubText(err.Error())} }

func activationMutationID(sub Subject, eventID string) string {
	return "act." + credDigest("binding.activate", sub.BindingID, eventID)[:40]
}

// ActivateProvisionedBinding finishes a provisioning whose credential was stored and confirmed but whose pending binding
// could not be activated (ErrBindingNotActivated). It requires the stored credential's audit outcome to be
// CriticalSucceeded, and it is idempotent: the activation mutation id derives from that event. An already active
// binding is not an error.
func (s *Service) ActivateProvisionedBinding(ctx context.Context, sub Subject, by ProvisioningContext) error {
	ps, err := s.provisioningStore()
	if err != nil {
		return err
	}
	if err := s.checkProvisioning(ctx, sub, by); err != nil {
		return err
	}
	b, err := s.provisioningBinding(ctx, sub)
	if err != nil {
		return err
	}
	if b.State == identity.BindingActive {
		return nil
	}
	meta, err := s.principalMeta(ctx, ps, sub.key())
	if err != nil {
		return err
	}
	if err := s.confirmedEvent(ctx, sub.Principal.OrgID, meta.LastAuditEventID); err != nil {
		return err
	}
	_, err = s.ids.ActivateBinding(ctx, identity.BindingTransitionRequest{Actor: by.Actor, Principal: sub.Principal,
		ClusterID: sub.ClusterID, ExpectedRevision: b.Revision, MutationID: activationMutationID(sub, meta.LastAuditEventID)})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBindingNotActivated, err)
	}
	return nil
}

// CancelProvisioning implements ProvisioningBroker. It removes the caller's own provisioning challenge and records the
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
		return ErrChallengeExpired
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

// RevokePrincipal is the audited use case that revokes the stored credential of one technical subject: the authorizer
// is asked, the critical intent is durable before anything is removed, then the exact record is deleted and the
// outcome recorded. It is allowed for a suspended or revoked identity and a revoked binding (cleanup must always
// be possible). After it, Token never issues a token for the subject, and a stale refresh cannot bring the
// record back (its write is conditional on a version that no longer exists).
func (s *Service) RevokePrincipal(ctx context.Context, sub Subject, by ProvisioningContext) error {
	ps, err := s.provisioningStore()
	if err != nil {
		return err
	}
	if err := s.checkProvisioning(ctx, sub, by); err != nil {
		return err
	}
	all, err := s.ids.ListBindings(ctx, sub.Principal)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBindingUnusable, err)
	}
	known := false
	for _, b := range all {
		if b.ID == sub.BindingID && b.ClusterID == sub.ClusterID && b.Principal == sub.Principal {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("%w: the binding was not found", ErrBindingUnusable)
	}
	key := sub.key()
	var base int64
	switch meta, err := s.principalMeta(ctx, ps, key); {
	case err == nil:
		base = meta.Version
	case !errors.Is(err, ErrChallengeRequired):
		return err
	}
	if err := s.appendIntent(ctx, s.intent(by, sub, auditActionRevoke, base, "")); err != nil {
		return err
	}
	if err := s.revokeExact(ctx, ps, RevokeFilter{ClusterID: sub.ClusterID, Scope: ScopePrincipal, Principal: sub.Principal, BindingID: sub.BindingID}); err != nil {
		if aerr := s.appendOutcome(ctx, by.EventID, audit.CriticalUnknown, 0, "store_error"); aerr != nil {
			return errors.Join(aerr, scrubStoreErr(err))
		}
		return scrubStoreErr(err)
	}
	return s.appendOutcome(ctx, by.EventID, audit.CriticalSucceeded, base, "")
}

// revokeExact removes the cached tokens, the live requests and the stored record of one technical subject.
func (s *Service) revokeExact(ctx context.Context, ps PrincipalStore, f RevokeFilter) error {
	// A local provisioning request is terminalized in its own audit stream before its provider state or challenge
	// is removed. If that outcome cannot be confirmed, abort the revoke rather than leave a live setup untracked.
	for _, r := range s.live.listIf(func(r liveRequest) bool { return r.provisioning && f.Matches(r.key) }) {
		if err := s.finishProvisioningFailure(ctx, r.prov, "binding_unusable", nil, r.id); err != nil {
			return err
		}
	}
	for _, r := range s.live.takeIf(func(r liveRequest) bool { return !r.provisioning && f.Matches(r.key) }) {
		r.cancel()
	}
	s.pcache.drop(f.Matches)
	_, err := ps.Revoke(ctx, f)
	return err
}
