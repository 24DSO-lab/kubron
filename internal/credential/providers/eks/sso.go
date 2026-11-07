// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package eks

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/idpnet"
)

// AWS IAM Identity Center (SSO) with the OAuth device flow (RFC 8628), as aws-cli does it. Sources:
// botocore sso-oidc and sso service models (operations RegisterClient, StartDeviceAuthorization, CreateToken,
// GetRoleCredentials), awscli/botocore/utils.py (SSOTokenFetcher: public client, scope sso:account:access,
// interval handling, slow_down +5 s) and botocore/tokens.py (SSOTokenProvider: grant refresh_token).
const (
	ssoClientName  = "kubron"
	ssoScope       = "sso:account:access"
	grantDevice    = "urn:ietf:params:oauth:grant-type:device_code"
	grantRefresh   = "refresh_token"
	defaultExpiry  = 10 * time.Minute
	maxExpiry      = 30 * time.Minute
	defaultPollSec = 5
	maxPollSec     = 60
	slowDownSec    = 5
	maxSSOField    = 512

	// A cached role credential is used while it stays valid for the whole life of a token (14 min) and a margin.
	credsMargin = 16 * time.Minute
	// An access token is renewed a little before it ends.
	tokenMargin = time.Minute
)

func validSSO(startURL, region, account, role string) bool {
	if len(startURL) > maxSSOField || checkRegion(region) != nil || !accountRE.MatchString(account) || !roleNmRE.MatchString(role) {
		return false
	}
	u, err := url.Parse(startURL)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

func ssoOIDC(region string) string {
	d, _, _ := partition(region)
	return "https://oidc." + region + "." + d
}

func ssoPortal(region string) string {
	d, _, _ := partition(region)
	return "https://portal.sso." + region + "." + d
}

// ssoCall sends one JSON request to an Identity Center endpoint. These calls are unsigned (the service model
// says authtype none); the secrets travel in the body or in the bearer header, never in the address.
func (s *session) ssoCall(ctx context.Context, method, endpoint string, body any, header map[string]string) (int, []byte, error) {
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, errSSORequest
		}
		rd = bytes.NewReader(b)
	}
	var req *http.Request
	var err error
	if rd != nil {
		req, err = http.NewRequestWithContext(ctx, method, endpoint, rd)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, endpoint, nil)
	}
	if err != nil {
		return 0, nil, errSSORequest
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return s.do(req)
}

// oauthCode returns the "error" word of an Identity Center error answer ("" when there is none).
func oauthCode(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil || len(e.Error) > 64 {
		return ""
	}
	return e.Error
}

type registration struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	ExpiresAt    int64  `json:"clientSecretExpiresAt"`
}

func (s *session) registerClient(ctx context.Context, ssoRegion string) (registration, error) {
	status, body, err := s.ssoCall(ctx, http.MethodPost, ssoOIDC(ssoRegion)+"/client/register", map[string]any{
		"clientName": ssoClientName, "clientType": "public", "scopes": []string{ssoScope},
	}, nil)
	if err != nil {
		return registration{}, err
	}
	var r registration
	if status != http.StatusOK || json.Unmarshal(body, &r) != nil || r.ClientID == "" || r.ClientSecret == "" {
		return registration{}, errSSORequest
	}
	return r, nil
}

type deviceAuth struct {
	DeviceCode              string      `json:"deviceCode"`
	UserCode                string      `json:"userCode"`
	VerificationURI         string      `json:"verificationUri"`
	VerificationURIComplete string      `json:"verificationUriComplete"`
	ExpiresIn               json.Number `json:"expiresIn"`
	Interval                json.Number `json:"interval"`
}

// startDevice registers a client and starts the device authorization. The user opens the link, checks the
// code and approves; this provider polls (continueDevice). The pending state holds the client secret and the
// device code and is encrypted by the Broker.
func (s *session) startDevice(ctx context.Context, cfg config, startURL, ssoRegion, account, role string) (credential.Result, error) {
	reg, err := s.registerClient(ctx, ssoRegion)
	if err != nil {
		return credential.Result{}, err
	}
	status, body, err := s.ssoCall(ctx, http.MethodPost, ssoOIDC(ssoRegion)+"/device_authorization", map[string]any{
		"clientId": reg.ClientID, "clientSecret": reg.ClientSecret, "startUrl": startURL,
	}, nil)
	if err != nil {
		return credential.Result{}, err
	}
	var d deviceAuth
	if status != http.StatusOK || json.Unmarshal(body, &d) != nil || d.DeviceCode == "" || d.UserCode == "" || d.VerificationURI == "" {
		return credential.Result{}, errSSORequest
	}
	// The address is opened by the browser of the user, not by this server, but it must not be a way to smuggle a
	// javascript: or file: address into the interface.
	link := d.VerificationURI
	if d.VerificationURIComplete != "" {
		link = d.VerificationURIComplete
	}
	for _, a := range []string{d.VerificationURI, link} {
		if err := idpnet.CheckURL(s.desktop, a); err != nil {
			return credential.Result{}, wrap(errSSORequest, err)
		}
	}
	now := s.now()
	life := defaultExpiry
	if n, err := d.ExpiresIn.Int64(); err == nil && n > 0 {
		life = min(time.Duration(n)*time.Second, maxExpiry)
	}
	interval := defaultPollSec
	if n, err := d.Interval.Int64(); err == nil && n >= 1 {
		interval = int(min(n, maxPollSec))
	}
	until := now.Add(life)
	p := pending{Step: stepDevice, Region: cfg.region, Cluster: cfg.cluster, StartURL: startURL, SSORegion: ssoRegion,
		AccountID: account, RoleName: role, ClientID: reg.ClientID, ClientSecret: reg.ClientSecret, ClientExpires: reg.ExpiresAt,
		DeviceCode: d.DeviceCode, Interval: interval, Expires: until.Unix(), NextPoll: now.Add(time.Duration(interval) * time.Second).Unix()}
	return credential.Result{
		Challenge: &credential.Challenge{Kind: credential.ChallengeDeviceCode, URL: link, UserCode: d.UserCode, Poll: true,
			IntervalSec: interval, ExpiresAt: until},
		Pending: p.encode(),
	}, nil
}

