// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package eks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// Values with a recognisable shape. The secrets carry CANARY so that a leak is easy to see.
const (
	testAKID    = "AKIAIOSFODNN7CANARY"
	testSecret  = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYCANARYKEY"
	testASIA    = "ASIAIOSFODNN7CANARY"
	testSession = "FwoGZXIvYXdzEBYaCANARY-session-token+/=="
	testRole    = "arn:aws:iam::111122223333:role/kubron-test"
	testARN     = "arn:aws:iam::111122223333:user/alice"
	testTempID  = "ASIATEMPORARYCANARY1"
	testTempSec = "temp-secret-CANARY-0123456789abcdef"
	testTempTok = "temp-session-token-CANARY"
	testSSOTok  = "sso-access-token-CANARY"
	testSSORef  = "sso-refresh-token-CANARY"
	testCliSec  = "sso-client-secret-CANARY"
	testDevCode = "device-code-CANARY-4711"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeAWS plays STS, IAM Identity Center OIDC and the SSO portal. Requests reach it through a transport that
// sends every https request to the test server and keeps the original host, so the tests also see which hosts
// the provider asked for. STS answers verify the SigV4 signature of the request with the secret they know.
type fakeAWS struct {
	t     *testing.T
	srv   *httptest.Server
	clock *fakeClock

	mu       sync.Mutex
	hosts    []string
	hits     map[string]int // by host prefix + path
	bodies   []string       // request bodies seen (to look for leaks)
	keys     map[string]string
	tokens   map[string]string // session token a key needs
	expired  map[string]bool
	arns     map[string]string
	roleOK   bool
	roleSeen []string
	down     bool

	// IAM Identity Center
	deviceStatus string // "pending", "approved", "denied", "slow", "expired"
	devicePolls  int
	verifyURI    string
	accessTTL    int
	refreshOK    bool
	roleMissing  bool
	issuedRefr   bool
	lifetime     time.Duration
}

func newFakeAWS(t *testing.T) *fakeAWS {
	f := &fakeAWS{t: t, clock: &fakeClock{t: time.Unix(1700000000, 0).UTC()}, hits: map[string]int{},
		keys:   map[string]string{testAKID: testSecret, testASIA: testSecret},
		tokens: map[string]string{testASIA: testSession}, expired: map[string]bool{},
		arns:   map[string]string{testAKID: testARN, testASIA: "arn:aws:sts::111122223333:assumed-role/dev/alice", testTempID: "arn:aws:sts::111122223333:assumed-role/kubron-test/kubron"},
		roleOK: true, deviceStatus: "pending", verifyURI: "https://device.example.com/authorize", accessTTL: 3600,
		refreshOK: true, issuedRefr: true, lifetime: time.Hour}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAWS) env() credential.Env {
	target := strings.TrimPrefix(f.srv.URL, "http://")
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" {
			f.t.Errorf("a request with scheme %q: only https may be used", r.URL.Scheme)
		}
		r2 := r.Clone(r.Context())
		r2.URL.Scheme, r2.URL.Host = "http", target
		r2.Host = r.URL.Host
		return http.DefaultTransport.RoundTrip(r2)
	})}
	return credential.Env{HTTP: hc, Now: f.clock.Now}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func (f *fakeAWS) hit(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for k, v := range f.hits {
		if strings.HasPrefix(k, prefix) {
			n += v
		}
	}
	return n
}

func (f *fakeAWS) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hosts)
}

func (f *fakeAWS) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.hosts = append(f.hosts, r.Host)
	f.bodies = append(f.bodies, string(body))
	down := f.down
	f.mu.Unlock()
	if down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	switch {
	case strings.HasPrefix(r.Host, "sts."):
		f.sts(w, r, body)
	case strings.HasPrefix(r.Host, "oidc."):
		f.oidc(w, r, body)
	case strings.HasPrefix(r.Host, "portal.sso."):
		f.portal(w, r)
	default:
		f.t.Errorf("unexpected host %q", r.Host)
		http.NotFound(w, r)
	}
}

func (f *fakeAWS) count(key string) {
	f.mu.Lock()
	f.hits[key]++
	f.mu.Unlock()
}

func stsError(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	fmt.Fprintf(w, `<ErrorResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><Error><Type>Sender</Type><Code>%s</Code><Message>secret details that must never be shown</Message></Error><RequestId>r</RequestId></ErrorResponse>`, code)
}

