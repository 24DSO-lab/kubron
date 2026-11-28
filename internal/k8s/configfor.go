// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/24DSO-lab/kubron/internal/credential"
	"github.com/24DSO-lab/kubron/internal/identity"
)

// This file is the single place where a rest.Config of a stored cluster is built (ADR-0018, section 4).
// Every consumer (resources, Helm, terminal, logs, port-forward, metrics, Argo) gets its configuration from a
// connection built here; TestRestConfigIsBuiltOnlyInConfigFor forbids building one anywhere else.

// AuthSession is who connects: the sign-in broker and the identity of the view. Without a broker only clusters
// that need no sign-in (static credentials, in-cluster) can connect.
type AuthSession struct {
	Broker    credential.Broker
	UserID    string
	SessionID string
	// OnAuthRequired is called when a request finds that the saved sign-in is gone (challenge required). It may be
	// called on every request: the receiver debounces. nil is fine.
	OnAuthRequired func(clusterID string)
}

// ConnPlan is the decision how one view connects to one cluster: who owns the credentials, how they are obtained
// and under which key the connection lives in the shared pool.
type ConnPlan struct {
	ClusterID string
	Subject   credential.Subject
	Desc      credential.Descriptor
	Key       string // pool key: (cluster, subject)
	session   AuthSession
	inCluster bool
	anonymous bool // проверка адреса и TLS: токен брокера не подставляется
	src       ClusterAuthSource
	// principal is set only by PlanForPrincipal: a technical (Team/Service) connection. It carries the broker alone:
	// there is no AuthSession (no user, no web session, no interactive callback) behind it, and none is fabricated.
	principal *principalPlan
}

// principalPlan is what a technical connection needs from the outside: the broker that holds its credential.
type principalPlan struct {
	broker credential.Broker
}

// tokenBroker is the broker the transport asks for tokens: the technical plan's own, or the session's.
func (p ConnPlan) tokenBroker() credential.Broker {
	if p.principal != nil {
		return p.principal.broker
	}
	return p.session.Broker
}

// authRequired tells the interface that a sign-in is needed. A technical connection never does: there is no human to ask,
// the background operation is blocked by the broker's ErrChallengeRequired.
func (p ConnPlan) authRequired() {
	if p.principal == nil && p.session.OnAuthRequired != nil {
		p.session.OnAuthRequired(p.ClusterID)
	}
}

// poolKey is the key of the shared pool. Team-owned credentials keep the cluster id (nothing changes for them);
// personal credentials get their own entry per user, so an informer started with the rights of one user is never
// served to another.
func poolKey(clusterID string, sub credential.Subject) string {
	if sub.Scope == credential.ScopeUser && sub.OwnerID != "" {
		return userPoolKey(clusterID, sub.OwnerID)
	}
	return clusterID
}

// userPoolKey cannot clash with a cluster id: ids never contain a NUL byte.
func userPoolKey(clusterID, owner string) string { return clusterID + "\x00u\x00" + owner }

// principalPoolKey is the pool key of a technical connection: cluster, principal kind, principal id (with its organization
// and team, which the id alone does not imply) and binding. The tag "p" separates it from a legacy user key
// ("u") and from the bare cluster id of the legacy team account; the identifiers are validated and contain no NUL
// byte, so two different technical subjects cannot produce the same key. No secret is part of it.
func principalPoolKey(clusterID string, p identity.PrincipalRef, bindingID string) string {
	return clusterID + "\x00p\x00" + string(p.Kind) + "\x00" + p.ID + "\x00" + p.OrgID + "\x00" + p.TeamID + "\x00" + bindingID
}

// InCluster reports that the server itself runs in this cluster: it connects under its own ServiceAccount and
// there is no sign-in.
func (p ConnPlan) InCluster() bool { return p.inCluster }

// scopeFor says whose credentials a cluster has. Static credentials and the shared account of the team (the
// choice of a cluster administrator, ModeTeamAccount) belong to the team; every other interactive kind is
// personal. The owner of a team is its id ("default" where there are no teams).
func scopeFor(d credential.Descriptor, src ClusterAuthSource, userID string) (credential.Scope, string) {
	if d.Kind == credential.KindStatic || src.Mode == ModeTeamAccount {
		if src.TeamID == "" {
			return credential.ScopeTeam, "default"
		}
		return credential.ScopeTeam, src.TeamID
	}
	return credential.ScopeUser, userID
}

