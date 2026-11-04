// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

const (
	flowLoopback = "loopback"
	flowPaste    = "paste"
	flowDevice   = "device"
	flowBrowser  = "browser-callback"

	maxPastedURL = 8 << 10
)

// pending is the private state of a sign-in that waits for the user (Request.Pending / Result.Pending). The
// Broker encrypts it; it never reaches the client.
type pending struct {
	V           int    `json:"v"`
	Flow        string `json:"flow"`
	State       string `json:"state,omitempty"`
	Nonce       string `json:"nonce,omitempty"`
	Verifier    string `json:"verifier,omitempty"`
	RedirectURI string `json:"redirectUri,omitempty"`
	LoopID      string `json:"loopId,omitempty"`

	// device flow
	DeviceCode string    `json:"deviceCode,omitempty"`
	Interval   int       `json:"interval,omitempty"` // seconds between polls
	Expires    time.Time `json:"expires,omitzero"`   // the device code stops working
	NextPoll   time.Time `json:"nextPoll,omitzero"`  // the earliest next request to the provider
}

func (p pending) encode() []byte {
	p.V = 1
	b, _ := json.Marshal(p)
	return b
}

func decodePending(b []byte, flow string) (pending, error) {
	var p pending
	if err := json.Unmarshal(b, &p); err != nil || p.V != 1 || p.Flow != flow {
		return pending{}, errPending
	}
	return p, nil
}

// newAuthRequest creates the secrets of an authorization-code request: state, nonce and the PKCE verifier
// (RFC 7636, method S256, always; the provider is not asked whether it wants it).
func (s *session) newAuthRequest(flow, redirectURI string) (pending, error) {
	p := pending{Flow: flow, RedirectURI: redirectURI}
	var err error
	if p.State, err = randString(s.rand, 24); err != nil {
		return p, err
	}
	if p.Nonce, err = randString(s.rand, 24); err != nil {
		return p, err
	}
	if p.Verifier, err = randString(s.rand, 32); err != nil {
		return p, err
	}
	return p, nil
}

// authURL builds the address the user opens to sign in.
func (s *session) authURL(d *discovery, p pending) (string, error) {
	if d.AuthorizationEndpoint == "" {
		return "", errDiscovery
	}
	u, err := url.Parse(d.AuthorizationEndpoint)
	if err != nil {
		return "", errDiscovery
	}
	q := u.Query()
	for k, vs := range s.cfg.extraAuth {
		q[k] = vs
	}
	sum := sha256.Sum256([]byte(p.Verifier))
	q.Set("response_type", "code")
	q.Set("client_id", s.cfg.clientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("scope", s.scopeParam())
	q.Set("state", p.State)
	q.Set("nonce", p.Nonce)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// redirectURI is the redirect URL registered at the provider for this client: the host name of the
// kubeconfig (localhost by default) and the given port.
func (s *session) redirectURI(port string) string {
	return "http://" + net.JoinHostPort(s.cfg.redirectHost, port)
}

// checkResponse validates the parameters of an authorization response against the request: the issuer
// (RFC 9207), the state, an error from the provider, and the presence of a code. It returns the code.
func (s *session) checkResponse(d *discovery, q url.Values, p pending) (string, error) {
	if err := d.checkIssuerParam(q); err != nil {
		return "", wrap(err, credential.ErrInvalidGrant) // a response from another issuer ends this request
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(p.State)) != 1 {
		return "", wrap(errState, credential.ErrAnswerRejected)
	}
	if q.Get("error") != "" {
		return "", wrap(errDenied, credential.ErrInvalidGrant)
	}
	code := q.Get("code")
	if code == "" {
		return "", wrap(errRedirect, credential.ErrInvalidAnswer)
	}
	return code, nil
}

// exchangeCode trades the authorization code for tokens (with the PKCE verifier) and checks the ID token,
// including its nonce. A code that the provider does not accept is spent or wrong: the request ends.
func (s *session) exchangeCode(ctx context.Context, d *discovery, req credential.Request, sv *saved, code string, p pending) (credential.Result, error) {
	tr, err := s.tokenCall(ctx, d, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.RedirectURI},
		"code_verifier": {p.Verifier},
	})
	if err != nil {
		if oauthCode(err) == "invalid_grant" {
			return credential.Result{}, wrap(errTokenRequest, credential.ErrInvalidGrant)
		}
		return credential.Result{}, err
	}
	return s.finish(ctx, d, req, sv, tr, p.Nonce, "", "", "")
}

