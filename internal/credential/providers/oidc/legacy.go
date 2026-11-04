// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// idTokenLeeway is how long a legacy ID token must stay valid to be used as it is.
const idTokenLeeway = 30 * time.Second

// startLegacy handles the grant legacy-refresh: a kubeconfig with the old `auth-provider: oidc` block carries
// the ID token and the refresh token that kubectl obtained. They are used once, in this order:
//
//  1. the ID token, when it is verified and not about to expire;
//  2. the refresh token (the rotated one is saved, see Result.Save);
//  3. when neither works, the interactive flow of the mode, as for authcode.
//
// The kubeconfig is not rewritten: what the provider learns (a rotated refresh token) goes only to Save, and
// the next Begin takes Saved before the old token of the kubeconfig. This function is reached only when
// nothing saved was usable.
func (s *session) startLegacy(ctx context.Context, req credential.Request) (credential.Result, error) {
	if s.cfg.idToken != "" || s.cfg.refreshToken != "" {
		d, err := s.discover(ctx)
		if err != nil {
			return credential.Result{}, err
		}
		if s.cfg.idToken != "" {
			if idt, err := s.verifier(ctx, d).Verify(ctx, s.cfg.idToken); err == nil && idt.Expiry.After(s.now().Add(idTokenLeeway)) {
				tok := credential.Token{Value: s.cfg.idToken, Expiry: idt.Expiry, Principal: idt.Subject}
				return credential.Result{Token: &tok, Save: s.saveFor(req, nil, "", "", s.cfg.refreshToken)}, nil
			}
		}
		if s.cfg.refreshToken != "" {
			tr, err := s.refresh(ctx, d, s.cfg.refreshToken)
			switch {
			case err == nil:
				return s.finish(ctx, d, req, nil, tr, "", "", "", s.cfg.refreshToken)
			case !staleRefresh(err):
				return credential.Result{}, err
			}
		}
	}
	if s.env.Desktop {
		return s.startLoopback(ctx)
	}
	return s.startRemote(ctx)
}
