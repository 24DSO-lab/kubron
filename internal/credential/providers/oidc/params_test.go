// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/credential"
	"github.com/24DSO-lab/kubron/internal/idpnet"
	"github.com/24DSO-lab/kubron/internal/netpolicy"
)

func signInWithPassword(t *testing.T, idp *fakeIdP, env credential.Env, extra map[string]string) (credential.Result, error) {
	t.Helper()
	args := map[string]string{"username": "alice", "password": idp.users["alice"]}
	for k, v := range extra {
		args[k] = v
	}
	return New().Begin(context.Background(), env, credential.Request{Descriptor: idp.descriptor("password", args), Subject: subjectUser()})
}

func TestWebPublicClientOverridesClientSecret(t *testing.T) {
	d := credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{
		"issuer-url": "https://idp.example.test", "client-id": "kubeconfig-client",
		"client-secret": "must-not-be-used", "web.client-id": "web-public-client",
		"web.redirect-url": "https://kubron.example.test/auth/cluster/callback", "web.issuer-url": "https://idp.example.test",
	}}
	cfg, err := newConfig(d, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.clientID != "web-public-client" || cfg.clientSecret != "" {
		t.Fatalf("web public client config: clientID=%q secret=%q", cfg.clientID, cfg.clientSecret)
	}
}

func TestWebRedirectWithoutPublicClientIsIgnored(t *testing.T) {
	d := credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{
		"issuer-url": "https://idp.example.test", "client-id": "kubeconfig-client",
		"client-secret": "kubeconfig-secret", "web.redirect-url": "https://kubron.example.test/auth/cluster/callback",
		"web.issuer-url": "https://idp.example.test",
	}}
	cfg, err := newConfig(d, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.webRedirectURL != "" || cfg.clientID != "kubeconfig-client" || cfg.clientSecret != "kubeconfig-secret" {
		t.Fatalf("callback was enabled without public client: %+v", cfg)
	}
}

func TestWebClientIDWithoutRedirectDoesNotOverrideKubeconfig(t *testing.T) {
	d := credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{
		"issuer-url": "https://idp.example.test", "client-id": "kubeconfig-client",
		"client-secret": "kubeconfig-secret", "web.client-id": "admin-public-client",
		"web.issuer-url": "https://idp.example.test",
	}}
	cfg, err := newConfig(d, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.webRedirectURL != "" || cfg.clientID != "kubeconfig-client" || cfg.clientSecret != "kubeconfig-secret" {
		t.Fatalf("partial web callback config changed legacy auth: %+v", cfg)
	}
}

