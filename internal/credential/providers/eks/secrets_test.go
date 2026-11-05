// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package eks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// shown collects everything the provider lets out to a place where a secret must not be: error texts (in the
// three usual formattings), the public challenge as the API would send it, tokens printed with fmt, answers
// printed with fmt, and the log. Save and Pending are not "shown": they are the data the Broker encrypts.
type shown struct {
	b      strings.Builder
	tokens []string
}

func (s *shown) err(err error) {
	if err != nil {
		fmt.Fprintf(&s.b, "%v|%+v|%#v|%q\n", err, err, err, err.Error())
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
		s.tokens = append(s.tokens, res.Token.Value)
	}
	fmt.Fprintf(&s.b, "%v|%+v\n", res, res)
}

func (s *shown) answer(a credential.Answer) { fmt.Fprintf(&s.b, "%v|%+v|%#v\n", a, a, a) }

// The canary test: every secret the flows handle has a recognisable value, all flows run (successes and every
// kind of failure), and none of those values may appear in anything shown.
func TestSecretsAreNotShown(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	wd, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	const wrongSecret, wrongSession, malformed = "WRONGsecretCANARY0123456789abcdef", "wrong-session-CANARY", "bad secret CANARY with spaces"
	out := &shown{}
	f := newFakeAWS(t)
	p, env := New(), f.env()
	run := func(d credential.Descriptor, method string, a credential.Answer) credential.Result {
		out.answer(a)
		res, err := signIn(t, p, env, d, method, a)
		out.result(res, err)
		return res
	}

	good := run(desc(nil), "keys", credential.Answer{"accessKeyId": testAKID, "secretAccessKey": testSecret})
	run(desc(nil), "keys", credential.Answer{"accessKeyId": testAKID, "secretAccessKey": wrongSecret})
	run(desc(nil), "keys", credential.Answer{"accessKeyId": testAKID, "secretAccessKey": malformed})
	run(desc(nil), "session", credential.Answer{"accessKeyId": testASIA, "secretAccessKey": testSecret, "sessionToken": testSession})
	run(desc(nil), "session", credential.Answer{"accessKeyId": testASIA, "secretAccessKey": testSecret, "sessionToken": wrongSession})
	run(desc(map[string]string{"role-arn": testRole}), "keys", credential.Answer{"accessKeyId": testAKID, "secretAccessKey": testSecret})
	f.mu.Lock()
	f.roleOK = false
	f.mu.Unlock()
	run(desc(map[string]string{"role-arn": testRole}), "keys", credential.Answer{"accessKeyId": testAKID, "secretAccessKey": testSecret})

	// Saved data: used, revoked, unreadable.
	r, err := p.Begin(t.Context(), env, credential.Request{Descriptor: desc(nil), Subject: subjectUser(), Saved: good.Save})
	out.result(r, err)
	r, err = p.Begin(t.Context(), env, credential.Request{Descriptor: desc(map[string]string{"role-arn": testRole}), Subject: subjectUser(), Saved: good.Save})
	out.result(r, err)
	f.mu.Lock()
	delete(f.keys, testAKID)
	f.mu.Unlock()
	r, err = p.Begin(t.Context(), env, credential.Request{Descriptor: desc(nil), Subject: subjectUser(), Saved: good.Save})
	out.result(r, err)
	r, err = p.Begin(t.Context(), env, credential.Request{Descriptor: desc(nil), Subject: subjectUser(), Saved: []byte("garbage " + testSecret)})
	out.result(r, err)

	// AWS down.
	f.mu.Lock()
	f.down = true
	f.mu.Unlock()
	run(desc(nil), "keys", credential.Answer{"accessKeyId": testAKID, "secretAccessKey": testSecret})

	// IAM Identity Center: pending, approved, refresh, denied, expired.
	for _, status := range []string{"approved", "denied", "expired"} {
		f2 := newFakeAWS(t)
		env2, d := f2.env(), desc(nil)
		res, err := p.Begin(t.Context(), env2, credential.Request{Descriptor: d, Subject: subjectUser()})
		out.result(res, err)
		res, err = step(t, p, env2, d, res, nil, credential.Answer{"eksMethod": "sso"})
		out.result(res, err)
		res, err = step(t, p, env2, d, res, nil, ssoAnswer)
		out.result(res, err)
		f2.mu.Lock()
		f2.deviceStatus = status
		f2.mu.Unlock()
		f2.clock.Advance(6 * time.Second)
		r, err = poll(t, p, env2, d, res)
		out.result(r, err)
		if status == "approved" {
			f2.clock.Advance(4 * time.Hour)
			r2, err := p.Begin(t.Context(), env2, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: r.Save})
			out.result(r2, err)
			f2.mu.Lock()
			f2.refreshOK = false
			f2.mu.Unlock()
			f2.clock.Advance(4 * time.Hour)
			r2, err = p.Begin(t.Context(), env2, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: r2.Save})
			out.result(r2, err)
		}
	}

	forbidden := map[string]string{
		"secret access key": testSecret, "wrong secret": wrongSecret, "session token": testSession, "wrong session token": wrongSession,
		"temporary secret": testTempSec, "temporary token": testTempTok, "sso access token": testSSOTok, "sso refresh token": testSSORef,
		"sso client secret": testCliSec, "malformed secret": malformed, "device code": testDevCode, "message of AWS": "secret details that must never be shown",
	}
	for i, tok := range out.tokens {
		forbidden[fmt.Sprintf("token %d", i)] = tok
		forbidden[fmt.Sprintf("signature of token %d", i)] = tok[len(tok)-32:]
	}
	hay := out.b.String() + logs.String()
	for what, v := range forbidden {
		if v != "" && strings.Contains(hay, v) {
			t.Errorf("%s (%q...) was shown", what, v[:min(len(v), 10)])
		}
	}
	if len(out.tokens) < 6 {
		t.Errorf("only %d tokens were issued: the flows did not run", len(out.tokens))
	}
	if !strings.Contains(hay, "rejected") || !strings.Contains(hay, "unreachable") {
		t.Error("the failure flows did not run")
	}
	if logs.Len() != 0 {
		t.Errorf("the provider must not log, got: %s", logs.String())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("the provider wrote files into the working directory: %v", ents)
	}
	// What the provider sends to AWS never contains the secret key either (it is only used to sign).
	for _, body := range f.bodies {
		if strings.Contains(body, testSecret) || strings.Contains(body, wrongSecret) {
			t.Error("a secret key was sent to AWS")
		}
	}
}

// The errors carry fixed texts: whatever the user typed stays out of them, and so does the answer of AWS.
func TestErrorTextsAreFixed(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	_, err := signIn(t, p, f.env(), desc(nil), "keys", credential.Answer{"accessKeyId": "AKIAUNKNOWNKEYID999", "secretAccessKey": testSecret})
	if err == nil || err.Error() != "AWS rejected the access keys" {
		t.Errorf("got %v", err)
	}
	f.mu.Lock()
	f.down = true
	f.mu.Unlock()
	_, err = signIn(t, p, f.env(), desc(nil), "keys", credential.Answer{"accessKeyId": testAKID, "secretAccessKey": testSecret})
	if err == nil || err.Error() != "AWS is unreachable" || !errors.Is(err, credential.ErrServiceUnavailable) {
		t.Errorf("got %v", err)
	}
}
