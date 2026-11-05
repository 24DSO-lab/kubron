// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package eks

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// decodeToken returns the presigned URL inside a token.
func decodeToken(t *testing.T, v string) *url.URL {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(v, tokenPrefix))
	if err != nil || !strings.HasPrefix(v, tokenPrefix) {
		t.Fatalf("not a token: %v", err)
	}
	u, err := url.Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func signedWith(t *testing.T, tok credential.Token) (akid, session string) {
	t.Helper()
	q := decodeToken(t, tok.Value).Query()
	return strings.Split(q.Get("X-Amz-Credential"), "/")[0], q.Get("X-Amz-Security-Token")
}

func TestUnsupportedAndZeroValues(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	env := f.env()
	bad := []credential.Descriptor{
		{},
		{Kind: credential.KindEKS},
		{Kind: credential.KindStatic, Params: map[string]string{"authenticator": "aws-cli"}},
		desc(map[string]string{"authenticator": "gke-gcloud-auth-plugin"}),
		desc(map[string]string{"authenticator": "AWS-CLI"}),
		desc(map[string]string{"region": "us-east-1/../x"}),
		desc(map[string]string{"region": "us-iso-east-1"}),
		desc(map[string]string{"cluster-name": "a b"}),
		desc(map[string]string{"cluster-name": "x\r\nHost: evil"}),
		desc(map[string]string{"role-arn": "not-an-arn"}),
		desc(map[string]string{"role-arn": "arn:aws-cn:iam::111122223333:role/x"}), // another partition than us-east-1
	}
	for i, d := range bad {
		if _, err := p.Begin(t.Context(), env, credential.Request{Descriptor: d}); !errors.Is(err, credential.ErrUnsupported) {
			t.Errorf("Begin %d: %v", i, err)
		}
		if _, err := p.Continue(t.Context(), env, credential.Request{Descriptor: d}); !errors.Is(err, credential.ErrUnsupported) {
			t.Errorf("Continue %d: %v", i, err)
		}
	}
	// Zero values everywhere else: nothing panics and the answer is a proper error.
	if _, err := p.Begin(t.Context(), credential.Env{}, credential.Request{}); !errors.Is(err, credential.ErrUnsupported) {
		t.Errorf("zero request: %v", err)
	}
	res, err := p.Continue(t.Context(), credential.Env{}, credential.Request{Descriptor: desc(nil)})
	if !errors.Is(err, credential.ErrInvalidAnswer) || res.Token != nil || res.Challenge != nil {
		t.Errorf("zero answer: %v %v", res, err)
	}
	if f.total() != 0 {
		t.Errorf("%d requests for input that is not usable", f.total())
	}
	// A provider without an HTTP client cannot reach AWS and says so.
	_, err = signIn(t, p, credential.Env{}, desc(nil), "keys", credential.Answer{"accessKeyId": testAKID, "secretAccessKey": testSecret})
	if !errors.Is(err, errNoHTTP) {
		t.Errorf("no client: %v", err)
	}
	if p.Kind() != credential.KindEKS {
		t.Error("kind")
	}
}

func TestFirstChallenge(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	res, err := p.Begin(t.Context(), f.env(), credential.Request{Descriptor: desc(nil), Subject: subjectUser()})
	c := mustChallenge(t, res, err, credential.ChallengeSecretFields)
	if names(c) != "eksMethod" || len(res.Pending) == 0 || res.Save != nil || c.ID != "" || !c.ExpiresAt.IsZero() {
		t.Errorf("%+v %s", c, res.Pending)
	}
	res, err = p.Begin(t.Context(), f.env(), credential.Request{Descriptor: desc(map[string]string{"region": "", "cluster-name": ""}), Subject: subjectUser()})
	if c = mustChallenge(t, res, err, credential.ChallengeSecretFields); names(c) != "eksMethod,eksRegion,eksClusterName" {
		t.Errorf("fields %s", names(c))
	}
	if f.total() != 0 {
		t.Error("the first challenge needs no network")
	}
	// The profile of the kubeconfig is ignored in every way: it changes nothing.
	a, _ := p.Begin(t.Context(), f.env(), credential.Request{Descriptor: desc(map[string]string{"profile": "work"})})
	b, _ := p.Begin(t.Context(), f.env(), credential.Request{Descriptor: desc(nil)})
	if names(*a.Challenge) != names(*b.Challenge) || string(a.Pending) != string(b.Pending) {
		t.Error("profile changed the sign-in")
	}
}

