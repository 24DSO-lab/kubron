// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package gke

import (
	"context"
	"errors"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// FieldServiceAccountJSON is the name of the answer field (the UI label is credential.field.serviceAccountJson).
// The field is secret and multi-line; the dialog draws it as a masked text area.
const FieldServiceAccountJSON = "serviceAccountJson"

// Provider implements credential.Provider for credential.KindGKE. It has no state.
type Provider struct{}

// New returns the provider.
func New() *Provider { return &Provider{} }

var _ credential.Provider = (*Provider)(nil)

// Kind implements credential.Provider.
func (*Provider) Kind() credential.Kind { return credential.KindGKE }

// Begin implements credential.Provider. If a key is saved, Begin signs in with it. When Google no longer accepts
// the saved key, or the saved data cannot be read, it is erased (Result.Save is an empty non-nil slice) and the
// user is asked for a key again. A failure that is not about the key (Google is unreachable) is returned as an
// error and the saved key stays.
//
// Descriptor is not read: the machine's own credentials ("use-adc") are never used.
func (*Provider) Begin(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	if len(req.Saved) == 0 {
		return credential.Result{Challenge: challenge()}, nil
	}
	k, err := parseKey(req.Saved)
	if err == nil {
		var tok credential.Token
		tok, err = exchange(ctx, env, k)
		if err == nil {
			return credential.Result{Token: &tok}, nil
		}
		if !errors.Is(err, credential.ErrAnswerRejected) {
			return credential.Result{}, err
		}
	}
	return credential.Result{Challenge: challenge(), Save: []byte{}}, nil
}

// Continue implements credential.Provider. The answer holds the key file in FieldServiceAccountJSON. On success
// the key is saved (without token_uri and the other fields) so that later sign-ins need no question.
func (*Provider) Continue(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	if req.Challenge.Kind != credential.ChallengeSecretFields {
		return credential.Result{}, credential.ErrUnsupported
	}
	k, err := parseKey([]byte(req.Answer[FieldServiceAccountJSON]))
	if err != nil {
		return credential.Result{}, err
	}
	tok, err := exchange(ctx, env, k)
	if err != nil {
		return credential.Result{}, err
	}
	return credential.Result{Token: &tok, Save: k.normalized()}, nil
}

func challenge() *credential.Challenge {
	return &credential.Challenge{
		Kind:   credential.ChallengeSecretFields,
		Fields: []credential.Field{{Name: FieldServiceAccountJSON, Secret: true}},
	}
}
