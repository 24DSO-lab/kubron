// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package gke

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// Sign-in of a Google user: the authorization-code flow with PKCE (RFC 7636, S256, RFC 8252 for the paste
// variant). It is available only when the administrator has configured an OAuth client of Google
// (NewWithUserOAuth). Google's device flow cannot be used here: it does not allow the cloud-platform scope.
//
// Two ways to return the browser to Kubron:
//   - browser-callback (web, WebRedirectURL set): Google redirects to the Kubron server; the Broker uses the
//     challenge ID as the OAuth state and the server hands the redirect address to Continue.
//   - paste-redirect (everything else): the redirect goes to a loopback address on which nothing listens, and the
//     user pastes the address of the page that did not open. It needs a client of the "Desktop app" type.
//
// The provider still keeps no state: the PKCE verifier, the state and the nonce are in Pending, which the Broker
// encrypts. The refresh token goes to Saved. The client id and the client secret come from the constructor;
// they never come from a kubeconfig, a Descriptor or an answer.

const (
	// AuthURL is the authorization endpoint of Google. Like TokenURL it is a constant.
	AuthURL = "https://accounts.google.com/o/oauth2/v2/auth"

	// UserScopes are the scopes of the user sign-in: the ID token with the e-mail address for the audit name,
	// and the two scopes of gke-gcloud-auth-plugin.
	UserScopes = "openid " + ScopeUserinfoEmail + " " + ScopeCloudPlatform

	// WebCallbackPath is the path of the Kubron server that receives the redirect of the browser.
	WebCallbackPath = "/auth/cluster/callback"

	flowBrowser      = "browser-callback"
	flowPaste        = "paste"
	fieldRedirectURL = "redirectUrl"
	savedKindUser    = "gke-user"

	maxPastedURL   = 8 << 10
	maxTokenAnswer = 1 << 20
	maxClientField = 256
)

// UserOAuth is the OAuth client of Google that the administrator registered for Kubron. For a web server it is
// a client of the "Web application" type whose authorized redirect URI is WebRedirectURL; without WebRedirectURL
// the paste variant is used and the client must be of the "Desktop app" type.
type UserOAuth struct {
	ClientID       string
	ClientSecret   string // Google issues one to both client types; it is a secret and is never logged
	WebRedirectURL string // the callback of this server (https, or http on loopback); empty: paste variant
}

func (UserOAuth) String() string   { return "[redacted google oauth client]" }
func (UserOAuth) GoString() string { return "[redacted google oauth client]" }

// NewWithUserOAuth returns the provider with Google user sign-in enabled.
func NewWithUserOAuth(c UserOAuth) (*Provider, error) {
	if c.ClientID == "" || len(c.ClientID) > maxClientField || !printableASCII(c.ClientID) ||
		len(c.ClientSecret) > maxClientField || !printableASCII(c.ClientSecret) || !validWebRedirect(c.WebRedirectURL) {
		return nil, errUserConfig
	}
	return &Provider{user: &c, rand: rand.Reader}, nil
}

func validWebRedirect(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Host == "" || strings.Trim(u.Path, "/") != strings.Trim(WebCallbackPath, "/") {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		h := u.Hostname()
		return h == "localhost" || h == "127.0.0.1" || h == "::1"
	}
	return false
}

// pending is the private state of a sign-in that waits for the user.
type pending struct {
	V           int    `json:"v"`
	Flow        string `json:"flow"`
	State       string `json:"state"`
	Nonce       string `json:"nonce"`
	Verifier    string `json:"verifier"`
	RedirectURI string `json:"redirectUri"`
	LoopID      string `json:"loopId,omitempty"`
}

func (p pending) encode() []byte {
	p.V = 1
	b, _ := json.Marshal(p)
	return b
}

func decodePending(b []byte, flow string) (pending, error) {
	var p pending
	if err := json.Unmarshal(b, &p); err != nil || p.V != 1 || p.Flow != flow || p.Verifier == "" || p.Nonce == "" {
		return pending{}, credential.ErrChallengeExpired
	}
	return p, nil
}

// savedUser is what is saved after a Google user signed in. It is bound to the client id: a refresh token works
// only for the client that obtained it.
type savedUser struct {
	V            int    `json:"v"`
	Kind         string `json:"kind"`
	RefreshToken string `json:"refreshToken"`
	Email        string `json:"email"`
	ClientID     string `json:"clientId"`
}

