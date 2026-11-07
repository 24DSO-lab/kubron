// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package eks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

const (
	maxBody         = 1 << 20
	roleSessionName = "kubron"
)

// session is the state of one Begin or Continue call; nothing outlives it. All network traffic of the
// provider goes through hc, which is Env.HTTP (the client with the SSRF policy). A session without it cannot
// reach AWS: the provider has no client of its own.
type session struct {
	hc  *http.Client
	now func() time.Time
	// desktop only changes which verification addresses are acceptable (http and loopback).
	desktop bool
}

// do sends a request and reads the answer (at most maxBody bytes).
func (s *session) do(req *http.Request) (int, []byte, error) {
	if s.hc == nil {
		return 0, nil, errNoHTTP
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return 0, nil, netFailure(req.Context(), err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return 0, nil, netFailure(req.Context(), err)
	}
	if len(body) > maxBody {
		return 0, nil, errAWSResponse
	}
	switch {
	case resp.StatusCode >= 500, resp.StatusCode == http.StatusTooManyRequests:
		return 0, nil, errUnreachable
	}
	return resp.StatusCode, body, nil
}

type xmlError struct {
	Code string `xml:"Error>Code"`
}

// stsCall sends one signed STS query (Action in form) with header-based SigV4 and returns the body of a 200
// answer. An answer with an error code of AWS is turned into one of this package's fixed errors.
func (s *session) stsCall(ctx context.Context, c creds, region string, form url.Values) ([]byte, error) {
	data := form.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+stsHost(region)+"/", strings.NewReader(data))
	if err != nil {
		return nil, errSign
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	sum := sha256.Sum256([]byte(data))
	if err := v4.NewSigner().SignHTTP(ctx, c.aws(), req, hex.EncodeToString(sum[:]), "sts", region, s.now().UTC()); err != nil {
		return nil, errSign
	}
	status, body, err := s.do(req)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return body, nil
	}
	var e xmlError
	if xml.Unmarshal(body, &e) != nil {
		return nil, errAWSResponse
	}
	return nil, stsErr(status, e.Code)
}

// stsErr maps the error code of an STS answer. The code (a fixed word of the AWS API) is compared, never shown.
func stsErr(status int, code string) error {
	switch code {
	case "InvalidClientTokenId", "SignatureDoesNotMatch", "ExpiredToken", "ExpiredTokenException", "IncompleteSignature",
		"MissingAuthenticationToken", "InvalidAccessKeyId", "UnrecognizedClientException":
		return errKeysRejected
	case "AccessDenied", "AccessDeniedException":
		return errRoleDenied
	case "RequestExpired", "RequestTimeTooSkewed":
		return errClockSkew
	case "Throttling", "ThrottlingException", "RequestLimitExceeded", "ServiceUnavailable":
		return errUnreachable
	case "ValidationError":
		return errBadRoleARN
	}
	if status == http.StatusForbidden || status == http.StatusUnauthorized {
		return errKeysRejected
	}
	return errAWSResponse
}

// identity calls sts:GetCallerIdentity with the credentials and returns the ARN of the caller. It is also the
// check that the keys work.
func (s *session) identity(ctx context.Context, c creds, region string) (string, error) {
	body, err := s.stsCall(ctx, c, region, url.Values{"Action": {stsAction}, "Version": {stsVersion}})
	if err != nil {
		return "", err
	}
	var r struct {
		Arn string `xml:"GetCallerIdentityResult>Arn"`
	}
	if xml.Unmarshal(body, &r) != nil || cleanPrincipal(r.Arn) == "" {
		return "", errAWSResponse
	}
	return r.Arn, nil
}

// assume calls sts:AssumeRole with the base credentials (RoleSessionName "kubron", default duration) and
// returns the temporary credentials.
func (s *session) assume(ctx context.Context, base creds, region, roleARN string) (creds, error) {
	body, err := s.stsCall(ctx, base, region, url.Values{
		"Action": {"AssumeRole"}, "Version": {stsVersion}, "RoleArn": {roleARN}, "RoleSessionName": {roleSessionName},
	})
	if err != nil {
		return creds{}, err
	}
	var r struct {
		ID      string `xml:"AssumeRoleResult>Credentials>AccessKeyId"`
		Secret  string `xml:"AssumeRoleResult>Credentials>SecretAccessKey"`
		Session string `xml:"AssumeRoleResult>Credentials>SessionToken"`
	}
	if xml.Unmarshal(body, &r) != nil || !akidRE.MatchString(r.ID) || r.Secret == "" || !printableToken(r.Session, maxSessionToken) ||
		!printableToken(r.Secret, 512) {
		return creds{}, errAWSResponse
	}
	return creds{accessKeyID: r.ID, secretAccessKey: r.Secret, sessionToken: r.Session}, nil
}
