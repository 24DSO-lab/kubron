// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/sso"
)

const (
	ssoFlowCookie       = "kubron_sso"
	secureSSOFlowCookie = "__Host-kubron_sso"
	ssoCallbackPath     = "/auth/sso/callback"
	maxSSOQuery         = 8 << 10
)

type ssoProvider interface {
	Begin(string) (string, sso.FlowState, error)
	Complete(context.Context, sso.FlowState, url.Values) (sso.Identity, error)
}

type ssoLoginFunc func(context.Context, any, sso.Identity) (*auth.UserInfo, error)

type ssoHandlerConfig struct {
	provider           ssoProvider
	flowKey            []byte
	login              ssoLoginFunc
	publicURL          string
	localLoginDisabled bool
	label              string
}

// WithSSO registers browser OIDC sign-in without coupling the server package to internal/app.
func WithSSO(provider ssoProvider, flowKey []byte, publicURL string, login ssoLoginFunc, localLoginDisabled bool, label string) HandlerOption {
	return func(o *handlerOptions) {
		if provider == nil || login == nil {
			return
		}
		o.sso = &ssoHandlerConfig{
			provider: provider, flowKey: append([]byte(nil), flowKey...), publicURL: strings.TrimRight(publicURL, "/"),
			login: login, localLoginDisabled: localLoginDisabled, label: strings.TrimSpace(label),
		}
	}
}

func ssoLoginHandler(g *Guard, cfg *ssoHandlerConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !g.limitAnonymous(w, r) {
			return
		}
		redirectURL := cfg.publicURL + ssoCallbackPath
		authURL, flow, err := cfg.provider.Begin(redirectURL)
		if err != nil {
			slog.WarnContext(r.Context(), "sso: could not begin sign-in", "error", err)
			http.Redirect(w, r, "/?sso_error=unavailable", http.StatusFound)
			return
		}
		returns := r.URL.Query()["return"]
		if len(returns) == 1 {
			flow.ReturnTo = safeSSOReturn(returns[0])
		}
		sealed, err := flow.Seal(cfg.flowKey)
		if err != nil {
			slog.WarnContext(r.Context(), "sso: could not protect sign-in state", "error", err)
			http.Redirect(w, r, "/?sso_error=unavailable", http.StatusFound)
			return
		}
		secure := g.secure(r)
		name := ssoFlowCookie
		if secure {
			name = secureSSOFlowCookie
		}
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: sealed, Path: "/", HttpOnly: true, Secure: secure,
			SameSite: http.SameSiteLaxMode, MaxAge: int((10 * time.Minute).Seconds()),
		})
		http.Redirect(w, r, authURL, http.StatusFound)
	})
}

func ssoCallbackHandler(g *Guard, cfg *ssoHandlerConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		clearSSOFlowCookie(w, r, g)

		fail := func(code string) {
			http.Redirect(w, r, "/?sso_error="+url.QueryEscape(code), http.StatusFound)
		}
		if r.Method != http.MethodGet || len(r.URL.RawQuery) > maxSSOQuery {
			fail("denied")
			return
		}
		q := r.URL.Query()
		states, codes, providerErrors := q["state"], q["code"], q["error"]
		validCode := len(codes) == 1 && strings.TrimSpace(codes[0]) != ""
		validProviderError := len(providerErrors) == 1 && strings.TrimSpace(providerErrors[0]) != ""
		if len(states) != 1 || strings.TrimSpace(states[0]) == "" || validCode == validProviderError {
			fail("denied")
			return
		}
		cookieName := ssoFlowCookie
		if g.secure(r) {
			cookieName = secureSSOFlowCookie
		}
		cookie, err := r.Cookie(cookieName)
		if err != nil || cookie.Value == "" {
			fail("expired")
			return
		}
		flow, err := sso.Open(cfg.flowKey, cookie.Value)
		if err != nil {
			fail(ssoFailureCode(err))
			return
		}
		identity, err := cfg.provider.Complete(r.Context(), flow, q)
		if err != nil {
			slog.WarnContext(r.Context(), "sso: identity provider callback failed", "error", err)
			fail(ssoFailureCode(err))
			return
		}
		if g.NewView == nil {
			fail("unavailable")
			return
		}
		view := g.NewView(g.clientIP(r))
		if view == nil {
			fail("unavailable")
			return
		}
		user, err := cfg.login(r.Context(), view, identity)
		if err != nil || user == nil {
			g.dropView(view)
			if err != nil {
				slog.WarnContext(r.Context(), "sso: local sign-in failed", "error", err)
			}
			fail(ssoFailureCode(err))
			return
		}
		token, sess, err := g.Sessions.Open(user.ID, view, g.clientIP(r))
		if err != nil {
			g.dropView(view)
			if errors.Is(err, ErrSessionStoreUnavailable) {
				w.Header().Set("Retry-After", "5")
				writeUnavailable(w)
				return
			}
			slog.ErrorContext(r.Context(), "sso: web session could not be stored", "error", err)
			w.Header().Set("Retry-After", "5")
			writeUnavailable(w)
			return
		}
		if g.Bind != nil {
			g.Bind(view, sess.ID)
		}
		g.setCookie(w, r, token, time.Until(sess.Expires))
		destination := safeSSOReturn(flow.ReturnTo)
		if destination == "" {
			destination = "/"
		}
		http.Redirect(w, r, destination, http.StatusFound)
	})
}

func safeSSOReturn(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") ||
		strings.Contains(raw, "\\") || strings.ContainsAny(raw, "\r\n") {
		return ""
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || strings.HasPrefix(parsed.Path, "//") ||
		strings.Contains(parsed.Path, "\\") || strings.ContainsAny(parsed.Path, "\r\n") {
		return ""
	}
	return raw
}

func clearSSOFlowCookie(w http.ResponseWriter, r *http.Request, g *Guard) {
	secure := g.secure(r)
	name := ssoFlowCookie
	if secure {
		name = secureSSOFlowCookie
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

func ssoFailureCode(err error) string {
	switch {
	case errors.Is(err, sso.ErrFlowExpired):
		return "expired"
	case errors.Is(err, sso.ErrDisabled), errors.Is(err, auth.ErrAccountDisabled):
		return "disabled"
	case errors.Is(err, auth.ErrExternalNotProvisioned), errors.Is(err, auth.ErrUserExists),
		errors.Is(err, auth.ErrSetupRequired), errors.Is(err, sso.ErrNoUsername):
		return "not_provisioned"
	case errors.Is(err, sso.ErrFlowInvalid), errors.Is(err, sso.ErrStateMismatch),
		errors.Is(err, sso.ErrIssuerMismatch), errors.Is(err, sso.ErrIDToken),
		errors.Is(err, sso.ErrAuthorization), errors.Is(err, sso.ErrMissingCode):
		return "denied"
	default:
		return "unavailable"
	}
}
