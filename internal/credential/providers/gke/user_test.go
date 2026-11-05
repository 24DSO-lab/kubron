// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package gke

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

const (
	testClientID     = "1234567890-abcdef.apps.googleusercontent.com"
	testClientSecret = "GOCSPX-test-client-secret-77"
	testWebRedirect  = "https://kubron.example.com/auth/cluster/callback"
	testUserEmail    = "alice@example.com"
	testRefresh      = "1//0g-test-refresh-token-5521"
	testUserAccess   = "ya29.user-access-token-93bd"
	testChallengeID  = "chal-0123456789abcdef"
)

func userProvider(t *testing.T, redirect string) *Provider {
	t.Helper()
	p, err := NewWithUserOAuth(UserOAuth{ClientID: testClientID, ClientSecret: testClientSecret, WebRedirectURL: redirect})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func idToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]any{"alg": "RS256", "typ": "JWT"}) + "." + enc(claims) + ".c2lnbmF0dXJl"
}

func goodClaims(nonce string) map[string]any {
	return map[string]any{
		"iss": "https://accounts.google.com", "aud": testClientID, "azp": testClientID,
		"exp": fixedNow.Add(time.Hour).Unix(), "nonce": nonce, "email": testUserEmail, "email_verified": true,
	}
}

// userGoogle answers the token endpoint like Google: the code grant (with the ID token of the pending nonce
// that the test sets) and the refresh grant.
type userGoogle struct {
	*google
	nonce    string
	override func(claims map[string]any)
	refresh  string // refresh_token of the answer
	idOmit   bool
}

func newUserGoogle(t *testing.T) *userGoogle {
	u := &userGoogle{refresh: testRefresh}
	u.google = newGoogle(t, func(c call) (int, string) {
		switch c.Form.Get("grant_type") {
		case "authorization_code":
			claims := goodClaims(u.nonce)
			if u.override != nil {
				u.override(claims)
			}
			ans := map[string]any{"access_token": testUserAccess, "token_type": "Bearer", "expires_in": 3599, "refresh_token": u.refresh}
			if !u.idOmit {
				ans["id_token"] = idToken(t, claims)
			}
			b, _ := json.Marshal(ans)
			return 200, string(b)
		case "refresh_token":
			ans := map[string]any{"access_token": testUserAccess, "token_type": "Bearer", "expires_in": 1800}
			if u.refresh != "" && u.refresh != testRefresh {
				ans["refresh_token"] = u.refresh
			}
			b, _ := json.Marshal(ans)
			return 200, string(b)
		}
		return 400, `{"error":"unsupported_grant_type"}`
	})
	return u
}

// begin starts a web sign-in and returns the challenge as the Broker stores it (with an ID) and the pending state.
func begin(t *testing.T, p *Provider, env credential.Env) (credential.Challenge, []byte, pending) {
	t.Helper()
	res, err := p.Begin(context.Background(), env, beginReq(nil))
	if err != nil || res.Challenge == nil {
		t.Fatalf("Begin: %+v %v", res, err)
	}
	c := *res.Challenge
	c.ID = testChallengeID
	flow := flowPaste
	if c.Kind == credential.ChallengeBrowserCallback {
		flow = flowBrowser
	}
	pd, err := decodePending(res.Pending, flow)
	if err != nil {
		t.Fatal(err)
	}
	return c, res.Pending, pd
}

func answerReq(c credential.Challenge, pend []byte, redirect string) credential.Request {
	r := continueReq("")
	r.Challenge, r.Pending, r.Answer = c, pend, credential.Answer{fieldRedirectURL: redirect}
	return r
}

func webEnv(g *google) credential.Env { e := envFor(g); e.Desktop = false; return e }

