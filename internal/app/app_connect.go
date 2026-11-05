// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/24DSO-labs/kubron/internal/audit"
	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/k8s"
)

// Events sent to the session that owns the sign-in (ADR-0018). The payloads carry identifiers only.
const (
	EventAuthCompleted = "cluster:auth-completed"
	EventAuthRequired  = "cluster:auth-required"
)

const authRequiredEvery = 5 * time.Second

// ErrSignInUnavailable is returned by the new sign-in methods when no broker is configured.
var ErrSignInUnavailable = errors.New("sign-in is not available")

// ChallengeRequiredError is what the legacy ConnectToCluster returns when the new flow needs the user to answer a
// challenge: an old client cannot render it, so it gets a typed error instead of a connection.
type ChallengeRequiredError struct {
	ClusterID string
	Kind      credential.ChallengeKind
}

func (e *ChallengeRequiredError) Error() string { return credential.ErrChallengeRequired.Error() }

// Unwrap lets errors.Is(err, credential.ErrChallengeRequired) work.
func (e *ChallengeRequiredError) Unwrap() error { return credential.ErrChallengeRequired }

// connectState is the per-session bookkeeping of the sign-in flow. It is a pointer in App so that session views
// (which copy App) do not copy the mutex and do not share state.
type connectState struct {
	mu       sync.Mutex
	pending  map[string]pendingChallenge // challenge id -> where it belongs, only for challenges this session started
	notified map[string]time.Time        // cluster id -> when cluster:auth-required was last sent
	seenSeq  uint64                      // how far of the replica's revocations this view has applied
}

func (s *connectState) seen() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seenSeq
}

func (s *connectState) markSeen(n uint64) {
	s.mu.Lock()
	if n > s.seenSeq {
		s.seenSeq = n
	}
	s.mu.Unlock()
}

func newConnectState() *connectState {
	return &connectState{pending: map[string]pendingChallenge{}, notified: map[string]time.Time{}}
}

// authRequiredDue reports whether cluster:auth-required may be sent now: a dead sign-in fails every request of
// the cluster, and the client needs the event once, not once per request.
func (s *connectState) authRequiredDue(clusterID string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.notified[clusterID]; ok && now.Sub(last) < authRequiredEvery {
		return false
	}
	s.notified[clusterID] = now
	return true
}

// pendingChallenge is what a session remembers about an open challenge: its cluster and the key of the identity
// provider it talks to (for the attempt limiter).
type pendingChallenge struct {
	cluster, idp string
	sub          credential.Subject // the subject the challenge was issued for: team or user
}

func (s *connectState) remember(challengeID string, p pendingChallenge) {
	s.mu.Lock()
	s.pending[challengeID] = p
	s.mu.Unlock()
}

func (s *connectState) cluster(challengeID string) (pendingChallenge, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[challengeID]
	return p, ok
}

func (s *connectState) forget(challengeID string) {
	s.mu.Lock()
	delete(s.pending, challengeID)
	s.mu.Unlock()
}

// SetBroker sets the sign-in broker; called before OnStartup. A function of the package, not a method of App:
// it must not appear in the Wails bindings or the RPC.
func SetBroker(a *App, b credential.Broker) {
	a.broker = b
	a.k8sMgr.SetAuthSession(k8s.AuthSession{Broker: b}) // фоновая проверка кластеров знает, чьи у кластера данные
}

// SetWebOIDCRedirectURL configures the callback URL from the validated KUBRON_PUBLIC_URL.
func SetWebOIDCRedirectURL(a *App, publicURL string) {
	if a == nil || publicURL == "" {
		return
	}
	a.webOIDCRedirectURL = strings.TrimRight(publicURL, "/") + "/auth/cluster/callback"
}