func waiting(c credential.Challenge, pend []byte) credential.Result {
	return credential.Result{Challenge: &c, Pending: pend}
}

type tokenAnswer struct {
	AccessToken  string      `json:"accessToken"`
	RefreshToken string      `json:"refreshToken"`
	ExpiresIn    json.Number `json:"expiresIn"`
}

func (s *session) createToken(ctx context.Context, ssoRegion string, req map[string]any) (tokenAnswer, string, error) {
	status, body, err := s.ssoCall(ctx, http.MethodPost, ssoOIDC(ssoRegion)+"/token", req, nil)
	if err != nil {
		return tokenAnswer{}, "", err
	}
	if status != http.StatusOK {
		code := oauthCode(body)
		if code == "" {
			code = "invalid_request"
		}
		return tokenAnswer{}, code, nil
	}
	var t tokenAnswer
	if json.Unmarshal(body, &t) != nil || !printableToken(t.AccessToken, maxSessionToken*4) {
		return tokenAnswer{}, "", errSSORequest
	}
	return t, "", nil
}

// continueDevice is a poll. The service is asked at most once per interval, however often the caller polls: a
// poll that comes early is answered "still waiting" without a request. authorization_pending keeps the
// interval, slow_down makes it 5 seconds longer (RFC 8628 3.5; aws-cli does the same).
func (s *session) continueDevice(ctx context.Context, req credential.Request, cfg config, p pending) (credential.Result, error) {
	if p.DeviceCode == "" || p.ClientID == "" || p.ClientSecret == "" || !validSSO(p.StartURL, p.SSORegion, p.AccountID, p.RoleName) ||
		checkRegion(p.Region) != nil || !clusterRE.MatchString(p.Cluster) {
		return credential.Result{}, credential.ErrChallengeExpired
	}
	now := s.now()
	if !now.Before(time.Unix(p.Expires, 0)) {
		return credential.Result{}, credential.ErrChallengeExpired
	}
	if now.Before(time.Unix(p.NextPoll, 0)) {
		return waiting(req.Challenge, req.Pending), nil
	}
	tok, code, err := s.createToken(ctx, p.SSORegion, map[string]any{
		"clientId": p.ClientID, "clientSecret": p.ClientSecret, "grantType": grantDevice, "deviceCode": p.DeviceCode,
	})
	if err != nil {
		return credential.Result{}, err // the network or an unreadable answer: try again later
	}
	switch code {
	case "":
	case "authorization_pending", "slow_down":
		if code == "slow_down" {
			p.Interval = min(p.Interval+slowDownSec, maxPollSec)
		}
		p.NextPoll = now.Add(time.Duration(p.Interval) * time.Second).Unix()
		c := req.Challenge
		c.IntervalSec = p.Interval
		return waiting(c, p.encode()), nil
	case "expired_token":
		return credential.Result{}, credential.ErrChallengeExpired
	case "access_denied":
		return credential.Result{}, staleGrant(errSSODenied)
	default:
		return credential.Result{}, staleGrant(errSSORequest)
	}
	sv := &saved{V: stateVersion, Type: savedSSO, Region: p.Region, Cluster: p.Cluster, StartURL: p.StartURL, SSORegion: p.SSORegion,
		AccountID: p.AccountID, RoleName: p.RoleName, ClientID: p.ClientID, ClientSecret: p.ClientSecret, ClientExpires: p.ClientExpires}
	s.applyToken(sv, tok)
	res, err := s.fromSSO(ctx, cfg.fill(p.Region, p.Cluster), sv, true)
	if err != nil {
		return credential.Result{}, err
	}
	return res, nil
}

