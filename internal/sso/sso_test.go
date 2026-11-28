// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package sso

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"golang.org/x/oauth2"
)

func TestLoadConfigDefaultsAndValidation(t *testing.T) {
	env := map[string]string{
		"KUBRON_OIDC_ISSUER":    "https://id.example.test",
		"KUBRON_OIDC_CLIENT_ID": "kubron",
	}
	cfg, err := LoadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.Scopes, " "); got != "openid profile email" {
		t.Fatalf("scopes = %q", got)
	}
	if cfg.UsernameClaim != "preferred_username" || cfg.GroupsClaim != "groups" ||
		cfg.DefaultRole != "user" || !cfg.AutoCreate {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}

	env["KUBRON_OIDC_ISSUER"] = "https://id.example.test?next=https://evil.test"
	if _, err := LoadConfig(func(k string) string { return env[k] }); err == nil {
		t.Fatal("query in issuer accepted")
	}
	env["KUBRON_OIDC_ISSUER"] = "http://127.0.0.1:8080"
	env["KUBRON_DESKTOP_MODE"] = "true"
	if _, err := LoadConfig(func(k string) string { return env[k] }); err != nil {
		t.Fatalf("desktop loopback issuer rejected: %v", err)
	}
	env["KUBRON_OIDC_ISSUER"] = "http://example.test"
	if _, err := LoadConfig(func(k string) string { return env[k] }); err == nil {
		t.Fatal("non-loopback HTTP issuer accepted")
	}
	env["KUBRON_OIDC_ISSUER"] = "https://id.example.test"
	env["KUBRON_OIDC_LINK_EXISTING"] = "not-a-boolean"
	if _, err := LoadConfig(func(k string) string { return env[k] }); err == nil {
		t.Fatal("invalid boolean configuration accepted")
	}
}

