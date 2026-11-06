// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package gke

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

const (
	testEmail  = "kubron-test@example-project.iam.gserviceaccount.com"
	testKeyID  = "0123456789abcdef0123456789abcdef01234567"
	testAccess = "ya29.test-access-token-1f9c"
)

var testRSA = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
})

func pkcs8PEM(t *testing.T, k *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// keyJSON builds a key file the way Google issues it; over replaces or adds fields (a nil value removes one).
func keyJSON(t *testing.T, over map[string]any) string {
	t.Helper()
	m := map[string]any{
		"type":                        "service_account",
		"project_id":                  "example-project",
		"private_key_id":              testKeyID,
		"private_key":                 pkcs8PEM(t, testRSA()),
		"client_email":                testEmail,
		"client_id":                   "123456789012345678901",
		"auth_uri":                    "https://accounts.google.com/o/oauth2/auth",
		"token_uri":                   "https://oauth2.googleapis.com/token",
		"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
		"universe_domain":             "googleapis.com",
	}
	for k, v := range over {
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// call is one request that reached the fake token endpoint.
type call struct {
	Method string
	Host   string
	Path   string
	Form   url.Values
}

// google is a TLS test server that plays the token endpoint, and a client whose every connection ends there.
// The client keeps the address it was asked for, so a test can tell "the request went to the Google address and
// was served by the fake" from "the request was aimed at another host".
type google struct {
	t       *testing.T
	mu      sync.Mutex
	calls   []call
	urls    []string
	handler func(call) (int, string)
	client  *http.Client
}

type recordingTransport struct {
	g    *google
	next http.RoundTripper
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.g.mu.Lock()
	r.g.urls = append(r.g.urls, req.URL.String())
	r.g.mu.Unlock()
	return r.next.RoundTrip(req)
}

// trapDefaultTransport makes any use of http.DefaultClient or http.DefaultTransport fail the test.
func trapDefaultTransport(t *testing.T) {
	t.Helper()
	old := http.DefaultTransport
	var mu sync.Mutex
	hits := 0
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		mu.Lock()
		hits++
		mu.Unlock()
		return nil, fmt.Errorf("default transport used")
	})
	t.Cleanup(func() {
		http.DefaultTransport = old
		mu.Lock()
		defer mu.Unlock()
		if hits != 0 {
			t.Errorf("http.DefaultTransport was used %d times", hits)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newGoogle(t *testing.T, h func(call) (int, string)) *google {
	t.Helper()
	trapDefaultTransport(t)
	g := &google{t: t, handler: h}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		form, _ := url.ParseQuery(string(body))
		c := call{Method: r.Method, Host: r.Host, Path: r.URL.Path, Form: form}
		g.mu.Lock()
		g.calls = append(g.calls, c)
		g.mu.Unlock()
		status, resp := g.handler(c)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", srv.Listener.Addr().String())
		},
		// The httptest certificate is valid for example.com; the Host header still says oauth2.googleapis.com.
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "example.com"},
	}
	t.Cleanup(tr.CloseIdleConnections)
	g.client = &http.Client{Transport: &recordingTransport{g: g, next: tr}, Timeout: 10 * time.Second}
	return g
}

func (g *google) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.calls)
}

func (g *google) snapshot() ([]call, []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]call(nil), g.calls...), append([]string(nil), g.urls...)
}

// tokenOK answers like Google does for a valid assertion.
func tokenOK(expiresIn int) func(call) (int, string) {
	return func(call) (int, string) {
		return 200, fmt.Sprintf(`{"access_token":%q,"token_type":"Bearer","expires_in":%d}`, testAccess, expiresIn)
	}
}

func replyWith(status int, body string) func(call) (int, string) {
	return func(call) (int, string) { return status, body }
}

var fixedNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func envFor(g *google) credential.Env {
	return credential.Env{HTTP: g.client, Now: func() time.Time { return fixedNow }}
}

func continueReq(answer string) credential.Request {
	return credential.Request{
		Descriptor: credential.Descriptor{Kind: credential.KindGKE, Params: map[string]string{}},
		Subject:    credential.Subject{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1"},
		Challenge:  credential.Challenge{Kind: credential.ChallengeSecretFields},
		Answer:     credential.Answer{FieldServiceAccountJSON: answer},
	}
}

func beginReq(saved []byte) credential.Request {
	return credential.Request{
		Descriptor: credential.Descriptor{Kind: credential.KindGKE, Params: map[string]string{}},
		Subject:    credential.Subject{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1"},
		Saved:      saved,
	}
}

// decodeAssertion checks the signature of the assertion with the public key and returns header and claims.
func decodeAssertion(t *testing.T, assertion string, pub *rsa.PublicKey) (map[string]any, map[string]any) {
	t.Helper()
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		t.Fatalf("assertion has %d parts", len(parts))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("assertion signature: %v", err)
	}
	decode := func(s string) map[string]any {
		raw, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	return decode(parts[0]), decode(parts[1])
}

func pkcs1PEM(k *rsa.PrivateKey) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
}

// refusingClient fails like idpnet does when the address policy refuses a connection.
func refusingClient() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Err: fmt.Errorf("address 10.0.0.1 is not allowed")}
	})}
}
