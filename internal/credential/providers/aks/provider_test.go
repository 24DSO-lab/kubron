// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package aks

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

func TestUnsupportedModesDoNotUseHostCredentials(t *testing.T) {
	for _, mode := range []string{"msi", "azurecli", "azd", "workloadidentity", "workload_identity"} {
		_, err := New().Begin(context.Background(), credential.Env{HTTP: http.DefaultClient},
			credential.Request{Descriptor: credential.Descriptor{Kind: credential.KindAKS, Params: map[string]string{"login": mode}}})
		if !errors.Is(err, credential.ErrUnsupported) { t.Fatalf("%s: got %v", mode, err) }
	}
}

func TestDeviceCodeFlowAndSlowDown(t *testing.T) {
	calls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case strings.HasSuffix(r.URL.Path, "/devicecode"):
			_, _ = w.Write([]byte(`{"device_code":"opaque-device","user_code":"ABCD-EFGH","verification_uri":"https://microsoft.com/devicelogin","expires_in":600,"interval":5}`))
		case calls == 2:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"slow_down"}`))
		default:
			_, _ = w.Write([]byte(`{"access_token":"eyJhbGciOiJub25lIn0.eyJvaWQiOiJhdWRpdC1pZCJ9.sig","refresh_token":"rotated","expires_in":3600}`))
		}
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	env := credential.Env{HTTP: srv.Client(), Now: func() time.Time { return now }}
	d := credential.Descriptor{Kind: credential.KindAKS, Params: map[string]string{"tenant-id": "tenant", "authority-host": srv.URL}}
	p := New()
	start, err := p.Begin(context.Background(), env, credential.Request{Descriptor: d})
	if err != nil || start.Challenge == nil || start.Challenge.Kind != credential.ChallengeDeviceCode { t.Fatalf("start=%+v err=%v", start, err) }
	if start.Challenge.UserCode != "ABCD-EFGH" || start.Challenge.IntervalSec != 5 { t.Fatalf("challenge=%+v", start.Challenge) }

	req := credential.Request{Descriptor: d, Challenge: *start.Challenge, Pending: start.Pending}
	now = now.Add(5 * time.Second)
	wait, err := p.Continue(context.Background(), env, req)
	if err != nil || wait.Challenge == nil || wait.Challenge.IntervalSec != 10 { t.Fatalf("slow_down=%+v err=%v", wait, err) }

	req.Challenge, req.Pending = *wait.Challenge, wait.Pending
	now = now.Add(10 * time.Second)
	done, err := p.Continue(context.Background(), env, req)
	if err != nil || done.Token == nil { t.Fatalf("done=%+v err=%v", done, err) }
	if done.Token.Principal != "audit-id" || string(done.Save) == "" || !strings.Contains(string(done.Save), "rotated") { t.Fatalf("token/save=%+v %s", done.Token, done.Save) }
}

func TestTenantIsRequestedRatherThanDefaulted(t *testing.T) {
	res, err := New().Begin(context.Background(), credential.Env{}, credential.Request{Descriptor: credential.Descriptor{Kind: credential.KindAKS}})
	if err != nil || res.Challenge == nil || len(res.Challenge.Fields) != 1 || res.Challenge.Fields[0].Name != "tenantId" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestAuthorityHostMustBeHTTPSAndRootOnly(t *testing.T) {
	for _, host := range []string{"http://127.0.0.1", "https://127.0.0.1/metadata", "https://user:pass@example.com", "file:///etc/passwd"} {
		_, err := parseConfig(credential.Descriptor{Params: map[string]string{"authority-host": host}})
		if !errors.Is(err, credential.ErrUnsupported) { t.Fatalf("%q: %v", host, err) }
	}
}

func TestSovereignCloudAuthorities(t *testing.T) {
	cases := map[string]string{
		"AzurePublicCloud": "https://login.microsoftonline.com",
		"AzureUSGovernmentCloud": "https://login.microsoftonline.us",
		"AzureChinaCloud": "https://login.chinacloudapi.cn",
	}
	for env, want := range cases {
		got, err := parseConfig(credential.Descriptor{Params: map[string]string{"environment": env}})
		if err != nil || got.authority != want { t.Fatalf("%s: %q %v", env, got.authority, err) }
	}
}

func TestSPNSecretIsNeverReturnedInErrors(t *testing.T) {
	const canary = "aks-client-secret-canary"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"`+canary+`"}`))
	}))
	defer srv.Close()
	d := credential.Descriptor{Kind: credential.KindAKS, Params: map[string]string{
		"login":"spn", "tenant-id":"tenant", "authority-host":srv.URL, "client-secret":canary,
	}}
	_, err := New().Begin(context.Background(), credential.Env{HTTP:srv.Client()}, credential.Request{Descriptor:d})
	if err == nil || strings.Contains(err.Error(), canary) { t.Fatalf("secret leaked: %v", err) }
}

func TestLegacyScope(t *testing.T) {
	c := config{serverID: defaultServerID, legacy: true}
	if got := scope(c); got != "sp:"+defaultServerID+"/.default" { t.Fatal(got) }
}

func TestTokenPrincipalIsOnlyLabel(t *testing.T) {
	tok, rt, err := parseToken([]byte(`{"access_token":"eyJhbGciOiJub25lIn0.eyJvaWQiOiJhYmMiLCJ1cG4iOiJ1c2VyIn0.sig","refresh_token":"r","expires_in":60}`), time.Now())
	if err != nil || rt != "r" || tok.Principal != "abc" { t.Fatalf("%+v %q %v", tok, rt, err) }
}
