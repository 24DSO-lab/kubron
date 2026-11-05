// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package eks

import (
	"bytes"
	"encoding/json"
)

// stateVersion is the version of the saved sign-in data and of the pending state written by this provider.
const stateVersion = 1

// Saved record types.
const (
	savedKeys = "keys" // access keys the user typed (long-lived, or temporary with a session token)
	savedSSO  = "sso"  // IAM Identity Center: client registration, tokens and the cached role credentials
)

// roleCreds are the temporary credentials of sso:GetRoleCredentials (they live about an hour).
type roleCreds struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	SessionToken    string `json:"sessionToken"`
	Expires         int64  `json:"expires"` // unix seconds
}

// saved is the data this provider keeps (Request.Saved / Result.Save): one JSON object, encrypted by the Broker.
// It holds secrets (keys, tokens, client secret) and is never printed or logged.
type saved struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	Region    string `json:"region,omitempty"`  // typed by the user when the kubeconfig has none
	Cluster   string `json:"cluster,omitempty"` // same
	Principal string `json:"principal,omitempty"`

	AccessKeyID     string `json:"accessKeyId,omitempty"`
	SecretAccessKey string `json:"secretAccessKey,omitempty"`
	SessionToken    string `json:"sessionToken,omitempty"`

	StartURL      string     `json:"ssoStartUrl,omitempty"`
	SSORegion     string     `json:"ssoRegion,omitempty"`
	AccountID     string     `json:"accountId,omitempty"`
	RoleName      string     `json:"roleName,omitempty"`
	ClientID      string     `json:"clientId,omitempty"`
	ClientSecret  string     `json:"clientSecret,omitempty"`
	ClientExpires int64      `json:"clientExpires,omitempty"`
	AccessToken   string     `json:"accessToken,omitempty"`
	AccessExpires int64      `json:"accessExpires,omitempty"`
	RefreshToken  string     `json:"refreshToken,omitempty"`
	Creds         *roleCreds `json:"creds,omitempty"`
}

// parseSaved reads saved data. An empty input is (nil, nil). Unreadable, unknown or inconsistent data is an
// error: the caller treats it as "nothing usable", asks the user again and erases the record.
func parseSaved(b []byte) (*saved, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	var s saved
	if err := json.Unmarshal(b, &s); err != nil || s.V != stateVersion {
		return nil, errState
	}
	if s.Region != "" && checkRegion(s.Region) != nil || s.Cluster != "" && !clusterRE.MatchString(s.Cluster) {
		return nil, errState
	}
	switch s.Type {
	case savedKeys:
		if !akidRE.MatchString(s.AccessKeyID) || !secretRE.MatchString(s.SecretAccessKey) ||
			s.SessionToken != "" && !printableToken(s.SessionToken, maxSessionToken) {
			return nil, errState
		}
	case savedSSO:
		if !validSSO(s.StartURL, s.SSORegion, s.AccountID, s.RoleName) || s.ClientID == "" || s.ClientSecret == "" {
			return nil, errState
		}
	default:
		return nil, errState
	}
	return &s, nil
}

func (s *saved) encode() []byte {
	c := *s
	c.V = stateVersion
	b, _ := json.Marshal(c) // only strings and integers: cannot fail
	return b
}

// Steps of the dialog (pending.Step).
const (
	stepMethod  = "method"  // asked: the sign-in method (and region / cluster name when the kubeconfig has none)
	stepKeys    = "keys"    // asked: access key ID and secret access key
	stepSession = "session" // asked: the same and a session token
	stepSSO     = "sso"     // asked: IAM Identity Center start URL, region, account and role
	stepDevice  = "device"  // waiting for the user to approve the device code
)

// pending is the private state of one sign-in dialog (Request.Pending / Result.Pending), encrypted by the
// Broker. In the device step it holds the client secret and the device code.
type pending struct {
	V       int    `json:"v"`
	Step    string `json:"step"`
	Region  string `json:"region,omitempty"`
	Cluster string `json:"cluster,omitempty"`

	StartURL  string `json:"ssoStartUrl,omitempty"`
	SSORegion string `json:"ssoRegion,omitempty"`
	AccountID string `json:"accountId,omitempty"`
	RoleName  string `json:"roleName,omitempty"`

	ClientID      string `json:"clientId,omitempty"`
	ClientSecret  string `json:"clientSecret,omitempty"`
	ClientExpires int64  `json:"clientExpires,omitempty"`
	DeviceCode    string `json:"deviceCode,omitempty"`
	Interval      int    `json:"interval,omitempty"`
	Expires       int64  `json:"expires,omitempty"`
	NextPoll      int64  `json:"nextPoll,omitempty"`
}

func (p pending) encode() []byte {
	p.V = stateVersion
	b, _ := json.Marshal(p)
	return b
}

// decodePending reads the pending state. Empty input is the first step; anything unreadable is errState.
func decodePending(b []byte) (pending, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return pending{V: stateVersion, Step: stepMethod}, nil
	}
	var p pending
	if err := json.Unmarshal(b, &p); err != nil || p.V != stateVersion {
		return pending{}, errState
	}
	switch p.Step {
	case stepMethod, stepKeys, stepSession, stepSSO, stepDevice:
		return p, nil
	}
	return pending{}, errState
}
