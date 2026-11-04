// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"

	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/idpnet"
)

// discovery is the part of the provider metadata (OpenID Connect Discovery, RFC 8414, RFC 8628, RFC 9207)
// this provider uses.
type discovery struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	DeviceEndpoint        string   `json:"device_authorization_endpoint"`
	AuthMethods           []string `json:"token_endpoint_auth_methods_supported"`
	SigningAlgs           []string `json:"id_token_signing_alg_values_supported"`
	IssParameter          bool     `json:"authorization_response_iss_parameter_supported"`
}

// session is the state of one Begin or Continue call; nothing outlives it.
type session struct {
	cfg  config
	env  credential.Env
	hc   *http.Client
	opts idpnet.Options
	now  func() time.Time
	rand randSource
	p    *Provider
	disc *discovery
}

func (p *Provider) newSession(env credential.Env, cfg config) (*session, error) {
	opts := idpnet.Options{Desktop: env.Desktop}
	hc := env.HTTP
	if hc == nil {
		var err error
		if hc, err = idpnet.NewClient(opts); err != nil {
			return nil, err
		}
	}
	hc, err := idpnet.WithTLS(hc, env.Desktop, cfg.caPEM, cfg.insecure)
	if err != nil {
		if errors.Is(err, idpnet.ErrInsecureTLS) {
			return nil, credential.ErrUnsupported
		}
		return nil, err
	}
	now := env.Now
	if now == nil {
		now = time.Now
	}
	return &session{cfg: cfg, env: env, hc: hc, opts: opts, now: now, rand: p.rand, p: p}, nil
}

// discover fetches the provider metadata once per session and checks it: the issuer must be the configured
// one and every endpoint must pass the same address rules as the connection itself.
func (s *session) discover(ctx context.Context) (*discovery, error) {
	if s.disc != nil {
		return s.disc, nil
	}
	url := trimSlash(s.cfg.issuer) + "/.well-known/openid-configuration"
	if err := s.opts.CheckURL(url); err != nil {
		return nil, wrap(errDiscovery, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errDiscovery
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.hc.Do(req)
	if err != nil {
		return nil, wrap(errDiscovery, netFailure(ctx, err))
	}
	body, err := idpnet.ReadBody(resp)
	if err != nil {
		return nil, wrap(errDiscovery, netFailure(ctx, err))
	}
	if !statusOK(resp) {
		return nil, errDiscovery
	}
	var d discovery
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, errDiscovery
	}
	if trimSlash(d.Issuer) != trimSlash(s.cfg.issuer) {
		return nil, errIssuerMismatch
	}
	if d.TokenEndpoint == "" || d.JWKSURI == "" {
		return nil, errDiscovery
	}
	for _, ep := range []string{d.AuthorizationEndpoint, d.TokenEndpoint, d.JWKSURI, d.DeviceEndpoint} {
		if ep == "" {
			continue
		}
		if err := s.opts.CheckURL(ep); err != nil {
			return nil, wrap(errDiscovery, err)
		}
	}
	s.disc = &d
	return s.disc, nil
}

// verifier checks ID tokens: signature (keys from the provider's JWKS), iss, aud, exp. The nonce is checked
// by the caller because only some flows have one.
func (s *session) verifier(ctx context.Context, d *discovery) *gooidc.IDTokenVerifier {
	var algs []string
	for _, a := range d.SigningAlgs {
		if isAsymmetric(a) {
			algs = append(algs, a)
		}
	}
	if len(algs) == 0 {
		algs = []string{"RS256"}
	}
	keys := gooidc.NewRemoteKeySet(gooidc.ClientContext(ctx, s.hc), d.JWKSURI)
	return gooidc.NewVerifier(d.Issuer, keys, &gooidc.Config{ClientID: s.cfg.clientID, SupportedSigningAlgs: algs, Now: s.now})
}

// isAsymmetric accepts only signature algorithms with public keys: "none" and HMAC never.
func isAsymmetric(alg string) bool {
	return slices.Contains([]string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA"}, alg)
}

// scopeParam is the space separated scope value.
func (s *session) scopeParam() string { return strings.Join(s.cfg.scopes, " ") }
