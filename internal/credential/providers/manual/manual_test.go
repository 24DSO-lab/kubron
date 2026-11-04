// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package manual

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

const secret = "S3CR3T-control-string-7f3a"

var now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func jwt(payload string) string {
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString([]byte(payload)) + "." + enc.EncodeToString([]byte("sig"))
}

func execCred(version, status string) string {
	return `{"kind":"ExecCredential","apiVersion":"` + version + `","status":` + status + `}`
}

func run(t *testing.T, answer string) (credential.Result, error) {
	t.Helper()
	return New().Continue(context.Background(), credential.Env{Now: func() time.Time { return now }},
		credential.Request{Answer: credential.Answer{FieldToken: answer}})
}

func TestKind(t *testing.T) {
	if New().Kind() != credential.KindExec {
		t.Fatal("manual provider must register as KindExec")
	}
}

func TestBeginShowsCommandOnly(t *testing.T) {
	d := credential.Descriptor{Kind: credential.KindExec, Exec: &credential.ExecSpec{
		APIVersion: "client.authentication.k8s.io/v1", Command: "aws", Args: []string{"eks", "get-token", "--cluster-name", "c"},
		Env: map[string]string{"AWS_SECRET_ACCESS_KEY": secret}, InstallHint: "install the AWS CLI",
	}}
	res, err := New().Begin(context.Background(), credential.Env{}, credential.Request{Descriptor: d})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Challenge
	if c == nil || c.Kind != credential.ChallengeManualToken || res.Token != nil {
		t.Fatalf("unexpected result: %+v", res)
	}
	if got := strings.Join(c.Command, " "); got != "aws eks get-token --cluster-name c" {
		t.Fatalf("command = %q", got)
	}
	if len(c.Fields) != 1 || c.Fields[0] != (credential.Field{Name: "token", Secret: true}) {
		t.Fatalf("fields = %+v", c.Fields)
	}
	if c.Message != "install the AWS CLI" {
		t.Fatalf("message = %q", c.Message)
	}
	if c.Exec != nil {
		t.Fatal("the challenge must not carry the exec block")
	}
	if fmt.Sprintf("%+v %v", c, c.Command) != strings.ReplaceAll(fmt.Sprintf("%+v %v", c, c.Command), secret, "") {
		t.Fatal("plugin environment value leaked into the challenge")
	}
	if len(res.Save) != 0 || res.Pending != nil {
		t.Fatal("manual provider must not persist anything")
	}
}

func TestBeginWithoutCommand(t *testing.T) {
	for _, d := range []credential.Descriptor{{Kind: credential.KindExec}, {Kind: credential.KindExec, Exec: &credential.ExecSpec{Command: "  "}}} {
		_, err := New().Begin(context.Background(), credential.Env{}, credential.Request{Descriptor: d})
		if !errors.Is(err, credential.ErrUnsupported) {
			t.Fatalf("err = %v, want ErrUnsupported", err)
		}
	}
}

func TestContinueExecCredential(t *testing.T) {
	exp := now.Add(time.Hour).Format(time.RFC3339)
	for _, version := range []string{"client.authentication.k8s.io/v1", "client.authentication.k8s.io/v1beta1"} {
		res, err := run(t, execCred(version, `{"token":"abc.def","expirationTimestamp":"`+exp+`"}`))
		if err != nil {
			t.Fatalf("%s: %v", version, err)
		}
		if res.Token == nil || res.Token.Value != "abc.def" || !res.Token.Expiry.Equal(now.Add(time.Hour)) {
			t.Fatalf("%s: token = %+v", version, res.Token)
		}
		if res.Challenge != nil || len(res.Save) != 0 {
			t.Fatalf("%s: result must hold the token only", version)
		}
	}
}

