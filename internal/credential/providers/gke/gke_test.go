// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package gke

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

func TestKind(t *testing.T) {
	if New().Kind() != credential.KindGKE {
		t.Fatal("the provider must register as KindGKE")
	}
}

func TestBeginWithoutSavedAsksForTheKey(t *testing.T) {
	g := newGoogle(t, tokenOK(3599))
	// "use-adc" asks for the machine's own credentials; it must change nothing.
	req := beginReq(nil)
	req.Descriptor.Params["use-adc"] = "true"
	res, err := New().Begin(context.Background(), envFor(g), req)
	if err != nil {
		t.Fatal(err)
	}
	c := res.Challenge
	if c == nil || res.Token != nil || c.Kind != credential.ChallengeSecretFields {
		t.Fatalf("want exactly a secret-fields challenge, got %+v", res)
	}
	if len(c.Fields) != 1 || c.Fields[0].Name != "serviceAccountJson" || !c.Fields[0].Secret {
		t.Fatalf("fields = %+v", c.Fields)
	}
	if c.ID != "" || !c.ExpiresAt.IsZero() {
		t.Fatal("ID and ExpiresAt belong to the Broker")
	}
	if res.Save != nil || res.Pending != nil {
		t.Fatal("nothing to save or keep yet")
	}
	if g.callCount() != 0 {
		t.Fatal("asking for the key must not touch the network")
	}
}

func TestBeginWithoutHTTPClientStillAsks(t *testing.T) {
	res, err := New().Begin(context.Background(), credential.Env{}, beginReq(nil))
	if err != nil || res.Challenge == nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestContinueIssuesTokenWithTheRightAssertion(t *testing.T) {
	g := newGoogle(t, tokenOK(1800))
	before := time.Now()
	res, err := New().Continue(context.Background(), envFor(g), continueReq(keyJSON(t, nil)))
	after := time.Now()
	if err != nil {
		t.Fatal(err)
	}
	if res.Token == nil || res.Challenge != nil {
		t.Fatalf("want exactly a token, got %+v", res)
	}
	if res.Token.Value != testAccess || res.Token.Principal != testEmail {
		t.Fatalf("token = %v principal = %q", *res.Token, res.Token.Principal)
	}
	// The lifetime comes from expires_in of the answer.
	if res.Token.Expiry.Before(before.Add(1800*time.Second)) || res.Token.Expiry.After(after.Add(1800*time.Second)) {
		t.Fatalf("expiry = %v, want about 1800s from now", res.Token.Expiry)
	}
	if res.Pending != nil {
		t.Fatal("the provider keeps no pending state")
	}

	calls, urls := g.snapshot()
	if len(calls) != 1 || len(urls) != 1 {
		t.Fatalf("calls = %d, urls = %d", len(calls), len(urls))
	}
	if urls[0] != "https://oauth2.googleapis.com/token" || calls[0].Host != "oauth2.googleapis.com" || calls[0].Method != "POST" {
		t.Fatalf("request: url=%q host=%q method=%q", urls[0], calls[0].Host, calls[0].Method)
	}
	if got := calls[0].Form.Get("grant_type"); got != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Fatalf("grant_type = %q", got)
	}
	hdr, claims := decodeAssertion(t, calls[0].Form.Get("assertion"), &testRSA().PublicKey)
	if hdr["alg"] != "RS256" || hdr["typ"] != "JWT" || hdr["kid"] != testKeyID {
		t.Fatalf("header = %v", hdr)
	}
	if claims["iss"] != testEmail {
		t.Fatalf("iss = %v", claims["iss"])
	}
	if claims["aud"] != "https://oauth2.googleapis.com/token" {
		t.Fatalf("aud = %v", claims["aud"])
	}
	if claims["scope"] != "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email" {
		t.Fatalf("scope = %v", claims["scope"])
	}
	if _, ok := claims["sub"]; ok {
		t.Fatal("there must be no sub claim: the account does not impersonate anyone")
	}
	iat, exp := int64(claims["iat"].(float64)), int64(claims["exp"].(float64))
	if exp <= iat || exp-iat > 3600 {
		t.Fatalf("exp-iat = %d, must be in (0, 3600]", exp-iat)
	}
	if exp > after.Unix()+3600 || exp <= before.Unix() {
		t.Fatalf("exp = %d is not within an hour of now", exp)
	}
}

