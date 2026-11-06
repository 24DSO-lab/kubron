// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"net"
	"net/url"
	"strings"
)

// SameIssuer reports whether two issuer URLs name the same identity provider. The comparison is exact except
// for what people type differently by accident: the case of the scheme and the host, trailing slashes and the
// default port. A value that is not an absolute http(s) URL, or that has a query, a fragment or user
// information (an issuer identifier has none of them), never matches anything: in particular an empty value
// does not match an empty value.
func SameIssuer(a, b string) bool {
	na, ok := normalizeIssuer(a)
	if !ok {
		return false
	}
	nb, ok := normalizeIssuer(b)
	return ok && na == nb
}

func normalizeIssuer(s string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", false
	}
	port := u.Port()
	switch {
	case port != "" && !(scheme == "https" && port == "443") && !(scheme == "http" && port == "80"):
		host = net.JoinHostPort(host, port)
	case strings.Contains(host, ":"): // an IPv6 address without a port
		host = "[" + host + "]"
	}
	return scheme + "://" + host + strings.TrimRight(u.EscapedPath(), "/"), true
}