// withWebOIDCParams removes any web-only overrides that could have come from cluster input,
// then injects the administrator-controlled values for this web request.
func withWebOIDCParams(d credential.Descriptor, clientID, redirectURL string) credential.Descriptor {
	p := make(map[string]string, len(d.Params)+2)
	for k, v := range d.Params {
		if k != "web.client-id" && k != "web.redirect-url" {
			p[k] = v
		}
	}
	// The callback is usable only when both the administrator-configured public client and
	// the server's validated public URL are present. Otherwise retain the legacy paste-redirect flow.
	if clientID != "" && redirectURL != "" {
		p["web.client-id"] = clientID
		p["web.redirect-url"] = redirectURL
	}
	d.Params = p
	return d
}

// emit sends an event to the owner of this view: to the web session if there is one, otherwise to the shell.
func (a *App) emit(event string, data any) {
	if a.sessionID != "" {
		if em, ok := a.platform.(sessionEmitter); ok {
			em.EmitTo(a.sessionID, event, data)
		}
		return // never broadcast a sign-in event to other sessions
	}
	a.platform.Emit(event, data)
}

type authEventData struct {
	ClusterID   string `json:"clusterId"`
	ChallengeID string `json:"challengeId,omitempty"`
}

// finishConnect makes the cluster the active one of this view and checks that it answers.
// bindAuthSession tells the connection manager who connects: it builds the plan (and the pool key) from it.
func (a *App) bindAuthSession() {
	s := k8s.AuthSession{Broker: a.broker, SessionID: a.sessionID}
	if a.broker != nil {
		if u, err := a.auth.Current(); err == nil {
			s.UserID = u.ID
		}
		s.OnAuthRequired = func(clusterID string) {
			if a.conn != nil && a.conn.authRequiredDue(clusterID, time.Now()) {
				a.emit(EventAuthRequired, authEventData{ClusterID: clusterID})
			}
		}
	}
	a.k8sMgr.SetAuthSession(s)
}

func (a *App) finishConnect(clusterID string) error {
	if a.finish != nil {
		return a.finish(clusterID)
	}
	a.bindAuthSession()
	if err := a.k8sMgr.ConnectToCluster(clusterID); err != nil {
		return err
	}
	if a.sessionID == "" {
		config, _ := k8s.LoadKubronConfig()
		if config == nil {
			config = &k8s.KubronConfig{}
		}
		config.LastActiveCluster = clusterID
		_ = k8s.SaveKubronConfig(config) // remembering the last cluster is not critical
	}
	return a.k8sMgr.TestConnection()
}

// connect is the shared body of ConnectCluster and the legacy ConnectToCluster. Without a broker, and for
// clusters that have their own connection mode, the old path is used unchanged.
func (a *App) connect(ctx context.Context, clusterID string) (credential.ConnectResult, error) {
	if a.broker == nil {
		return credential.ConnectResult{Status: "connected"}, a.finishConnect(clusterID)
	}
	a.bindAuthSession()
	plan, err := k8s.PlanFor(a.k8sMgr.AuthSessionOf(), clusterID)
	if err != nil {
		return credential.ConnectResult{}, err
	}
	if plan.Legacy() { // in-cluster: no sign-in
		return credential.ConnectResult{Status: "connected"}, a.finishConnect(clusterID)
	}
	if a.sessionID != "" && plan.Desc.Kind == credential.KindOIDC {
		clientID := ""
		if a.store != nil {
			clientID, err = a.store.WebOIDCClientID()
			if err != nil {
				return credential.ConnectResult{}, err
			}
		}
		plan.Desc = withWebOIDCParams(plan.Desc, clientID, a.webOIDCRedirectURL)
	}
	res, err := a.broker.Acquire(ctx, plan.Subject, plan.Desc)
	if err != nil {
		return credential.ConnectResult{}, err
	}
	if res.Challenge != nil && plan.Subject.Scope == credential.ScopeTeam && !a.canManageCluster(clusterID) {
		// The shared account of a team is entered by a cluster administrator only: anybody else would connect with
		// credentials of their own choice as the team.
		_ = a.broker.Cancel(ctx, plan.Subject, res.Challenge.ID)
		return credential.ConnectResult{}, credential.ErrChallengeRequired
	}
	return a.connectResultOf(clusterID, plan, res)
}

