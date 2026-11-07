// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"bytes"
	"encoding/json"

	"github.com/24DSO-lab/kubron/internal/credential"
)

// savedVersion is the version of the saved sign-in data written by this provider.
const savedVersion = 1

// Saved record types.
const (
	savedRefresh  = "refresh"  // a refresh token only; the only thing kept for ScopeUser
	savedPassword = "password" // team login and password, optionally with the latest refresh token
)

// saved is the sign-in data this provider keeps (Request.Saved / Result.Save). It is one JSON object.
//
// Compatibility: ticket 3.7 stored {issuerUrl, clientId, clientSecret, scopes, username, password}
// without a version. Such a record (V == 0) is read as a "password" record, so the bytes copied by the
// migration work as they are; the first write replaces it with version 1, keeping all of its fields.
type saved struct {
	V            int      `json:"v,omitempty"`
	Type         string   `json:"type,omitempty"`
	IssuerURL    string   `json:"issuerUrl,omitempty"`
	ClientID     string   `json:"clientId,omitempty"`
	ClientSecret string   `json:"clientSecret,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	Username     string   `json:"username,omitempty"`
	Password     string   `json:"password,omitempty"`
	RefreshToken string   `json:"refreshToken,omitempty"`
}

// parseSaved reads saved data. An empty input is (nil, nil). Unreadable or unknown data is an error: the
// caller treats it as "nothing usable", asks the user again and erases the record.
func parseSaved(b []byte) (*saved, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	var s saved
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, errPending
	}
	switch {
	case s.V > savedVersion:
		return nil, errPending
	case s.V == 0: // ticket 3.7 format
		if s.Username == "" || s.Password == "" {
			if s.RefreshToken == "" {
				return nil, errPending
			}
			s.Type = savedRefresh
		} else {
			s.Type = savedPassword
		}
		s.V = savedVersion
	}
	switch s.Type {
	case savedRefresh:
		if s.RefreshToken == "" {
			return nil, errPending
		}
	case savedPassword:
		if s.Username == "" || s.Password == "" {
			return nil, errPending
		}
	default:
		return nil, errPending
	}
	return &s, nil
}

func (s *saved) encode() []byte {
	c := *s
	c.V = savedVersion
	b, _ := json.Marshal(c) // only strings and a string slice: cannot fail
	return b
}

// boundTo reports whether the record belongs to this issuer and client (an empty field in the record, as in
// refresh records written by older code, matches anything).
func (s *saved) boundTo(issuer, clientID string) bool {
	return (s.IssuerURL == "" || trimSlash(s.IssuerURL) == trimSlash(issuer)) &&
		(s.ClientID == "" || s.ClientID == clientID)
}

// newSaved builds the record to keep after a successful sign-in. Only ScopeTeam keeps the login and the
// password (to sign in again without asking): a personal password is never saved.
func newSaved(cfg config, scope credential.Scope, old *saved, username, password, refresh string) []byte {
	rec := saved{V: savedVersion, IssuerURL: cfg.issuer, ClientID: cfg.clientID, RefreshToken: refresh}
	rec.Type = savedRefresh
	if scope == credential.ScopeTeam {
		if old != nil && old.Type == savedPassword && username == "" {
			username, password = old.Username, old.Password
		}
		if username != "" && password != "" {
			rec.Type, rec.Username, rec.Password = savedPassword, username, password
			if old != nil { // keep what the 3.7 record carried: the secret and scopes are not in the kubeconfig
				rec.ClientSecret, rec.Scopes = old.ClientSecret, old.Scopes
			}
		}
	}
	if rec.Type == savedRefresh && refresh == "" {
		return nil
	}
	return rec.encode()
}
