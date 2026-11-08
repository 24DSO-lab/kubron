// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/credential"
	"github.com/24DSO-lab/kubron/internal/idpnet"
)

// TestKeycloakPasswordAndRefresh exercises the production provider against a real Keycloak realm.
// It is opt-in locally and enabled by the credential-keycloak GitHub Actions workflow.
func TestKeycloakPasswordAndRefresh(t *testing.T) {
	issuer := strings.TrimRight(os.Getenv("KUBRON_KEYCLOAK_ISSUER"), "/")
	if issuer == "" {
		t.Skip("set KUBRON_KEYCLOAK_ISSUER to run the Keycloak integration test")
	}
	username, password := os.Getenv("KUBRON_KEYCLOAK_USERNAME"), os.Getenv("KUBRON_KEYCLOAK_PASSWORD")
	if username == "" || password == "" {
		t.Fatal("KUBRON_KEYCLOAK_USERNAME and KUBRON_KEYCLOAK_PASSWORD are required")
	}

	httpClient, err := idpnet.NewClient(idpnet.Options{Desktop: true})
	if err != nil {
		t.Fatal(err)
	}
	provider := New()
	defer provider.Close()

	req := credential.Request{
		Descriptor: credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{
			"grant": "password", "issuer-url": issuer, "client-id": "kubron-integration",
			"username": username, "password": password,
		}},
		Subject: credential.Subject{ClusterID: "keycloak-integration", Scope: credential.ScopeUser,
			OwnerID: "keycloak-integration-user", SessionID: "keycloak-integration-session"},
	}
	env := credential.Env{HTTP: httpClient, Desktop: true}

	first, err := provider.Begin(context.Background(), env, req)
	if err != nil {
		t.Fatalf("password grant: %v", err)
	}
	if first.Token == nil || first.Token.Value == "" || first.Token.Principal == "" {
		t.Fatal("Keycloak password grant did not produce a verified ID token and principal")
	}
	if len(first.Save) == 0 {
		t.Fatal("Keycloak did not issue refresh credentials for the integration client")
	}

	// The second call must use the saved refresh token rather than asking for the password again.
	req.Descriptor.Params["username"] = ""
	req.Descriptor.Params["password"] = ""
	req.Saved = first.Save
	second, err := provider.Begin(context.Background(), env, req)
	if err != nil {
		t.Fatalf("refresh grant: %v", err)
	}
	if second.Token == nil || second.Token.Value == "" || second.Token.Principal != first.Token.Principal {
		t.Fatal("Keycloak refresh did not preserve the verified subject")
	}

	// Exercise the authorization-code + PKCE flow through the same paste-redirect
	// challenge used by the web client. A small browser client follows Keycloak's
	// login redirects and stops at Kubron's deliberately unserved localhost callback.
	authDescriptor := credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{
		"grant": "authcode-keyboard", "issuer-url": issuer, "client-id": "kubron-integration",
	}}
	authRequest := credential.Request{Descriptor: authDescriptor, Subject: req.Subject}
	challenge, err := provider.Begin(context.Background(), env, authRequest)
	if err != nil || challenge.Challenge == nil || challenge.Challenge.URL == "" {
		t.Fatalf("start authorization-code flow: challenge=%v err=%v", challenge.Challenge != nil, err)
	}
	redirectURL, err := keycloakLogin(challenge.Challenge.URL, username, password)
	if err != nil {
		t.Fatalf("complete Keycloak login: %v", err)
	}
	completed, err := provider.Continue(context.Background(), env, credential.Request{
		Descriptor: authDescriptor, Subject: req.Subject, Challenge: *challenge.Challenge,
		Pending: challenge.Pending, Answer: credential.Answer{"redirectUrl": redirectURL},
	})
	if err != nil {
		t.Fatalf("exchange authorization code: %v", err)
	}
	if completed.Token == nil || completed.Token.Value == "" || completed.Token.Principal == "" {
		t.Fatal("Keycloak authorization-code flow did not produce a verified ID token and principal")
	}

	// Exercise the desktop RFC 8252 loopback listener against the same real IdP. The OpenURL
	// callback emulates a browser: it completes the Keycloak login and follows the redirect
	// to the ephemeral 127.0.0.1 listener opened by the provider.
	loopEnv := env
	loopEnv.OpenURL = func(authURL string) error {
		redirectURL, err := keycloakLogin(authURL, username, password)
		if err != nil {
			return err
		}
		resp, err := http.Get(redirectURL)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("desktop loopback callback returned HTTP %d", resp.StatusCode)
		}
		return nil
	}
	loopDescriptor := credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{
		"grant": "authcode", "issuer-url": issuer, "client-id": "kubron-integration",
		"listen-address": "127.0.0.1:0", "redirect-url-hostname": "127.0.0.1",
	}}
	loopStarted, err := provider.Begin(context.Background(), loopEnv, credential.Request{
		Descriptor: loopDescriptor, Subject: req.Subject,
	})
	if err != nil || loopStarted.Challenge == nil || loopStarted.Challenge.Kind != credential.ChallengeBrowserLoopback {
		t.Fatalf("start desktop loopback flow: challenge=%v err=%v", loopStarted.Challenge != nil, err)
	}
	loopCompleted, err := provider.Continue(context.Background(), loopEnv, credential.Request{
		Descriptor: loopDescriptor, Subject: req.Subject, Challenge: *loopStarted.Challenge,
		Pending: loopStarted.Pending,
	})
	if err != nil || loopCompleted.Token == nil || loopCompleted.Token.Principal != first.Token.Principal {
		t.Fatalf("complete desktop loopback flow: token=%v err=%v", loopCompleted.Token != nil, err)
	}

	// Exercise the real device authorization endpoint and one RFC 8628 pending poll.
	deviceDescriptor := credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{
		"grant": "device-code", "issuer-url": issuer, "client-id": "kubron-device-integration",
	}}
	deviceStarted, err := provider.Begin(context.Background(), env, credential.Request{
		Descriptor: deviceDescriptor, Subject: req.Subject,
	})
	if err != nil || deviceStarted.Challenge == nil || deviceStarted.Challenge.Kind != credential.ChallengeDeviceCode {
		t.Fatalf("start Keycloak device flow: challenge=%v err=%v", deviceStarted.Challenge != nil, err)
	}
	deviceChallenge := *deviceStarted.Challenge
	if deviceChallenge.URL == "" || deviceChallenge.UserCode == "" || !deviceChallenge.Poll || deviceChallenge.IntervalSec < 1 {
		t.Fatalf("incomplete Keycloak device challenge: %+v", deviceChallenge)
	}
	time.Sleep(time.Duration(deviceChallenge.IntervalSec)*time.Second + 100*time.Millisecond)
	pending, err := provider.Continue(context.Background(), env, credential.Request{
		Descriptor: deviceDescriptor, Subject: req.Subject, Challenge: deviceChallenge, Pending: deviceStarted.Pending,
	})
	if err != nil || pending.Challenge == nil || pending.Challenge.Kind != credential.ChallengeDeviceCode {
		t.Fatalf("Keycloak device authorization should remain pending before user approval: challenge=%v err=%v",
			pending.Challenge != nil, err)
	}
}