// connectResultOf turns a broker result into the wire result: a challenge is remembered for this session, a token (or
// nothing, for static credentials) connects the cluster.
func (a *App) connectResultOf(clusterID string, plan k8s.ConnPlan, res credential.Result) (credential.ConnectResult, error) {
	if res.Challenge != nil {
		a.conn.remember(res.Challenge.ID, pendingChallenge{cluster: clusterID, idp: idpKey(plan.Desc, clusterID), sub: plan.Subject})
		a.recordAuthEvent(auditAuthChallenge, audit.ResultOK, clusterID, "", "kind", string(res.Challenge.Kind))
		return credential.ConnectResult{Status: "challenge", Challenge: res.Challenge}, nil
	}
	if plan.Desc.Kind != credential.KindStatic { // static credentials involve no sign-in
		a.recordAuthEvent(auditAuthSuccess, audit.ResultOK, clusterID, principalOf(res))
	}
	if err := a.finishConnect(clusterID); err != nil {
		return credential.ConnectResult{}, err
	}
	return credential.ConnectResult{Status: "connected"}, nil
}

// ConnectCluster connects to a cluster or returns the challenge the user has to answer.
func (a *App) ConnectCluster(clusterID string) (credential.ConnectResult, error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return credential.ConnectResult{}, err
	}
	return a.connect(context.Background(), clusterID)
}

// advance runs Answer or Poll for a challenge that belongs to this session and finishes the connection when the
// sign-in completes.
func (a *App) advance(challengeID string, run func(credential.Subject) (credential.Result, error)) (credential.ConnectResult, error) {
	pc, ok := a.conn.cluster(challengeID)
	if !ok {
		return credential.ConnectResult{}, credential.ErrChallengeExpired
	}
	clusterID := pc.cluster
	need := auth.PermRead
	if pc.sub.Scope == credential.ScopeTeam {
		need = auth.PermClusterManage // the shared account of a team
	}
	if err := a.authzCluster(need, clusterID); err != nil {
		return credential.ConnectResult{}, err
	}
	res, err := run(pc.sub)
	if err != nil {
		if errors.Is(err, credential.ErrChallengeExpired) {
			a.conn.forget(challengeID)
		}
		a.recordAuthEvent(auditAuthFailure, audit.ResultFailed, clusterID, "", "reason", authFailureReason(err))
		return credential.ConnectResult{}, err
	}
	if res.Challenge != nil {
		if res.Challenge.ID != challengeID { // a new challenge (a fallback), not the same one still waiting
			a.conn.forget(challengeID)
			a.recordAuthEvent(auditAuthChallenge, audit.ResultOK, clusterID, "", "kind", string(res.Challenge.Kind))
		}
		a.conn.remember(res.Challenge.ID, pc)
		return credential.ConnectResult{Status: "challenge", Challenge: res.Challenge}, nil
	}
	a.conn.forget(challengeID)
	a.recordAuthEvent(auditAuthSuccess, audit.ResultOK, clusterID, principalOf(res))
	if err := a.finishConnect(clusterID); err != nil {
		return credential.ConnectResult{}, err
	}
	a.emit(EventAuthCompleted, authEventData{ClusterID: clusterID, ChallengeID: challengeID})
	return credential.ConnectResult{Status: "connected"}, nil
}

// AnswerChallenge passes the user's answer (values are secret) to the broker.
func (a *App) AnswerChallenge(challengeID string, values map[string]string) (credential.ConnectResult, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return credential.ConnectResult{}, err
	}
	if a.broker == nil {
		return credential.ConnectResult{}, ErrSignInUnavailable
	}
	if err := a.limitAnswer(challengeID); err != nil {
		return credential.ConnectResult{}, err
	}
	return a.advance(challengeID, func(sub credential.Subject) (credential.Result, error) {
		return a.broker.Answer(context.Background(), sub, challengeID, credential.Answer(values))
	})
}

