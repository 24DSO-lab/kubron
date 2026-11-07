// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/credential"
	"github.com/24DSO-lab/kubron/internal/idpnet"
)

const (
	testClient = "kubron"
	testSecret = "CANARY-client-secret-5f3a"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type authCode struct {
	user, nonce, challenge, redirect string
}

type deviceGrant struct {
	user                string // set when approved
	denied              bool
	expires             time.Time
	interval            time.Duration
	lastPoll            time.Time
	slowDownPenaltyDone bool
}

// fakeIdP is a Keycloak-like identity provider: discovery, token (password, refresh_token,
// authorization_code with PKCE, device_code), device authorization and JWKS. It records what it was asked.
type fakeIdP struct {
	t      *testing.T
	srv    *httptest.Server
	key    *rsa.PrivateKey
	clock  *fakeClock
	secret string // required client secret; "" for a public client

	mu            sync.Mutex
	users         map[string]string
	refresh       map[string]string    // refresh token -> user (single use: rotation with reuse detection)
	codes         map[string]*authCode // authorization code -> data (single use)
	devices       map[string]*deviceGrant
	deviceEnabled bool
	issParam      bool     // advertise and send RFC 9207 iss
	issOverride   string   // issuer put into ID tokens and discovery instead of the real one
	audOverride   string   // audience of ID tokens
	noRefresh     bool     // do not issue refresh tokens
	authMethods   []string // token_endpoint_auth_methods_supported
	badSignature  bool
	hits          map[string]int
	forms         []url.Values
	sawBasic      bool
	lastIDToken   string
	lastAccess    string
	lastRefresh   string
	badEndpoint   string // name of a discovery endpoint (auth, token, jwks, device) that points at a forbidden address
	noSub         bool
	deviceRefuse  bool          // the device endpoint is advertised but answers unauthorized_client
	deviceTTL     time.Duration // real lifetime of device codes (default 10 minutes)
	deviceEvery   time.Duration // real minimum polling interval (default 5 seconds; always advertised as 5)
	deviceURI     string        // verification_uri override
	nonceOverride string        // nonce put into ID tokens of the code flow instead of the requested one
	noIDToken     bool
	tokenIssuer   string        // issuer inside ID tokens only
	ttl           time.Duration // lifetime of ID tokens (default 5 minutes)
	counter       int
	tokenStatus   int // when not 0, the token endpoint answers with this HTTP status and no OAuth error
}

func newFakeIdP(t *testing.T, tls bool) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{t: t, key: key, clock: &fakeClock{t: time.Now()}, users: map[string]string{"alice": "s3cret-CANARY-pw"},
		refresh: map[string]string{}, codes: map[string]*authCode{}, devices: map[string]*deviceGrant{},
		hits: map[string]int{}, deviceEnabled: true}
	h := http.HandlerFunc(f.serve)
	if tls {
		f.srv = httptest.NewTLSServer(h)
	} else {
		f.srv = httptest.NewServer(h)
	}
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) issuer() string { return f.srv.URL + "/realms/kubron" }

func (f *fakeIdP) next(prefix string) string {
	f.counter++
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%d-%s", prefix, f.counter, base64.RawURLEncoding.EncodeToString(b))
}

func (f *fakeIdP) hit(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[name]
}

func (f *fakeIdP) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/realms/kubron")
	f.hits[path]++
	switch path {
	case "/.well-known/openid-configuration":
		f.discovery(w)
	case "/jwks":
		f.jwks(w)
	case "/token":
		if f.tokenStatus != 0 {
			w.WriteHeader(f.tokenStatus)
			return
		}
		f.token(w, r)
	case "/device":
		f.device(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeIdP) discovery(w http.ResponseWriter) {
	ep := func(name, path string) string {
		if f.badEndpoint == name {
			return "https://169.254.169.254" + path
		}
		return f.issuer() + path
	}
	doc := map[string]any{
		"issuer":                 f.issuer(),
		"authorization_endpoint": ep("auth", "/auth"),
		"token_endpoint":         ep("token", "/token"),
		"jwks_uri":               ep("jwks", "/jwks"),
	}
	if f.issOverride != "" {
		doc["issuer"] = f.issOverride
	}
	if f.deviceEnabled {
		doc["device_authorization_endpoint"] = ep("device", "/device")
	}
	if f.issParam {
		doc["authorization_response_iss_parameter_supported"] = true
	}
	if f.authMethods != nil {
		doc["token_endpoint_auth_methods_supported"] = f.authMethods
	}
	writeJSON(w, 200, doc)
}

func (f *fakeIdP) jwks(w http.ResponseWriter) {
	writeJSON(w, 200, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
		"n": b64(f.key.N.Bytes()), "e": b64(big.NewInt(int64(f.key.E)).Bytes()),
	}}})
}

