// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package gke

import (
	"errors"

	"github.com/24DSO-lab/kubron/internal/credential"
)

// Errors of this provider. The texts are English, fixed and registered in backend.json (credGKE*). None of
// them contains anything that came from the user or from Google: a key, an assertion, a token or an address
// cannot reach a log line, an event or an API answer through them.
var (
	errNoHTTP         = errors.New("the HTTP client for Google sign-in is not configured")
	errKeyTooLarge    = errors.New("the service account key is too large")
	errKeyJSON        = errors.New("the service account key is not valid JSON")
	errKeyType        = errors.New("the key is not a service account key")
	errKeyEmail       = errors.New("the service account key has no valid client_email")
	errKeyPrivate     = errors.New("the service account private key could not be read")
	errKeyUnsupported = errors.New("this kind of Google credentials is not supported")
	errRejected       = errors.New("Google rejected the service account key")
	errTokenRequest   = errors.New("Google rejected the token request")
	errTokenResponse  = errors.New("Google returned an invalid token response")
	errUnreachable    = errors.New("Google sign-in is unreachable")

	errUserConfig    = errors.New("the Google sign-in client is not configured correctly")
	errRedirect      = errors.New("the pasted address is not a valid Google sign-in response")
	errAuthState     = errors.New("the sign-in response does not match this request")
	errAuthDenied    = errors.New("Google sign-in was denied or cancelled")
	errCodeRejected  = errors.New("Google did not accept the sign-in code")
	errIDToken       = errors.New("the Google ID token does not match this sign-in")
	errSavedRejected = errors.New("Google no longer accepts the saved sign-in")
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

// invalid marks an answer that cannot be used at all (credential.ErrInvalidAnswer): it is not a wrong guess.
func invalid(detail error) error {
	return &providerError{kind: credential.ErrInvalidAnswer, detail: detail}
}

// unsupported marks a mode this provider does not handle (credential.ErrUnsupported).
func unsupported(detail error) error {
	return &providerError{kind: credential.ErrUnsupported, detail: detail}
}

// rejectedKey marks a key that Google does not accept (credential.ErrAnswerRejected): the user may try again.
func rejectedKey() error {
	return &providerError{kind: credential.ErrAnswerRejected, detail: errRejected}
}

// unavailable marks a failure that is not the user's fault (credential.ErrServiceUnavailable).
func unavailable() error {
	return &providerError{kind: credential.ErrServiceUnavailable, detail: errUnreachable}
}