// startBrowserCallback starts an authorization-code flow whose redirect returns to the Kubron web server.
// The broker replaces the provisional state in the URL with the challenge ID when it stores the challenge;
// that ID is unpredictable and the challenge store binds it to the originating web session.
func (s *session) startBrowserCallback(ctx context.Context) (credential.Result, error) {
	d, err := s.discover(ctx)
	if err != nil {
		return credential.Result{}, err
	}
	p, err := s.newAuthRequest(flowBrowser, s.cfg.webRedirectURL)
	if err != nil {
		return credential.Result{}, err
	}
	u, err := s.authURL(d, p)
	if err != nil {
		return credential.Result{}, err
	}
	return credential.Result{
		Challenge: &credential.Challenge{Kind: credential.ChallengeBrowserCallback, URL: u, Poll: true, IntervalSec: 2},
		Pending: p.encode(),
	}, nil
}

// continueBrowserCallback waits for the browser redirect or exchanges its authorization code.
func (s *session) continueBrowserCallback(ctx context.Context, req credential.Request, sv *saved) (credential.Result, error) {
	p, err := decodePending(req.Pending, flowBrowser)
	if err != nil {
		return credential.Result{}, credential.ErrChallengeExpired
	}
	// Challenge IDs are generated by the broker after Begin and replace the provisional state in the
	// authorization URL. The broker validates the ID against the caller's session before invoking us.
	p.State = req.Challenge.ID
	raw := strings.TrimSpace(req.Answer["redirectUrl"])
	if raw == "" {
		return waiting(req.Challenge, req.Pending), nil
	}
	if len(raw) > maxPastedURL {
		return credential.Result{}, credential.ErrInvalidAnswer
	}
	u, err := url.Parse(raw)
	want, werr := url.Parse(s.cfg.webRedirectURL)
	if err != nil || werr != nil || !sameRedirect(u, want) {
		return credential.Result{}, wrap(errRedirect, credential.ErrInvalidAnswer)
	}
	d, err := s.discover(ctx)
	if err != nil {
		return credential.Result{}, err
	}
	code, err := s.checkResponse(d, u.Query(), p)
	if err != nil {
		return credential.Result{}, err
	}
	return s.exchangeCode(ctx, d, req, sv, code, p)
}

// startPaste starts the flow for people who cannot receive the redirect (a web server, a remote shell): the
// user opens the address, signs in, and pastes the address the browser ended up at.
func (s *session) startPaste(ctx context.Context) (credential.Result, error) {
	d, err := s.discover(ctx)
	if err != nil {
		return credential.Result{}, err
	}
	_, port, _ := net.SplitHostPort(s.cfg.listen[0])
	p, err := s.newAuthRequest(flowPaste, s.redirectURI(port))
	if err != nil {
		return credential.Result{}, err
	}
	u, err := s.authURL(d, p)
	if err != nil {
		return credential.Result{}, err
	}
	return credential.Result{
		Challenge: &credential.Challenge{
			Kind:   credential.ChallengePasteRedirect,
			URL:    u,
			Fields: []credential.Field{{Name: "redirectUrl", Secret: true}}, // it carries the code
		},
		Pending: p.encode(),
	}, nil
}

// continuePaste handles the pasted address. It must be an address of the registered redirect URL with the
// state of this request; whatever else is pasted is rejected and may be tried again. The authorization code
// works once: pasting the same address again fails at the provider.
func (s *session) continuePaste(ctx context.Context, req credential.Request, sv *saved) (credential.Result, error) {
	p, err := decodePending(req.Pending, flowPaste)
	if err != nil {
		return credential.Result{}, credential.ErrChallengeExpired
	}
	raw := strings.TrimSpace(req.Answer["redirectUrl"])
	if raw == "" || len(raw) > maxPastedURL {
		return credential.Result{}, credential.ErrInvalidAnswer
	}
	u, err := url.Parse(raw)
	want, werr := url.Parse(p.RedirectURI)
	if err != nil || werr != nil || !sameRedirect(u, want) {
		return credential.Result{}, wrap(errRedirect, credential.ErrInvalidAnswer)
	}
	d, err := s.discover(ctx)
	if err != nil {
		return credential.Result{}, err
	}
	code, err := s.checkResponse(d, u.Query(), p)
	if err != nil {
		return credential.Result{}, err
	}
	return s.exchangeCode(ctx, d, req, sv, code, p)
}

// sameRedirect compares a pasted address with the registered redirect URL: scheme, host and path (an empty
// path is "/"). The query is the provider's answer and is not compared.
func sameRedirect(got, want *url.URL) bool {
	path := func(u *url.URL) string { return "/" + strings.Trim(u.Path, "/") }
	return strings.EqualFold(got.Scheme, want.Scheme) && strings.EqualFold(got.Host, want.Host) && path(got) == path(want) && got.User == nil
}

// waiting is the result of a poll that has nothing new: the same challenge, the same private state.
func waiting(c credential.Challenge, pend []byte) credential.Result {
	c.ID, c.ExpiresAt = "", time.Time{}
	return credential.Result{Challenge: &c, Pending: pend}
}