func (f *fakeIdP) clientOK(r *http.Request) bool {
	id, secret, basic := r.BasicAuth()
	if basic {
		f.sawBasic = true
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	return id == testClient && secret == f.secret
}

func (f *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.forms = append(f.forms, r.PostForm)
	if !f.clientOK(r) {
		writeJSON(w, 401, map[string]string{"error": "invalid_client"})
		return
	}
	form := r.PostForm
	switch form.Get("grant_type") {
	case "password":
		if pw, ok := f.users[form.Get("username")]; !ok || pw != form.Get("password") {
			writeJSON(w, 401, map[string]string{"error": "invalid_grant", "error_description": "Invalid user credentials for " + form.Get("username")})
			return
		}
		f.issue(w, form.Get("username"), "")
	case "refresh_token":
		user, ok := f.refresh[form.Get("refresh_token")]
		if !ok {
			writeJSON(w, 400, map[string]string{"error": "invalid_grant", "error_description": "Token is not active"})
			return
		}
		delete(f.refresh, form.Get("refresh_token"))
		f.issue(w, user, "")
	case "authorization_code":
		c, ok := f.codes[form.Get("code")]
		delete(f.codes, form.Get("code")) // one time
		sum := sha256.Sum256([]byte(form.Get("code_verifier")))
		if !ok || c.redirect != form.Get("redirect_uri") || b64(sum[:]) != c.challenge {
			writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
			return
		}
		f.issue(w, c.user, c.nonce)
	case "urn:ietf:params:oauth:grant-type:device_code":
		f.pollDevice(w, form.Get("device_code"))
	default:
		writeJSON(w, 400, map[string]string{"error": "unsupported_grant_type"})
	}
}

func (f *fakeIdP) pollDevice(w http.ResponseWriter, code string) {
	dg, ok := f.devices[code]
	now := f.clock.Now()
	switch {
	case !ok:
		writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
	case now.After(dg.expires):
		writeJSON(w, 400, map[string]string{"error": "expired_token"})
	case !dg.lastPoll.IsZero() && now.Sub(dg.lastPoll) < dg.interval:
		if !dg.slowDownPenaltyDone { // the provider asks for 5 seconds more, once
			dg.interval += 5 * time.Second
			dg.slowDownPenaltyDone = true
		}
		dg.lastPoll = now
		writeJSON(w, 400, map[string]string{"error": "slow_down"})
	case dg.denied:
		writeJSON(w, 400, map[string]string{"error": "access_denied"})
	case dg.user == "":
		dg.lastPoll = now
		writeJSON(w, 400, map[string]string{"error": "authorization_pending"})
	default:
		delete(f.devices, code)
		f.issue(w, dg.user, "")
	}
}

func (f *fakeIdP) device(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.forms = append(f.forms, r.PostForm)
	if !f.deviceEnabled || f.deviceRefuse {
		writeJSON(w, 400, map[string]string{"error": "unauthorized_client"})
		return
	}
	if !f.clientOK(r) {
		writeJSON(w, 401, map[string]string{"error": "invalid_client"})
		return
	}
	code := f.next("device")
	ttl, every := f.deviceTTL, f.deviceEvery
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	if every == 0 {
		every = 5 * time.Second
	}
	f.devices[code] = &deviceGrant{expires: f.clock.Now().Add(ttl), interval: every}
	uri, complete := f.issuer()+"/device-login", f.issuer()+"/device-login?user_code=ABCD-EFGH"
	if f.deviceURI != "" {
		uri, complete = f.deviceURI, ""
	}
	writeJSON(w, 200, map[string]any{
		"device_code": code, "user_code": "ABCD-EFGH", "verification_uri": uri,
		"verification_uri_complete": complete, "expires_in": 600, "interval": 5,
	})
}

// issue answers with a full token set; the caller holds f.mu.
func (f *fakeIdP) issue(w http.ResponseWriter, user, nonce string) {
	iss := f.issuer()
	if f.tokenIssuer != "" {
		iss = f.tokenIssuer
	}
	aud := testClient
	if f.audOverride != "" {
		aud = f.audOverride
	}
	now := f.clock.Now()
	ttl := f.ttl
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	claims := map[string]any{"iss": iss, "sub": "sub-" + user, "aud": aud, "iat": now.Unix(), "exp": now.Add(ttl).Unix(),
		"preferred_username": user}
	if f.noSub {
		delete(claims, "sub")
	}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	idt := f.sign(claims)
	if f.badSignature {
		idt = idt[:len(idt)-6] + "AAAAAA"
	}
	if f.noIDToken {
		idt = ""
	}
	resp := map[string]any{"access_token": f.next("access"), "id_token": idt, "token_type": "Bearer", "expires_in": 300}
	if !f.noRefresh {
		rt := f.next("refresh")
		f.refresh[rt] = user
		resp["refresh_token"] = rt
		f.lastRefresh = rt
	}
	f.lastIDToken, f.lastAccess = idt, resp["access_token"].(string)
	writeJSON(w, 200, resp)
}

func (f *fakeIdP) sign(claims map[string]any) string {
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	pl, _ := json.Marshal(claims)
	in := b64(hdr) + "." + b64(pl)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		f.t.Fatal(err)
	}
	return in + "." + b64(sig)
}

