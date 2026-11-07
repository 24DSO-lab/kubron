// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package manual

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

const (
	// MaxAnswerBytes bounds what the user can paste: an ExecCredential is a few KiB at most.
	MaxAnswerBytes = 64 << 10
	// DefaultTTL is the lifetime of a token that carries no expiry of its own.
	DefaultTTL = 5 * time.Minute
	// FieldToken is the name of the answer field (the UI label is credential.field.token).
	FieldToken      = "token"
	maxPrincipalLen = 256
	kindExecCred    = "ExecCredential"
)

var supportedAPIVersions = map[string]bool{
	"client.authentication.k8s.io/v1":      true,
	"client.authentication.k8s.io/v1beta1": true,
}

// Errors of this provider. The texts are English and are registered in backend.json (credUIManual*). None of
// them carries user input.
var (
	errNoCommand       = errors.New("the kubeconfig has no plugin command to show")
	errTokenEmpty      = errors.New("the token is empty")
	errTooLarge        = errors.New("the pasted text is too large")
	errBadJSON         = errors.New("the pasted text is not a valid ExecCredential")
	errBadKind         = errors.New("the pasted ExecCredential has an unexpected kind or apiVersion")
	errNoToken         = errors.New("the pasted ExecCredential has no token")
	errCertUnsupported = errors.New("client certificates returned by a plugin are not supported")
	errBadExpiry       = errors.New("the token expiration time is invalid")
	errExpired         = errors.New("the token has already expired")
	errBadToken        = errors.New("the token must be a single line without spaces")
)

// providerError ties a detail to a contract error: errors.Is matches the contract error, the text is the
// detail. The Broker reduces it to the contract error before it reaches a caller.
type providerError struct {
	kind   error
	detail error
}

func (e *providerError) Error() string        { return e.detail.Error() }
func (e *providerError) Unwrap() error        { return e.detail }
func (e *providerError) Is(target error) bool { return target == e.kind }

// rejected marks a pasted value that cannot be used. It is credential.ErrInvalidAnswer, not ErrAnswerRejected: pasting
// something of the wrong shape is not a wrong guess and must not use up an attempt.
func rejected(detail error) error {
	return &providerError{kind: credential.ErrInvalidAnswer, detail: detail}
}
func unsupported(detail error) error {
	return &providerError{kind: credential.ErrUnsupported, detail: detail}
}

// Provider implements credential.Provider for credential.KindExec in web mode.
type Provider struct{}

// New returns the provider. It has no state.
func New() *Provider { return &Provider{} }

// Kind implements credential.Provider.
func (*Provider) Kind() credential.Kind { return credential.KindExec }

// Begin implements credential.Provider. It only describes what the user has to do; nothing is executed.
func (*Provider) Begin(_ context.Context, _ credential.Env, req credential.Request) (credential.Result, error) {
	x := req.Descriptor.Exec
	if x == nil || strings.TrimSpace(x.Command) == "" {
		return credential.Result{}, unsupported(errNoCommand)
	}
	// Environment values are not part of the challenge: they may hold secrets and the user runs the command
	// in their own environment.
	command := append([]string{x.Command}, x.Args...)
	return credential.Result{Challenge: &credential.Challenge{
		Kind:    credential.ChallengeManualToken,
		Fields:  []credential.Field{{Name: FieldToken, Secret: true}},
		Command: command,
		Message: x.InstallHint,
	}}, nil
}

// Continue implements credential.Provider. The answer is an ExecCredential JSON document or a bare token.
func (*Provider) Continue(_ context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	now := time.Now
	if env.Now != nil {
		now = env.Now
	}
	tok, err := Parse(req.Answer[FieldToken], now())
	if err != nil {
		return credential.Result{}, err
	}
	return credential.Result{Token: &tok}, nil
}

// Parse turns the pasted text into a token. It is exported for tests and for callers that validate input
// before sending it. now is the reference time for expiry checks.
func Parse(input string, now time.Time) (credential.Token, error) {
	if len(input) > MaxAnswerBytes {
		return credential.Token{}, rejected(errTooLarge)
	}
	text := strings.TrimSpace(input)
	if text == "" {
		return credential.Token{}, rejected(errTokenEmpty)
	}
	if text[0] == '{' {
		return parseExecCredential(text, now)
	}
	return bareToken(text, now)
}

type execCredential struct {
	Kind       string          `json:"kind"`
	APIVersion string          `json:"apiVersion"`
	Spec       json.RawMessage `json:"spec"`
	Status     *execStatus     `json:"status"`
}

type execStatus struct {
	Token                 string `json:"token"`
	ExpirationTimestamp   string `json:"expirationTimestamp"`
	ClientCertificateData string `json:"clientCertificateData"`
	ClientKeyData         string `json:"clientKeyData"`
}

func parseExecCredential(text string, now time.Time) (credential.Token, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	var ec execCredential
	if err := dec.Decode(&ec); err != nil {
		return credential.Token{}, rejected(errBadJSON) // err is dropped: its text may quote the input
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) { // trailing data after the document
		return credential.Token{}, rejected(errBadJSON)
	}
	if ec.Kind != kindExecCred || !supportedAPIVersions[ec.APIVersion] {
		return credential.Token{}, rejected(errBadKind)
	}
	if ec.Status == nil {
		return credential.Token{}, rejected(errNoToken)
	}
	if ec.Status.ClientCertificateData != "" || ec.Status.ClientKeyData != "" {
		return credential.Token{}, unsupported(errCertUnsupported)
	}
	if ec.Status.Token == "" {
		return credential.Token{}, rejected(errNoToken)
	}
	if !validToken(ec.Status.Token) {
		return credential.Token{}, rejected(errBadToken)
	}
	claims, _ := jwtClaims(ec.Status.Token)
	var expiry time.Time
	switch {
	case ec.Status.ExpirationTimestamp != "":
		t, err := time.Parse(time.RFC3339, ec.Status.ExpirationTimestamp)
		if err != nil {
			return credential.Token{}, rejected(errBadExpiry)
		}
		expiry = t
	case !claims.exp.IsZero():
		expiry = claims.exp
	default:
		expiry = now.Add(DefaultTTL)
	}
	if !expiry.After(now) {
		return credential.Token{}, rejected(errExpired)
	}
	return credential.Token{Value: ec.Status.Token, Expiry: expiry}, nil
}

func bareToken(text string, now time.Time) (credential.Token, error) {
	if !validToken(text) {
		return credential.Token{}, rejected(errBadToken)
	}
	claims, _ := jwtClaims(text)
	expiry := now.Add(DefaultTTL)
	if !claims.exp.IsZero() {
		expiry = claims.exp
	}
	if !expiry.After(now) {
		return credential.Token{}, rejected(errExpired)
	}
	return credential.Token{Value: text, Expiry: expiry}, nil
}

// validToken accepts printable ASCII without spaces. A token ends up in an Authorization header, so line
// breaks and control characters must never get through.
func validToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= 0x20 || s[i] >= 0x7f {
			return false
		}
	}
	return true
}

type claims struct {
	exp time.Time
}

// jwtClaims reads only exp from a JWT payload without verifying the signature. The API server verifies the
// token; an unverified sub must never be presented as an authenticated Kubernetes identity in audit or UI.
func jwtClaims(token string) (claims, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return claims{}, false
	}
	var p struct {
		Exp json.Number `json:"exp"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&p); err != nil {
		return claims{}, false
	}
	var out claims
	if secs, err := p.Exp.Float64(); err == nil && secs > 0 && secs < 1<<40 {
		out.exp = time.Unix(int64(secs), 0)
	}
	return out, true
}