func TestFlowStateSealOpenAndTamper(t *testing.T) {
	flow := FlowState{
		State:       "state",
		Nonce:       "nonce",
		Verifier:    "verifier",
		RedirectURL: "http://127.0.0.1/callback",
		CreatedAt:   time.Now().UTC(),
	}
	key := []byte("a sufficiently long master secret")
	sealed, err := flow.Seal(key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Open(key, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != flow.State || got.Nonce != flow.Nonce || got.Verifier != flow.Verifier {
		t.Fatalf("round trip mismatch: %#v", got)
	}
	if _, err := Open([]byte("different master secret"), sealed); err == nil {
		t.Fatal("wrong key accepted")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(sealed)
	raw[len(raw)-1] ^= 1
	if _, err := Open(key, base64.RawURLEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("tampered flow accepted")
	}

	flow.CreatedAt = time.Now().Add(-flowLifetime - time.Second)
	expired, err := flow.Seal(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(key, expired); err != ErrFlowExpired {
		t.Fatalf("expired flow error = %v", err)
	}
}

func TestRoleFor(t *testing.T) {
	p := &Provider{cfg: Config{
		DefaultRole:    "user",
		AdminGroups:    []string{"platform-admins"},
		OperatorGroups: []string{"ops"},
	}}
	cases := []struct {
		groups []string
		role   string
		mapped bool
	}{
		{groups: []string{"ops"}, role: "operator", mapped: true},
		{groups: []string{"ops", "platform-admins"}, role: "admin", mapped: true},
		{groups: []string{"developers"}, role: "user", mapped: false},
	}
	for _, tc := range cases {
		role, mapped := p.RoleFor(tc.groups)
		if role != tc.role || mapped != tc.mapped {
			t.Errorf("RoleFor(%v) = %q, %v", tc.groups, role, mapped)
		}
	}
}

func TestNormalizeUsername(t *testing.T) {
	cases := []struct {
		name      string
		candidate string
		email     string
		want      string
	}{
		{name: "preferred claim", candidate: "alice", email: "alice@example.test", want: "alice"},
		{name: "trim preferred claim", candidate: " alice ", want: "alice"},
		{name: "email local part fallback", candidate: "not a username", email: "alice@example.test", want: "alice"},
		{name: "no valid source", candidate: "bad name", email: "a@b", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeUsername(tc.candidate, tc.email); got != tc.want {
				t.Fatalf("normalizeUsername() = %q, want %q", got, tc.want)
			}
		})
	}
}

type fakeIDP struct {
	server            *httptest.Server
	key               *rsa.PrivateKey
	nonce             string
	verifierChallenge string
	claims            map[string]any
}

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIDP{key: key, claims: map[string]any{}}
	mux := http.NewServeMux()
	f.server = httptest.NewServer(mux)
	issuer := f.server.URL

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
			"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/jwks",
			"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
			"id_token_signing_alg_values_supported":          []string{"RS256"},
			"authorization_response_iss_parameter_supported": true,
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{
			Keys: []jose.JSONWebKey{{
				Key: &key.PublicKey, KeyID: "test-key", Algorithm: string(jose.RS256), Use: "sig",
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if sha256String(r.Form.Get("code_verifier")) != f.verifierChallenge {
			http.Error(w, "invalid verifier", http.StatusBadRequest)
			return
		}
		now := time.Now()
		claims := map[string]any{
			"iss": issuer, "sub": "subject-1", "aud": "kubron",
			"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "nonce": f.nonce,
		}
		for k, v := range f.claims {
			claims[k] = v
		}
		signer, err := jose.NewSigner(
			jose.SigningKey{Algorithm: jose.RS256, Key: key},
			(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"),
		)
		if err != nil {
			http.Error(w, "signing failed", http.StatusInternalServerError)
			return
		}
		raw, err := jwt.Signed(signer).Claims(claims).Serialize()
		if err != nil {
			http.Error(w, "signing failed", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access", "token_type": "Bearer", "expires_in": 3600, "id_token": raw,
		})
	})
	return f
}

func sha256String(s string) string {
	sum := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (f *fakeIDP) close() {
	f.server.Close()
}

func (f *fakeIDP) provider(t *testing.T) *Provider {
	t.Helper()
	cfg := Config{
		Issuer: f.server.URL, ClientID: "kubron", ClientSecret: "secret", Desktop: true,
		Scopes: []string{"openid", "profile", "email"}, UsernameClaim: "preferred_username",
		GroupsClaim: "groups", DefaultRole: "user",
	}
	p, err := NewProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *fakeIDP) begin(t *testing.T, p *Provider) (FlowState, url.Values) {
	t.Helper()
	redirect := "http://127.0.0.1:43210/callback"
	authURL, flow, err := p.Begin(redirect)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	f.nonce = q.Get("nonce")
	f.verifierChallenge = q.Get("code_challenge")
	return flow, url.Values{
		"state": {flow.State},
		"iss":   {f.server.URL},
		"code":  {"test-code"},
	}
}

func TestOIDCCompleteSuccessAndSecurityFailures(t *testing.T) {
	f := newFakeIDP(t)
	defer f.close()
	p := f.provider(t)
	f.claims = map[string]any{
		"preferred_username": "alice", "email": "alice@example.test",
		"email_verified": true, "name": "Alice", "groups": []string{"ops"},
	}
	flow, query := f.begin(t, p)
	identity, err := p.Complete(context.Background(), flow, query)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Subject != "subject-1" || identity.Username != "alice" ||
		identity.Email != "alice@example.test" || !identity.EmailVerified || len(identity.Groups) != 1 {
		t.Fatalf("unexpected identity: %#v", identity)
	}

	queryBad := url.Values{"state": {"wrong"}, "code": {"test-code"}}
	if _, err := p.Complete(context.Background(), flow, queryBad); err != ErrStateMismatch {
		t.Fatalf("state error = %v", err)
	}
	flow2, query2 := f.begin(t, p)
	query2.Set("iss", "https://attacker.example")
	if _, err := p.Complete(context.Background(), flow2, query2); err != ErrIssuerMismatch {
		t.Fatalf("issuer error = %v", err)
	}
	flow3, query3 := f.begin(t, p)
	query3.Set("error", "access_denied")
	query3.Set("error_description", "must not be copied to errors")
	if _, err := p.Complete(context.Background(), flow3, query3); err != ErrAuthorization {
		t.Fatalf("IdP error = %v", err)
	}
	flow4, query4 := f.begin(t, p)
	flow4.Verifier = oauth2.GenerateVerifier()
	if _, err := p.Complete(context.Background(), flow4, query4); err != ErrAuthorization {
		t.Fatalf("PKCE error = %v", err)
	}
	flow5, query5 := f.begin(t, p)
	f.claims["preferred_username"] = "bad name"
	if _, err := p.Complete(context.Background(), flow5, query5); err != ErrNoUsername {
		t.Fatalf("username error = %v", err)
	}
}

func TestCompleteRejectsWrongNonceAudienceAndExpiry(t *testing.T) {
	cases := []struct {
		name   string
		claims map[string]any
	}{
		{name: "nonce", claims: map[string]any{"nonce": "wrong"}},
		{name: "audience", claims: map[string]any{"aud": "other"}},
		{name: "expired", claims: map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeIDP(t)
			defer f.close()
			p := f.provider(t)
			flow, query := f.begin(t, p)
			f.claims = tc.claims
			if _, err := p.Complete(context.Background(), flow, query); err != ErrIDToken {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestProviderRetriesDiscoveryAfterTemporaryFailure(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	p, err := NewProvider(Config{Issuer: server.URL, ClientID: "kubron", Desktop: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.provider(context.Background()); err == nil {
		t.Fatal("expected discovery failure")
	}
	if _, err := p.provider(context.Background()); err == nil {
		t.Fatal("expected retry failure")
	}
	if calls != 2 {
		t.Fatalf("discovery calls = %d", calls)
	}
}