func TestUserBeginBrowserCallbackBuildsTheAuthorizationRequest(t *testing.T) {
	g := newUserGoogle(t)
	c, pend, pd := begin(t, userProvider(t, testWebRedirect), webEnv(g.google))
	if c.Kind != credential.ChallengeBrowserCallback || !c.Poll || c.IntervalSec == 0 || len(c.Fields) != 0 {
		t.Fatalf("challenge = %+v", c)
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Scheme+"://"+u.Host+u.Path != AuthURL {
		t.Fatalf("authorization address = %q", c.URL)
	}
	q := u.Query()
	sum := sha256.Sum256([]byte(pd.Verifier))
	for k, want := range map[string]string{
		"response_type": "code", "client_id": testClientID, "redirect_uri": testWebRedirect,
		"scope":                 "openid https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/cloud-platform",
		"code_challenge":        base64.RawURLEncoding.EncodeToString(sum[:]),
		"code_challenge_method": "S256", "access_type": "offline", "prompt": "consent", "nonce": pd.Nonce,
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	if q.Get("state") == "" {
		t.Error("no state")
	}
	for _, secret := range []string{testClientSecret, pd.Verifier} {
		if strings.Contains(c.URL, secret) {
			t.Fatal("a secret is in the address the browser opens")
		}
	}
	if len(pend) == 0 || g.callCount() != 0 {
		t.Fatal("starting needs private state and no request")
	}
}

func TestUserBeginPasteVariant(t *testing.T) {
	g := newUserGoogle(t)
	for name, env := range map[string]credential.Env{
		"desktop":         {HTTP: g.client, Desktop: true},
		"web without URL": {HTTP: g.client},
	} {
		t.Run(name, func(t *testing.T) {
			p := userProvider(t, "")
			if name == "desktop" {
				p = userProvider(t, testWebRedirect) // the callback is for the web server only
			}
			c, _, pd := begin(t, p, env)
			if c.Kind != credential.ChallengePasteRedirect || len(c.Fields) != 1 || c.Fields[0].Name != "redirectUrl" || !c.Fields[0].Secret || c.Poll {
				t.Fatalf("challenge = %+v", c)
			}
			if !strings.HasPrefix(pd.RedirectURI, "http://127.0.0.1:") {
				t.Fatalf("redirect = %q", pd.RedirectURI)
			}
			u, _ := url.Parse(pd.RedirectURI)
			if port := u.Port(); len(port) != 5 || port < "49152" {
				t.Fatalf("port %q is not in the dynamic range", port)
			}
			if got, _ := url.Parse(c.URL); got.Query().Get("redirect_uri") != pd.RedirectURI {
				t.Fatal("the address does not carry the redirect")
			}
		})
	}
}

func TestWithoutClientTheKeyIsAsked(t *testing.T) {
	res, err := New().Begin(context.Background(), credential.Env{}, beginReq(nil))
	if err != nil || res.Challenge == nil || res.Challenge.Kind != credential.ChallengeSecretFields {
		t.Fatalf("%+v %v", res, err)
	}
	r := continueReq("")
	r.Challenge.Kind = credential.ChallengeBrowserCallback
	if _, err := New().Continue(context.Background(), credential.Env{}, r); !errors.Is(err, credential.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestUserCallbackWaitsUntilTheBrowserReturns(t *testing.T) {
	g := newUserGoogle(t)
	p := userProvider(t, testWebRedirect)
	c, pend, _ := begin(t, p, webEnv(g.google))
	res, err := p.Continue(context.Background(), webEnv(g.google), answerReq(c, pend, ""))
	if err != nil || res.Challenge == nil || res.Token != nil {
		t.Fatalf("%+v %v", res, err)
	}
	if res.Challenge.ID != "" || !res.Challenge.ExpiresAt.IsZero() || res.Challenge.Kind != c.Kind || !bytes.Equal(res.Pending, pend) {
		t.Fatalf("waiting must keep the request and leave ID and ExpiresAt to the Broker: %+v", res)
	}
	if g.callCount() != 0 {
		t.Fatal("waiting needs no request")
	}
}

func TestUserCallbackIssuesTokenAndSavesRefreshToken(t *testing.T) {
	g := newUserGoogle(t)
	p := userProvider(t, testWebRedirect)
	c, pend, pd := begin(t, p, webEnv(g.google))
	g.nonce = pd.Nonce
	back := testWebRedirect + "?state=" + testChallengeID + "&code=4%2F0AbCd-code&scope=openid&authuser=0"
	res, err := p.Continue(context.Background(), webEnv(g.google), answerReq(c, pend, back))
	if err != nil {
		t.Fatal(err)
	}
	if res.Token == nil || res.Challenge != nil || res.Pending != nil {
		t.Fatalf("want exactly a token: %+v", res)
	}
	if res.Token.Value != testUserAccess || res.Token.Principal != testUserEmail || !res.Token.Expiry.Equal(fixedNow.Add(3599*time.Second)) {
		t.Fatalf("token = %v principal %q expiry %v", *res.Token, res.Token.Principal, res.Token.Expiry)
	}
	calls, urls := g.snapshot()
	if len(calls) != 1 || urls[0] != TokenURL || calls[0].Host != "oauth2.googleapis.com" {
		t.Fatalf("calls = %d urls = %v", len(calls), urls)
	}
	f := calls[0].Form
	for k, want := range map[string]string{
		"grant_type": "authorization_code", "code": "4/0AbCd-code", "redirect_uri": testWebRedirect,
		"code_verifier": pd.Verifier, "client_id": testClientID, "client_secret": testClientSecret,
	} {
		if f.Get(k) != want {
			t.Errorf("form %s = %q, want %q", k, f.Get(k), want)
		}
	}
	sv, ok := parseSavedUser(res.Save)
	if !ok || sv.RefreshToken != testRefresh || sv.Email != testUserEmail || sv.ClientID != testClientID {
		t.Fatalf("saved = %v ok=%v", sv, ok)
	}
	if savedKind(res.Save) != "user" {
		t.Fatal("savedKind")
	}
}

func TestUserPasteIssuesToken(t *testing.T) {
	g := newUserGoogle(t)
	p := userProvider(t, "")
	c, pend, pd := begin(t, p, webEnv(g.google))
	g.nonce = pd.Nonce
	for _, back := range []string{pd.RedirectURI + "/?state=" + pd.State + "&code=abc", pd.RedirectURI + "?code=abc&state=" + pd.State} {
		res, err := p.Continue(context.Background(), webEnv(g.google), answerReq(c, pend, "  "+back+"\n"))
		if err != nil || res.Token == nil {
			t.Fatalf("%s: %+v %v", back, res, err)
		}
	}
}

func TestUserRedirectChecks(t *testing.T) {
	g := newUserGoogle(t)
	p := userProvider(t, testWebRedirect)
	c, pend, pd := begin(t, p, webEnv(g.google))
	g.nonce = pd.Nonce
	ok := testChallengeID
	cases := []struct {
		name string
		back string
		want error
	}{
		{"wrong state", testWebRedirect + "?state=other&code=c", credential.ErrAnswerRejected},
		{"no state", testWebRedirect + "?code=c", credential.ErrAnswerRejected},
		{"other host", "https://evil.example/auth/cluster/callback?state=" + ok + "&code=c", credential.ErrInvalidAnswer},
		{"other path", "https://kubron.example.com/other?state=" + ok + "&code=c", credential.ErrInvalidAnswer},
		{"userinfo", "https://u:p@kubron.example.com/auth/cluster/callback?state=" + ok + "&code=c", credential.ErrInvalidAnswer},
		{"other scheme", "http://kubron.example.com/auth/cluster/callback?state=" + ok + "&code=c", credential.ErrInvalidAnswer},
		{"two codes", testWebRedirect + "?state=" + ok + "&code=a&code=b", credential.ErrInvalidAnswer},
		{"two states", testWebRedirect + "?state=" + ok + "&state=" + ok + "&code=a", credential.ErrInvalidAnswer},
		{"no code", testWebRedirect + "?state=" + ok, credential.ErrInvalidAnswer},
		{"not an address", "%zz", credential.ErrInvalidAnswer},
		{"too long", testWebRedirect + "?state=" + ok + "&code=" + strings.Repeat("a", maxPastedURL), credential.ErrInvalidAnswer},
		{"denied", testWebRedirect + "?state=" + ok + "&error=access_denied", credential.ErrInvalidGrant},
		{"foreign issuer", testWebRedirect + "?state=" + ok + "&code=c&iss=https%3A%2F%2Fevil.example", credential.ErrInvalidGrant},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := p.Continue(context.Background(), webEnv(g.google), answerReq(c, pend, tc.back))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if res.Token != nil || res.Save != nil {
				t.Fatalf("res = %+v", res)
			}
		})
	}
	if g.callCount() != 0 {
		t.Fatalf("a bad redirect must not reach the network (%d calls)", g.callCount())
	}
}

func TestUserLostPendingStateExpiresTheRequest(t *testing.T) {
	g := newUserGoogle(t)
	p := userProvider(t, testWebRedirect)
	c, pend, _ := begin(t, p, webEnv(g.google))
	for _, bad := range [][]byte{nil, []byte("x"), []byte(`{"v":2}`), bytes.ReplaceAll(pend, []byte(`"browser-callback"`), []byte(`"paste"`))} {
		_, err := p.Continue(context.Background(), webEnv(g.google), answerReq(c, bad, testWebRedirect+"?state="+testChallengeID+"&code=c"))
		if !errors.Is(err, credential.ErrChallengeExpired) {
			t.Fatalf("pending %q: err = %v", bad, err)
		}
	}
}

func TestUserIDTokenChecks(t *testing.T) {
	cases := map[string]func(map[string]any){
		"wrong nonce":       func(c map[string]any) { c["nonce"] = "another" },
		"no nonce":          func(c map[string]any) { delete(c, "nonce") },
		"wrong audience":    func(c map[string]any) { c["aud"] = "other-client" },
		"audience list":     func(c map[string]any) { c["aud"] = []string{"other-client"} },
		"wrong authorized":  func(c map[string]any) { c["azp"] = "other-client" },
		"wrong issuer":      func(c map[string]any) { c["iss"] = "https://evil.example" },
		"expired":           func(c map[string]any) { c["exp"] = fixedNow.Add(-time.Minute).Unix() },
		"no expiry":         func(c map[string]any) { delete(c, "exp") },
		"unverified e-mail": func(c map[string]any) { c["email_verified"] = false },
		"unverified string": func(c map[string]any) { c["email_verified"] = "false" },
		"no e-mail":         func(c map[string]any) { delete(c, "email") },
		"bad e-mail":        func(c map[string]any) { c["email"] = "a b@x.com" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			g := newUserGoogle(t)
			p := userProvider(t, testWebRedirect)
			c, pend, pd := begin(t, p, webEnv(g.google))
			g.nonce, g.override = pd.Nonce, mutate
			res, err := p.Continue(context.Background(), webEnv(g.google), answerReq(c, pend, testWebRedirect+"?state="+testChallengeID+"&code=c"))
			if !errors.Is(err, credential.ErrInvalidGrant) || res.Token != nil || res.Save != nil {
				t.Fatalf("res=%+v err=%v", res, err)
			}
		})
	}
	t.Run("audience list with the client", func(t *testing.T) {
		g := newUserGoogle(t)
		p := userProvider(t, testWebRedirect)
		c, pend, pd := begin(t, p, webEnv(g.google))
		g.nonce, g.override = pd.Nonce, func(c map[string]any) { c["aud"] = []string{"x", testClientID}; delete(c, "azp") }
		if _, err := p.Continue(context.Background(), webEnv(g.google), answerReq(c, pend, testWebRedirect+"?state="+testChallengeID+"&code=c")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		g := newUserGoogle(t)
		p := userProvider(t, testWebRedirect)
		c, pend, _ := begin(t, p, webEnv(g.google))
		g.idOmit = true
		_, err := p.Continue(context.Background(), webEnv(g.google), answerReq(c, pend, testWebRedirect+"?state="+testChallengeID+"&code=c"))
		if !errors.Is(err, credential.ErrInvalidGrant) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestUserCodeExchangeFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   error
	}{
		"spent code":  {400, `{"error":"invalid_grant"}`, credential.ErrInvalidGrant},
		"server down": {503, `oops`, credential.ErrServiceUnavailable},
		"rate limit":  {429, `{"error":"rate_limit_exceeded"}`, credential.ErrServiceUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			g := newGoogle(t, replyWith(tc.status, tc.body))
			p := userProvider(t, testWebRedirect)
			c, pend, _ := begin(t, p, webEnv(g))
			_, err := p.Continue(context.Background(), webEnv(g), answerReq(c, pend, testWebRedirect+"?state="+testChallengeID+"&code=c"))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	t.Run("other OAuth error", func(t *testing.T) {
		g := newGoogle(t, replyWith(401, `{"error":"invalid_client"}`))
		p := userProvider(t, testWebRedirect)
		c, pend, _ := begin(t, p, webEnv(g))
		_, err := p.Continue(context.Background(), webEnv(g), answerReq(c, pend, testWebRedirect+"?state="+testChallengeID+"&code=c"))
		if err == nil || errors.Is(err, credential.ErrInvalidGrant) || errors.Is(err, credential.ErrServiceUnavailable) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no HTTP client", func(t *testing.T) {
		p := userProvider(t, testWebRedirect)
		g := newGoogle(t, tokenOK(1))
		c, pend, _ := begin(t, p, webEnv(g))
		_, err := p.Continue(context.Background(), credential.Env{}, answerReq(c, pend, testWebRedirect+"?state="+testChallengeID+"&code=c"))
		if !errors.Is(err, errNoHTTP) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		g := newUserGoogle(t)
		p := userProvider(t, testWebRedirect)
		c, pend, _ := begin(t, p, webEnv(g.google))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := p.Continue(ctx, webEnv(g.google), answerReq(c, pend, testWebRedirect+"?state="+testChallengeID+"&code=c"))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	})
}

func savedFor(clientID string) []byte {
	return savedUser{RefreshToken: testRefresh, Email: testUserEmail, ClientID: clientID}.encode()
}

func TestUserBeginRefreshesSilently(t *testing.T) {
	g := newUserGoogle(t)
	p := userProvider(t, testWebRedirect)
	res, err := p.Begin(context.Background(), webEnv(g.google), beginReq(savedFor(testClientID)))
	if err != nil || res.Token == nil || res.Challenge != nil || res.Save != nil {
		t.Fatalf("%+v %v", res, err)
	}
	if res.Token.Principal != testUserEmail || !res.Token.Expiry.Equal(fixedNow.Add(1800*time.Second)) {
		t.Fatalf("token = %v", *res.Token)
	}
	calls, urls := g.snapshot()
	f := calls[0].Form
	if len(calls) != 1 || urls[0] != TokenURL || f.Get("grant_type") != "refresh_token" || f.Get("refresh_token") != testRefresh ||
		f.Get("client_id") != testClientID || f.Get("client_secret") != testClientSecret {
		t.Fatalf("refresh request: %v %v", urls, f)
	}
}

func TestUserBeginSavesARotatedRefreshToken(t *testing.T) {
	g := newUserGoogle(t)
	g.refresh = "1//0g-rotated-token"
	res, err := userProvider(t, testWebRedirect).Begin(context.Background(), webEnv(g.google), beginReq(savedFor(testClientID)))
	if err != nil {
		t.Fatal(err)
	}
	if sv, ok := parseSavedUser(res.Save); !ok || sv.RefreshToken != "1//0g-rotated-token" || sv.Email != testUserEmail {
		t.Fatalf("save = %v", sv)
	}
}

func TestUserBeginWithRefusedRefreshTokenErasesAndAsksAgain(t *testing.T) {
	g := newGoogle(t, replyWith(400, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
	res, err := userProvider(t, testWebRedirect).Begin(context.Background(), webEnv(g), beginReq(savedFor(testClientID)))
	if err != nil || res.Challenge == nil || res.Challenge.Kind != credential.ChallengeBrowserCallback || res.Token != nil || res.Save == nil || len(res.Save) != 0 || len(res.Pending) == 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestUserBeginKeepsRefreshTokenWhenGoogleIsDown(t *testing.T) {
	g := newGoogle(t, replyWith(503, ``))
	res, err := userProvider(t, testWebRedirect).Begin(context.Background(), webEnv(g), beginReq(savedFor(testClientID)))
	if !errors.Is(err, credential.ErrServiceUnavailable) || res.Save != nil || res.Challenge != nil {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestUserSavedFromAnotherClientIsNotSentToGoogle(t *testing.T) {
	g := newUserGoogle(t)
	res, err := userProvider(t, testWebRedirect).Begin(context.Background(), webEnv(g.google), beginReq(savedFor("another-client.apps.googleusercontent.com")))
	if err != nil || res.Challenge == nil || res.Save == nil || len(res.Save) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if g.callCount() != 0 {
		t.Fatal("a refresh token of another client must not be sent")
	}
}

func TestSavedUserSignInWithoutClientFallsBackToKey(t *testing.T) {
	g := newUserGoogle(t)
	res, err := New().Begin(context.Background(), envFor(g.google), beginReq(savedFor(testClientID)))
	if err != nil || res.Challenge == nil || res.Challenge.Kind != credential.ChallengeSecretFields || res.Save == nil || len(res.Save) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if g.callCount() != 0 {
		t.Fatal("no request without a client")
	}
}

func TestSavedKeyStillWorksWhenTheClientIsConfigured(t *testing.T) {
	g := newGoogle(t, tokenOK(3599))
	k, _ := parseKey([]byte(keyJSON(t, nil)))
	p := userProvider(t, testWebRedirect)
	res, err := p.Begin(context.Background(), webEnv(g), beginReq(k.normalized()))
	if err != nil || res.Token == nil || res.Token.Principal != testEmail {
		t.Fatalf("%+v %v", res, err)
	}
	// A key that is no longer accepted is asked for again as a key, not replaced by a Google sign-in.
	g2 := newGoogle(t, replyWith(400, `{"error":"invalid_grant"}`))
	res, err = p.Begin(context.Background(), webEnv(g2), beginReq(k.normalized()))
	if err != nil || res.Challenge == nil || res.Challenge.Kind != credential.ChallengeSecretFields || len(res.Save) != 0 || res.Save == nil {
		t.Fatalf("%+v %v", res, err)
	}
	// Pasting a key still works.
	res, err = p.Continue(context.Background(), webEnv(g), continueReq(keyJSON(t, nil)))
	if err != nil || res.Token == nil {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestNewWithUserOAuthValidation(t *testing.T) {
	good := UserOAuth{ClientID: testClientID, ClientSecret: testClientSecret, WebRedirectURL: testWebRedirect}
	for name, mut := range map[string]func(*UserOAuth){
		"loopback http":  func(u *UserOAuth) { u.WebRedirectURL = "http://127.0.0.1:8080/auth/cluster/callback" },
		"localhost http": func(u *UserOAuth) { u.WebRedirectURL = "http://localhost:8080/auth/cluster/callback/" },
		"no secret":      func(u *UserOAuth) { u.ClientSecret = "" },
		"no redirect":    func(u *UserOAuth) { u.WebRedirectURL = "" },
	} {
		u := good
		mut(&u)
		if _, err := NewWithUserOAuth(u); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, mut := range map[string]func(*UserOAuth){
		"no client id":    func(u *UserOAuth) { u.ClientID = "" },
		"client id space": func(u *UserOAuth) { u.ClientID = "a b" },
		"secret newline":  func(u *UserOAuth) { u.ClientSecret = "a\nb" },
		"http remote":     func(u *UserOAuth) { u.WebRedirectURL = "http://kubron.example.com/auth/cluster/callback" },
		"wrong path":      func(u *UserOAuth) { u.WebRedirectURL = "https://kubron.example.com/cb" },
		"fragment":        func(u *UserOAuth) { u.WebRedirectURL = testWebRedirect + "#x" },
		"query":           func(u *UserOAuth) { u.WebRedirectURL = testWebRedirect + "?a=b" },
		"userinfo":        func(u *UserOAuth) { u.WebRedirectURL = "https://u:p@kubron.example.com/auth/cluster/callback" },
		"other scheme":    func(u *UserOAuth) { u.WebRedirectURL = "ftp://kubron.example.com/auth/cluster/callback" },
	} {
		u := good
		mut(&u)
		if _, err := NewWithUserOAuth(u); !errors.Is(err, errUserConfig) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if s := fmt.Sprintf("%v|%+v|%#v", good, good, good); strings.Contains(s, testClientSecret) || strings.Contains(s, testClientID) {
		t.Fatal("the client is printed")
	}
}

// The canary test for the user flow: every secret has a recognisable value; none may show up in errors,
// results (the address of the browser is public by nature and carries none of them) or logs.
func TestUserSecretsAreNotShown(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	const (
		codeCanary = "4/CANARY-AUTH-CODE-61ce"
		descCanary = "CANARY-GOOGLE-DESCRIPTION-9f3a"
		idCanary   = "CANARY-ID-TOKEN-SUBJECT-1d70"
	)
	var sh shown
	secrets := []string{testClientSecret, testRefresh, testUserAccess, codeCanary, descCanary, idCanary, "1//0g-rotated-canary"}

	run := func(reply func(call) (int, string), nonceFn func(string), back string) {
		g := newGoogle(t, reply)
		p := userProvider(t, testWebRedirect)
		c, pend, pd := begin(t, p, webEnv(g))
		secrets = append(secrets, pd.Verifier)
		nonceFn(pd.Nonce)
		sh.result(p.Continue(context.Background(), webEnv(g), answerReq(c, pend, back)))
		sh.result(p.Begin(context.Background(), webEnv(g), beginReq(savedFor(testClientID))))
		sh.result(p.Continue(context.Background(), credential.Env{}, answerReq(c, pend, back)))
	}
	back := testWebRedirect + "?state=" + testChallengeID + "&code=" + url.QueryEscape(codeCanary)
	nonce := ""
	ok := func(c call) (int, string) {
		cl := goodClaims(nonce)
		cl["sub"] = idCanary
		b, _ := json.Marshal(map[string]any{"access_token": testUserAccess, "token_type": "Bearer", "expires_in": 3599, "refresh_token": testRefresh, "id_token": idToken(t, cl)})
		return 200, string(b)
	}
	run(ok, func(n string) { nonce = n }, back)
	run(replyWith(400, fmt.Sprintf(`{"error":"invalid_grant","error_description":%q,"refresh_token":%q}`, descCanary, testRefresh)), func(string) {}, back)
	run(replyWith(503, descCanary+testRefresh), func(string) {}, back)
	run(replyWith(200, descCanary+testUserAccess), func(string) {}, back)
	run(replyWith(401, fmt.Sprintf(`{"error":"invalid_client","error_description":%q}`, descCanary)), func(string) {}, back)
	run(ok, func(string) {}, back) // the nonce of the ID token does not match
	run(ok, func(n string) { nonce = n }, testWebRedirect+"?state=wrong&code="+url.QueryEscape(codeCanary))
	run(ok, func(n string) { nonce = n }, "https://evil.example/?code="+url.QueryEscape(codeCanary)+"&state="+testChallengeID)

	out := sh.b.String() + "\n" + logs.String()
	for _, s := range secrets {
		if strings.Contains(out, s) {
			t.Errorf("a secret value shows up in errors, results or logs: %.30q...", s)
		}
	}
	if logs.Len() != 0 {
		t.Errorf("the provider must not log, got %d bytes", logs.Len())
	}
}