func TestKeysFlow(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	env, d := f.env(), desc(nil)
	res, err := signIn(t, p, env, d, "keys", credential.Answer{"accessKeyId": testAKID, "secretAccessKey": testSecret})
	tok := mustToken(t, res, err)
	if tok.Principal != testARN || !tok.Expiry.Equal(f.clock.Now().Add(14*time.Minute)) {
		t.Errorf("principal %q expiry %v", tok.Principal, tok.Expiry)
	}
	if akid, sess := signedWith(t, tok); akid != testAKID || sess != "" {
		t.Errorf("signed with %q %q", akid, sess)
	}
	sv, perr := parseSaved(res.Save)
	if perr != nil || sv.Type != savedKeys || sv.AccessKeyID != testAKID || sv.Principal != testARN || !strings.Contains(string(res.Save), `"v":1`) {
		t.Fatalf("saved %v", perr)
	}
	if f.hit("sts GetCallerIdentity") != 1 {
		t.Errorf("keys are checked once: %d", f.hit("sts GetCallerIdentity"))
	}

	// Later: a token from what is saved, nothing is asked.
	f.clock.Advance(14 * time.Minute)
	res, err = p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: res.Save})
	again := mustToken(t, res, err)
	if res.Save != nil || again.Principal != testARN || again.Value == tok.Value {
		t.Errorf("again: %v %q", res, again.Principal)
	}
	saved := []byte(strings.TrimSpace(string(mustSave(t, p, env, d))))

	// AWS cannot be reached: the token is signed anyway (signing needs no network).
	f.mu.Lock()
	f.down = true
	f.mu.Unlock()
	res, err = p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: saved})
	if off := mustToken(t, res, err); off.Principal != testARN {
		t.Errorf("offline principal %q", off.Principal)
	}
	f.mu.Lock()
	f.down = false
	delete(f.keys, testAKID) // the keys were revoked
	f.mu.Unlock()
	res, err = p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: saved})
	mustChallenge(t, res, err, credential.ChallengeSecretFields)
	if res.Save == nil || len(res.Save) != 0 {
		t.Errorf("a record that does not work must be erased: %q", res.Save)
	}

	// Unreadable or foreign saved data is not used and is erased.
	for _, bad := range []string{"x", `{"v":2,"type":"keys"}`, `{"v":1,"type":"keys","accessKeyId":"x"}`, `{"v":1,"type":"other"}`, `{"v":1,"type":"keys","accessKeyId":"` + testAKID + `","secretAccessKey":"` + testSecret + `","region":"nope"}`} {
		res, err = p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: []byte(bad)})
		mustChallenge(t, res, err, credential.ChallengeSecretFields)
		if len(res.Save) != 0 || res.Save == nil {
			t.Errorf("%q: save %q", bad, res.Save)
		}
	}
}

func mustSave(t *testing.T, p *Provider, env credential.Env, d credential.Descriptor) []byte {
	t.Helper()
	sv := saved{Type: savedKeys, AccessKeyID: testAKID, SecretAccessKey: testSecret, Principal: testARN}
	return sv.encode()
}