func TestContinueSavesTheKeyWithoutTokenURI(t *testing.T) {
	g := newGoogle(t, tokenOK(3599))
	in := keyJSON(t, map[string]any{"token_uri": "https://evil.example/steal", "client_x509_cert_url": "https://cert.example/x"})
	res, err := New().Continue(context.Background(), envFor(g), continueReq(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Save) == 0 {
		t.Fatal("the key must be saved")
	}
	for _, s := range []string{"token_uri", "evil.example", "cert.example", "project_id", "auth_uri"} {
		if strings.Contains(string(res.Save), s) {
			t.Fatalf("saved data keeps %q", s)
		}
	}
	// What is saved is a key that signs in again.
	k, err := parseKey(res.Save)
	if err != nil || k.email != testEmail || k.keyID != testKeyID {
		t.Fatalf("saved data does not read back: %v", err)
	}
}

func TestTokenURIOfTheFileIsIgnored(t *testing.T) {
	for _, evil := range []string{
		"http://127.0.0.1:1/token",
		"https://evil.example/token",
		"https://oauth2.googleapis.com.evil.example/token",
		"file:///etc/passwd",
		"",
	} {
		t.Run(evil, func(t *testing.T) {
			g := newGoogle(t, tokenOK(3599))
			in := keyJSON(t, map[string]any{"token_uri": evil})
			if _, err := New().Continue(context.Background(), envFor(g), continueReq(in)); err != nil {
				t.Fatal(err)
			}
			calls, urls := g.snapshot()
			if len(urls) != 1 || urls[0] != "https://oauth2.googleapis.com/token" || len(calls) != 1 {
				t.Fatalf("urls = %v", urls)
			}
			_, claims := decodeAssertion(t, calls[0].Form.Get("assertion"), &testRSA().PublicKey)
			if claims["aud"] != "https://oauth2.googleapis.com/token" {
				t.Fatalf("aud = %v: the audience must not come from the file either", claims["aud"])
			}
		})
	}
}

func TestMissingExpiresInGetsShortDefault(t *testing.T) {
	g := newGoogle(t, replyWith(200, fmt.Sprintf(`{"access_token":%q,"token_type":"Bearer"}`, testAccess)))
	res, err := New().Continue(context.Background(), envFor(g), continueReq(keyJSON(t, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if want := fixedNow.Add(DefaultTTL); !res.Token.Expiry.Equal(want) {
		t.Fatalf("expiry = %v, want %v", res.Token.Expiry, want)
	}
}

func TestPKCS1KeyIsAccepted(t *testing.T) {
	// Older keys are "BEGIN RSA PRIVATE KEY".
	g := newGoogle(t, tokenOK(3599))
	pk := pkcs1PEM(testRSA())
	res, err := New().Continue(context.Background(), envFor(g), continueReq(keyJSON(t, map[string]any{"private_key": pk})))
	if err != nil || res.Token == nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestBeginWithSavedKeyIssuesTokenSilently(t *testing.T) {
	g := newGoogle(t, tokenOK(3599))
	k, err := parseKey([]byte(keyJSON(t, nil)))
	if err != nil {
		t.Fatal(err)
	}
	res, err := New().Begin(context.Background(), envFor(g), beginReq(k.normalized()))
	if err != nil {
		t.Fatal(err)
	}
	if res.Token == nil || res.Challenge != nil || res.Save != nil {
		t.Fatalf("want a token and no change of saved data, got %+v", res)
	}
	if g.callCount() != 1 {
		t.Fatalf("calls = %d", g.callCount())
	}
}

func TestBeginWithRejectedSavedKeyErasesItAndAsksAgain(t *testing.T) {
	g := newGoogle(t, replyWith(400, `{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`))
	k, _ := parseKey([]byte(keyJSON(t, nil)))
	res, err := New().Begin(context.Background(), envFor(g), beginReq(k.normalized()))
	if err != nil {
		t.Fatal(err)
	}
	if res.Challenge == nil || res.Token != nil {
		t.Fatalf("want a challenge, got %+v", res)
	}
	if res.Save == nil || len(res.Save) != 0 {
		t.Fatalf("Save must be an empty non-nil slice, got %#v", res.Save)
	}
}

func TestBeginWithUnreadableSavedDataErasesItWithoutNetwork(t *testing.T) {
	g := newGoogle(t, tokenOK(3599))
	res, err := New().Begin(context.Background(), envFor(g), beginReq([]byte("not a key")))
	if err != nil || res.Challenge == nil || res.Save == nil || len(res.Save) != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if g.callCount() != 0 {
		t.Fatal("unreadable saved data needs no request")
	}
}

func TestBeginKeepsSavedKeyWhenGoogleIsUnavailable(t *testing.T) {
	g := newGoogle(t, replyWith(503, `{"error":"backend_error"}`))
	k, _ := parseKey([]byte(keyJSON(t, nil)))
	res, err := New().Begin(context.Background(), envFor(g), beginReq(k.normalized()))
	if !errors.Is(err, credential.ErrServiceUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if res.Save != nil || res.Challenge != nil {
		t.Fatalf("a failure that is not about the key must leave saved data alone, got %+v", res)
	}
}

func TestInvalidGrantIsAWrongKey(t *testing.T) {
	g := newGoogle(t, replyWith(400, `{"error":"invalid_grant","error_description":"Invalid JWT: Token must be a short-lived token"}`))
	_, err := New().Continue(context.Background(), envFor(g), continueReq(keyJSON(t, nil)))
	if !errors.Is(err, credential.ErrAnswerRejected) {
		t.Fatalf("err = %v", err)
	}
	if errors.Is(err, credential.ErrInvalidAnswer) || errors.Is(err, credential.ErrUnsupported) {
		t.Fatal("a rejected key is a wrong guess, not an unusable answer")
	}
}

func TestOtherFailuresOfGoogle(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		body         string
		wantUnavail  bool
		wantRejected bool
	}{
		{"server error", 500, `{"error":"backend_error"}`, true, false},
		{"bad gateway", 502, `<html>bad gateway</html>`, true, false},
		{"rate limited", 429, `{"error":"rate_limit_exceeded"}`, true, false},
		{"invalid scope", 400, `{"error":"invalid_scope"}`, false, false},
		{"unauthorized client", 401, `{"error":"unauthorized_client"}`, false, false},
		{"invalid grant without JSON", 400, `invalid_grant`, false, false},
		{"not JSON", 200, `<html>captive portal</html>`, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newGoogle(t, replyWith(c.status, c.body))
			_, err := New().Continue(context.Background(), envFor(g), continueReq(keyJSON(t, nil)))
			if err == nil {
				t.Fatal("want an error")
			}
			if got := errors.Is(err, credential.ErrServiceUnavailable); got != c.wantUnavail {
				t.Fatalf("unavailable = %v, err = %v", got, err)
			}
			if errors.Is(err, credential.ErrAnswerRejected) || errors.Is(err, credential.ErrInvalidAnswer) || errors.Is(err, credential.ErrUnsupported) {
				t.Fatalf("err = %v must not be a contract error about the answer", err)
			}
		})
	}
}

func TestBadTokenResponses(t *testing.T) {
	for name, body := range map[string]string{
		"no token":    `{"token_type":"Bearer","expires_in":3599}`,
		"empty token": `{"access_token":"","token_type":"Bearer","expires_in":3599}`,
		"other type":  `{"access_token":"abc","token_type":"MAC","expires_in":3599}`,
		"newline":     `{"access_token":"abc\ndef","token_type":"Bearer","expires_in":3599}`,
		"space":       `{"access_token":"abc def","token_type":"Bearer","expires_in":3599}`,
		"non ascii":   `{"access_token":"tok\u00e9n","token_type":"Bearer","expires_in":3599}`,
		"huge":        fmt.Sprintf(`{"access_token":%q,"token_type":"Bearer","expires_in":3599}`, strings.Repeat("a", 9<<10)),
	} {
		t.Run(name, func(t *testing.T) {
			g := newGoogle(t, replyWith(200, body))
			res, err := New().Continue(context.Background(), envFor(g), continueReq(keyJSON(t, nil)))
			if err == nil || res.Token != nil {
				t.Fatalf("res=%+v err=%v", res, err)
			}
		})
	}
}

func TestInvalidAnswersNeverReachTheNetwork(t *testing.T) {
	longKey := keyJSON(t, map[string]any{"padding": strings.Repeat("x", MaxKeyBytes)})
	cases := map[string]string{
		"empty":             "",
		"blank":             " \n\t ",
		"not JSON":          "hello",
		"truncated":         keyJSON(t, nil)[:100],
		"JSON array":        `[1,2]`,
		"trailing data":     keyJSON(t, nil) + `{"a":1}`,
		"no type":           keyJSON(t, map[string]any{"type": nil}),
		"unknown type":      keyJSON(t, map[string]any{"type": "something_else"}),
		"type is a number":  keyJSON(t, map[string]any{"type": 7}),
		"no client_email":   keyJSON(t, map[string]any{"client_email": nil}),
		"email with space":  keyJSON(t, map[string]any{"client_email": "a b@x.com"}),
		"email two @":       keyJSON(t, map[string]any{"client_email": "a@b@x.com"}),
		"email newline":     keyJSON(t, map[string]any{"client_email": "a@x.com\nevil"}),
		"email too long":    keyJSON(t, map[string]any{"client_email": strings.Repeat("a", 300) + "@x.com"}),
		"key id newline":    keyJSON(t, map[string]any{"private_key_id": "a\nb"}),
		"no private_key":    keyJSON(t, map[string]any{"private_key": nil}),
		"private_key text":  keyJSON(t, map[string]any{"private_key": "not a pem"}),
		"private_key empty": keyJSON(t, map[string]any{"private_key": ""}),
		"private_key broken": keyJSON(t, map[string]any{
			"private_key": "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"}),
		"private_key is a number": keyJSON(t, map[string]any{"private_key": 5}),
		"too large":               longKey,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			g := newGoogle(t, tokenOK(3599))
			_, err := New().Continue(context.Background(), envFor(g), continueReq(in))
			if !errors.Is(err, credential.ErrInvalidAnswer) {
				t.Fatalf("err = %v, want ErrInvalidAnswer", err)
			}
			if errors.Is(err, credential.ErrAnswerRejected) {
				t.Fatal("an unusable answer is not a wrong guess")
			}
			if g.callCount() != 0 {
				t.Fatal("an unusable answer must not reach the network")
			}
		})
	}
}

func TestKeyOfExactlyMaxSizeIsNotTooLarge(t *testing.T) {
	in := keyJSON(t, nil)
	pad := MaxKeyBytes - len(in) - len(`,"p":""`)
	in = in[:len(in)-1] + `,"p":"` + strings.Repeat("x", pad) + `"}`
	if len(in) != MaxKeyBytes {
		t.Fatalf("len = %d", len(in))
	}
	if _, err := parseKey([]byte(in)); err != nil {
		t.Fatalf("a key of exactly %d bytes must be read: %v", MaxKeyBytes, err)
	}
	if _, err := parseKey([]byte(in + " ")); !errors.Is(err, credential.ErrInvalidAnswer) {
		t.Fatalf("one byte more must be refused: %v", err)
	}
}

func TestOtherKindsOfGoogleCredentialsAreUnsupported(t *testing.T) {
	for _, typ := range []string{
		"external_account", "external_account_authorized_user", "authorized_user",
		"impersonated_service_account", "gdch_service_account",
	} {
		t.Run(typ, func(t *testing.T) {
			g := newGoogle(t, tokenOK(3599))
			in := keyJSON(t, map[string]any{"type": typ, "token_url": "https://sts.googleapis.com/v1/token"})
			_, err := New().Continue(context.Background(), envFor(g), continueReq(in))
			if !errors.Is(err, credential.ErrUnsupported) {
				t.Fatalf("err = %v, want ErrUnsupported", err)
			}
			if g.callCount() != 0 {
				t.Fatal("an unsupported kind must not reach the network")
			}
		})
	}
	// Workload identity federation as Google writes it: no private key at all.
	g := newGoogle(t, tokenOK(3599))
	wif := `{"type":"external_account","audience":"//iam.googleapis.com/projects/1/locations/global/workloadIdentityPools/p/providers/q",` +
		`"subject_token_type":"urn:ietf:params:oauth:token-type:jwt","token_url":"https://sts.googleapis.com/v1/token",` +
		`"credential_source":{"file":"/var/run/secrets/token"}}`
	if _, err := New().Continue(context.Background(), envFor(g), continueReq(wif)); !errors.Is(err, credential.ErrUnsupported) {
		t.Fatalf("workload identity federation: err = %v", err)
	}
}

func TestContinueOfAnotherChallengeIsUnsupported(t *testing.T) {
	g := newGoogle(t, tokenOK(3599))
	for _, k := range []credential.ChallengeKind{credential.ChallengeCredentials, credential.ChallengeDeviceCode, credential.ChallengePasteRedirect, credential.ChallengeManualToken, ""} {
		req := continueReq(keyJSON(t, nil))
		req.Challenge.Kind = k
		if _, err := New().Continue(context.Background(), envFor(g), req); !errors.Is(err, credential.ErrUnsupported) {
			t.Fatalf("kind %q: err = %v", k, err)
		}
	}
	if g.callCount() != 0 {
		t.Fatal("no request expected")
	}
}

func TestMissingHTTPClientIsAnErrorNotADefaultClient(t *testing.T) {
	trapDefaultTransport(t) // fails the test if http.DefaultClient is used
	_, err := New().Continue(context.Background(), credential.Env{}, continueReq(keyJSON(t, nil)))
	if !errors.Is(err, errNoHTTP) {
		t.Fatalf("err = %v", err)
	}
	k, _ := parseKey([]byte(keyJSON(t, nil)))
	if _, err := New().Begin(context.Background(), credential.Env{}, beginReq(k.normalized())); !errors.Is(err, errNoHTTP) {
		t.Fatalf("Begin with saved key: err = %v", err)
	}
}

func TestCancelledContextStaysACancelledContext(t *testing.T) {
	g := newGoogle(t, tokenOK(3599))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New().Continue(ctx, envFor(g), continueReq(keyJSON(t, nil)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if errors.Is(err, credential.ErrServiceUnavailable) {
		t.Fatal("a cancellation is not an outage")
	}
}

func TestRefusalOfTheAddressPolicyIsUnavailable(t *testing.T) {
	// Env.HTTP is the client with the SSRF policy; its refusal is a transport error.
	env := credential.Env{HTTP: refusingClient(), Now: func() time.Time { return fixedNow }}
	_, err := New().Continue(context.Background(), env, continueReq(keyJSON(t, nil)))
	if !errors.Is(err, credential.ErrServiceUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestTokenIsRedactedWhenPrinted(t *testing.T) {
	g := newGoogle(t, tokenOK(3599))
	res, err := New().Continue(context.Background(), envFor(g), continueReq(keyJSON(t, nil)))
	if err != nil {
		t.Fatal(err)
	}
	out := fmt.Sprintf("%v|%+v|%#v", *res.Token, res.Token, *res.Token)
	if strings.Contains(out, testAccess) {
		t.Fatal("the token is printed")
	}
	k, _ := parseKey([]byte(keyJSON(t, nil)))
	out = fmt.Sprintf("%v|%+v|%#v|%v", k, *k, *k, json.RawMessage(nil))
	if strings.Contains(out, "BEGIN") || strings.Contains(out, testEmail) {
		t.Fatal("the key is printed")
	}
}

func TestCancellationAbortsTheRequestInFlight(t *testing.T) {
	arrived := make(chan struct{}, 1)
	release := make(chan struct{})
	g := newGoogle(t, func(call) (int, string) {
		arrived <- struct{}{}
		<-release
		return 200, fmt.Sprintf(`{"access_token":%q,"token_type":"Bearer","expires_in":3599}`, testAccess)
	})
	t.Cleanup(func() { close(release) }) // runs before the server is closed
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := New().Continue(ctx, envFor(g), continueReq(keyJSON(t, nil)))
		done <- err
	}()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never arrived")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelling the context did not stop the request")
	}
}

func TestRedirectsAreLeftToEnvHTTP(t *testing.T) {
	// A redirect of the token endpoint is followed (or refused) by Env.HTTP, whose policy checks every hop;
	// the provider's own client must not follow it a second time.
	var hops []string
	env := credential.Env{HTTP: &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			hops = append(hops, r.URL.String())
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://elsewhere.example/token"}}, Body: http.NoBody, Request: r}, nil
		}),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	_, err := New().Continue(context.Background(), env, continueReq(keyJSON(t, nil)))
	if err == nil {
		t.Fatal("a redirect is not a token")
	}
	if len(hops) != 1 || hops[0] != TokenURL {
		t.Fatalf("hops = %v", hops)
	}
}
