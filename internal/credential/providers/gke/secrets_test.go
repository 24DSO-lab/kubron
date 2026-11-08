// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package gke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/credential"
)

// shown collects everything the provider lets out to a place where a secret must not be: error texts (in the
// three usual formattings), the challenge as the API would send it, tokens, answers and keys printed with fmt,
// and the log. Save and Pending are not "shown": they are the data the Broker encrypts.
type shown struct{ b strings.Builder }

func (s *shown) err(err error) {
	if err != nil {
		fmt.Fprintf(&s.b, "%v|%+v|%#v|%s\n", err, err, err, err.Error())
	}
}

func (s *shown) result(res credential.Result, err error) {
	s.err(err)
	if res.Challenge != nil {
		j, _ := json.Marshal(res.Challenge)
		s.b.Write(j)
		s.b.WriteByte('\n')
	}
	if res.Token != nil {
		fmt.Fprintf(&s.b, "%v|%+v|%#v\n", *res.Token, res.Token, *res.Token)
	}
}

// The canary test: every secret the flows handle has a recognisable value, all flows run (a success and every
// kind of failure), and none of the values may appear in anything shown.
func TestSecretsAreNotShown(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	const (
		tokenCanary  = "ya29.CANARY-ACCESS-TOKEN-7d2e"
		extraCanary  = "CANARY-EXTRA-FIELD-5b81"
		evilURI      = "https://token-uri-canary.invalid/steal"
		badKeyCanary = "CANARY-BAD-PRIVATE-KEY-c03f"
		typeCanary   = "canary-type-e4a9"
		descCanary   = "CANARY-GOOGLE-ERROR-TEXT-88aa"
	)
	pemText := pkcs8PEM(t, testRSA())
	var body []string
	for _, line := range strings.Split(pemText, "\n") {
		if line != "" && !strings.HasPrefix(line, "-----") {
			body = append(body, line)
		}
	}
	b64 := strings.Join(body, "")
	secrets := []string{
		tokenCanary, extraCanary, evilURI, badKeyCanary, typeCanary, descCanary,
		b64[:48], b64[len(b64)/2 : len(b64)/2+48], b64[len(b64)-48:], body[1], body[len(body)-2],
		testKeyID,
	}

	var sh shown
	var assertions []string
	srvBody := func(status int, text string) func(call) (int, string) {
		return func(c call) (int, string) {
			assertions = append(assertions, c.Form.Get("assertion"))
			return status, text
		}
	}
	flows := []struct {
		name   string
		answer string
		reply  func(call) (int, string)
	}{
		{"success", keyJSON(t, map[string]any{"token_uri": evilURI, "client_x509_cert_url": extraCanary}),
			srvBody(200, fmt.Sprintf(`{"access_token":%q,"token_type":"Bearer","expires_in":3599}`, tokenCanary))},
		{"invalid_grant", keyJSON(t, nil),
			srvBody(400, fmt.Sprintf(`{"error":"invalid_grant","error_description":%q,"access_token":%q}`, descCanary, tokenCanary))},
		{"server error", keyJSON(t, nil), srvBody(503, descCanary+tokenCanary)},
		{"invalid_scope", keyJSON(t, nil), srvBody(400, fmt.Sprintf(`{"error":"invalid_scope","error_description":%q}`, descCanary))},
		{"not JSON", keyJSON(t, nil), srvBody(200, descCanary+tokenCanary)},
		{"bad token type", keyJSON(t, nil), srvBody(200, fmt.Sprintf(`{"access_token":%q,"token_type":"MAC"}`, tokenCanary))},
		{"bad private key", keyJSON(t, map[string]any{"private_key": badKeyCanary, "client_x509_cert_url": extraCanary}), nil},
		{"bad JSON", `{"type":"service_account","private_key":"` + badKeyCanary + `" ` + extraCanary, nil},
		{"unknown type", keyJSON(t, map[string]any{"type": typeCanary, "client_x509_cert_url": extraCanary}), nil},
		{"unsupported type", keyJSON(t, map[string]any{"type": "external_account", "client_x509_cert_url": extraCanary}), nil},
		{"bad email", keyJSON(t, map[string]any{"client_email": extraCanary + " x"}), nil},
	}
	for _, f := range flows {
		reply := f.reply
		if reply == nil {
			reply = srvBody(200, "{}")
		}
		g := newGoogle(t, reply)
		env := envFor(g)
		req := continueReq(f.answer)
		fmt.Fprintf(&sh.b, "%v|%+v|%#v\n", req.Answer, req.Answer, req.Answer)
		res, err := New().Continue(context.Background(), env, req)
		sh.result(res, err)
		if f.name == "success" {
			if res.Token == nil || res.Token.Value != tokenCanary {
				t.Fatalf("the success flow must succeed: %+v %v", res, err)
			}
			// Begin with the saved key walks the silent path with the same secrets.
			bres, berr := New().Begin(context.Background(), env, beginReq(res.Save))
			sh.result(bres, berr)
		}
	}
	// Begin with a saved key that Google rejects, and with one that cannot be read.
	for _, reply := range []func(call) (int, string){
		srvBody(400, fmt.Sprintf(`{"error":"invalid_grant","error_description":%q}`, descCanary)),
		srvBody(500, descCanary),
	} {
		g := newGoogle(t, reply)
		k, _ := parseKey([]byte(keyJSON(t, nil)))
		fmt.Fprintf(&sh.b, "%v|%+v|%#v\n", k, *k, *k)
		res, err := New().Begin(context.Background(), envFor(g), beginReq(k.normalized()))
		sh.result(res, err)
	}
	res, err := New().Begin(context.Background(), credential.Env{}, beginReq([]byte(badKeyCanary)))
	sh.result(res, err)
	_, err = New().Continue(context.Background(), credential.Env{}, continueReq(keyJSON(t, nil))) // no HTTP client
	sh.err(err)

	for _, a := range assertions {
		if a != "" {
			secrets = append(secrets, a) // the signed assertion is a bearer proof of the key
		}
	}
	out := sh.b.String() + "\n" + logs.String()
	for _, s := range secrets {
		if strings.Contains(out, s) {
			t.Errorf("a secret value shows up in errors, results or logs: %.40q...", s)
		}
	}
	if logs.Len() != 0 {
		t.Errorf("the provider must not log at all, got %d bytes", logs.Len())
	}
}
