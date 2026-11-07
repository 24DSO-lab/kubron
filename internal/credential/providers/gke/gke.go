// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package gke

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// FieldServiceAccountJSON is the name of the answer field (the UI label is credential.field.serviceAccountJson).
// The field is secret and multi-line; the dialog draws it as a masked text area.
const FieldServiceAccountJSON = "serviceAccountJson"

// Provider implements credential.Provider for credential.KindGKE. It has no state: the optional Google OAuth
// client (NewWithUserOAuth) is configuration that never changes.
type Provider struct {
	user *UserOAuth
	rand io.Reader // nil: crypto/rand; tests replace it

	// The desktop loopback listeners (loopback.go): the one thing that lives outside Pending, in this process.
	loops  loopbackRegistry
	listen func(network, address string) (net.Listener, error) // nil: net.Listen; tests replace it
	ttl    time.Duration                                       // nil: loopbackTTL; tests shorten it
}

// New returns the provider with service account key sign-in only.
func New() *Provider { return &Provider{} }

var _ credential.Provider = (*Provider)(nil)

// Kind implements credential.Provider.
func (*Provider) Kind() credential.Kind { return credential.KindGKE }

// Begin implements credential.Provider. If something is saved, Begin signs in with it: a service account key
// (exchanged for a token) or the refresh token of a Google user. When Google no longer accepts it, or the saved
// data cannot be read, it is erased (Result.Save is an empty non-nil slice) and the user is asked again. A failure
// that is not about the saved sign-in (Google is unreachable) is returned as an error and the saved data stays.
//
// A new sign-in asks for a Google account when an OAuth client is configured and for a service account key
// otherwise. A saved key that stopped working is asked for again as a key.
//
// Descriptor is not read: the machine's own credentials ("use-adc") are never used.
func (p *Provider) Begin(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	if len(req.Saved) == 0 {
		return p.fresh(env)
	}
	var (
		res credential.Result
		err error
	)
	switch savedKind(req.Saved) {
	case "sa":
		k, kerr := parseKey(req.Saved)
		if kerr == nil {
			var tok credential.Token
			if tok, err = exchange(ctx, env, k); err == nil {
				return credential.Result{Token: &tok}, nil
			}
			if !errors.Is(err, credential.ErrAnswerRejected) {
				return credential.Result{}, err
			}
		}
		res = credential.Result{Challenge: challenge()}
	case "user":
		sv, ok := parseSavedUser(req.Saved)
		if p.user == nil {
			res = credential.Result{Challenge: challenge()} // the client is gone from the configuration
		} else if !ok || sv.ClientID != p.user.ClientID {
			if res, err = p.fresh(env); err != nil {
				return credential.Result{}, err
			}
		} else {
			if res, err = p.refresh(ctx, env, sv); err == nil {
				return res, nil
			}
			if !errors.Is(err, credential.ErrAnswerRejected) {
				return credential.Result{}, err
			}
			if res, err = p.fresh(env); err != nil {
				return credential.Result{}, err
			}
		}
	default:
		if res, err = p.fresh(env); err != nil {
			return credential.Result{}, err
		}
	}
	res.Save = []byte{}
	return res, nil
}

// fresh starts a sign-in that has nothing saved.
func (p *Provider) fresh(env credential.Env) (credential.Result, error) {
	if p.user != nil {
		return p.startUser(env)
	}
	return credential.Result{Challenge: challenge()}, nil
}

// Continue implements credential.Provider. The answer of a secret-fields challenge holds the key file in
// FieldServiceAccountJSON; on success the key is saved (without token_uri and the other fields). The answer of a
// browser-callback or paste-redirect challenge is the address that Google redirected the browser to.
func (p *Provider) Continue(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	switch req.Challenge.Kind {
	case credential.ChallengeSecretFields:
	case credential.ChallengeBrowserCallback, credential.ChallengeBrowserLoopback, credential.ChallengePasteRedirect:
		if p.user == nil {
			return credential.Result{}, credential.ErrUnsupported
		}
		return p.continueUser(ctx, env, req)
	default:
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
