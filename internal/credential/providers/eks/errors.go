// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package eks

import (
	"context"
	"errors"

	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/idpnet"
	"github.com/24DSO-labs/kubron/internal/netpolicy"
)

// Errors of this provider. The texts are English and fixed: nothing that came from the user, the kubeconfig or AWS
// (a key, a token, a role name, a response body) is ever part of them. They are registered in backend.json
// (credEKS*). Contract errors (credential.ErrAnswerRejected and the others) are attached with providerError.
var (
	errBadRegion       = errors.New("the AWS region is not valid")
	errRegionRequired  = errors.New("the AWS region is required")
	errClusterRequired = errors.New("the EKS cluster name is required")
	errBadCluster      = errors.New("the EKS cluster name is not valid")
	errBadRoleARN      = errors.New("the IAM role ARN is not valid for this region")
	errPartition       = errors.New("this AWS partition is not supported")
	errKeyFormat       = errors.New("the access key ID or secret access key is not valid")
	errSessionToken    = errors.New("the session token is missing or not valid")
	errMethod          = errors.New("the sign-in method must be keys, session or sso")
	errSSOFields       = errors.New("the IAM Identity Center details are not valid")
	errKeysRejected    = errors.New("AWS rejected the access keys")
	errRoleDenied      = errors.New("AWS does not allow these credentials to assume the role")
	errAWSResponse     = errors.New("AWS returned an unexpected response")
	errNoHTTP          = errors.New("no HTTP client was provided for AWS requests")
	errSign            = errors.New("the token could not be signed")
	errSSORequest      = errors.New("AWS IAM Identity Center rejected the request")
	errSSODenied       = errors.New("the IAM Identity Center sign-in was denied")
	errSSORole         = errors.New("the account or role is not available to this user in IAM Identity Center")
	errState           = errors.New("the sign-in state is invalid")
	errBlocked         = errors.New("the address is not allowed")

	errUnreachable = &unavailable{"AWS is unreachable"}
	errClockSkew   = &unavailable{"the clock of this server differs too much from the AWS clock"}
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

// invalid marks an answer that cannot be used at all (an empty field, a value of the wrong shape). It does not
// use up an attempt and the dialog stays open.
func invalid(detail error) error {
	return &providerError{kind: credential.ErrInvalidAnswer, detail: detail}
}

// rejected marks an answer that AWS refused (wrong keys, no right to assume the role). The user may try again.
func rejected(detail error) error {
	return &providerError{kind: credential.ErrAnswerRejected, detail: detail}
}

// unsupported marks a sign-in that this provider cannot do; the Broker falls back to the exec provider.
func unsupported(detail error) error {
	return &providerError{kind: credential.ErrUnsupported, detail: detail}
}

// staleGrant marks saved data or a pending sign-in that does not work any more (the user signs in again).
func staleGrant(detail error) error {
	return &providerError{kind: credential.ErrInvalidGrant, detail: detail}
}

// asAnswer turns the "AWS refused these credentials" errors into a wrong answer the user may retry.
func asAnswer(err error) error {
	if errors.Is(err, errKeysRejected) || errors.Is(err, errRoleDenied) {
		return rejected(err)
	}
	return err
}

// unavailable is an error that means "AWS cannot be used right now": it matches credential.ErrServiceUnavailable,
// so that the application does not show it as wrong credentials.
type unavailable struct{ text string }

func (e *unavailable) Error() string        { return e.text }
func (e *unavailable) Is(target error) bool { return target == credential.ErrServiceUnavailable }

// wrapped is an error with a fixed message and a typed cause for errors.Is.
type wrapped struct{ msg, cause error }

func (w *wrapped) Error() string   { return w.msg.Error() }
func (w *wrapped) Unwrap() []error { return []error{w.msg, w.cause} }

// wrap returns msg whose Is-chain also contains cause. The text of cause is never shown.
func wrap(msg, cause error) error {
	if cause == nil {
		return msg
	}
	return &wrapped{msg: msg, cause: cause}
}

// netFailure turns a transport error into an error that is safe to show: a cancelled or expired context stays
// as it is (callers rely on it), a refusal of the address policy keeps its meaning, anything else is
// "unreachable" without the text of the original error (it contains addresses).
func netFailure(ctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, idpnet.ErrBodyTooLarge):
		return errAWSResponse
	case errors.Is(err, idpnet.ErrScheme), netpolicy.IsBlocked(err):
		return wrap(errUnreachable, errBlocked)
	}
	return errUnreachable
}
