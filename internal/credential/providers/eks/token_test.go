// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package eks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

type golden struct {
	Name, AccessKeyID, SecretAccessKey, SessionToken, Region, Cluster, URL, Token string
	Unix                                                                          int64
}

func loadGolden(t *testing.T) []golden {
	t.Helper()
	b, err := os.ReadFile("testdata/golden_tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	var g []golden
	if err := json.Unmarshal(b, &g); err != nil || len(g) < 4 {
		t.Fatalf("golden file: %v", err)
	}
	return g
}

// The tokens are the same, byte for byte, as the ones botocore makes with the hooks of
// awscli/customizations/eks/get_token.py (see testdata/gen_golden.py).
func TestTokenMatchesAwsCli(t *testing.T) {
	for _, g := range loadGolden(t) {
		c := creds{accessKeyID: g.AccessKeyID, secretAccessKey: g.SecretAccessKey, sessionToken: g.SessionToken}
		got, err := signToken(t.Context(), c, g.Region, g.Cluster, time.Unix(g.Unix, 0))
		if err != nil {
			t.Fatalf("%s: %v", g.Name, err)
		}
		if got == g.Token {
			continue
		}
		// botocore puts X-Amz-Security-Token after X-Amz-SignedHeaders, the Go SDK (which aws-iam-authenticator uses
		// as well) sorts it before. The signature covers the sorted canonical query, so only the order of the two
		// parameters differs and every value, the signature included, is the same. Without a session token the
		// tokens must be identical.
		raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(got, tokenPrefix))
		gu, err1 := url.Parse(string(raw))
		wu, err2 := url.Parse(g.URL)
		if g.SessionToken == "" || err1 != nil || err2 != nil || gu.Host != wu.Host || gu.Path != wu.Path ||
			gu.Query().Encode() != wu.Query().Encode() || !strings.HasPrefix(got, tokenPrefix) {
			t.Errorf("%s: token differs from aws-cli\n got  %s\n want %s", g.Name, raw, g.URL)
		}
	}
}

// The structure of the token as the verifier of aws-iam-authenticator demands it.
func TestTokenStructure(t *testing.T) {
	for _, tc := range []struct{ region, host string }{
		{"us-east-1", "sts.us-east-1.amazonaws.com"},
		{"eu-central-1", "sts.eu-central-1.amazonaws.com"},
		{"cn-north-1", "sts.cn-north-1.amazonaws.com.cn"},
		{"cn-northwest-1", "sts.cn-northwest-1.amazonaws.com.cn"},
		{"us-gov-east-1", "sts.us-gov-east-1.amazonaws.com"},
	} {
		now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
		tok, err := signToken(t.Context(), creds{accessKeyID: testAKID, secretAccessKey: testSecret, sessionToken: "tok-1"}, tc.region, "my-cluster", now)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(tok, "k8s-aws-v1.") || strings.ContainsAny(tok, "=+/") {
			t.Fatalf("prefix or alphabet: %q", tok)
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(tok, tokenPrefix))
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(string(raw))
		if err != nil {
			t.Fatal(err)
		}
		if u.Scheme != "https" || u.Host != tc.host || u.Path != "/" {
			t.Errorf("%s: %s://%s%s", tc.region, u.Scheme, u.Host, u.Path)
		}
		q := u.Query()
		var keys []string
		for k, v := range q {
			if len(v) != 1 {
				t.Errorf("repeated parameter %s", k)
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		want := "Action,Version,X-Amz-Algorithm,X-Amz-Credential,X-Amz-Date,X-Amz-Expires,X-Amz-Security-Token,X-Amz-Signature,X-Amz-SignedHeaders"
		if strings.Join(keys, ",") != want { // exactly the whitelist of the verifier
			t.Errorf("parameters %v", keys)
		}
		if q.Get("Action") != "GetCallerIdentity" || q.Get("Version") != "2011-06-15" || q.Get("X-Amz-Expires") != "60" ||
			q.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" || q.Get("X-Amz-Date") != "20261104T120000Z" || q.Get("X-Amz-SignedHeaders") != "host;x-k8s-aws-id" {
			t.Errorf("values %v", q)
		}
		if q.Get("X-Amz-Credential") != testAKID+"/20261104/"+tc.region+"/sts/aws4_request" {
			t.Errorf("credential scope %q", q.Get("X-Amz-Credential"))
		}
		if strings.Contains(string(raw), testSecret) || strings.Contains(string(raw), "my-cluster") {
			t.Error("the secret key or the cluster name is in the URL (the name is only signed, not sent)")
		}
	}
}

// The cluster name changes the signature, so a token works for one cluster only.
func TestTokenIsBoundToCluster(t *testing.T) {
	c := creds{accessKeyID: testAKID, secretAccessKey: testSecret}
	now := time.Unix(1700000000, 0)
	a, _ := signToken(t.Context(), c, "us-east-1", "one", now)
	b, _ := signToken(t.Context(), c, "us-east-1", "two", now)
	if a == b {
		t.Fatal("two clusters, one token")
	}
}

// The official example of AWS "Authenticating requests: using query parameters (SigV4)":
// https://docs.aws.amazon.com/AmazonS3/latest/API/sigv4-query-string-auth.html
func TestSigV4QueryVector(t *testing.T) {
	c := creds{accessKeyID: "AKIAIOSFODNN7EXAMPLE", secretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	got, err := presignGET(t.Context(), "https://examplebucket.s3.amazonaws.com/test.txt?X-Amz-Expires=86400", nil, c, "s3", "us-east-1",
		"UNSIGNED-PAYLOAD", time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	const want = "https://examplebucket.s3.amazonaws.com/test.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256" +
		"&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20130524T000000Z" +
		"&X-Amz-Expires=86400&X-Amz-SignedHeaders=host&X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// The "get-vanilla" case of the AWS Signature Version 4 test suite (aws-sig-v4-test-suite): the header-based
// form that the STS calls of this provider use.
func TestSigV4HeaderVector(t *testing.T) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.amazonaws.com/", nil)
	err := v4.NewSigner().SignHTTP(t.Context(), aws.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"},
		req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "service", "us-east-1", time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	const want = "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, " +
		"Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestPartitionsAndRegions(t *testing.T) {
	for region, ok := range map[string]bool{"us-east-1": true, "ap-southeast-4": true, "cn-north-1": true, "us-gov-west-1": true, "il-central-1": true,
		"": false, "US-EAST-1": false, "us-east": false, "us-east-1.evil.com": false, "us-east-1/../x": false, "x": false, "us-east-1:443@h": false} {
		if got := regionRE.MatchString(region); got != ok {
			t.Errorf("%q: %v", region, got)
		}
	}
	for _, iso := range []string{"us-iso-east-1", "us-isob-east-1", "eu-isoe-west-1"} {
		if checkRegion(iso) != errPartition {
			t.Errorf("%s must be unsupported", iso)
		}
	}
	if stsHost("cn-north-1") != "sts.cn-north-1.amazonaws.com.cn" || stsHost("eu-west-1") != "sts.eu-west-1.amazonaws.com" {
		t.Error("sts host")
	}
}
