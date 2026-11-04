// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

const (
	deviceGrantType     = "urn:ietf:params:oauth:grant-type:device_code"
	defaultDeviceExpiry = 10 * time.Minute
	maxDeviceExpiry     = 30 * time.Minute
	defaultPollSeconds  = 5 // RFC 8628 3.2
	maxPollSeconds      = 60
	slowDownSeconds     = 5 // RFC 8628 3.5
)

type deviceResponse struct {
	DeviceCode              string      `json:"device_code"`
	UserCode                string      `json:"user_code"`
	VerificationURI         string      `json:"verification_uri"`
	VerificationURIComplete string      `json:"verification_uri_complete"`
	ExpiresIn               json.Number `json:"expires_in"`
	Interval                json.Number `json:"interval"`
}

// startDevice starts the device authorization grant (RFC 8628): the user opens an address on any device and
// types a code, and this provider polls the token endpoint. errDeviceUnavailable means that the provider
// does not offer it to this client (no endpoint, or unauthorized_client / unsupported_grant_type), and the
// caller falls back to another flow; every other failure is returned.
func (s *session) startDevice(ctx context.Context) (credential.Result, error) {
	d, err := s.discover(ctx)
	if err != nil {
		return credential.Result{}, err
	}
	if d.DeviceEndpoint == "" {
		return credential.Result{}, errDeviceUnavailable
	}
	body, err := s.postRaw(ctx, d, d.DeviceEndpoint, url.Values{"scope": {s.scopeParam()}})
	if err != nil {
		if c := oauthCode(err); c == "unauthorized_client" || c == "unsupported_grant_type" {
			return credential.Result{}, errDeviceUnavailable
		}
		return credential.Result{}, err
	}
	var dr deviceResponse
	if json.Unmarshal(body, &dr) != nil || dr.DeviceCode == "" || dr.UserCode == "" || dr.VerificationURI == "" {
		return credential.Result{}, errTokenResponse
	}
	// The address is opened by the user's browser, not by this server, but it must not be a way to smuggle
	// a javascript: or file: address into the interface.
	link := dr.VerificationURI
	if dr.VerificationURIComplete != "" {
		link = dr.VerificationURIComplete
	}
	for _, a := range []string{dr.VerificationURI, link} {
		if err := s.opts.CheckURL(a); err != nil {
			return credential.Result{}, wrap(errTokenResponse, err)
		}
	}
	expires := defaultDeviceExpiry
	if n, err := dr.ExpiresIn.Int64(); err == nil && n > 0 {
		expires = min(time.Duration(n)*time.Second, maxDeviceExpiry)
	}
	interval := pollInterval(dr.Interval)
	until := s.now().Add(expires)
	p := pending{Flow: flowDevice, DeviceCode: dr.DeviceCode, Interval: interval, Expires: until, NextPoll: s.now().Add(time.Duration(interval) * time.Second)}
	return credential.Result{
		Challenge: &credential.Challenge{Kind: credential.ChallengeDeviceCode, URL: link, UserCode: dr.UserCode,
			Poll: true, IntervalSec: interval, ExpiresAt: until},
		Pending: p.encode(),
	}, nil
}

func pollInterval(n json.Number) int {
	v, err := n.Int64()
	if err != nil || v < 1 {
		return defaultPollSeconds
	}
	return int(min(v, maxPollSeconds))
}

// continueDevice is a poll. The provider is asked at most once per interval, however often the caller polls:
// a poll that comes early is answered "still waiting" without a request. authorization_pending keeps the
// interval, slow_down makes it 5 seconds longer for the rest of the request (RFC 8628 3.5).
func (s *session) continueDevice(ctx context.Context, req credential.Request, sv *saved) (credential.Result, error) {
	p, err := decodePending(req.Pending, flowDevice)
	if err != nil || p.DeviceCode == "" {
		return credential.Result{}, credential.ErrChallengeExpired
	}
	now := s.now()
	if !now.Before(p.Expires) {
		return credential.Result{}, credential.ErrChallengeExpired
	}
	if now.Before(p.NextPoll) {
		return waiting(req.Challenge, req.Pending), nil
	}
	d, err := s.discover(ctx)
	if err != nil {
		return credential.Result{}, err
	}
	tr, err := s.tokenCall(ctx, d, url.Values{"grant_type": {deviceGrantType}, "device_code": {p.DeviceCode}})
	if err != nil {
		switch oauthCode(err) {
		case "authorization_pending":
		case "slow_down":
			p.Interval = min(p.Interval+slowDownSeconds, maxPollSeconds)
		case "expired_token":
			return credential.Result{}, credential.ErrChallengeExpired
		case "access_denied":
			return credential.Result{}, wrap(errDenied, credential.ErrInvalidGrant)
		default:
			if oauthCode(err) == "" {
				return credential.Result{}, err // the network or an unreadable answer: try again later
			}
			return credential.Result{}, wrap(errTokenRequest, credential.ErrInvalidGrant)
		}
		p.NextPoll = now.Add(time.Duration(p.Interval) * time.Second)
		c := req.Challenge
		c.IntervalSec = p.Interval
		return waiting(c, p.encode()), nil
	}
	return s.finish(ctx, d, req, sv, tr, "", "", "", "")
}
