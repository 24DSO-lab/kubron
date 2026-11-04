// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"strings"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// Values of Descriptor.Params["grant"].
const (
	grantPassword         = "password"
	grantAuthcode         = "authcode"
	grantAuthcodeKeyboard = "authcode-keyboard"
	grantDeviceCode       = "device-code"
	grantLegacyRefresh    = "legacy-refresh"
)

// config is what a Descriptor says about the identity provider, after defaults and checks.
type config struct {
	grant        string
	issuer       string
	clientID     string
	clientSecret string
	scopes       []string // always starts with "openid"
	// username and password from the kubeconfig (grant password); empty when the user is asked.
	username, password string
}

func trimSlash(s string) string { return strings.TrimRight(s, "/") }

// newConfig reads the descriptor. Values missing there are taken from a ticket 3.7 record (issuer, client,
// secret, scopes were entered together with the password); the descriptor wins when both have a value.
func newConfig(d credential.Descriptor, sv *saved) (config, error) {
	p := d.Params
	c := config{
		grant:        strings.TrimSpace(p["grant"]),
		issuer:       strings.TrimSpace(p["issuer-url"]),
		clientID:     strings.TrimSpace(p["client-id"]),
		clientSecret: p["client-secret"],
		username:     p["username"],
		password:     p["password"],
	}
	var extra []string
	if p["scope"] != "" {
		extra = strings.Fields(p["scope"])
	}
	if sv != nil {
		if c.issuer == "" {
			c.issuer = sv.IssuerURL
		}
		if c.clientID == "" {
			c.clientID = sv.ClientID
		}
		if c.clientSecret == "" {
			c.clientSecret = sv.ClientSecret
		}
		if len(extra) == 0 {
			extra = sv.Scopes
		}
	}
	if c.issuer == "" || c.clientID == "" {
		return config{}, errConfig
	}
	c.scopes = scopeList(extra)
	switch c.grant {
	case "":
		c.grant = grantAuthcode
	case grantPassword, grantAuthcode, grantAuthcodeKeyboard, grantDeviceCode, grantLegacyRefresh:
	default:
		return config{}, credential.ErrUnsupported
	}
	return c, nil
}

// scopeList returns "openid" followed by the extra scopes without duplicates.
func scopeList(extra []string) []string {
	out := []string{"openid"}
	seen := map[string]bool{"openid": true}
	for _, s := range extra {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
