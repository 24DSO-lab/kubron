// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"strings"

	"github.com/24DSO-labs/kubron/internal/credential"
)

const maxCredentialLen = 1024

// startPassword asks for a user name and a password. A user name and password written in the kubeconfig
// (kubelogin --username/--password) are tried first.
func (s *session) startPassword(ctx context.Context, req credential.Request, sv *saved) (credential.Result, error) {
	if s.cfg.username != "" && s.cfg.password != "" {
		d, err := s.discover(ctx)
		if err != nil {
			return credential.Result{}, err
		}
		tr, err := s.passwordGrant(ctx, d, s.cfg.username, s.cfg.password)
		switch {
		case err == nil:
			return s.finish(ctx, d, req, sv, tr, "", s.cfg.username, s.cfg.password, "")
		case !rejected(err):
			return credential.Result{}, err
		}
	}
	return credential.Result{Challenge: &credential.Challenge{
		Kind:   credential.ChallengeCredentials,
		Fields: []credential.Field{{Name: "username"}, {Name: "password", Secret: true}},
	}}, nil
}

// continueCredentials handles the answer of the credentials challenge. A refused user name or password is
// credential.ErrAnswerRejected (the user may try again); the provider's explanation is not passed on.
func (s *session) continueCredentials(ctx context.Context, req credential.Request, sv *saved) (credential.Result, error) {
	username := strings.TrimSpace(req.Answer["username"])
	password := req.Answer["password"]
	if username == "" || password == "" || len(username) > maxCredentialLen || len(password) > maxCredentialLen {
		return credential.Result{}, credential.ErrAnswerRejected
	}
	d, err := s.discover(ctx)
	if err != nil {
		return credential.Result{}, err
	}
	tr, err := s.passwordGrant(ctx, d, username, password)
	if err != nil {
		if rejected(err) {
			return credential.Result{}, credential.ErrAnswerRejected
		}
		return credential.Result{}, err
	}
	return s.finish(ctx, d, req, sv, tr, "", username, password, "")
}