var (
	keycloakFormRE  = regexp.MustCompile(`(?is)<form\b[^>]*>`)
	keycloakInputRE = regexp.MustCompile(`(?is)<input\b[^>]*>`)
	keycloakAttrRE  = regexp.MustCompile(`(?is)([a-zA-Z_:][a-zA-Z0-9_:-]*)\s*=\s*["']([^"']*)["']`)
)

// keycloakLogin emulates the human browser part of the authorization-code flow and returns
// the redirect URL that the user would paste into the Kubron challenge.
func keycloakLogin(authURL, username, password string) (string, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return "", err
	}
	browser := &http.Client{
		Jar: jar, Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			host := strings.ToLower(req.URL.Hostname())
			if host == "localhost" || host == "127.0.0.1" {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	resp, err := browser.Get(authURL)
	if err != nil {
		return "", err
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	if readErr != nil {
		return "", readErr
	}
	var action string
	values := url.Values{}
	form := keycloakFormRE.Find(body)
	if form == nil {
		return "", fmt.Errorf("Keycloak login form was not found (HTTP %d)", resp.StatusCode)
	}
	for _, match := range keycloakAttrRE.FindAllSubmatch(form, -1) {
		key := strings.ToLower(string(match[1]))
		if key == "action" {
			action = html.UnescapeString(string(match[2]))
		}
	}
	if action == "" {
		return "", fmt.Errorf("Keycloak login form has no action")
	}
	for _, input := range keycloakInputRE.FindAll(body, -1) {
		attrs := map[string]string{}
		for _, match := range keycloakAttrRE.FindAllSubmatch(input, -1) {
			attrs[strings.ToLower(string(match[1]))] = html.UnescapeString(string(match[2]))
		}
		if name := attrs["name"]; name != "" {
			values.Add(name, attrs["value"])
		}
	}
	values.Set("username", username)
	values.Set("password", password)
	loginURL, err := resp.Request.URL.Parse(action)
	if err != nil {
		return "", err
	}
	loginResp, err := browser.PostForm(loginURL.String(), values)
	if err != nil {
		return "", err
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode < 300 || loginResp.StatusCode >= 400 {
		return "", fmt.Errorf("Keycloak login did not redirect (HTTP %d)", loginResp.StatusCode)
	}
	location := loginResp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("Keycloak login redirect has no location")
	}
	redirect, err := loginResp.Request.URL.Parse(location)
	if err != nil {
		return "", err
	}
	return redirect.String(), nil
}