// PlanFor decides how the session connects to the cluster. It reads no secrets and makes no network calls.
func PlanFor(s AuthSession, clusterID string) (ConnPlan, error) {
	p := ConnPlan{ClusterID: clusterID, Key: clusterID, session: s}
	src, err := LoadClusterAuthSource(clusterID)
	if err != nil {
		return ConnPlan{}, err
	}
	p.src = src
	switch src.Mode {
	case ModeInCluster: // builds its own configuration, nothing to sign in to
		p.inCluster = true
		return p, nil
	case "", ModeKubeconfig, ModeTeamAccount:
	default:
		return ConnPlan{}, fmt.Errorf("unknown connection mode %q", src.Mode)
	}
	describe := credential.Describe // a pure function: the same answer with or without a broker
	if s.Broker != nil {
		describe = s.Broker.Describe
	}
	d, err := describe(src.Kubeconfig, src.ContextName)
	if err != nil {
		return ConnPlan{}, err
	}
	p.Desc = d
	p.Subject = credential.Subject{ClusterID: clusterID, SessionID: s.SessionID}
	p.Subject.Scope, p.Subject.OwnerID = scopeFor(d, src, s.UserID)
	p.Key = poolKey(clusterID, p.Subject)
	return p, nil
}

// ErrPrincipalPlan means that a technical connection cannot be planned: the principal is not a Team/Service principal, the
// binding is missing, the cluster belongs to another Team, the connection mode or the sign-in method is not usable by a
// technical identity, or no broker is given. The text names the reason without identifiers or secrets.
var ErrPrincipalPlan = errors.New("the technical connection cannot be planned")

func principalPlanError(reason string) error { return fmt.Errorf("%w: %s", ErrPrincipalPlan, reason) }

// PlanForPrincipal decides how a technical (Team or Service) principal connects to one cluster through its credential
// binding. It reads no secrets and makes no network calls, like PlanFor; ConfigFor remains the only constructor of the
// rest.Config.
//
// It refuses a UserPrincipal, a missing binding id, a Team principal whose Team does not own the cluster (the stored
// cluster's team is the trusted fact), an in-cluster cluster, and a cluster whose sign-in is static (the identity of the
// kubeconfig is the shared account, which a technical principal must never fall back to). The plan has no interactive
// callback and no AuthSession; whether the binding is active is decided by the broker on every token request.
func PlanForPrincipal(b credential.Broker, principal identity.PrincipalRef, bindingID, clusterID string) (ConnPlan, error) {
	if b == nil {
		return ConnPlan{}, principalPlanError("no sign-in broker")
	}
	if principal.Kind == identity.PrincipalUser {
		return ConnPlan{}, principalPlanError("a user is not a technical principal")
	}
	sub := credential.Subject{ClusterID: clusterID, Scope: credential.ScopePrincipal, Principal: principal, BindingID: bindingID}
	if bindingID == "" {
		return ConnPlan{}, principalPlanError("the credential binding is missing")
	}
	if err := sub.Validate(); err != nil {
		return ConnPlan{}, principalPlanError("invalid principal, binding or cluster")
	}
	src, err := LoadClusterAuthSource(clusterID)
	if err != nil {
		return ConnPlan{}, err
	}
	switch src.Mode {
	case "", ModeKubeconfig, ModeTeamAccount:
	case ModeInCluster:
		return ConnPlan{}, principalPlanError("the cluster uses the server's own account")
	default:
		return ConnPlan{}, principalPlanError("unsupported connection mode")
	}
	if principal.Kind == identity.PrincipalTeam && src.TeamID != principal.TeamID {
		return ConnPlan{}, principalPlanError("the cluster belongs to another team")
	}
	d, err := b.Describe(src.Kubeconfig, src.ContextName)
	if err != nil {
		return ConnPlan{}, err
	}
	if d.Kind == credential.KindStatic {
		return ConnPlan{}, principalPlanError("the cluster has a static sign-in")
	}
	return ConnPlan{ClusterID: clusterID, Subject: sub, Desc: d, Key: principalPoolKey(clusterID, principal, bindingID),
		src: src, principal: &principalPlan{broker: b}}, nil
}

