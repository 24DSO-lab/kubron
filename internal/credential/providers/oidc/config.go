// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"encoding/base64"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/idpnet"
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

	useAccessToken bool       // the credential is the access token instead of the ID token
	extraAuth      url.Values // extra parameters of the authorization request
	caPEM          []byte     // certificate-authority-data of the provider (replaces the system roots)
	insecure       bool       // insecure-skip-tls-verify (desktop only)
	listen         []string   // loopback listen addresses of the desktop authorization-code flow
	redirectHost   string     // host name of the redirect URL (default localhost)
	skipBrowser    bool       // do not open the browser
}

// Defaults of kubelogin: the same ports, so that redirect URLs registered for it keep working.
var defaultListen = []string{"127.0.0.1:8000", "127.0.0.1:18000"}

// reservedAuthParams cannot be set through auth-request-extra-params: the provider sets them itself.
var reservedAuthParams = []string{"response_type", "client_id", "redirect_uri", "scope", "state", "nonce",
	"code_challenge", "code_challenge_method", "request", "request_uri"}

// fileParams would make the provider read a file of the machine it runs on. Nothing here reads files: a CA
// comes inline (ca-data) and anything that names a path is refused, on the desktop too.
var fileParams = []string{"ca-file", "certificate-authority", "idp-certificate-authority", "ca-path", "token-cache-dir"}

func trimSlash(s string) string { return strings.TrimRight(s, "/") }

// newConfig reads the descriptor. Values missing there are taken from a ticket 3.7 record (issuer, client,
// secret, scopes were entered together with the password); the descriptor wins when both have a value.
func newConfig(d credential.Descriptor, sv *saved, desktop bool) (config, error) {
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
	if err := c.readOptions(p, desktop); err != nil {
		return config{}, err
	}
	return c, nil
}

// readOptions reads the optional settings and applies the rules of the mode: on the web server
// insecure-skip-tls-verify is refused and so is everything that names a file.
func (c *config) readOptions(p map[string]string, desktop bool) error {
	for _, k := range fileParams {
		if p[k] != "" {
			return credential.ErrUnsupported
		}
	}
	c.insecure = isTrue(p["insecure-skip-tls-verify"])
	if c.insecure && !desktop {
		return credential.ErrUnsupported
	}
	c.useAccessToken = isTrue(p["use-access-token"])
	c.skipBrowser = isTrue(p["skip-open-browser"])
	if ca := strings.TrimSpace(p["ca-data"]); ca != "" {
		pem, err := decodeCA(ca)
		if err != nil {
			return err
		}
		c.caPEM = pem
	}
	if raw := p["auth-request-extra-params"]; raw != "" {
		q, err := url.ParseQuery(raw)
		if err != nil {
			return credential.ErrUnsupported
		}
		for _, k := range reservedAuthParams {
			q.Del(k)
		}
		c.extraAuth = q
	}
	listen, err := parseListen(p["listen-address"])
	if err != nil {
		return err
	}
	c.listen = listen
	c.redirectHost = "localhost"
	if h := strings.TrimSpace(p["redirect-url-hostname"]); h != "" {
		if !validHost(h) {
			return credential.ErrUnsupported
		}
		c.redirectHost = h
	}
	return nil
}

func isTrue(s string) bool { return strings.EqualFold(strings.TrimSpace(s), "true") }

// decodeCA accepts the base64 of a kubeconfig certificate-authority-data and, leniently, PEM text.
func decodeCA(v string) ([]byte, error) {
	if strings.Contains(v, "-----BEGIN") {
		return []byte(v), nil
	}
	b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(v), ""))
	if err != nil || !strings.Contains(string(b), "-----BEGIN") {
		return nil, idpnet.ErrBadCA
	}
	return b, nil
}

// parseListen reads "127.0.0.1:8000,127.0.0.1:18000". Only loopback addresses are accepted (RFC 8252 7.3):
// the redirect carries an authorization code and must not be reachable from the network.
func parseListen(v string) ([]string, error) {
	if strings.TrimSpace(v) == "" {
		return defaultListen, nil
	}
	var out []string
	for _, a := range strings.Split(v, ",") {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		host, port, err := net.SplitHostPort(a)
		if err != nil {
			return nil, credential.ErrUnsupported
		}
		if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
			return nil, credential.ErrUnsupported
		}
		if host == "localhost" {
			host = "127.0.0.1"
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return nil, credential.ErrUnsupported
		}
		out = append(out, net.JoinHostPort(host, port))
	}
	if len(out) == 0 {
		return nil, credential.ErrUnsupported
	}
	return out, nil
}

// validHost accepts a bare host name or IP for the redirect URL.
func validHost(h string) bool {
	u, err := url.Parse("http://" + h)
	return err == nil && u.Host == h && u.Hostname() == h && u.Path == "" && u.User == nil
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