// verifySigV4 recomputes the Authorization header of a header-signed request and compares.
func (f *fakeAWS) verifySigV4(r *http.Request, body []byte) (akid, token string, ok bool) {
	auth := r.Header.Get("Authorization")
	rest, found := strings.CutPrefix(auth, "AWS4-HMAC-SHA256 ")
	if !found {
		return "", "", false
	}
	parts := map[string]string{}
	for _, kv := range strings.Split(rest, ", ") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			parts[k] = v
		}
	}
	scope := strings.Split(parts["Credential"], "/")
	if len(scope) != 5 {
		return "", "", false
	}
	akid, region := scope[0], scope[2]
	secret, known := f.keys[akid]
	if !known {
		return akid, "", false
	}
	at, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
	if err != nil {
		return akid, "", false
	}
	token = r.Header.Get("X-Amz-Security-Token")
	req, _ := http.NewRequest(r.Method, "https://"+r.Host+r.URL.RequestURI(), strings.NewReader(string(body)))
	for _, h := range strings.Split(parts["SignedHeaders"], ";") {
		if h != "host" && h != "x-amz-date" && h != "x-amz-security-token" {
			req.Header.Set(h, r.Header.Get(h))
		}
	}
	sum := sha256.Sum256(body)
	err = v4.NewSigner().SignHTTP(r.Context(), aws.Credentials{AccessKeyID: akid, SecretAccessKey: secret, SessionToken: token}, req,
		hex.EncodeToString(sum[:]), "sts", region, at)
	return akid, token, err == nil && req.Header.Get("Authorization") == auth
}

