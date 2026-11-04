// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package oidc is the sign-in provider for OpenID Connect clusters (ADR-0018, kind credential.KindOIDC).
//
// One Provider serves every grant of Descriptor.Params["grant"]: password, authcode, authcode-keyboard,
// device-code and legacy-refresh. The provider is a function of its Request and Env: it has no storage,
// writes nothing to disk and reads no files, and it keeps no state between calls. The private state of a
// pending sign-in (PKCE verifier, state, device code) travels in Request.Pending / Result.Pending and is
// encrypted by the Broker. The single exception is the desktop loopback listener of the authorization-code
// flow, whose socket lives in the memory of the process (see loopback.go).
//
// Secrets (password, tokens, authorization code, client secret) are never put into an error text.
package oidc

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"

	"github.com/24DSO-labs/kubron/internal/credential"
)

type randSource = io.Reader

// Provider implements credential.Provider for credential.KindOIDC.
type Provider struct {
	rand  randSource
	loops loopbackRegistry
}

// Option configures New.
type Option func(*Provider)

// WithRand replaces the source of random numbers (tests only).
func WithRand(r io.Reader) Option { return func(p *Provider) { p.rand = r } }

// New returns a Provider.
func New(opts ...Option) *Provider {
	p := &Provider{rand: rand.Reader}
	for _, o := range opts {
		o(p)
	}
	return p
}

var _ credential.Provider = (*Provider)(nil)

// Kind implements credential.Provider.
func (*Provider) Kind() credential.Kind { return credential.KindOIDC }

// Begin implements credential.Provider. It uses what is saved (a refresh token; for a team also a login and
// password) and, when that is not enough, starts the interactive flow of the grant. Saved data that does not
// work any more is erased (Result.Save is an empty non-nil slice) together with the new challenge.
func (p *Provider) Begin(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	sv, svErr := parseSaved(req.Saved)
	stale := len(req.Saved) > 0 && svErr != nil
	cfg, err := newConfig(req.Descriptor, sv, env.Desktop)
	if err != nil {
		return credential.Result{}, err
	}
	s, err := p.newSession(env, cfg)
	if err != nil {
		return credential.Result{}, err
	}
	if sv != nil && !sv.boundTo(cfg.issuer, cfg.clientID) {
		sv, stale = nil, true
	}
	if sv != nil {
		res, err := s.silent(ctx, req, sv)
		if err == nil {
			return res, nil
		}
		if !errors.Is(err, credential.ErrInvalidGrant) {
			return credential.Result{}, err
		}
		stale = true
	}
	res, err := s.interactive(ctx, req, sv)
	if err != nil {
		return credential.Result{}, err
	}
	if stale && res.Save == nil {
		res.Save = []byte{}
	}
	return res, nil
}

// Continue implements credential.Provider. req.Challenge.Kind says which step is being continued; for the
// challenges with Poll (device code, loopback) an empty Answer is a poll.
func (p *Provider) Continue(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	sv, _ := parseSaved(req.Saved)
	cfg, err := newConfig(req.Descriptor, sv, env.Desktop)
	if err != nil {
		return credential.Result{}, err
	}
	s, err := p.newSession(env, cfg)
	if err != nil {
		return credential.Result{}, err
	}
	switch req.Challenge.Kind {
	case credential.ChallengeCredentials:
		return s.continueCredentials(ctx, req, sv)
	case credential.ChallengePasteRedirect:
		return s.continuePaste(ctx, req, sv)
	case credential.ChallengeBrowserCallback:
		return s.continueBrowserCallback(ctx, req, sv)
	case credential.ChallengeBrowserLoopback:
		return s.continueLoopback(ctx, req, sv)
	case credential.ChallengeDeviceCode:
		return s.continueDevice(ctx, req, sv)
	default:
		return credential.Result{}, credential.ErrUnsupported
	}
}

// saveFor returns the Result.Save for a finished sign-in: nil when nothing changed.
func (s *session) saveFor(req credential.Request, old *saved, username, password, refresh string) []byte {
	b := newSaved(s.cfg, req.Subject.Scope, old, username, password, refresh)
	if b == nil {
		if len(req.Saved) > 0 {
			return []byte{} // the provider issued no refresh token: do not keep a stale one
		}
		return nil
	}
	if bytes.Equal(b, req.Saved) {
		return nil
	}
	return b
}

// silent signs in with what is saved. credential.ErrInvalidGrant means that nothing saved works any more.
func (s *session) silent(ctx context.Context, req credential.Request, sv *saved) (credential.Result, error) {
	d, err := s.discover(ctx)
	if err != nil {
		return credential.Result{}, err
	}
	if sv.RefreshToken != "" {
		tr, err := s.refresh(ctx, d, sv.RefreshToken)
		switch {
		case err == nil:
			return s.finish(ctx, d, req, sv, tr, "", "", "", sv.RefreshToken)
		case !staleRefresh(err):
			return credential.Result{}, err
		}
	}
	if sv.Type == savedPassword {
		tr, err := s.passwordGrant(ctx, d, sv.Username, sv.Password)
		switch {
		case err == nil:
			return s.finish(ctx, d, req, sv, tr, "", sv.Username, sv.Password, "")
		case !rejected(err):
			return credential.Result{}, err
		}
	}
	return credential.Result{}, credential.ErrInvalidGrant
}

// finish turns a token response into a Result: the verified token and the data to save. keepRefresh is the
// refresh token that stays valid when the provider does not rotate it.
func (s *session) finish(ctx context.Context, d *discovery, req credential.Request, old *saved, tr *tokenResponse, nonce, username, password, keepRefresh string) (credential.Result, error) {
	tok, err := s.token(ctx, d, tr, nonce, username)
	if err != nil {
		return credential.Result{}, err
	}
	refresh := tr.RefreshToken
	if refresh == "" {
		refresh = keepRefresh
	}
	return credential.Result{Token: &tok, Save: s.saveFor(req, old, username, password, refresh)}, nil
}

// rejected reports a token endpoint answer that means "wrong user name or password".
func rejected(err error) bool {
	c := oauthCode(err)
	return c == "invalid_grant" || c == "access_denied"
}

// startRemote prefers the device flow (RFC 8628), then a server callback when the web client is configured,
// and finally the compatible paste-redirect flow. Desktop authcode uses its local loopback listener.
func (s *session) startRemote(ctx context.Context) (credential.Result, error) {
	res, err := s.startDevice(ctx)
	if !errors.Is(err, errDeviceUnavailable) {
		return res, err
	}
	if s.env.Desktop {
		return s.startLoopback(ctx)
	}
	if s.cfg.webRedirectURL != "" {
		return s.startBrowserCallback(ctx)
	}
	return s.startPaste(ctx)
}

// interactive starts the flow of the grant when nothing saved was enough.
func (s *session) interactive(ctx context.Context, req credential.Request, sv *saved) (credential.Result, error) {
	switch s.cfg.grant {
	case grantPassword:
		return s.startPassword(ctx, req, sv)
	case grantAuthcodeKeyboard:
		return s.startPaste(ctx)
	case grantAuthcode:
		if s.env.Desktop {
			return s.startLoopback(ctx)
		}
		return s.startRemote(ctx)
	case grantDeviceCode:
		return s.startRemote(ctx)
	case grantLegacyRefresh:
		return s.startLegacy(ctx, req)
	default:
		return credential.Result{}, credential.ErrUnsupported
	}
}
