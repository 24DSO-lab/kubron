// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/credential"
	"github.com/24DSO-lab/kubron/internal/idpnet"
)

// tokenResponse is a token endpoint answer.
type tokenResponse struct {
	AccessToken  string      `json:"access_token"`
	IDToken      string      `json:"id_token"`
	RefreshToken string      `json:"refresh_token"`
	TokenType    string      `json:"token_type"`
	ExpiresIn    json.Number `json:"expires_in"`
}

// oauthError is an OAuth error answer (RFC 6749 5.2). Only the code is kept, and only for decisions: the
// description of the provider is dropped because it may repeat what was sent (a user name, for example).
type oauthError struct {
	code   string
	status int // HTTP status of the answer
}

func (e *oauthError) Error() string { return errTokenRequest.Error() }

// Is: every failed token request is errTokenRequest; a server error or throttling (5xx, 429) is in addition "the
// service is unavailable": it says nothing about the user's credentials.
func (e *oauthError) Is(target error) bool {
	return target == errTokenRequest || (target == credential.ErrServiceUnavailable && (e.status >= 500 || e.status == http.StatusTooManyRequests))
}

// oauthCode returns the OAuth error code of err, "" when err is not an OAuth error answer.
func oauthCode(err error) string {
	var oe *oauthError
	if errors.As(err, &oe) {
		return oe.code
	}
	return ""
}

// staleRefresh reports an answer that means "this refresh token cannot be used any more".
func staleRefresh(err error) bool {
	c := oauthCode(err)
	return c == "invalid_grant" || c == "invalid_request"
}

// useBasic reports whether the client secret goes into an Authorization header (the default of RFC 6749
// when the provider does not say otherwise) or into the form body.
func (s *session) useBasic(d *discovery) bool {
	if len(d.AuthMethods) == 0 {
		return true
	}
	return slices.Contains(d.AuthMethods, "client_secret_basic")
}

// postRaw sends a form to a provider endpoint with the client authentication. A 2xx answer is returned as it
// is; anything else is an *oauthError with the error code of the answer.
func (s *session) postRaw(ctx context.Context, d *discovery, endpoint string, form url.Values) ([]byte, error) {
	basic := s.cfg.clientSecret != "" && s.useBasic(d)
	if !basic {
		form.Set("client_id", s.cfg.clientID)
		if s.cfg.clientSecret != "" {
			form.Set("client_secret", s.cfg.clientSecret)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errTokenRequest
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if basic {
		req.SetBasicAuth(url.QueryEscape(s.cfg.clientID), url.QueryEscape(s.cfg.clientSecret))
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return nil, netFailure(ctx, err)
	}
	body, err := idpnet.ReadBody(resp)
	if err != nil {
		return nil, netFailure(ctx, err)
	}
	if !statusOK(resp) {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		return nil, &oauthError{code: e.Error, status: resp.StatusCode}
	}
	return body, nil
}

// post is postRaw for endpoints that answer with tokens.
func (s *session) post(ctx context.Context, d *discovery, endpoint string, form url.Values) (*tokenResponse, error) {
	body, err := s.postRaw(ctx, d, endpoint, form)
	if err != nil {
		return nil, err
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, errTokenResponse
	}
	return &tr, nil
}

func (s *session) tokenCall(ctx context.Context, d *discovery, form url.Values) (*tokenResponse, error) {
	return s.post(ctx, d, d.TokenEndpoint, form)
}

// refresh exchanges a refresh token for new tokens.
func (s *session) refresh(ctx context.Context, d *discovery, refreshToken string) (*tokenResponse, error) {
	return s.tokenCall(ctx, d, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"scope":         {s.scopeParam()},
	})
}

// passwordGrant is the resource owner password credentials grant (RFC 6749 4.3).
func (s *session) passwordGrant(ctx context.Context, d *discovery, username, password string) (*tokenResponse, error) {
	return s.tokenCall(ctx, d, url.Values{
		"grant_type": {"password"},
		"username":   {username},
		"password":   {password},
		"scope":      {s.scopeParam()},
	})
}

// token checks the ID token of a token response and builds the credential.Token. nonce is checked when it is
// not empty. login is used as the principal when the token has no sub claim.
func (s *session) token(ctx context.Context, d *discovery, tr *tokenResponse, nonce, login string) (credential.Token, error) {
	if tr.IDToken == "" {
		return credential.Token{}, errTokenResponse
	}
	idt, err := s.verifier(ctx, d).Verify(ctx, tr.IDToken)
	if err != nil {
		if ctx.Err() != nil {
			return credential.Token{}, ctx.Err()
		}
		return credential.Token{}, errIDToken
	}
	if nonce != "" && subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(nonce)) != 1 {
		return credential.Token{}, errNonce
	}
	principal := idt.Subject
	if principal == "" {
		principal = login
	}
	tok := credential.Token{Value: tr.IDToken, Expiry: idt.Expiry, Principal: principal}
	if s.cfg.useAccessToken {
		// kubelogin --oidc-use-access-token: the API server is configured to accept the access token. The ID
		// token was still verified above, so the sign-in itself is trusted.
		if tr.AccessToken == "" {
			return credential.Token{}, errTokenResponse
		}
		tok.Value = tr.AccessToken
		if n, err := tr.ExpiresIn.Int64(); err == nil && n > 0 {
			tok.Expiry = s.now().Add(time.Duration(n) * time.Second)
		}
	}
	return tok, nil
}

// checkIssuerParam applies RFC 9207 to the parameters of an authorization response: when the response names
// its issuer it must be the one that was asked (a client that talks to several providers is otherwise open to
// a mix-up attack), and when the provider announced that it always sends iss, it must be there.
func (d *discovery) checkIssuerParam(q url.Values) error {
	iss := q.Get("iss")
	switch {
	case iss != "" && trimSlash(iss) != trimSlash(d.Issuer):
		return errMixup
	case iss == "" && d.IssParameter:
		return errMixup
	}
	return nil
}

// randString returns n random bytes as unpadded base64url.
func randString(r io.Reader, n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", errPending
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