// CompleteWebOIDCCallback completes a browser redirect for the authenticated web session.
// It is a package function, not an App method, so it is not exposed through Wails or RPC.
func CompleteWebOIDCCallback(ctx context.Context, a *App, state, redirectURL string) error {
	if a == nil || a.sessionID == "" || state == "" || redirectURL == "" {
		return credential.ErrChallengeExpired
	}
	if a.auth == nil {
		return auth.ErrNotAuthenticated
	}
	if a.broker == nil {
		return ErrSignInUnavailable
	}
	cb, ok := a.broker.(credential.WebCallbackBroker)
	if !ok {
		return ErrSignInUnavailable
	}
	if _, err := a.auth.Current(); err != nil {
		return err
	}
	sub, desc, err := cb.CallbackSubject(ctx, a.sessionID, state)
	if err != nil {
		if errors.Is(err, credential.ErrChallengeExpired) {
			return credential.ErrChallengeExpired
		}
		return err
	}
	need := auth.PermRead
	if sub.Scope == credential.ScopeTeam {
		need = auth.PermClusterManage
	}
	if err := a.authzCluster(need, sub.ClusterID); err != nil {
		return err
	}
	if err := a.limitCallback(idpKey(desc, sub.ClusterID)); err != nil {
		return err
	}
	res, err := cb.AnswerCallback(ctx, sub, state, redirectURL)
	if err != nil {
		a.recordAuthEvent(auditAuthFailure, audit.ResultFailed, sub.ClusterID, "", "reason", authFailureReason(err))
		return err
	}
	if res.Token == nil || res.Challenge != nil {
		return credential.ErrChallengeExpired
	}
	a.conn.forget(state)
	a.recordAuthEvent(auditAuthSuccess, audit.ResultOK, sub.ClusterID, principalOf(res))
	if err := a.finishConnect(sub.ClusterID); err != nil {
		return err
	}
	a.emit(EventAuthCompleted, authEventData{ClusterID: sub.ClusterID, ChallengeID: state})
	return nil
}

// PollChallenge asks the broker whether a waiting sign-in (device code, browser) has completed.
func (a *App) PollChallenge(challengeID string) (credential.ConnectResult, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return credential.ConnectResult{}, err
	}
	if a.broker == nil {
		return credential.ConnectResult{}, ErrSignInUnavailable
	}
	return a.advance(challengeID, func(sub credential.Subject) (credential.Result, error) {
		return a.broker.Poll(context.Background(), sub, challengeID)
	})
}

// CancelChallenge abandons a sign-in of this session.
func (a *App) CancelChallenge(challengeID string) error {
	if err := a.authz(auth.PermRead); err != nil {
		return err
	}
	if a.broker == nil {
		return ErrSignInUnavailable
	}
	pc, ok := a.conn.cluster(challengeID)
	if !ok {
		return credential.ErrChallengeExpired
	}
	a.conn.forget(challengeID)
	return a.broker.Cancel(context.Background(), pc.sub, challengeID)
}

// ListMyConnections lists the sign-ins the current user has saved (no secrets).
func (a *App) ListMyConnections() ([]credential.ConnectionInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	if a.broker == nil {
		return []credential.ConnectionInfo{}, nil
	}
	u, err := a.auth.Current()
	if err != nil {
		return nil, err
	}
	list, err := a.broker.List(context.Background(), u.ID)
	if list == nil {
		list = []credential.ConnectionInfo{}
	}
	return list, err
}

// DisconnectCluster removes the current user's saved sign-in for a cluster.
func (a *App) DisconnectCluster(clusterID string) error {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return err
	}
	if a.broker == nil {
		return ErrSignInUnavailable
	}
	u, err := a.auth.Current()
	if err != nil {
		return err
	}
	if err := a.revokePersonal(u.ID, clusterID, "disconnect"); err != nil {
		return err
	}
	a.applyRevocations() // close this user's terminals, logs and port-forwards at once, not at the next check
	return nil
}

// principalOf is the Kubernetes subject of a completed sign-in; empty if the provider gave none.
func principalOf(res credential.Result) string {
	if res.Token != nil {
		return res.Token.Principal
	}
	return ""
}

// canManageCluster tells whether the user of this view administers the cluster (its team).
func (a *App) canManageCluster(clusterID string) bool {
	_, err := a.auth.CheckCluster(auth.PermClusterManage, clusterID)
	return err == nil
}