func TestAskedRegionAndClusterAreSaved(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	env, d := f.env(), desc(map[string]string{"region": "", "cluster-name": ""})
	res, err := signIn(t, p, env, d, "keys", credential.Answer{"accessKeyId": testAKID, "secretAccessKey": testSecret})
	tok := mustToken(t, res, err)
	q := decodeToken(t, tok.Value).Query()
	if !strings.Contains(q.Get("X-Amz-Credential"), "/us-east-1/sts/") {
		t.Errorf("region %v", q)
	}
	sv, _ := parseSaved(res.Save)
	if sv.Region != "us-east-1" || sv.Cluster != "prod" {
		t.Fatalf("saved %+v", sv)
	}
	// Begin with the saved record needs no questions even though the kubeconfig has no region and cluster.
	res, err = p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: res.Save})
	mustToken(t, res, err)
	// The kubeconfig wins over the record.
	res, err = p.Begin(t.Context(), env, credential.Request{Descriptor: desc(map[string]string{"region": "eu-west-1"}), Subject: subjectUser(), Saved: sv.encode()})
	if q := decodeToken(t, mustToken(t, res, err).Value).Query(); !strings.Contains(q.Get("X-Amz-Credential"), "/eu-west-1/sts/") {
		t.Errorf("kubeconfig region ignored: %v", q)
	}
}

func TestSessionKeys(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	env, d := f.env(), desc(nil)
	res, err := signIn(t, p, env, d, "session", credential.Answer{"accessKeyId": testASIA, "secretAccessKey": testSecret, "sessionToken": testSession})
	tok := mustToken(t, res, err)
	if akid, sess := signedWith(t, tok); akid != testASIA || sess != testSession {
		t.Errorf("signed with %q %q", akid, sess)
	}
	if sv, _ := parseSaved(res.Save); sv == nil || sv.SessionToken != testSession {
		t.Error("session token not saved")
	}
	// Temporary keys without their token are an input error and use no attempt; so is a token on the wrong path.
	for _, tc := range []struct {
		method string
		a      credential.Answer
	}{
		{"keys", credential.Answer{"accessKeyId": testASIA, "secretAccessKey": testSecret}},
		{"session", credential.Answer{"accessKeyId": testASIA, "secretAccessKey": testSecret, "sessionToken": "has space"}},
		{"session", credential.Answer{"accessKeyId": testASIA, "secretAccessKey": testSecret, "sessionToken": ""}},
	} {
		if _, err := signIn(t, p, env, d, tc.method, tc.a); !errors.Is(err, credential.ErrInvalidAnswer) {
			t.Errorf("%s %v: %v", tc.method, tc.a, err)
		}
	}
	// A wrong token and an expired token are refused by AWS: the user may try again.
	if _, err := signIn(t, p, env, d, "session", credential.Answer{"accessKeyId": testASIA, "secretAccessKey": testSecret, "sessionToken": "wrongtoken"}); !errors.Is(err, credential.ErrAnswerRejected) {
		t.Errorf("wrong token: %v", err)
	}
	f.mu.Lock()
	f.expired[testASIA] = true
	f.mu.Unlock()
	if _, err := signIn(t, p, env, d, "session", credential.Answer{"accessKeyId": testASIA, "secretAccessKey": testSecret, "sessionToken": testSession}); !errors.Is(err, credential.ErrAnswerRejected) {
		t.Errorf("expired: %v", err)
	}
	// The saved temporary keys that have expired lead to a new sign-in.
	res, err = p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: (&saved{Type: savedKeys, AccessKeyID: testASIA, SecretAccessKey: testSecret, SessionToken: testSession}).encode()})
	mustChallenge(t, res, err, credential.ChallengeSecretFields)
	if res.Save == nil || len(res.Save) != 0 {
		t.Error("expired keys were not erased")
	}
}

func TestWrongKeys(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	for name, a := range map[string]credential.Answer{
		"wrong secret":   {"accessKeyId": testAKID, "secretAccessKey": strings.Repeat("A", 40)},
		"unknown key id": {"accessKeyId": "AKIAUNKNOWNKEYID999", "secretAccessKey": testSecret},
	} {
		res, err := signIn(t, p, f.env(), desc(nil), "keys", a)
		if !errors.Is(err, credential.ErrAnswerRejected) || errors.Is(err, credential.ErrInvalidAnswer) || res.Token != nil || res.Save != nil {
			t.Errorf("%s: %v %v", name, res, err)
		}
	}
}

