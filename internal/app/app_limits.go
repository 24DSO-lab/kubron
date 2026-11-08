// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"github.com/24DSO-lab/kubron/internal/signin"
)

// AttemptLimiter is a token bucket per key (see signin.AttemptLimiter): the web server passes its own limiter.
type AttemptLimiter = signin.AttemptLimiter

// RateLimitedError is returned when too many answers were sent (see signin.RateLimitedError). The web server maps it
// to 429 with Retry-After.
type RateLimitedError = signin.RateLimitedError

// SetAnswerLimiters sets the limits of AnswerChallenge: per application user and per identity provider. nil means
// no limit (desktop). A function of the package, not a method of App: it must not appear in the Wails bindings or
// the RPC.
func SetAnswerLimiters(a *App, perUser, perIdP AttemptLimiter) {
	a.limitUser, a.limitIDP = perUser, perIdP
}

// limitAnswer takes one attempt from the user's bucket and from the identity provider's bucket. The user's own
// bucket goes first: one user cannot use up the allowance of everybody else at the same IdP.
func (a *App) limitAnswer(challengeID string) error {
	pc, ok := a.conn.Lookup(challengeID)
	if !ok {
		return nil // an unknown challenge fails in advance without reaching any IdP
	}
	return a.limitCallback(pc.IdP)
}

// limitCallback takes one request from the application-user and IdP buckets. It is shared by RPC answers
// and the browser callback endpoint; callback routing does not depend on process-local challenge bookkeeping.
func (a *App) limitCallback(idp string) error {
	return signin.Limiter{User: a.limitUser, IdP: a.limitIDP}.Take(a.currentUserID, idp)
}