// authorize plays the browser: it checks the authorization request, signs the user in and returns the
// redirect the provider would send the browser to.
func (f *fakeIdP) authorize(authURL, user string) string {
	f.t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		f.t.Fatal(err)
	}
	q := u.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != testClient || q.Get("code_challenge_method") != "S256" ||
		q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("nonce") == "" || q.Get("redirect_uri") == "" {
		f.t.Fatalf("malformed authorization request: %v", q)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	code := f.next("code")
	nonce := q.Get("nonce")
	if f.nonceOverride != "" {
		nonce = f.nonceOverride
	}
	f.codes[code] = &authCode{user: user, nonce: nonce, challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri")}
	back, _ := url.Parse(q.Get("redirect_uri"))
	if back.Path == "" {
		back.Path = "/" // a browser shows the empty path as "/"
	}
	bq := back.Query()
	bq.Set("code", code)
	bq.Set("state", q.Get("state"))
	if f.issParam {
		bq.Set("iss", f.issuer())
	}
	back.RawQuery = bq.Encode()
	return back.String()
}

// approveDevice marks every pending device grant as approved by user (or denied).
func (f *fakeIdP) approveDevice(user string, deny bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.devices {
		d.user, d.denied = user, deny
	}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- helpers shared by the tests ----

func (f *fakeIdP) descriptor(grant string, extra map[string]string) credential.Descriptor {
	p := map[string]string{"grant": grant, "issuer-url": f.issuer(), "client-id": testClient}
	if f.secret != "" {
		p["client-secret"] = f.secret
	}
	for k, v := range extra {
		p[k] = v
	}
	return credential.Descriptor{Kind: credential.KindOIDC, Params: p}
}

func (f *fakeIdP) env(desktop bool) credential.Env {
	hc, err := idpnet.NewClient(idpnet.Options{Desktop: true}) // httptest servers are http on loopback
	if desktop && err != nil {
		f.t.Fatal(err)
	}
	if !desktop {
		hc = f.srv.Client() // web mode over TLS: trust the test certificate
	}
	return credential.Env{HTTP: hc, Now: f.clock.Now, Desktop: desktop}
}

func subjectUser() credential.Subject {
	return credential.Subject{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1"}
}

func subjectTeam() credential.Subject {
	return credential.Subject{ClusterID: "c1", Scope: credential.ScopeTeam, OwnerID: "t1"}
}

func mustToken(t *testing.T, res credential.Result, err error) credential.Token {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Token == nil {
		t.Fatalf("expected a token, got %+v", res)
	}
	return *res.Token
}

func mustChallenge(t *testing.T, res credential.Result, err error, kind credential.ChallengeKind) credential.Challenge {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Challenge == nil || res.Challenge.Kind != kind {
		t.Fatalf("expected a %s challenge, got %+v", kind, res)
	}
	return *res.Challenge
}

func loopbackPrefixes() []netip.Prefix {
	return []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
}