func TestInvalidInput(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	env := f.env()
	good := credential.Answer{"accessKeyId": testAKID, "secretAccessKey": testSecret}
	for name, tc := range map[string]struct {
		params   map[string]string
		first    credential.Answer
		keys     credential.Answer
		wantErr  error
		wantText error
	}{
		"empty method":         {nil, credential.Answer{"eksMethod": ""}, good, credential.ErrInvalidAnswer, errMethod},
		"unknown method":       {nil, credential.Answer{"eksMethod": "oauth"}, good, credential.ErrInvalidAnswer, errMethod},
		"missing region":       {map[string]string{"region": ""}, credential.Answer{"eksMethod": "keys", "eksRegion": ""}, good, credential.ErrInvalidAnswer, errRegionRequired},
		"blank region":         {map[string]string{"region": ""}, credential.Answer{"eksMethod": "keys", "eksRegion": "   "}, good, credential.ErrInvalidAnswer, errRegionRequired},
		"bad region":           {map[string]string{"region": ""}, credential.Answer{"eksMethod": "keys", "eksRegion": "mars-1"}, good, credential.ErrInvalidAnswer, errBadRegion},
		"region with a host":   {map[string]string{"region": ""}, credential.Answer{"eksMethod": "keys", "eksRegion": "us-east-1.evil.example"}, good, credential.ErrInvalidAnswer, errBadRegion},
		"isolated partition":   {map[string]string{"region": ""}, credential.Answer{"eksMethod": "keys", "eksRegion": "us-iso-east-1"}, good, credential.ErrUnsupported, errPartition},
		"missing cluster":      {map[string]string{"cluster-name": ""}, credential.Answer{"eksMethod": "keys", "eksClusterName": ""}, good, credential.ErrInvalidAnswer, errClusterRequired},
		"bad cluster":          {map[string]string{"cluster-name": ""}, credential.Answer{"eksMethod": "keys", "eksClusterName": "a b\nc"}, good, credential.ErrInvalidAnswer, errBadCluster},
		"role in another part": {map[string]string{"role-arn": "arn:aws-cn:iam::111122223333:role/x", "region": ""}, credential.Answer{"eksMethod": "keys", "eksRegion": "us-east-1"}, good, credential.ErrInvalidAnswer, errBadRoleARN},
		"empty key id":         {nil, credential.Answer{"eksMethod": "keys"}, credential.Answer{"accessKeyId": "", "secretAccessKey": testSecret}, credential.ErrInvalidAnswer, errKeyFormat},
		"empty secret":         {nil, credential.Answer{"eksMethod": "keys"}, credential.Answer{"accessKeyId": testAKID, "secretAccessKey": ""}, credential.ErrInvalidAnswer, errKeyFormat},
		"no fields at all":     {nil, credential.Answer{"eksMethod": "keys"}, nil, credential.ErrInvalidAnswer, errKeyFormat},
		"lowercase key id":     {nil, credential.Answer{"eksMethod": "keys"}, credential.Answer{"accessKeyId": strings.ToLower(testAKID), "secretAccessKey": testSecret}, credential.ErrInvalidAnswer, errKeyFormat},
		"secret with space":    {nil, credential.Answer{"eksMethod": "keys"}, credential.Answer{"accessKeyId": testAKID, "secretAccessKey": "has a space in the middle of it......"}, credential.ErrInvalidAnswer, errKeyFormat},
		"header injection":     {nil, credential.Answer{"eksMethod": "keys"}, credential.Answer{"accessKeyId": testAKID + "\r\nX: y", "secretAccessKey": testSecret}, credential.ErrInvalidAnswer, errKeyFormat},
	} {
		d := desc(tc.params)
		res, err := p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
		mustChallenge(t, res, err, credential.ChallengeSecretFields)
		next, err := step(t, p, env, d, res, nil, tc.first)
		if err == nil {
			_, err = step(t, p, env, d, next, nil, tc.keys)
		}
		if !errors.Is(err, tc.wantErr) || !errors.Is(err, tc.wantText) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if f.total() != 0 {
		t.Errorf("%d requests for input that is not usable", f.total())
	}
}

func TestAssumeRole(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	env, d := f.env(), desc(map[string]string{"role-arn": testRole})
	res, err := signIn(t, p, env, d, "keys", credential.Answer{"accessKeyId": testAKID, "secretAccessKey": testSecret})
	tok := mustToken(t, res, err)
	if len(f.roleSeen) != 1 || f.roleSeen[0] != testRole+"|kubron|" {
		t.Errorf("AssumeRole: %v", f.roleSeen)
	}
	if akid, sess := signedWith(t, tok); akid != testTempID || sess != testTempTok {
		t.Errorf("the token must be signed with the temporary credentials: %q %q", akid, sess)
	}
	if tok.Principal != testRole {
		t.Errorf("principal %q", tok.Principal)
	}
	if sv, _ := parseSaved(res.Save); sv == nil || sv.AccessKeyID != testAKID || strings.Contains(string(res.Save), testTempSec) {
		t.Error("the base keys, not the temporary ones, must be saved")
	}
	if f.hit("sts GetCallerIdentity") != 0 {
		t.Error("with a role AssumeRole is the check")
	}
	// Later: assumed again from the saved base keys.
	res2, err := p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: res.Save})
	if mustToken(t, res2, err); len(f.roleSeen) != 2 || res2.Save != nil {
		t.Errorf("roles %v save %q", f.roleSeen, res2.Save)
	}

	// The role refuses these keys: the answer is rejected (the user may try other keys).
	f.mu.Lock()
	f.roleOK = false
	f.mu.Unlock()
	if _, err := signIn(t, p, env, d, "keys", credential.Answer{"accessKeyId": testAKID, "secretAccessKey": testSecret}); !errors.Is(err, credential.ErrAnswerRejected) || !errors.Is(err, errRoleDenied) {
		t.Errorf("denied: %v", err)
	}
	// ... and when it happens later with saved keys it is not a reason to erase them or to ask again.
	res2, err = p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: res.Save})
	if err == nil || errors.Is(err, credential.ErrInvalidGrant) || res2.Challenge != nil || res2.Save != nil || !errors.Is(err, errRoleDenied) {
		t.Errorf("saved + denied: %v %v", res2, err)
	}
	// Wrong base keys with a role.
	if _, err := signIn(t, p, env, d, "keys", credential.Answer{"accessKeyId": testAKID, "secretAccessKey": strings.Repeat("B", 40)}); !errors.Is(err, credential.ErrAnswerRejected) {
		t.Errorf("wrong keys with a role: %v", err)
	}
}

