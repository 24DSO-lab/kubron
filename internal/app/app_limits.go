// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// AttemptLimiter is a token bucket per key: the same shape as server.RateLimiter, which is what the web server
// passes in, so sign-in attempts are limited by the limiter the server already has.
type AttemptLimiter interface {
	Allow(key string) (ok bool, retryAfter time.Duration)
}

// RateLimitedError is returned when too many answers were sent. The web server maps it to 429 with Retry-After
// (it recognises the RetryAfterSeconds method, so the server does not import this package).
type RateLimitedError struct{ wait time.Duration }

// RetryAfterSeconds is the wait before the next attempt, rounded up.
func (e *RateLimitedError) RetryAfterSeconds() int {
	return max(1, int((e.wait+time.Second-1)/time.Second))
}

// Error goes through fmt.Errorf so that i18n-check requires a template in backend.json.
func (e *RateLimitedError) Error() string {
	return fmt.Errorf("too many sign-in attempts, try again in %d s", e.RetryAfterSeconds()).Error()
}

// SetAnswerLimiters sets the limits of AnswerChallenge: per application user and per identity provider. nil means
// no limit (desktop). A function of the package, not a method of App: it must not appear in the Wails bindings or
// the RPC.
func SetAnswerLimiters(a *App, perUser, perIdP AttemptLimiter) {
	a.limitUser, a.limitIDP = perUser, perIdP
}

// idpKey identifies the identity provider of a sign-in for the limiter: the host of the issuer, or, for kinds
// without one (cloud plugins), the kind and the cluster. The key is used only inside the limiter, never in
// metrics or logs.
func idpKey(d credential.Descriptor, clusterID string) string {
	if u, err := url.Parse(d.Params["issuer-url"]); err == nil && u.Host != "" {
		return "idp:" + strings.ToLower(u.Host)
	}
	return "kind:" + string(d.Kind) + ":" + clusterID
}

// limitAnswer takes one attempt from the user's bucket and from the identity provider's bucket. The user's own
// bucket goes first: one user cannot use up the allowance of everybody else at the same IdP.
func (a *App) limitAnswer(challengeID string) error {
	pc, ok := a.conn.cluster(challengeID)
	if !ok {
		return nil // an unknown challenge fails in advance without reaching any IdP
	}
	if a.limitUser != nil {
		if uid := a.currentUserID(); uid != "" {
			if ok, wait := a.limitUser.Allow("answer:" + uid); !ok {
				return &RateLimitedError{wait: wait}
			}
		}
	}
	if a.limitIDP != nil {
		if ok, wait := a.limitIDP.Allow("answer:" + pc.idp); !ok {
			return &RateLimitedError{wait: wait}
		}
	}
	return nil
}
