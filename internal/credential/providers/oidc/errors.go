// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"errors"
	"net/http"

	"github.com/24DSO-labs/kubron/internal/idpnet"
	"github.com/24DSO-labs/kubron/internal/netpolicy"
)

// Errors of this provider. Their texts are fixed and never contain anything that came from the identity
// provider, the kubeconfig or the user: a password, token, code or client secret cannot reach a log line,
// an event or an API answer through them. The cause of a failure is kept only as a typed error (see wrap).
var (
	errConfig            = errors.New("the OIDC issuer URL and client ID are required")
	errDiscovery         = errors.New("the identity provider discovery failed")
	errIssuerMismatch    = errors.New("the identity provider issuer does not match the configured issuer")
	errUnreachable       = errors.New("the identity provider is unreachable")
	errTokenRequest      = errors.New("the identity provider rejected the token request")
	errTokenResponse     = errors.New("the identity provider returned an invalid token response")
	errIDToken           = errors.New("the ID token could not be verified")
	errState             = errors.New("the sign-in response does not match the request")
	errDenied            = errors.New("the identity provider denied the sign-in request")
	errRedirect          = errors.New("the pasted address is not a valid sign-in response")
	errListen            = errors.New("the local sign-in listener could not be started")
	errDeviceUnavailable = errors.New("the identity provider does not offer the device sign-in")
	errMixup             = errors.New("the sign-in response came from an unexpected issuer")
	errNonce             = errors.New("the ID token nonce does not match")
	errPending           = errors.New("the sign-in state is invalid")
)

// wrapped is an error with a fixed message and a typed cause for errors.Is / errors.As.
type wrapped struct{ msg, cause error }

func (w *wrapped) Error() string   { return w.msg.Error() }
func (w *wrapped) Unwrap() []error { return []error{w.msg, w.cause} }

// wrap returns msg whose Is-chain also contains cause. cause's text is never shown.
func wrap(msg, cause error) error {
	if cause == nil {
		return msg
	}
	return &wrapped{msg: msg, cause: cause}
}

// netFailure turns a transport error into an error that is safe to show: a cancelled or expired context
// stays as it is (callers rely on it), the fixed errors of idpnet and a policy refusal keep their meaning,
// anything else is "unreachable" without the text of the original error (it contains addresses).
func netFailure(ctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, idpnet.ErrScheme), errors.Is(err, idpnet.ErrBodyTooLarge):
		return wrap(errUnreachable, firstOf(err, idpnet.ErrScheme, idpnet.ErrBodyTooLarge))
	case netpolicy.IsBlocked(err):
		return wrap(errUnreachable, errBlocked)
	}
	return errUnreachable
}

// errBlocked marks a connection that the address policy refused.
var errBlocked = errors.New("the address is not allowed")

func firstOf(err error, targets ...error) error {
	for _, t := range targets {
		if errors.Is(err, t) {
			return t
		}
	}
	return nil
}

// statusOK reports a 2xx answer.
func statusOK(resp *http.Response) bool { return resp.StatusCode >= 200 && resp.StatusCode < 300 }
