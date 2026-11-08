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

// ConfigFor returns the rest.Config for the plan. For broker-managed sign-ins the kubeconfig's exec and
// auth-provider blocks are removed and the token is put into every request by a transport that asks the broker
// each time, so refresh and expiry are the broker's business and the configuration holds no secret.
func ConfigFor(p ConnPlan) (*rest.Config, error) {
	if p.inCluster {
		return inClusterRestConfig()
	}
	if p.Desc.Kind != credential.KindStatic && p.session.Broker == nil {
		return nil, errNoBroker
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
	tok, err := t.p.session.Broker.Token(req.Context(), t.p.Subject, t.p.Desc)
	if err != nil {
		if errors.Is(err, credential.ErrChallengeRequired) && t.p.session.OnAuthRequired != nil {
			t.p.session.OnAuthRequired(t.p.ClusterID)
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
	inv, ok := t.p.session.Broker.(credential.Invalidator)
	if !ok {
		return
	}
	inv.Invalidate(credential.Key{ClusterID: t.p.Subject.ClusterID, Scope: t.p.Subject.Scope, OwnerID: t.p.Subject.OwnerID})
	if _, err := t.p.session.Broker.Token(req.Context(), t.p.Subject, t.p.Desc); errors.Is(err, credential.ErrChallengeRequired) && t.p.session.OnAuthRequired != nil {
		t.p.session.OnAuthRequired(t.p.ClusterID)
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