func TestWebRedirectURLValidation(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"https://kubron.example.test/auth/cluster/callback", true},
		{"http://localhost:8080/auth/cluster/callback", true},
		{"http://127.0.0.1:8080/auth/cluster/callback", true},
		{"http://kubron.example.test/auth/cluster/callback", false},
		{"https://kubron.example.test/other", false},
		{"https://user@kubron.example.test/auth/cluster/callback", false},
		{"javascript:alert(1)", false},
	} {
		if got := validWebRedirectURL(tc.url); got != tc.want {
			t.Errorf("validWebRedirectURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestExtraParamsCannotChangeAuthorizationResponseMode(t *testing.T) {
	d := credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{
		"issuer-url": "https://idp.example.test", "client-id": "client",
		"auth-request-extra-params": "response_mode=form_post&state=attacker",
	}}
	cfg, err := newConfig(d, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.extraAuth.Has("response_mode") || cfg.extraAuth.Has("state") {
		t.Fatalf("reserved OAuth parameters survived filtering: %v", cfg.extraAuth)
	}
}

func TestScopes(t *testing.T) {
	idp := newFakeIdP(t, false)
	res, err := signInWithPassword(t, idp, idp.env(true), map[string]string{"scope": "groups email openid groups"})
	mustToken(t, res, err)
	if got := idp.forms[0].Get("scope"); got != "openid groups email" {
		t.Fatalf("scope %q", got)
	}
}

func TestAccessTokenMode(t *testing.T) {
	idp := newFakeIdP(t, false)
	res, err := signInWithPassword(t, idp, idp.env(true), map[string]string{"use-access-token": "true"})
	tok := mustToken(t, res, err)
	if tok.Value != idp.lastAccess || tok.Value == idp.lastIDToken {
		t.Fatalf("the access token must be the credential")
	}
	if d := tok.Expiry.Sub(idp.clock.Now()); d != 300*time.Second {
		t.Fatalf("expiry follows expires_in, got %v", d)
	}
}

func TestPrincipal(t *testing.T) {
	idp := newFakeIdP(t, false)
	res, err := signInWithPassword(t, idp, idp.env(true), nil)
	if tok := mustToken(t, res, err); tok.Principal != "sub-alice" {
		t.Fatalf("principal %q: the sub claim comes first", tok.Principal)
	}
	idp.noSub = true
	res, err = signInWithPassword(t, idp, idp.env(true), nil)
	if tok := mustToken(t, res, err); tok.Principal != "alice" {
		t.Fatalf("principal %q: without sub the login is used", tok.Principal)
	}
}

func TestIDTokenIsVerified(t *testing.T) {
	for name, tc := range map[string]struct {
		set  func(*fakeIdP)
		want error
	}{
		"bad signature":   {func(f *fakeIdP) { f.badSignature = true }, errIDToken},
		"wrong audience":  {func(f *fakeIdP) { f.audOverride = "another-client" }, errIDToken},
		"foreign issuer":  {func(f *fakeIdP) { f.tokenIssuer = "https://evil.example.com" }, errIDToken},
		"expired":         {func(f *fakeIdP) { f.ttl = -time.Minute }, errIDToken},
		"no ID token":     {func(f *fakeIdP) { f.noIDToken = true }, errTokenResponse},
		"issuer mismatch": {func(f *fakeIdP) { f.issOverride = "https://evil.example.com/realms/kubron" }, errIssuerMismatch},
	} {
		idp := newFakeIdP(t, false)
		tc.set(idp)
		res, err := signInWithPassword(t, idp, idp.env(true), nil)
		if !errors.Is(err, tc.want) || res.Token != nil {
			t.Errorf("%s: got %v (token %v), want %v", name, err, res.Token != nil, tc.want)
		}
	}
}

func TestInsecureTLSAndFilesOnlyWhereAllowed(t *testing.T) {
	idp := newFakeIdP(t, true)
	hc, _ := idpnet.NewClient(idpnet.Options{})
	web := credential.Env{HTTP: hc, Now: idp.clock.Now}
	desk := web
	desk.Desktop = true

	// the web server refuses insecure-skip-tls-verify before any request
	_, err := signInWithPassword(t, idp, web, map[string]string{"insecure-skip-tls-verify": "true"})
	if !errors.Is(err, credential.ErrUnsupported) {
		t.Fatalf("web: got %v", err)
	}
	if n := idp.hit("/.well-known/openid-configuration"); n != 0 {
		t.Fatalf("refusal must come before the network (%d requests)", n)
	}
	// without it the provider's certificate is not trusted
	if _, err = signInWithPassword(t, idp, desk, nil); err == nil {
		t.Fatal("an untrusted certificate must fail")
	}
	// the desktop may skip verification
	res, err := signInWithPassword(t, idp, desk, map[string]string{"insecure-skip-tls-verify": "true"})
	mustToken(t, res, err)

	// nothing reads files, anywhere
	for _, key := range fileParams {
		for _, env := range []credential.Env{web, desk} {
			if _, err := signInWithPassword(t, idp, env, map[string]string{key: "/etc/ssl/ca.pem"}); !errors.Is(err, credential.ErrUnsupported) {
				t.Errorf("%s: got %v", key, err)
			}
		}
	}
}

func TestCertificateAuthorityData(t *testing.T) {
	idp := newFakeIdP(t, true)
	hc, _ := idpnet.NewClient(idpnet.Options{})
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: idp.srv.Certificate().Raw})
	for _, mode := range []bool{false, true} { // web and desktop
		env := credential.Env{HTTP: hc, Now: idp.clock.Now, Desktop: mode}
		res, err := signInWithPassword(t, idp, env, map[string]string{"ca-data": base64.StdEncoding.EncodeToString(caPEM)})
		mustToken(t, res, err)
	}
	env := credential.Env{HTTP: hc, Now: idp.clock.Now}
	if _, err := signInWithPassword(t, idp, env, map[string]string{"ca-data": "###"}); !errors.Is(err, idpnet.ErrBadCA) {
		t.Fatalf("got %v", err)
	}
}

func TestWebRefusesPlainHTTP(t *testing.T) {
	idp := newFakeIdP(t, false) // http://127.0.0.1
	hc, _ := idpnet.NewClient(idpnet.Options{})
	_, err := signInWithPassword(t, idp, credential.Env{HTTP: hc, Now: idp.clock.Now}, nil)
	if !errors.Is(err, errDiscovery) || !errors.Is(err, idpnet.ErrScheme) {
		t.Fatalf("got %v", err)
	}
	if n := idp.hit("/.well-known/openid-configuration"); n != 0 {
		t.Fatalf("the provider must not be contacted over http (%d)", n)
	}
}

