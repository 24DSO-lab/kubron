// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package idpnet builds the only HTTP client that talks to identity providers (ADR-0018, ticket C.1).
//
// Every request of the client goes through the same rules, whichever endpoint it targets (discovery,
// authorization, token, device, JWKS, userinfo, token revocation) and wherever the address came from
// (kubeconfig or a response of the identity provider):
//
//   - the web server accepts https only; the desktop also accepts http and loopback addresses;
//   - the SSRF policy netpolicy.ScopeClusterAPI is applied to the address a socket really connects to
//     and again to every redirect;
//   - requests have a timeout and responses are limited to MaxBody bytes (reading more is an error, not
//     a silent truncation);
//   - proxies from the environment are never used.
package idpnet

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/24DSO-labs/kubron/internal/netpolicy"
)

const (
	// MaxBody is the largest response body that is read (1 MiB).
	MaxBody = 1 << 20
	// DefaultTimeout is the whole-request timeout when Options.Timeout is zero.
	DefaultTimeout = 15 * time.Second

	maxRedirects = 5
)

var (
	// ErrScheme means that the address is not allowed in this mode (plain http on the web server, or a
	// scheme that is not http(s) at all).
	ErrScheme = errors.New("only HTTPS addresses are allowed for the identity provider")
	// ErrInsecureTLS means that skipping TLS verification was requested where it is forbidden.
	ErrInsecureTLS = errors.New("skipping TLS verification is not allowed here")
	// ErrBodyTooLarge means that a response exceeded MaxBody.
	ErrBodyTooLarge = errors.New("the identity provider response is too large")
	// ErrBadCA means that the supplied CA certificate data cannot be used.
	ErrBadCA = errors.New("the identity provider CA certificate is not valid")
)

// Options configure NewClient.
type Options struct {
	// Desktop allows http and loopback addresses and, with InsecureSkipVerify, an unverified TLS peer.
	Desktop bool
	// Policy is the SSRF policy. Nil means netpolicy.For(netpolicy.ScopeClusterAPI) on the web server
	// and no policy on the desktop. A policy that is not set at all restricts nothing (see netpolicy).
	Policy *netpolicy.Policy
	// Timeout bounds one request including redirects; DefaultTimeout when zero.
	Timeout time.Duration
	// CAData is PEM data that replaces the system roots (certificate-authority-data of the provider).
	CAData []byte
	// InsecureSkipVerify disables TLS verification. Desktop only; the web server gets ErrInsecureTLS.
	InsecureSkipVerify bool
}

func (o Options) policy() *netpolicy.Policy {
	if o.Policy != nil {
		return o.Policy
	}
	if o.Desktop {
		return nil
	}
	return netpolicy.For(netpolicy.ScopeClusterAPI)
}

// CheckURL validates an address before it is used: scheme, host and, for an IP literal, the policy.
// Names are not resolved here; the connection itself is checked by the client (the address a socket
// connects to is what counts, so DNS rebinding does not help).
func (o Options) CheckURL(raw string) error { return checkURL(o.Desktop, o.policy(), raw) }

// CheckURL is Options{Desktop: desktop}.CheckURL(raw).
func CheckURL(desktop bool, raw string) error { return Options{Desktop: desktop}.CheckURL(raw) }

func checkURL(desktop bool, pol *netpolicy.Policy, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return ErrScheme
	}
	return checkParsed(desktop, pol, u)
}

func checkParsed(desktop bool, pol *netpolicy.Policy, u *url.URL) error {
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if !desktop {
			return ErrScheme
		}
	default:
		return ErrScheme
	}
	if u.Hostname() == "" {
		return ErrScheme
	}
	return pol.CheckURL(u.String())
}

// NewClient returns the client for identity-provider requests.
func NewClient(o Options) (*http.Client, error) {
	if o.InsecureSkipVerify && !o.Desktop {
		return nil, ErrInsecureTLS
	}
	pol := o.policy()
	tr := pol.NewTransport()
	tr.Proxy = nil // also for a nil policy: no proxy from the environment
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if err := applyTLS(tr, o.CAData, o.InsecureSkipVerify); err != nil {
		return nil, err
	}
	return newClient(&guard{base: tr, desktop: o.Desktop, pol: pol}, o.Timeout), nil
}