// ConfigFor returns the rest.Config for the plan. For broker-managed sign-ins the kubeconfig's exec and
// auth-provider blocks are removed and the token is put into every request by a transport that asks the broker
// each time, so refresh and expiry are the broker's business and the configuration holds no secret.
func ConfigFor(p ConnPlan) (*rest.Config, error) {
	if p.inCluster {
		return inClusterRestConfig()
	}
	if p.Desc.Kind != credential.KindStatic && p.tokenBroker() == nil {
		return nil, errNoBroker
	}
	if p.principal != nil && p.Desc.Kind == credential.KindStatic {
		// A technical connection has no static identity of its own; the kubeconfig's one is the shared account.
		return nil, principalPlanError("the cluster has a static sign-in")
	}
	cfg, err := loadStoredKubeconfig(p.ClusterID, p.src.ContextName)
	if err != nil {
		return nil, err
	}
	ctxName := p.src.ContextName
	if ctxName == "" {
		if ctxName, err = pickContext(cfg, ""); err != nil {
			return nil, err
		}
	}
	// Address policy and local files only: the sign-in method is the broker's business, and an exec block is
	// never run here.
	if err := checkClusterAddress(cfg, ctxName); err != nil {
		return nil, err
	}
	if !currentPolicy().AllowLocalFiles {
		if err := checkLocalFiles(cfg, ctxName); err != nil {
			return nil, err
		}
	}
	if c := cfg.Contexts[ctxName]; c != nil {
		if ai := cfg.AuthInfos[c.AuthInfo]; ai != nil {
			ai.Exec, ai.AuthProvider = nil, nil
		}
	}
	rc, err := clientcmd.NewNonInteractiveClientConfig(*cfg, ctxName, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("configuration error: %w", err)
	}
	if p.Desc.Kind != credential.KindStatic && !p.anonymous {
		rc.Wrap(brokerTransport(p))
	}
	finishRestConfig(rc)
	return rc, nil
}

// finishRestConfig applies what every cluster connection gets: client limits and the address policy.
func finishRestConfig(rc *rest.Config) {
	rc.QPS = 50
	rc.Burst = 100
	applyClusterAddrPolicy(rc)
}

// loadStoredKubeconfig reads the stored kubeconfig of a cluster. The file store keeps a file, and client-go
// resolves relative paths inside it from the directory of the file; other stores give bytes.
func loadStoredKubeconfig(clusterID, _ string) (*api.Config, error) {
	if path := GetClusterKubeconfigPath(clusterID); path != "" {
		if cfg, err := clientcmd.LoadFromFile(path); err == nil {
			return cfg, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
		}
	}
	data, err := clusterStore.Kubeconfig(clusterID)
	if err != nil {
		return nil, fmt.Errorf("kubeconfig not found for cluster %s", clusterID)
	}
	cfg, err := clientcmd.Load(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
	}
	return cfg, nil
}

// brokerRoundTripper puts the broker's token into each request. The token is not stored, logged or put into an
// error: only the broker's own (already scrubbed) error is returned.
type brokerRoundTripper struct {
	next http.RoundTripper
	p    ConnPlan
}

func brokerTransport(p ConnPlan) func(http.RoundTripper) http.RoundTripper {
	return func(next http.RoundTripper) http.RoundTripper { return &brokerRoundTripper{next: next, p: p} }
}

func (t *brokerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	tok, err := t.p.tokenBroker().Token(req.Context(), t.p.Subject, t.p.Desc)
	if err != nil {
		if errors.Is(err, credential.ErrChallengeRequired) {
			t.p.authRequired()
		}
		return nil, err
	}
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+tok.Value)
	resp, err := t.next.RoundTrip(r)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		t.rejected(req)
	}
	return resp, err
}

// rejected handles a 401 from the API server: the token the broker handed out is no longer accepted. The cached
// token is dropped (a token without expiry lives until exactly this moment, as in client-go) and the request is NOT
// repeated: the caller sees the 401, and the next request gets a fresh token. If a new token cannot be had without the
// user, the interface is told to ask for a sign-in.
func (t *brokerRoundTripper) rejected(req *http.Request) {
	inv, ok := t.p.tokenBroker().(credential.Invalidator)
	if !ok {
		return
	}
	sub := t.p.Subject
	inv.Invalidate(credential.Key{ClusterID: sub.ClusterID, Scope: sub.Scope, OwnerID: sub.OwnerID, Principal: sub.Principal, BindingID: sub.BindingID})
	if _, err := t.p.tokenBroker().Token(req.Context(), sub, t.p.Desc); errors.Is(err, credential.ErrChallengeRequired) {
		t.p.authRequired()
	}
}

var errNoBroker = errors.New("this cluster needs a sign-in, but no sign-in broker is configured")

// inClusterRestConfig is the configuration of the cluster the server itself runs in.
func inClusterRestConfig() (*rest.Config, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster configuration: %w", err)
	}
	cfg.QPS = 50
	cfg.Burst = 100 // the API server address of in-cluster is the cluster's own: no address policy
	return cfg, nil
}

// DropPrincipalConnection closes the pooled connection (informers) of one technical subject. It is called when a binding
// is revoked or a credential is replaced; it never touches the connection of another principal, binding or user.
func (m *Manager) DropPrincipalConnection(clusterID string, principal identity.PrincipalRef, bindingID string) {
	key := principalPoolKey(clusterID, principal, bindingID)
	m.pool.removeIf(func(c *ClusterConnection) bool { return c.key == key })
}