// Addresses that come from the provider's own answer get the same check as the one from the kubeconfig.
func TestEndpointsFromDiscoveryAreChecked(t *testing.T) {
	netpolicy.Set(netpolicy.ScopeClusterAPI, netpolicy.New("test", netpolicy.ProfilePermissive, nil))
	t.Cleanup(func() { netpolicy.Set(netpolicy.ScopeClusterAPI, nil) })
	for _, ep := range []string{"auth", "token", "jwks", "device"} {
		idp := newFakeIdP(t, true)
		idp.badEndpoint = ep
		// the test provider itself is on loopback, which the policy forbids; allow only it
		netpolicy.Set(netpolicy.ScopeClusterAPI, netpolicy.New("test", netpolicy.ProfilePermissive, loopbackPrefixes()))
		_, err := signInWithPassword(t, idp, idp.env(false), nil)
		if !errors.Is(err, errDiscovery) {
			t.Errorf("%s endpoint at a metadata address: got %v", ep, err)
		}
		if idp.hit("/token") != 0 {
			t.Errorf("%s: the token endpoint was called", ep)
		}
	}
}

func TestParseListen(t *testing.T) {
	ok := map[string][]string{
		"":                                {"127.0.0.1:8000", "127.0.0.1:18000"},
		"127.0.0.1:9000":                  {"127.0.0.1:9000"},
		"localhost:9000, 127.0.0.1:19000": {"127.0.0.1:9000", "127.0.0.1:19000"},
		"[::1]:9000":                      {"[::1]:9000"},
	}
	for in, want := range ok {
		got, err := parseListen(in)
		if err != nil || strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"0.0.0.0:8000", "192.168.1.5:8000", "example.com:8000", "127.0.0.1", "127.0.0.1:abc", "127.0.0.1:99999", ","} {
		if _, err := parseListen(in); !errors.Is(err, credential.ErrUnsupported) {
			t.Errorf("%q accepted (%v)", in, err)
		}
	}
}

func TestExtraAuthParams(t *testing.T) {
	d := credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{"issuer-url": "https://idp", "client-id": "c",
		"auth-request-extra-params": "audience=api&prompt=login&redirect_uri=https://evil&state=x&scope=admin&nonce=n"}}
	cfg, err := newConfig(d, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.extraAuth.Get("audience") != "api" || cfg.extraAuth.Get("prompt") != "login" {
		t.Fatalf("%v", cfg.extraAuth)
	}
	for _, k := range []string{"redirect_uri", "state", "scope", "nonce"} {
		if _, ok := cfg.extraAuth[k]; ok {
			t.Errorf("%s must not be overridable", k)
		}
	}
	d.Params["grant"] = "client-credentials"
	if _, err := newConfig(d, nil, true); !errors.Is(err, credential.ErrUnsupported) {
		t.Errorf("unknown grant: %v", err)
	}
	if _, err := newConfig(credential.Descriptor{Params: map[string]string{"client-id": "c"}}, nil, true); !errors.Is(err, errConfig) {
		t.Errorf("missing issuer: %v", err)
	}
}

