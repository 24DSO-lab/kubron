// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package idpnet

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/netpolicy"
)

type fixedResolver map[string]netip.Addr

func (r fixedResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := r[host]; ok {
		return []netip.Addr{a}, nil
	}
	return nil, errors.New("no such host")
}

func webClient(t *testing.T, pol *netpolicy.Policy) *http.Client {
	t.Helper()
	c, err := NewClient(Options{Policy: pol})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func get(c *http.Client, url string) error {
	resp, err := c.Get(url)
	if err != nil {
		return err
	}
	_, err = ReadBody(resp)
	return err
}

func TestWebRequiresHTTPS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	if err := get(webClient(t, nil), srv.URL); !errors.Is(err, ErrScheme) {
		t.Fatalf("plain http on the web server must be refused, got %v", err)
	}
	if err := CheckURL(false, "ftp://idp.example.com/"); !errors.Is(err, ErrScheme) {
		t.Fatalf("got %v", err)
	}
	if err := CheckURL(false, "https://user:pw@idp.example.com/"); err == nil {
		t.Fatal("credentials in the address must be refused")
	}
}

func TestDesktopAllowsHTTPAndLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	c, err := NewClient(Options{Desktop: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := get(c, srv.URL); err != nil {
		t.Fatalf("desktop must reach a loopback http provider: %v", err)
	}
}

// Every kind of identity-provider endpoint is just a request of this client: the forbidden address is
// refused whatever the endpoint is, both for an IP literal and for a name that resolves to it.
func TestEveryEndpointRejectsForbiddenAddress(t *testing.T) {
	endpoints := []string{
		"/.well-known/openid-configuration", // discovery
		"/auth",                             // authorization
		"/token",                            // token
		"/device",                           // device authorization
		"/jwks",                             // JWKS
		"/userinfo",                         // userinfo
		"/revoke",                           // token revocation
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the request must not reach the server")
	}))
	defer srv.Close()
	port := srv.URL[strings.LastIndex(srv.URL, ":"):]
	pol := netpolicy.New("test", netpolicy.ProfilePermissive, nil)
	pol.Resolver = fixedResolver{"idp.test": netip.MustParseAddr("127.0.0.1"), "meta.test": netip.MustParseAddr("169.254.169.254")}
	c := webClient(t, pol)
	targets := []string{"https://127.0.0.1" + port, "https://idp.test" + port, "https://meta.test" + port,
		"https://169.254.169.254", "https://[::1]" + port}
	for _, ep := range endpoints {
		for _, base := range targets {
			err := get(c, base+ep)
			if err == nil {
				t.Errorf("%s%s: forbidden address accepted", base, ep)
			} else if !netpolicy.IsBlocked(err) {
				t.Errorf("%s%s: refused for another reason: %v", base, ep, err)
			}
		}
	}
}

func TestRedirectsAreChecked(t *testing.T) {
	var hops int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops++
		http.Redirect(w, r, r.URL.Query().Get("to"), http.StatusFound)
	}))
	defer srv.Close()
	pol := netpolicy.New("test", netpolicy.ProfilePermissive, []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")})
	base := webClient(t, pol)
	c := srv.Client()
	c.Transport.(*http.Transport).Proxy = nil
	base.Transport.(*guard).base.TLSClientConfig = c.Transport.(*http.Transport).TLSClientConfig

	if err := get(base, srv.URL+"/?to=https://169.254.169.254/token"); !netpolicy.IsBlocked(err) {
		t.Fatalf("redirect to a metadata address must be blocked, got %v", err)
	}
	if err := get(base, srv.URL+"/?to=http://idp.example.com/token"); !errors.Is(err, ErrScheme) {
		t.Fatalf("redirect to plain http must be refused, got %v", err)
	}
	if hops == 0 {
		t.Fatal("the allowed server was never reached")
	}
}

func TestBodyLimit(t *testing.T) {
	size := MaxBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", size)))
	}))
	defer srv.Close()
	c, _ := NewClient(Options{Desktop: true})
	if err := get(c, srv.URL); err != nil {
		t.Fatalf("a body of exactly MaxBody must pass: %v", err)
	}
	size = MaxBody + 1
	if err := get(c, srv.URL); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("an oversized body must be an error, got %v", err)
	}
}

func TestNoProxyFromEnvironment(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	c, _ := NewClient(Options{Desktop: true})
	if c.Transport.(*guard).base.Proxy != nil {
		t.Fatal("the transport must not use a proxy")
	}
	if err := get(c, srv.URL); err != nil {
		t.Fatal(err)
	}
}

func TestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer srv.Close()
	c, _ := NewClient(Options{Desktop: true, Timeout: 100 * time.Millisecond})
	if err := get(c, srv.URL); err == nil {
		t.Fatal("expected a timeout")
	}
}

func TestInsecureTLSOnlyOnDesktop(t *testing.T) {
	if _, err := NewClient(Options{InsecureSkipVerify: true}); !errors.Is(err, ErrInsecureTLS) {
		t.Fatalf("web must refuse insecure-skip-tls-verify, got %v", err)
	}
	base := webClient(t, nil)
	if _, err := WithTLS(base, false, nil, true); !errors.Is(err, ErrInsecureTLS) {
		t.Fatalf("got %v", err)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	desk, _ := NewClient(Options{Desktop: true})
	if err := get(desk, srv.URL); err == nil {
		t.Fatal("an unknown CA must fail verification")
	}
	skip, err := WithTLS(desk, true, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := get(skip, srv.URL); err != nil {
		t.Fatalf("desktop with insecure-skip-tls-verify: %v", err)
	}
}

func TestCustomCA(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	c, err := NewClient(Options{Desktop: true, CAData: caPEM})
	if err != nil {
		t.Fatal(err)
	}
	if err := get(c, srv.URL); err != nil {
		t.Fatalf("a client trusting the provider CA must connect: %v", err)
	}
	if _, err := NewClient(Options{CAData: []byte("not pem")}); !errors.Is(err, ErrBadCA) {
		t.Fatalf("got %v", err)
	}
	derived, err := WithTLS(webClient(t, nil), false, caPEM, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := get(derived, srv.URL); err != nil {
		t.Fatalf("derived client: %v", err)
	}
}

func TestWrapClientPreservesTLSAndAddsNetworkGuard(t *testing.T) {
	// The default single-user registry is intentionally empty; install the same
	// permissive cluster policy used by the server so this test exercises the guard.
	pol := netpolicy.New("idp-test", netpolicy.ProfilePermissive, nil)
	netpolicy.Set(netpolicy.ScopeClusterAPI, pol)
	t.Cleanup(func() { netpolicy.Set(netpolicy.ScopeClusterAPI, nil) })
	requests := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	desktop, err := WrapClient(srv.Client(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := get(desktop, srv.URL); err != nil {
		t.Fatalf("wrapped desktop client must preserve its test CA: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected one desktop request, got %d", requests)
	}

	requests = 0
	web, err := WrapClient(srv.Client(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := get(web, srv.URL); err == nil || !netpolicy.IsBlocked(err) {
		t.Fatalf("web client must block loopback before connecting, got %v", err)
	}
	if requests != 0 {
		t.Fatalf("web request reached loopback server: %d", requests)
	}
}

func TestWrapClientRejectsCustomRoundTripper(t *testing.T) {
	base := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unexpected request")
	})}
	if _, err := WrapClient(base, true); !errors.Is(err, ErrBadCA) {
		t.Fatalf("custom transports must fail closed, got %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