func (savedUser) String() string   { return "[redacted saved google sign-in]" }
func (savedUser) GoString() string { return "[redacted saved google sign-in]" }

func (s savedUser) encode() []byte {
	s.V, s.Kind = 1, savedKindUser
	b, _ := json.Marshal(s)
	return b
}

func parseSavedUser(raw []byte) (savedUser, bool) {
	var s savedUser
	if len(raw) > MaxKeyBytes || json.Unmarshal(raw, &s) != nil || s.V != 1 || s.Kind != savedKindUser ||
		s.RefreshToken == "" || len(s.RefreshToken) > maxTokenLen || !printableASCII(s.RefreshToken) || !validEmail(s.Email) || s.ClientID == "" {
		return savedUser{}, false
	}
	return s, true
}

// savedKind tells which of the two saved forms the data is: "sa" (a service account key), "user" or "".
func savedKind(raw []byte) string {
	var h struct {
		Type string `json:"type"`
		Kind string `json:"kind"`
	}
	if len(raw) > MaxKeyBytes || json.Unmarshal(raw, &h) != nil {
		return ""
	}
	switch {
	case h.Type == typeServiceAccount:
		return "sa"
	case h.Kind == savedKindUser:
		return "user"
	}
	return ""
}

func (p *Provider) reader() io.Reader {
	if p.rand == nil {
		return rand.Reader
	}
	return p.rand
}