func TestWebClientIDOverridesKubeconfigOnlyInWeb(t *testing.T) {
	d := credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{
		"issuer-url": "https://idp.example", "client-id": "kubeconfig-client",
		"web.client-id": "admin-public-client", "web.redirect-url": "https://kubron.example/auth/cluster/callback",
		"web.issuer-url": "https://idp.example",
	}}
	web, err := newConfig(d, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if web.clientID != "admin-public-client" || web.webRedirectURL != "https://kubron.example/auth/cluster/callback" {
		t.Fatalf("web overrides not applied: client=%q redirect=%q", web.clientID, web.webRedirectURL)
	}
	desktop, err := newConfig(d, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if desktop.clientID != "kubeconfig-client" || desktop.webRedirectURL != "" {
		t.Fatalf("web-only settings affected desktop: client=%q redirect=%q", desktop.clientID, desktop.webRedirectURL)
	}
}

func TestIssuerParameter(t *testing.T) {
	d := &discovery{Issuer: "https://idp.example.com/realms/a"}
	for _, tc := range []struct {
		name string
		iss  string
		sent bool
		want error
	}{
		{"same", "https://idp.example.com/realms/a", false, nil},
		{"trailing slash", "https://idp.example.com/realms/a/", false, nil},
		{"other issuer", "https://evil.example.com", false, errMixup},
		{"absent, not announced", "", false, nil},
		{"absent, announced", "", true, errMixup},
	} {
		d.IssParameter = tc.sent
		q := url.Values{}
		if tc.iss != "" {
			q.Set("iss", tc.iss)
		}
		if err := d.checkIssuerParam(q); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

func TestSigningAlgorithms(t *testing.T) {
	for _, a := range []string{"RS256", "ES256", "PS384", "EdDSA"} {
		if !isAsymmetric(a) {
			t.Errorf("%s must be accepted", a)
		}
	}
	for _, a := range []string{"none", "HS256", "HS512", ""} {
		if isAsymmetric(a) {
			t.Errorf("%q must be refused", a)
		}
	}
}

func TestWebClientAppliesOnlyToAuthcodeOfTheSameIssuer(t *testing.T) {
	const issuer = "https://idp.example.test/realms/kubron"
	params := func(grant, webIssuer string) map[string]string {
		p := map[string]string{
			"issuer-url": issuer, "client-id": "kubeconfig-client", "client-secret": "kubeconfig-secret",
			"web.client-id": "admin-public-client", "web.redirect-url": "https://kubron.example.test/auth/cluster/callback",
		}
		if grant != "" {
			p["grant"] = grant
		}
		if webIssuer != "" {
			p["web.issuer-url"] = webIssuer
		}
		return p
	}
	for _, tc := range []struct {
		name, grant, webIssuer string
		applied                bool
	}{
		{"authcode of the same issuer", "authcode", issuer, true},
		{"default grant is authcode", "", issuer, true},
		{"same issuer written differently", "authcode", "HTTPS://IDP.example.test/realms/kubron/", true},
		{"password grant", "password", issuer, false},
		{"device-code grant", "device-code", issuer, false},
		{"authcode-keyboard grant", "authcode-keyboard", issuer, false},
		{"legacy-refresh grant", "legacy-refresh", issuer, false},
		{"another host", "authcode", "https://other-idp.example.test/realms/kubron", false},
		{"another realm of the same host", "authcode", "https://idp.example.test/realms/other", false},
		{"no issuer binding", "authcode", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := credential.Descriptor{Kind: credential.KindOIDC, Params: params(tc.grant, tc.webIssuer)}
			cfg, err := newConfig(d, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			if tc.applied {
				if cfg.clientID != "admin-public-client" || cfg.clientSecret != "" || cfg.webRedirectURL == "" {
					t.Fatalf("the web client was not applied: %+v", cfg)
				}
				return
			}
			if cfg.clientID != "kubeconfig-client" || cfg.clientSecret != "kubeconfig-secret" || cfg.webRedirectURL != "" {
				t.Fatalf("the web client changed a flow it does not belong to: %+v", cfg)
			}
		})
	}
}

func TestSameIssuer(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"https://idp.example.test/realms/kubron", "https://idp.example.test/realms/kubron", true},
		{"https://idp.example.test/realms/kubron", "https://idp.example.test/realms/kubron/", true},
		{"https://IDP.Example.test/realms/kubron", "https://idp.example.test/realms/kubron", true},
		{"https://idp.example.test:443/realms/kubron", "https://idp.example.test/realms/kubron", true},
		{"http://idp.example.test:80", "http://idp.example.test", true},
		{" https://idp.example.test ", "https://idp.example.test/", true},
		{"https://[::1]:8443/x", "https://[::1]:8443/x/", true},
		{"https://[::1]/x", "https://[::1]:443/x", true},
		{"https://idp.example.test:8443", "https://idp.example.test", false},
		{"https://idp.example.test/realms/kubron", "https://idp.example.test/realms/Kubron", false},
		{"https://idp.example.test/a", "https://idp.example.test/b", false},
		{"https://idp.example.test", "http://idp.example.test", false},
		{"https://idp.example.test", "https://idp.example.test.evil.test", false},
		{"https://idp.example.test/?x=1", "https://idp.example.test/?x=1", false},
		{"https://idp.example.test/?", "https://idp.example.test/?", false},
		{"https://idp.example.test/#a", "https://idp.example.test/#a", false},
		{"https://user@idp.example.test", "https://user@idp.example.test", false},
		{"", "", false},
		{"idp.example.test", "idp.example.test", false},
		{"https://", "https://", false},
		{"ftp://idp.example.test", "ftp://idp.example.test", false},
	} {
		if got := SameIssuer(tc.a, tc.b); got != tc.want {
			t.Errorf("SameIssuer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
