// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"sync"

	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/k8s"
)

// Events sent to the session that owns the sign-in (ADR-0018). The payloads carry identifiers only.
const (
	EventAuthCompleted = "cluster:auth-completed"
	EventAuthRequired  = "cluster:auth-required"
)

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
	mu      sync.Mutex
	pending map[string]string // challenge id -> cluster id, only for challenges this session started
}

func newConnectState() *connectState { return &connectState{pending: map[string]string{}} }

func (s *connectState) remember(challengeID, clusterID string) {
	s.mu.Lock()
	s.pending[challengeID] = clusterID
	s.mu.Unlock()
}

func (s *connectState) cluster(challengeID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.pending[challengeID]
	return id, ok
}

func (s *connectState) forget(challengeID string) {
	s.mu.Lock()
	delete(s.pending, challengeID)
	s.mu.Unlock()
}

// SetBroker sets the sign-in broker; called before OnStartup. A function of the package, not a method of App:
// it must not appear in the Wails bindings or the RPC.
func SetBroker(a *App, b credential.Broker) { a.broker = b }

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

// subjectFor builds the subject of a sign-in for the current user. Static credentials belong to the team, every
// interactive kind to the user (ADR-0018, section 3).
func (a *App) subjectFor(clusterID string, d credential.Descriptor, src k8s.ClusterAuthSource) (credential.Subject, error) {
	u, err := a.auth.Current()
	if err != nil {
		return credential.Subject{}, err
	}
	sub := credential.Subject{ClusterID: clusterID, SessionID: a.sessionID}
	if d.Kind == credential.KindStatic {
		sub.Scope, sub.OwnerID = credential.ScopeTeam, src.TeamID
		if sub.OwnerID == "" {
			sub.OwnerID = "default"
		}
		return sub, nil
	}
	sub.Scope, sub.OwnerID = credential.ScopeUser, u.ID
	return sub, nil
}

// userSubject is the subject for calls that carry only a challenge id: the broker binds the challenge to it.
func (a *App) userSubject(clusterID string) (credential.Subject, error) {
	u, err := a.auth.Current()
	if err != nil {
		return credential.Subject{}, err
	}
	return credential.Subject{ClusterID: clusterID, Scope: credential.ScopeUser, OwnerID: u.ID, SessionID: a.sessionID}, nil
}

// finishConnect makes the cluster the active one of this view and checks that it answers.
func (a *App) finishConnect(clusterID string) error {
	if a.finish != nil {
		return a.finish(clusterID)
	}
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
	src, err := k8s.LoadClusterAuthSource(clusterID)
	if err != nil {
		return credential.ConnectResult{}, err
	}
	if src.Mode != "" { // in-cluster and oidc-password build their own connection
		return credential.ConnectResult{Status: "connected"}, a.finishConnect(clusterID)
	}
	d, err := a.broker.Describe(src.Kubeconfig, src.ContextName)
	if err != nil {
		return credential.ConnectResult{}, err
	}
	sub, err := a.subjectFor(clusterID, d, src)
	if err != nil {
		return credential.ConnectResult{}, err
	}
	res, err := a.broker.Acquire(ctx, sub, d)
	if err != nil {
		return credential.ConnectResult{}, err
	}
	return a.resultOf(clusterID, res)
}

// resultOf turns a broker result into the wire result: a challenge is remembered for this session, a token (or
// nothing, for static credentials) connects the cluster.
func (a *App) resultOf(clusterID string, res credential.Result) (credential.ConnectResult, error) {
	if res.Challenge != nil {
		a.conn.remember(res.Challenge.ID, clusterID)
		return credential.ConnectResult{Status: "challenge", Challenge: res.Challenge}, nil
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
	clusterID, ok := a.conn.cluster(challengeID)
	if !ok {
		return credential.ConnectResult{}, credential.ErrChallengeExpired
	}
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return credential.ConnectResult{}, err
	}
	sub, err := a.userSubject(clusterID)
	if err != nil {
		return credential.ConnectResult{}, err
	}
	res, err := run(sub)
	if err != nil {
		if errors.Is(err, credential.ErrChallengeExpired) {
			a.conn.forget(challengeID)
		}
		return credential.ConnectResult{}, err
	}
	if res.Challenge != nil {
		if res.Challenge.ID != challengeID {
			a.conn.forget(challengeID)
		}
		a.conn.remember(res.Challenge.ID, clusterID)
		return credential.ConnectResult{Status: "challenge", Challenge: res.Challenge}, nil
	}
	a.conn.forget(challengeID)
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
	return a.advance(challengeID, func(sub credential.Subject) (credential.Result, error) {
		return a.broker.Answer(context.Background(), sub, challengeID, credential.Answer(values))
	})
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
	clusterID, ok := a.conn.cluster(challengeID)
	if !ok {
		return credential.ErrChallengeExpired
	}
	sub, err := a.userSubject(clusterID)
	if err != nil {
		return err
	}
	a.conn.forget(challengeID)
	return a.broker.Cancel(context.Background(), sub, challengeID)
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
	return a.broker.Revoke(context.Background(), credential.RevokeFilter{ClusterID: clusterID, OwnerID: u.ID, Scope: credential.ScopeUser})
}