func (p *Provider) randString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(p.reader(), b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (p *Provider) newAuthRequest(flow, redirectURI string) (pending, error) {
	pd := pending{Flow: flow, RedirectURI: redirectURI}
	var err error
	if pd.State, err = p.randString(24); err != nil {
		return pd, err
	}
	if pd.Nonce, err = p.randString(24); err != nil {
		return pd, err
	}
	pd.Verifier, err = p.randString(32)
	return pd, err
}

// loopbackRedirect is the redirect address of the paste variant: a loopback address with a port in the dynamic
// range. Nothing listens there; the browser shows an error page and the address is what the user pastes.
func (p *Provider) loopbackRedirect() (string, error) {
	var b [2]byte
	if _, err := io.ReadFull(p.reader(), b[:]); err != nil {
		return "", err
	}
	port := 49152 + (int(b[0])<<8|int(b[1]))%16384
	return "http://127.0.0.1:" + strconv.Itoa(port), nil
}

func (p *Provider) authURL(pd pending) string {
	sum := sha256.Sum256([]byte(pd.Verifier))
	u, _ := url.Parse(AuthURL) // a constant
	u.RawQuery = url.Values{
		"response_type":         {"code"},
		"client_id":             {p.user.ClientID},
		"redirect_uri":          {pd.RedirectURI},
		"scope":                 {UserScopes},
		"state":                 {pd.State},
		"nonce":                 {pd.Nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		// Google gives a refresh token only to an offline request that shows the consent screen.
		"access_type": {"offline"},
		"prompt":      {"consent"},
	}.Encode()
	return u.String()
}

// startUser starts the sign-in of a Google user for this mode.
func (p *Provider) startUser(env credential.Env) (credential.Result, error) {
	if env.Desktop {
		if res, ok, err := p.startLoopback(env); ok {
			return res, err
		}
		// The port could not be opened: fall through to the paste variant.
	}
	if !env.Desktop && p.user.WebRedirectURL != "" {
		pd, err := p.newAuthRequest(flowBrowser, p.user.WebRedirectURL)
		if err != nil {
			return credential.Result{}, err
		}
		return credential.Result{
			Challenge: &credential.Challenge{Kind: credential.ChallengeBrowserCallback, URL: p.authURL(pd), Poll: true, IntervalSec: 2},
			Pending:   pd.encode(),
		}, nil
	}
	redirect, err := p.loopbackRedirect()
	if err != nil {
		return credential.Result{}, err
	}
	pd, err := p.newAuthRequest(flowPaste, redirect)
	if err != nil {
		return credential.Result{}, err
	}
	return credential.Result{
		Challenge: &credential.Challenge{
			Kind:   credential.ChallengePasteRedirect,
			URL:    p.authURL(pd),
			Fields: []credential.Field{{Name: fieldRedirectURL, Secret: true}}, // it carries the code
		},
		Pending: pd.encode(),
	}, nil
}

// continueUser handles the answer of browser-callback and paste-redirect challenges.
func (p *Provider) continueUser(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	flow := flowPaste
	switch req.Challenge.Kind {
	case credential.ChallengeBrowserCallback:
		flow = flowBrowser
	case credential.ChallengeBrowserLoopback:
		return p.continueLoopback(ctx, env, req)
	}
	pd, err := decodePending(req.Pending, flow)
	if err != nil {
		return credential.Result{}, err
	}
	if flow == flowBrowser {
		// The Broker made its challenge ID the OAuth state when it stored the challenge.
		pd.State = req.Challenge.ID
	}
	raw := strings.TrimSpace(req.Answer[fieldRedirectURL])
	if raw == "" && flow == flowBrowser {
		c := req.Challenge
		c.ID, c.ExpiresAt = "", time.Time{}
		return credential.Result{Challenge: &c, Pending: req.Pending}, nil // nothing new: keep waiting
	}
	if raw == "" || len(raw) > maxPastedURL {
		return credential.Result{}, invalid(errRedirect)
	}
	u, err := url.Parse(raw)
	want, werr := url.Parse(pd.RedirectURI)
	if err != nil || werr != nil || !sameRedirect(u, want) {
		return credential.Result{}, invalid(errRedirect)
	}
	q := u.Query()
	for _, k := range []string{"state", "code", "error", "iss"} {
		if len(q[k]) > 1 {
			return credential.Result{}, invalid(errRedirect)
		}
	}
	return p.handleResponse(ctx, env, pd, q)
}

// handleResponse validates the parameters of an authorization response against the request and exchanges the code.
func (p *Provider) handleResponse(ctx context.Context, env credential.Env, pd pending, q url.Values) (credential.Result, error) {
	// Bind the response to this request before reading anything else from it.
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(pd.State)) != 1 {
		return credential.Result{}, &providerError{kind: credential.ErrAnswerRejected, detail: errAuthState}
	}
	if iss := q.Get("iss"); iss != "" && iss != idTokenIssuer { // RFC 9207
		return credential.Result{}, &providerError{kind: credential.ErrInvalidGrant, detail: errIDToken}
	}
	if q.Get("error") != "" {
		return credential.Result{}, &providerError{kind: credential.ErrInvalidGrant, detail: errAuthDenied}
	}
	code := q.Get("code")
	if code == "" {
		return credential.Result{}, invalid(errRedirect)
	}
	return p.exchangeCode(ctx, env, pd, code)
}

// sameRedirect compares an address with the registered redirect address: scheme, host and path. The query is
// the answer of Google and is not compared.
func sameRedirect(got, want *url.URL) bool {
	path := func(u *url.URL) string { return "/" + strings.Trim(u.Path, "/") }
	return strings.EqualFold(got.Scheme, want.Scheme) && strings.EqualFold(got.Host, want.Host) && path(got) == path(want) && got.User == nil
}

func (p *Provider) exchangeCode(ctx context.Context, env credential.Env, pd pending, code string) (credential.Result, error) {
	tr, err := p.tokenCall(ctx, env, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {pd.RedirectURI},
		"code_verifier": {pd.Verifier},
	})
	if err != nil {
		var oe *oauthError
		if errors.As(err, &oe) && oe.Code == "invalid_grant" {
			return credential.Result{}, &providerError{kind: credential.ErrInvalidGrant, detail: errCodeRejected} // spent or wrong code
		}
		return credential.Result{}, err
	}
	email, err := checkIDToken(tr.IDToken, p.user.ClientID, pd.Nonce, nowOf(env))
	if err != nil {
		return credential.Result{}, err
	}
	tok, err := userToken(env, tr, email)
	if err != nil {
		return credential.Result{}, err
	}
	res := credential.Result{Token: &tok}
	if tr.RefreshToken != "" {
		res.Save = savedUser{RefreshToken: tr.RefreshToken, Email: email, ClientID: p.user.ClientID}.encode()
	}
	return res, nil
}

// refresh signs in with a saved refresh token. A refresh token that Google no longer accepts is
// credential.ErrAnswerRejected for the caller (Begin), which erases it and asks again.
func (p *Provider) refresh(ctx context.Context, env credential.Env, sv savedUser) (credential.Result, error) {
	tr, err := p.tokenCall(ctx, env, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {sv.RefreshToken}})
	if err != nil {
		var oe *oauthError
		if errors.As(err, &oe) && oe.Code == "invalid_grant" {
			return credential.Result{}, &providerError{kind: credential.ErrAnswerRejected, detail: errSavedRejected}
		}
		return credential.Result{}, err
	}
	tok, err := userToken(env, tr, sv.Email)
	if err != nil {
		return credential.Result{}, err
	}
	res := credential.Result{Token: &tok}
	if tr.RefreshToken != "" && tr.RefreshToken != sv.RefreshToken { // Google may rotate the token
		sv.RefreshToken = tr.RefreshToken
		res.Save = sv.encode()
	}
	return res, nil
}