func (f *fakeAWS) sts(w http.ResponseWriter, r *http.Request, body []byte) {
	form, _ := url.ParseQuery(string(body))
	action := form.Get("Action")
	f.count("sts " + action)
	akid, token, ok := f.verifySigV4(r, body)
	f.mu.Lock()
	secretKnown := false
	_, secretKnown = f.keys[akid]
	need := f.tokens[akid]
	expired := f.expired[akid]
	f.mu.Unlock()
	switch {
	case !secretKnown:
		stsError(w, 403, "InvalidClientTokenId")
		return
	case need != "" && token != need:
		stsError(w, 403, "InvalidClientTokenId")
		return
	case !ok:
		stsError(w, 403, "SignatureDoesNotMatch")
		return
	case expired:
		stsError(w, 403, "ExpiredToken")
		return
	}
	switch action {
	case "GetCallerIdentity":
		fmt.Fprintf(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Arn>%s</Arn><UserId>AIDA</UserId><Account>111122223333</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`, f.arns[akid])
	case "AssumeRole":
		f.mu.Lock()
		f.roleSeen = append(f.roleSeen, form.Get("RoleArn")+"|"+form.Get("RoleSessionName")+"|"+form.Get("DurationSeconds"))
		f.keys[testTempID], f.tokens[testTempID] = testTempSec, testTempTok
		ok := f.roleOK
		f.mu.Unlock()
		if !ok {
			stsError(w, 403, "AccessDenied")
			return
		}
		fmt.Fprintf(w, `<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleResult><Credentials><AccessKeyId>%s</AccessKeyId><SecretAccessKey>%s</SecretAccessKey><SessionToken>%s</SessionToken><Expiration>2030-01-01T00:00:00Z</Expiration></Credentials><AssumedRoleUser><Arn>arn:aws:sts::111122223333:assumed-role/kubron-test/kubron</Arn></AssumedRoleUser></AssumeRoleResult></AssumeRoleResponse>`, testTempID, testTempSec, testTempTok)
	default:
		stsError(w, 400, "InvalidAction")
	}
}

func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeAWS) oidc(w http.ResponseWriter, r *http.Request, body []byte) {
	f.count("oidc " + r.URL.Path)
	var in map[string]any
	_ = json.Unmarshal(body, &in)
	switch r.URL.Path {
	case "/client/register":
		if in["clientType"] != "public" {
			jsonOut(w, 400, map[string]string{"error": "invalid_client_metadata"})
			return
		}
		jsonOut(w, 200, map[string]any{"clientId": "client-id-1", "clientSecret": testCliSec, "clientSecretExpiresAt": f.clock.Now().Add(90 * 24 * time.Hour).Unix()})
	case "/device_authorization":
		if in["clientSecret"] != testCliSec || in["startUrl"] == "" {
			jsonOut(w, 401, map[string]string{"error": "invalid_client"})
			return
		}
		jsonOut(w, 200, map[string]any{"deviceCode": testDevCode, "userCode": "ABCD-EFGH", "verificationUri": f.verifyURI,
			"verificationUriComplete": f.verifyURI + "?user_code=ABCD-EFGH", "expiresIn": 600, "interval": 5})
	case "/token":
		f.token(w, in)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeAWS) token(w http.ResponseWriter, in map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch in["grantType"] {
	case grantDevice:
		f.devicePolls++
		if in["deviceCode"] != testDevCode {
			jsonOut(w, 400, map[string]string{"error": "invalid_grant"})
			return
		}
		switch f.deviceStatus {
		case "pending":
			jsonOut(w, 400, map[string]string{"error": "authorization_pending"})
		case "slow":
			f.deviceStatus = "pending"
			jsonOut(w, 400, map[string]string{"error": "slow_down"})
		case "denied":
			jsonOut(w, 400, map[string]string{"error": "access_denied"})
		case "expired":
			jsonOut(w, 400, map[string]string{"error": "expired_token"})
		default:
			out := map[string]any{"accessToken": testSSOTok, "tokenType": "Bearer", "expiresIn": f.accessTTL}
			if f.issuedRefr {
				out["refreshToken"] = testSSORef
			}
			jsonOut(w, 200, out)
		}
	case grantRefresh:
		if !f.refreshOK || in["refreshToken"] != testSSORef || in["clientSecret"] != testCliSec {
			jsonOut(w, 400, map[string]string{"error": "invalid_grant"})
			return
		}
		jsonOut(w, 200, map[string]any{"accessToken": testSSOTok, "tokenType": "Bearer", "expiresIn": f.accessTTL})
	default:
		jsonOut(w, 400, map[string]string{"error": "unsupported_grant_type"})
	}
}

func (f *fakeAWS) portal(w http.ResponseWriter, r *http.Request) {
	f.count("portal " + r.URL.Path)
	if r.Header.Get("x-amz-sso_bearer_token") != testSSOTok {
		jsonOut(w, 401, map[string]string{"message": "no"})
		return
	}
	f.mu.Lock()
	missing, life := f.roleMissing, f.lifetime
	f.mu.Unlock()
	if missing || r.URL.Query().Get("account_id") != "111122223333" || r.URL.Query().Get("role_name") != "Admin" {
		jsonOut(w, 404, map[string]string{"message": "no such role"})
		return
	}
	f.mu.Lock()
	f.keys[testTempID], f.tokens[testTempID] = testTempSec, testTempTok
	f.mu.Unlock()
	jsonOut(w, 200, map[string]any{"roleCredentials": map[string]any{"accessKeyId": testTempID, "secretAccessKey": testTempSec,
		"sessionToken": testTempTok, "expiration": f.clock.Now().Add(life).UnixMilli()}})
}

// --- helpers of the tests ---------------------------------------------------------------------------

func desc(params map[string]string) credential.Descriptor {
	p := map[string]string{"authenticator": "aws-cli", "cluster-name": "prod", "region": "us-east-1"}
	for k, v := range params {
		if v == "" {
			delete(p, k)
		} else {
			p[k] = v
		}
	}
	return credential.Descriptor{Kind: credential.KindEKS, Params: p}
}

func subjectUser() credential.Subject {
	return credential.Subject{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1"}
}

func mustToken(t *testing.T, res credential.Result, err error) credential.Token {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Token == nil || res.Challenge != nil {
		t.Fatalf("expected exactly a token, got %v", res)
	}
	return *res.Token
}

func mustChallenge(t *testing.T, res credential.Result, err error, kind credential.ChallengeKind) credential.Challenge {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Challenge == nil || res.Token != nil || res.Challenge.Kind != kind {
		t.Fatalf("expected exactly a %s challenge, got %v", kind, res)
	}
	return *res.Challenge
}

func names(c credential.Challenge) string {
	var n []string
	for _, f := range c.Fields {
		n = append(n, f.Name)
	}
	return strings.Join(n, ",")
}

// step runs one Continue with the previous result's pending state.
func step(t *testing.T, p *Provider, env credential.Env, d credential.Descriptor, prev credential.Result, saved []byte, a credential.Answer) (credential.Result, error) {
	t.Helper()
	return p.Continue(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: saved, Pending: prev.Pending, Challenge: *prev.Challenge, Answer: a})
}

// signIn goes through the whole keys dialog and returns the last result.
func signIn(t *testing.T, p *Provider, env credential.Env, d credential.Descriptor, method string, keys credential.Answer) (credential.Result, error) {
	t.Helper()
	res, err := p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
	mustChallenge(t, res, err, credential.ChallengeSecretFields)
	first := credential.Answer{fieldMethod: method}
	for _, f := range res.Challenge.Fields {
		switch f.Name {
		case fieldRegion:
			first[fieldRegion] = "us-east-1"
		case fieldCluster:
			first[fieldCluster] = "prod"
		}
	}
	res, err = step(t, p, env, d, res, nil, first)
	mustChallenge(t, res, err, credential.ChallengeSecretFields)
	return step(t, p, env, d, res, nil, keys)
}

