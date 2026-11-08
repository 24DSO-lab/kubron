// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/credential"
	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/signin"
	"github.com/24DSO-lab/kubron/internal/store"
)

// Events sent to the session that owns the sign-in (ADR-0018). The payloads carry identifiers only.
const (
	EventAuthCompleted = signin.EventAuthCompleted
	EventAuthRequired  = signin.EventAuthRequired
)

// ErrSignInUnavailable is returned by the new sign-in methods when no broker is configured.
var ErrSignInUnavailable = signin.ErrUnavailable

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
			if a.conn != nil && a.conn.AuthRequiredDue(clusterID, time.Now()) {
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

// connect is the body of ConnectCluster. Without a broker, and for
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
	if plan.InCluster() { // in-cluster: no sign-in
		return credential.ConnectResult{Status: "connected"}, a.finishConnect(clusterID)
	}
	if a.sessionID != "" && plan.Desc.Kind == credential.KindOIDC {
		var web store.WebOIDC
		if a.store != nil {
			web, err = a.store.WebOIDC()
			if err != nil {
				return credential.ConnectResult{}, err
			}
		}
		plan.Desc = signin.WithWebOIDCParams(plan.Desc, web, a.webOIDCRedirectURL)
	}
	res, err := a.broker.Acquire(ctx, plan.Subject, plan.Desc)
	if err != nil {
		return credential.ConnectResult{}, err
	}
	if res.Challenge != nil && plan.Subject.Scope == credential.ScopeTeam && !a.canManageCluster(clusterID) {
		// The shared account of a team is entered by a cluster administrator only: anybody else would connect with
		// credentials of their own choice as the team. The challenge is withdrawn even if ctx has already ended.
		cctx, cancel := a.detachedCtx(storeTimeout)
		defer cancel()
		_ = a.broker.Cancel(cctx, plan.Subject, res.Challenge.ID)
		return credential.ConnectResult{}, signin.SharedAccountNotSignedIn{}
	}
	return a.connectResultOf(clusterID, plan, res)
}

// connectResultOf turns a broker result into the wire result: a challenge is remembered for this session, a token (or
// nothing, for static credentials) connects the cluster.
func (a *App) connectResultOf(clusterID string, plan k8s.ConnPlan, res credential.Result) (credential.ConnectResult, error) {
	if res.Challenge != nil {
		a.conn.Remember(res.Challenge.ID, signin.Pending{Cluster: clusterID, IdP: signin.IDPKey(plan.Desc, clusterID), Sub: plan.Subject})
		a.recordAuthEvent(auditAuthChallenge, audit.ResultOK, clusterID, "", "kind", string(res.Challenge.Kind))
		return credential.ConnectResult{Status: "challenge", Challenge: res.Challenge}, nil
	}
	if plan.Desc.Kind != credential.KindStatic { // static credentials involve no sign-in
		a.recordAuthEvent(auditAuthSuccess, audit.ResultOK, clusterID, signin.PrincipalOf(res))
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
	ctx, cancel := a.opCtx(signInTimeout)
	defer cancel()
	return a.connect(ctx, clusterID)
}

// advance runs Answer or Poll for a challenge that belongs to this session and finishes the connection when the
// sign-in completes.
func (a *App) advance(challengeID string, run func(credential.Subject) (credential.Result, error)) (credential.ConnectResult, error) {
	pc, ok := a.conn.Lookup(challengeID)
	if !ok {
		return credential.ConnectResult{}, credential.ErrChallengeExpired
	}
	clusterID := pc.Cluster
	need := auth.PermRead
	if pc.Sub.Scope == credential.ScopeTeam {
		need = auth.PermClusterManage // the shared account of a team
	}
	if err := a.authzCluster(need, clusterID); err != nil {
		return credential.ConnectResult{}, err
	}
	res, err := run(pc.Sub)
	if err != nil {
		if errors.Is(err, credential.ErrChallengeExpired) {
			a.conn.Forget(challengeID)
		}
		a.recordAuthEvent(auditAuthFailure, audit.ResultFailed, clusterID, "", "reason", authFailureReason(err))
		return credential.ConnectResult{}, err
	}
	if res.Challenge != nil {
		if res.Challenge.ID != challengeID { // a new challenge (a fallback), not the same one still waiting
			a.conn.Forget(challengeID)
			a.recordAuthEvent(auditAuthChallenge, audit.ResultOK, clusterID, "", "kind", string(res.Challenge.Kind))
		}
		a.conn.Remember(res.Challenge.ID, pc)
		return credential.ConnectResult{Status: "challenge", Challenge: res.Challenge}, nil
	}
	a.conn.Forget(challengeID)
	a.recordAuthEvent(auditAuthSuccess, audit.ResultOK, clusterID, signin.PrincipalOf(res))
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
	ctx, cancel := a.opCtx(signInTimeout)
	defer cancel()
	return a.advance(challengeID, func(sub credential.Subject) (credential.Result, error) {
		return a.broker.Answer(ctx, sub, challengeID, credential.Answer(values))
	})
}

// CompleteWebOIDCCallback completes a browser redirect for the authenticated web session.
// It is a package function, not an App method, so it is not exposed through Wails or RPC.
func CompleteWebOIDCCallback(ctx context.Context, a *App, state, redirectURL string) error {
	if a == nil || a.sessionID == "" || state == "" || redirectURL == "" {
		return credential.ErrChallengeExpired
	}
	// The request context ends with the browser connection; the time of the exchange with the IdP is bounded too.
	ctx, cancel := context.WithTimeout(ctx, signInTimeout)
	defer cancel()
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
	if err := a.limitCallback(signin.IDPKey(desc, sub.ClusterID)); err != nil {
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
	a.conn.Forget(state)
	a.recordAuthEvent(auditAuthSuccess, audit.ResultOK, sub.ClusterID, signin.PrincipalOf(res))
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
	ctx, cancel := a.opCtx(signInTimeout)
	defer cancel()
	return a.advance(challengeID, func(sub credential.Subject) (credential.Result, error) {
		return a.broker.Poll(ctx, sub, challengeID)
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
	pc, ok := a.conn.Lookup(challengeID)
	if !ok {
		return credential.ErrChallengeExpired
	}
	a.conn.Forget(challengeID)
	ctx, cancel := a.detachedCtx(storeTimeout) // the request is withdrawn even if the view is being released
	defer cancel()
	return a.broker.Cancel(ctx, pc.Sub, challengeID)
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
	ctx, cancel := a.opCtx(storeTimeout)
	defer cancel()
	list, err := a.broker.List(ctx, u.ID)
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

// canManageCluster tells whether the user of this view administers the cluster (its team).
func (a *App) canManageCluster(clusterID string) bool {
	_, err := a.auth.CheckCluster(auth.PermClusterManage, clusterID)
	return err == nil
}