// oauthError is an answer of the token endpoint that carries an OAuth error code. Its text is fixed.
type oauthError struct{ Code string }

func (*oauthError) Error() string { return errTokenRequest.Error() }

type tokenAnswer struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
}

// tokenCall posts a form to TokenURL through env.HTTP, bound to ctx. The client id and secret are added here.
func (p *Provider) tokenCall(ctx context.Context, env credential.Env, form url.Values) (tokenAnswer, error) {
	if env.HTTP == nil {
		return tokenAnswer{}, errNoHTTP
	}
	form.Set("client_id", p.user.ClientID)
	if p.user.ClientSecret != "" {
		form.Set("client_secret", p.user.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenAnswer{}, unavailable()
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := env.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return tokenAnswer{}, ctx.Err()
		}
		return tokenAnswer{}, unavailable() // the text of err can hold addresses: it is dropped
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenAnswer+1))
	if err != nil || len(body) > maxTokenAnswer {
		return tokenAnswer{}, unavailable()
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return tokenAnswer{}, unavailable()
	}
	if resp.StatusCode != http.StatusOK {
		return tokenAnswer{}, &oauthError{Code: oauthErrorCode(body)}
	}
	var tr tokenAnswer
	if json.Unmarshal(body, &tr) != nil {
		return tokenAnswer{}, errTokenResponse
	}
	return tr, nil
}

func nowOf(env credential.Env) time.Time {
	if env.Now != nil {
		return env.Now()
	}
	return time.Now()
}

func userToken(env credential.Env, tr tokenAnswer, email string) (credential.Token, error) {
	v := tr.AccessToken
	if v == "" || len(v) > maxTokenLen || !printableASCII(v) || !strings.EqualFold(tr.TokenType, "bearer") {
		return credential.Token{}, errTokenResponse
	}
	ttl := DefaultTTL
	if tr.ExpiresIn > 0 && tr.ExpiresIn <= 24*3600 {
		ttl = time.Duration(tr.ExpiresIn) * time.Second
	}
	return credential.Token{Value: v, Expiry: nowOf(env).Add(ttl), Principal: email}, nil
}

const idTokenIssuer = "https://accounts.google.com"

// checkIDToken reads the claims of the ID token that Google returned from the token endpoint and returns the
// e-mail address. The signature is not verified: the token came straight from Google over TLS in answer to a
// request of this client (OpenID Connect Core 3.1.3.7, item 6), and only claims about this very sign-in are used:
// issuer, audience, expiry, nonce, address.
func checkIDToken(raw, clientID, nonce string, now time.Time) (string, error) {
	bad := &providerError{kind: credential.ErrInvalidGrant, detail: errIDToken}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || len(raw) > maxTokenLen {
		return "", bad
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", bad
	}
	var c struct {
		Iss           string          `json:"iss"`
		Aud           json.RawMessage `json:"aud"`
		Azp           string          `json:"azp"`
		Exp           int64           `json:"exp"`
		Nonce         string          `json:"nonce"`
		Email         string          `json:"email"`
		EmailVerified json.RawMessage `json:"email_verified"`
	}
	if json.Unmarshal(payload, &c) != nil {
		return "", bad
	}
	if c.Iss != idTokenIssuer && c.Iss != "accounts.google.com" {
		return "", bad
	}
	var aud []string
	var one string
	switch {
	case json.Unmarshal(c.Aud, &one) == nil:
		aud = []string{one}
	case json.Unmarshal(c.Aud, &aud) != nil:
		return "", bad
	}
	found := false
	for _, a := range aud {
		if a == clientID {
			found = true
		}
	}
	if !found || (c.Azp != "" && c.Azp != clientID) || c.Exp <= now.Unix() {
		return "", bad
	}
	if subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(nonce)) != 1 {
		return "", bad
	}
	if v := strings.Trim(string(c.EmailVerified), `"`); v == "false" || !validEmail(c.Email) {
		return "", bad
	}
	return c.Email, nil
}
