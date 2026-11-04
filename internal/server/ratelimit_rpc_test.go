// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"
	"testing"
)

// limitedErr is shaped like app.RateLimitedError: the server knows only the method.
type limitedErr struct{ secs int }

func (e *limitedErr) Error() string          { return "too many sign-in attempts" }
func (e *limitedErr) RetryAfterSeconds() int { return e.secs }

type limitedTarget struct{}

func (limitedTarget) Answer() error { return &limitedErr{secs: 7} }

// A method that reports "too many attempts" is answered with 429 and Retry-After, and the status is what the
// metrics see.
func TestRateLimitedMethodIsAnswered429WithRetryAfter(t *testing.T) {
	if status, code := MapError(&limitedErr{secs: 3}); status != http.StatusTooManyRequests || code != "rate_limited" {
		t.Fatalf("MapError: %d %s", status, code)
	}
	d, err := NewDispatcher(limitedTarget{}, []string{"Answer"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec, rep := post(WithRequestID(NewHandler(d, nil, nil)), "/api/rpc/Answer", "[]", nil)
	if rec.Code != http.StatusTooManyRequests || rep.Error == nil || rep.Error.Code != "rate_limited" {
		t.Fatalf("status %d, reply %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Retry-After"); got != "7" {
		t.Fatalf("Retry-After = %q", got)
	}
}