// applyToken stores a fresh access token (and the refresh token, when the service sent one) in sv.
func (s *session) applyToken(sv *saved, t tokenAnswer) {
	life := time.Hour
	if n, err := t.ExpiresIn.Int64(); err == nil && n > 0 {
		life = time.Duration(n) * time.Second
	}
	sv.AccessToken = t.AccessToken
	sv.AccessExpires = s.now().Add(life).Unix()
	if t.RefreshToken != "" {
		sv.RefreshToken = t.RefreshToken
	}
}

// refreshAccess renews the access token with the refresh token (grant refresh_token, as botocore does). Any
// refusal means that the user has to sign in again (ErrInvalidGrant); the network is not a refusal.
func (s *session) refreshAccess(ctx context.Context, sv *saved) error {
	if sv.RefreshToken == "" || sv.ClientExpires != 0 && !s.now().Before(time.Unix(sv.ClientExpires, 0)) {
		return staleGrant(errSSORequest)
	}
	t, code, err := s.createToken(ctx, sv.SSORegion, map[string]any{
		"clientId": sv.ClientID, "clientSecret": sv.ClientSecret, "grantType": grantRefresh, "refreshToken": sv.RefreshToken,
	})
	if err != nil {
		return err
	}
	if code != "" {
		return staleGrant(errSSORequest)
	}
	s.applyToken(sv, t)
	return nil
}

// roleCredentials calls sso:GetRoleCredentials. 401 and 403 mean that the access token is not valid, 404 that
// the user has no such account or role: both end in a new sign-in.
func (s *session) roleCredentials(ctx context.Context, sv *saved) (*roleCreds, error) {
	q := url.Values{"role_name": {sv.RoleName}, "account_id": {sv.AccountID}}
	status, body, err := s.ssoCall(ctx, http.MethodGet, ssoPortal(sv.SSORegion)+"/federation/credentials?"+q.Encode(), nil,
		map[string]string{"x-amz-sso_bearer_token": sv.AccessToken})
	if err != nil {
		return nil, err
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, staleGrant(errSSORole)
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, staleGrant(errSSORequest)
	default:
		return nil, errSSORequest
	}
	var r struct {
		RoleCredentials struct {
			AccessKeyID     string      `json:"accessKeyId"`
			SecretAccessKey string      `json:"secretAccessKey"`
			SessionToken    string      `json:"sessionToken"`
			Expiration      json.Number `json:"expiration"` // milliseconds since the epoch
		} `json:"roleCredentials"`
	}
	if json.Unmarshal(body, &r) != nil {
		return nil, errAWSResponse
	}
	rc := r.RoleCredentials
	ms, err := strconv.ParseInt(rc.Expiration.String(), 10, 64)
	if err != nil || !akidRE.MatchString(rc.AccessKeyID) || !printableToken(rc.SecretAccessKey, 512) ||
		!printableToken(rc.SessionToken, maxSessionToken) || ms <= 0 {
		return nil, errAWSResponse
	}
	return &roleCreds{AccessKeyID: rc.AccessKeyID, SecretAccessKey: rc.SecretAccessKey, SessionToken: rc.SessionToken, Expires: ms / 1000}, nil
}

// fromSSO issues a token from an Identity Center record: it reuses the cached role credentials while they are
// good for the whole token, otherwise it renews the access token (refresh token) and asks for new role
// credentials. fresh is true right after the device approval: the principal is read from STS then.
func (s *session) fromSSO(ctx context.Context, cfg config, sv *saved, fresh bool) (credential.Result, error) {
	changed := fresh
	if sv.Creds == nil || !s.now().Add(credsMargin).Before(time.Unix(sv.Creds.Expires, 0)) {
		if !s.now().Add(tokenMargin).Before(time.Unix(sv.AccessExpires, 0)) {
			if err := s.refreshAccess(ctx, sv); err != nil {
				return credential.Result{}, err
			}
		}
		rc, err := s.roleCredentials(ctx, sv)
		if err != nil {
			return credential.Result{}, err
		}
		sv.Creds, changed = rc, true
		if cfg.roleARN == "" {
			sv.Principal = "" // read again below
		}
	}
	base := creds{accessKeyID: sv.Creds.AccessKeyID, secretAccessKey: sv.Creds.SecretAccessKey, sessionToken: sv.Creds.SessionToken}
	principal := sv.Principal
	if cfg.roleARN != "" {
		principal = cfg.roleARN
	} else if principal == "" {
		arn, err := s.identity(ctx, base, cfg.region)
		switch {
		case err == nil:
			principal = arn
			if sv.Principal != arn {
				sv.Principal, changed = arn, true
			}
		case fresh:
			return credential.Result{}, asAnswer(err)
		default:
			return credential.Result{}, err
		}
	}
	tok, err := s.issue(ctx, cfg, base, principal)
	if err != nil {
		return credential.Result{}, err
	}
	res := credential.Result{Token: tok}
	if changed {
		res.Save = sv.encode()
	}
	return res, nil
}

// trimmed returns the value of an answer field without surrounding spaces.
func trimmed(a credential.Answer, k string) string { return strings.TrimSpace(a[k]) }