func TestAuthenticatorsIssueTheSameToken(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	keys := credential.Answer{"accessKeyId": testAKID, "secretAccessKey": testSecret}
	a, err := signIn(t, p, f.env(), desc(nil), "keys", keys)
	b, err2 := signIn(t, p, f.env(), desc(map[string]string{"authenticator": "aws-iam-authenticator"}), "keys", keys)
	if mustToken(t, a, err).Value != mustToken(t, b, err2).Value {
		t.Error("aws-cli and aws-iam-authenticator tokens differ")
	}
}

func TestPendingIsChecked(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	env := f.env()
	ask := desc(map[string]string{"region": "", "cluster-name": ""}) // region and cluster come from the pending state
	for _, pend := range []string{"garbage", `{"v":2,"step":"keys"}`, `{"v":1,"step":"nope"}`, `{"v":1,"step":"keys","region":"nope","cluster":"x"}`,
		`{"v":1,"step":"keys","region":"us-east-1","cluster":"a b"}`, `{"v":1,"step":"keys"}`, `{"v":1,"step":"device"}`} {
		_, err := p.Continue(t.Context(), env, credential.Request{Descriptor: ask, Pending: []byte(pend),
			Challenge: credential.Challenge{Kind: credential.ChallengeSecretFields}, Answer: credential.Answer{"accessKeyId": testAKID, "secretAccessKey": testSecret}})
		if !errors.Is(err, credential.ErrChallengeExpired) {
			t.Errorf("%s: %v", pend, err)
		}
	}
	if f.total() != 0 {
		t.Error("network used for a broken pending state")
	}
}
