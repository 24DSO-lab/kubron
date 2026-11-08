// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/credential"
)

const (
	webOIDCCallbackOK   = "<!doctype html><html lang=\"en\"><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width\"><title>Authentication complete</title><main><h1>Authentication complete</h1><p>You can return to Kubron.</p></main></html>"
	webOIDCCallbackFail = "<!doctype html><html lang=\"en\"><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width\"><title>Authentication not completed</title><main><h1>Authentication not completed</h1><p>Return to Kubron and try signing in again.</p></main></html>"
)

// webOIDCCallbackHandler receives the IdP's top-level GET redirect. SameSite=Lax session cookies are sent
// for this navigation; unlike RPC it intentionally does not require a CSRF header. The OAuth state is the
// broker-generated challenge ID and is checked against both the authenticated user and this session.
func webOIDCCallbackHandler(g *Guard, complete func(context.Context, any, string, string) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		fail := func(status int) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(webOIDCCallbackFail))
		}
		if r.Method != http.MethodGet || len(r.URL.RawQuery) > 8<<10 {
			fail(http.StatusBadRequest)
			return
		}
		q := r.URL.Query()
		states := q["state"]
		if len(states) != 1 || strings.TrimSpace(states[0]) == "" {
			fail(http.StatusBadRequest)
			return
		}
		sess, err := g.Sessions.Lookup(g.token(r))
		if errors.Is(err, ErrSessionStoreUnavailable) {
			w.Header().Set("Retry-After", "5")
			fail(http.StatusServiceUnavailable)
			return
		}
		if err != nil || sess.View == nil {
			fail(http.StatusBadRequest)
			return
		}

		scheme := "http"
		if g.secure(r) {
			scheme = "https"
		}
		redirectURL := fmt.Sprintf("%s://%s%s", scheme, r.Host, r.URL.RequestURI())
		if err := complete(r.Context(), sess.View, states[0], redirectURL); err != nil {
			var limited retryAfterError
			if errors.As(err, &limited) {
				w.Header().Set("Retry-After", fmt.Sprint(limited.RetryAfterSeconds()))
				fail(http.StatusTooManyRequests)
				return
			}
			if callbackRejected(err) {
				fail(http.StatusBadRequest)
				return
			}
			w.Header().Set("Retry-After", "5")
			fail(http.StatusServiceUnavailable)
			return
		}
		g.Sessions.Activity(sess.ID)
		if g.ActiveCluster != nil {
			if clusterID := g.ActiveCluster(sess.View); clusterID != sess.ActiveCluster {
				g.Sessions.SetActiveCluster(sess.ID, clusterID)
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(webOIDCCallbackOK))
	})
}

func callbackRejected(err error) bool {
	return errors.Is(err, credential.ErrChallengeExpired) ||
		errors.Is(err, credential.ErrAnswerRejected) ||
		errors.Is(err, credential.ErrInvalidAnswer) ||
		errors.Is(err, credential.ErrInvalidGrant) ||
		errors.Is(err, credential.ErrUnsupported) ||
		errors.Is(err, auth.ErrNotAuthenticated) ||
		errors.Is(err, auth.ErrForbidden) ||
		errors.Is(err, auth.ErrClusterNotFound)
}