func newClient(g *guard, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &http.Client{Transport: g, Timeout: timeout, CheckRedirect: g.checkRedirect}
}

func applyTLS(tr *http.Transport, ca []byte, insecure bool) error {
	cfg := tr.TLSClientConfig
	if cfg == nil {
		cfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	cfg = cfg.Clone()
	if len(ca) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(ca) {
			return ErrBadCA
		}
		cfg.RootCAs = pool
	}
	cfg.InsecureSkipVerify = insecure //nolint:gosec // desktop only, explicit kubeconfig option
	tr.TLSClientConfig = cfg
	return nil
}

// WithTLS derives a client with a provider-specific CA (or, on the desktop, without verification) from a
// client made by NewClient. A client of another origin (a test server's) is cloned when its transport is
// an *http.Transport. With neither option the base client is returned as it is.
func WithTLS(base *http.Client, desktop bool, ca []byte, insecure bool) (*http.Client, error) {
	if insecure && !desktop {
		return nil, ErrInsecureTLS
	}
	if len(ca) == 0 && !insecure {
		return base, nil
	}
	var g *guard
	switch t := base.Transport.(type) {
	case *guard:
		g = &guard{base: t.base.Clone(), desktop: t.desktop, pol: t.pol}
	case *http.Transport:
		g = &guard{base: t.Clone(), desktop: desktop, pol: Options{Desktop: desktop}.policy()}
		g.base.Proxy = nil
	default:
		return nil, ErrBadCA
	}
	if err := applyTLS(g.base, ca, insecure); err != nil {
		return nil, err
	}
	c := newClient(g, base.Timeout)
	return c, nil
}

// WrapClient derives a policy-enforcing client from an existing client. It preserves a standard
// transport's TLS roots and timeout (useful for private test authorities), but never preserves its proxy,
// redirect handler or custom RoundTripper. Unsupported transports fail closed.
func WrapClient(base *http.Client, desktop bool) (*http.Client, error) {
	if base == nil {
		return NewClient(Options{Desktop: desktop})
	}
	var tr *http.Transport
	if base.Transport == nil {
		tr = http.DefaultTransport.(*http.Transport).Clone()
	} else {
		switch t := base.Transport.(type) {
		case *guard:
			tr = t.base.Clone()
		case *http.Transport:
			tr = t.Clone()
		default:
			return nil, ErrBadCA
		}
	}
	tr.Proxy = nil
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		tr.TLSClientConfig = tr.TLSClientConfig.Clone()
		if tr.TLSClientConfig.MinVersion == 0 || tr.TLSClientConfig.MinVersion < tls.VersionTLS12 {
			tr.TLSClientConfig.MinVersion = tls.VersionTLS12
		}
	}
	pol := Options{Desktop: desktop}.policy()
	return newClient(&guard{base: tr, desktop: desktop, pol: pol}, base.Timeout), nil
}

// guard is the round tripper that enforces the rules on every request, including each redirect hop.
type guard struct {
	base    *http.Transport
	desktop bool
	pol     *netpolicy.Policy
}

func (g *guard) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := checkParsed(g.desktop, g.pol, r.URL); err != nil {
		closeBody(r)
		return nil, err
	}
	resp, err := g.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	resp.Body = &limitBody{rc: resp.Body, left: MaxBody}
	return resp, nil
}

func (g *guard) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("stopped after too many redirects")
	}
	return checkParsed(g.desktop, g.pol, req.URL)
}

func closeBody(r *http.Request) {
	if r.Body != nil {
		_ = r.Body.Close()
	}
}

// limitBody fails with ErrBodyTooLarge when more than MaxBody bytes are read.
type limitBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *limitBody) Read(p []byte) (int, error) {
	if b.left < 0 {
		return 0, ErrBodyTooLarge
	}
	if int64(len(p)) > b.left+1 {
		p = p[:b.left+1]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	if b.left < 0 {
		return n - int(-b.left), ErrBodyTooLarge
	}
	return n, err
}

func (b *limitBody) Close() error { return b.rc.Close() }

// ReadBody reads a whole response body (already limited by the client) and closes it.
func ReadBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
