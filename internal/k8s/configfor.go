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

	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/netpolicy"
)

// This file is the single place where a rest.Config of a stored cluster is built (ADR-0018, section 4).
// Every consumer (resources, Helm, terminal, logs, port-forward, metrics, Argo) gets its configuration from a
// connection built here; TestRestConfigIsBuiltOnlyInConfigFor forbids building one anywhere else.

// AuthSession is who connects: the sign-in broker and the identity of the view. A zero value (no broker) means the
// old path: the stored kubeconfig as it is, with exec plugins handled by the existing policy.
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
	legacy    bool
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

// Legacy reports that the cluster is connected without the broker (in-cluster, or no broker is configured).
func (p ConnPlan) Legacy() bool { return p.legacy }

// scopeFor says whose credentials a cluster has. Static credentials and the shared technical account of
// oidc-password (the choice of the team admin, migrated from task 3.7) belong to the team; every other interactive
// kind is personal. The owner of a team is its id ("default" where there are no teams).
func scopeFor(d credential.Descriptor, src ClusterAuthSource, userID string) (credential.Scope, string) {
	if d.Kind == credential.KindStatic || src.Mode == ModeOIDCPassword {
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
	if s.Broker == nil {
		p.legacy = true
		return p, nil
	}
	src, err := LoadClusterAuthSource(clusterID)
	if err != nil {
		return ConnPlan{}, err
	}
	p.src = src
	if src.Mode != "" && src.Mode != ModeOIDCPassword { // in-cluster builds its own configuration
		p.legacy = true
		return p, nil
	}
	d, err := s.Broker.Describe(src.Kubeconfig, src.ContextName)
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
	if p.legacy {
		return legacyRestConfig(p.ClusterID)
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

// legacyRestConfig is the configuration of a stored cluster without the broker: in-cluster and oidc-password
// modes, or the stored kubeconfig as it is under the connection policy. It is what buildClusterConnection used
// to do inline.
func legacyRestConfig(clusterID string) (*rest.Config, error) {
	meta, err := LoadClusterMeta(clusterID)
	if err != nil {
		return nil, fmt.Errorf("cluster metadata not found: %w", err)
	}
	// in-cluster and oidc-password build the configuration themselves (no exec plugin, no files of the server).
	if config, ok, err := buildModeConfig(clusterID, meta); ok {
		if err != nil {
			return nil, err
		}
		config.QPS = 50
		config.Burst = 100
		if meta.Mode != ModeInCluster { // the API server address of in-cluster is the cluster's own
			applyClusterAddrPolicy(config)
		}
		return config, nil
	}

	overrides := &clientcmd.ConfigOverrides{}
	if meta.ContextName != "" {
		overrides.CurrentContext = meta.ContextName
	}

	// The policy applies at connection time as well, not only when a cluster is added: neither clusters imported
	// from desktop files nor a kubeconfig saved before the restrictions were enabled get through.
	if pol := currentPolicy(); !pol.AllowExec || !pol.AllowLocalFiles || netpolicy.For(netpolicy.ScopeClusterAPI) != nil {
		if err := checkStoredPolicy(clusterID, meta); err != nil {
			return nil, err
		}
	}

	var config *rest.Config
	if kcPath := GetClusterKubeconfigPath(clusterID); kcPath != "" {
		config, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: kcPath},
			overrides,
		).ClientConfig()
	} else {
		var data []byte
		if data, err = clusterStore.Kubeconfig(clusterID); err != nil {
			return nil, fmt.Errorf("kubeconfig not found for cluster %s", clusterID)
		}
		var cfg *api.Config
		if cfg, err = clientcmd.Load(data); err != nil {
			return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
		}
		config, err = clientcmd.NewNonInteractiveClientConfig(*cfg, meta.ContextName, overrides, nil).ClientConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("configuration error: %w", err)
	}
	finishRestConfig(config)
	return config, nil
}
