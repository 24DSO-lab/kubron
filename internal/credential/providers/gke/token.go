// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package gke

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"

	"github.com/24DSO-lab/kubron/internal/credential"
)

const (
	// TokenURL is the only address the provider talks to. It is a constant: no kubeconfig field, no answer of
	// the user and no field of the key file (token_uri) can change it.
	TokenURL = "https://oauth2.googleapis.com/token"
	// ScopeCloudPlatform and ScopeUserinfoEmail are the scopes of gke-gcloud-auth-plugin.
	ScopeCloudPlatform = "https://www.googleapis.com/auth/cloud-platform"
	ScopeUserinfoEmail = "https://www.googleapis.com/auth/userinfo.email"
	// DefaultTTL is the lifetime of a token whose answer has no expires_in (Google always sends one).
	DefaultTTL = 5 * time.Minute

	maxTokenLen = 8 << 10
)

// exchange runs the JWT-bearer grant (RFC 7523) for the key and returns the access token. The request goes
// through env.HTTP: golang.org/x/oauth2 takes its client from the context value oauth2.HTTPClient and uses
// http.DefaultClient only when there is none, so a missing env.HTTP is an error here and never a fallback.
//
// jwt.Config sends the request with Client.PostForm, which has no context, so a cancelled sign-in would not
// stop it. The client that is handed to the library therefore binds every request to ctx (see boundClient).
//
// The assertion carries iss (the key's client_email), scope, aud (TokenURL, because Audience is left empty),
// iat and exp. Expires is left at zero: the library then makes exp one hour after iat, which is the longest
// lifetime Google accepts.
func exchange(ctx context.Context, env credential.Env, k *key) (credential.Token, error) {
	if env.HTTP == nil {
		return credential.Token{}, errNoHTTP
	}
	cfg := &jwt.Config{
		Email:        k.email,
		PrivateKey:   []byte(k.privateKey),
		PrivateKeyID: k.keyID,
		Scopes:       []string{ScopeCloudPlatform, ScopeUserinfoEmail},
		TokenURL:     TokenURL,
	}
	hctx := context.WithValue(ctx, oauth2.HTTPClient, boundClient(ctx, env.HTTP))
	tok, err := cfg.TokenSource(hctx).Token()
	if err != nil {
		return credential.Token{}, classify(ctx, err)
	}
	return buildToken(env, k, tok)
}

// boundClient returns a client whose requests are the requests of next (env.HTTP) bound to ctx. It adds no
// transport of its own: every request, with its connection, address policy, redirect checks, timeout and body
// limit, is carried out by next.Do. Redirects are followed there, so this client never follows one itself.
func boundClient(ctx context.Context, next *http.Client) *http.Client {
	return &http.Client{
		Transport:     boundTransport{ctx: ctx, next: next},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type boundTransport struct {
	ctx  context.Context
	next *http.Client
}

func (b boundTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return b.next.Do(r.WithContext(b.ctx))
}

// classify turns a failure of the token request into an error that is safe to show. The text of err is
// never used: the library puts addresses into it and, for a non-2xx answer, keeps the body.
func classify(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err() // callers rely on a cancelled or expired context staying as it is
	}
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) || re.Response == nil {
		// A transport failure (including a refusal of the address policy), or an answer that is not
		// JSON: nothing the user can fix by changing the key.
		return unavailable()
	}
	status := re.Response.StatusCode
	if status >= 500 || status == 429 {
		return unavailable()
	}
	// jwt.Config builds the RetrieveError without ErrorCode, so the code is read from the body here.
	if oauthErrorCode(re.Body) == "invalid_grant" {
		return rejectedKey() // the key was deleted or disabled, or the signature does not match it
	}
	return errTokenRequest
}

func oauthErrorCode(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	return e.Error
}

func buildToken(env credential.Env, k *key, tok *oauth2.Token) (credential.Token, error) {
	v := tok.AccessToken
	if v == "" || len(v) > maxTokenLen || !printableASCII(v) || !strings.EqualFold(tok.Type(), "bearer") {
		return credential.Token{}, errTokenResponse
	}
	expiry := tok.Expiry
	if expiry.IsZero() {
		now := time.Now
		if env.Now != nil {
			now = env.Now
		}
		expiry = now().Add(DefaultTTL)
	}
	return credential.Token{Value: v, Expiry: expiry, Principal: k.email}, nil
}