func TestContinueExecCredentialWithoutExpiry(t *testing.T) {
	res, err := run(t, execCred("client.authentication.k8s.io/v1", `{"token":"opaque"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Token.Expiry.Equal(now.Add(DefaultTTL)) {
		t.Fatalf("expiry = %v", res.Token.Expiry)
	}
}

func TestContinueBareToken(t *testing.T) {
	res, err := run(t, "  opaque-token \n")
	if err != nil {
		t.Fatal(err)
	}
	if res.Token.Value != "opaque-token" || !res.Token.Expiry.Equal(now.Add(DefaultTTL)) {
		t.Fatalf("token = %+v", res.Token)
	}
}

func TestBareJWTUsesExpAndSub(t *testing.T) {
	exp := now.Add(2 * time.Hour).Unix()
	res, err := run(t, jwt(fmt.Sprintf(`{"exp":%d,"sub":"alice"}`, exp)))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Token.Expiry.Equal(time.Unix(exp, 0)) || res.Token.Principal != "alice" {
		t.Fatalf("token = %+v", res.Token)
	}
}

func TestExpiredIsRejected(t *testing.T) {
	past := now.Add(-time.Minute)
	for name, in := range map[string]string{
		"jwt":       jwt(fmt.Sprintf(`{"exp":%d}`, past.Unix())),
		"timestamp": execCred("client.authentication.k8s.io/v1", `{"token":"t","expirationTimestamp":"`+past.Format(time.RFC3339)+`"}`),
	} {
		if _, err := run(t, in); !errors.Is(err, credential.ErrInvalidAnswer) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestRejectedInputs(t *testing.T) {
	cases := map[string]string{
		"empty":            "   ",
		"inner space":      "two words",
		"newline":          "tok\nen",
		"control":          "tok\x00en",
		"non-ascii":        "tök",
		"not json":         `{"token":`,
		"trailing data":    execCred("client.authentication.k8s.io/v1", `{"token":"t"}`) + ` {}`,
		"unknown field":    `{"kind":"ExecCredential","apiVersion":"client.authentication.k8s.io/v1","extra":1,"status":{"token":"t"}}`,
		"unknown status":   execCred("client.authentication.k8s.io/v1", `{"token":"t","extra":1}`),
		"wrong kind":       `{"kind":"Other","apiVersion":"client.authentication.k8s.io/v1","status":{"token":"t"}}`,
		"wrong apiVersion": execCred("client.authentication.k8s.io/v2", `{"token":"t"}`),
		"no status":        `{"kind":"ExecCredential","apiVersion":"client.authentication.k8s.io/v1"}`,
		"no token":         execCred("client.authentication.k8s.io/v1", `{}`),
		"bad token chars":  execCred("client.authentication.k8s.io/v1", `{"token":"a b"}`),
		"bad timestamp":    execCred("client.authentication.k8s.io/v1", `{"token":"t","expirationTimestamp":"tomorrow"}`),
	}
	for name, in := range cases {
		res, err := run(t, in)
		if !errors.Is(err, credential.ErrInvalidAnswer) || errors.Is(err, credential.ErrAnswerRejected) {
			t.Errorf("%s: err = %v, want ErrInvalidAnswer", name, err)
		}
		if res.Token != nil {
			t.Errorf("%s: a token was returned with an error", name)
		}
	}
}

func TestClientCertificateIsUnsupported(t *testing.T) {
	for _, status := range []string{`{"clientCertificateData":"x","clientKeyData":"y"}`, `{"token":"t","clientCertificateData":"x"}`} {
		if _, err := run(t, execCred("client.authentication.k8s.io/v1", status)); !errors.Is(err, credential.ErrUnsupported) {
			t.Fatalf("status %s: err = %v, want ErrUnsupported", status, err)
		}
	}
}

func TestSizeLimit(t *testing.T) {
	if _, err := run(t, strings.Repeat("a", MaxAnswerBytes+1)); !errors.Is(err, credential.ErrInvalidAnswer) {
		t.Fatalf("oversized answer: err = %v", err)
	}
	if _, err := run(t, strings.Repeat("a", MaxAnswerBytes)); err != nil {
		t.Fatalf("answer at the limit: err = %v", err)
	}
}

// The control string must not appear in any error text, in the textual forms of the result, or in logs.
func TestSecretNeverLeaks(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	inputs := []string{
		secret + " with space",
		`{"kind":"ExecCredential","apiVersion":"client.authentication.k8s.io/v1","status":{"token":"` + secret + `","expirationTimestamp":"` + secret + `"}}`,
		`{"kind":"ExecCredential","apiVersion":"client.authentication.k8s.io/v1","status":{"token":"` + secret + `","clientCertificateData":"` + secret + `"}}`,
		`{"kind":"` + secret + `","apiVersion":"x"}`,
		`{"token":"` + secret + `"`,
		`{"kind":"ExecCredential","apiVersion":"client.authentication.k8s.io/v1","unknown":"` + secret + `"}`,
		strings.Repeat(secret, MaxAnswerBytes/len(secret)+2),
		jwt(`{"exp":1,"sub":"` + secret + `"}`),
	}
	for i, in := range inputs {
		_, err := run(t, in)
		if err == nil {
			t.Fatalf("input %d: expected an error", i)
		}
		for _, s := range []string{err.Error(), fmt.Sprintf("%v %+v %#v", err, err, err)} {
			if strings.Contains(s, secret) {
				t.Fatalf("input %d: error text leaks the secret: %q", i, s)
			}
		}
	}

	// A successful answer: the token is in Result.Token, and only there.
	res, err := run(t, secret)
	if err != nil {
		t.Fatal(err)
	}
	if res.Token.Value != secret {
		t.Fatal("token value was changed")
	}
	if s := fmt.Sprintf("%v %+v %#v %s", *res.Token, *res.Token, *res.Token, *res.Token); strings.Contains(s, secret) {
		t.Fatalf("Token text forms leak the secret: %q", s)
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatalf("logs contain the secret: %q", logs.String())
	}
}


func FuzzParseExecCredential(f *testing.F) {
	f.Add("")
	f.Add("opaque-token")
	f.Add(execCred("client.authentication.k8s.io/v1", `{"token":"opaque","expirationTimestamp":"2026-11-04T13:00:00Z"}`))
	f.Add(execCred("client.authentication.k8s.io/v1beta1", `{"token":"abc.def"}`))
	f.Add(`{"kind":"ExecCredential","apiVersion":"client.authentication.k8s.io/v1","status":{"token":"t"}} trailing`)
	f.Add(execCred("client.authentication.k8s.io/v1", `{"clientCertificateData":"cert","clientKeyData":"key"}`))
	f.Add(strings.Repeat("x", MaxAnswerBytes+1))
	f.Add("\x00")

	f.Fuzz(func(t *testing.T, input string) {
		token, err := Parse(input, now)
		if err != nil {
			if !errors.Is(err, credential.ErrInvalidAnswer) && !errors.Is(err, credential.ErrUnsupported) {
				t.Fatalf("unexpected parser error class: %T %v", err, err)
			}
			if input != "" && strings.Contains(err.Error(), input) {
				t.Fatal("parser error echoed untrusted input")
			}
			return
		}
		if token.Value == "" {
			t.Fatal("parser accepted an empty token")
		}
		if !token.Expiry.After(now) {
			t.Fatalf("parser accepted a token with non-future expiry: %v", token.Expiry)
		}
	})
}
