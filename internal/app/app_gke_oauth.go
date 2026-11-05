// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/credential/providers/gke"
)

// SetGKEOAuth gives the App the store of the Google OAuth client and the client the running broker was built with
// (nil if none); called before OnStartup. A function of the package, not a method of App, for the same reason as
// SetBroker. It is a desktop setting: the web server takes the client from its environment.
func SetGKEOAuth(a *App, store *GKEOAuthStore, active *gke.UserOAuth) {
	a.gkeOAuth = store
	a.gkeOAuthActive = ""
	if active != nil {
		a.gkeOAuthActive = active.ClientID
	}
}

// GetGKEOAuth returns the state of the desktop setting. Desktop only (it is in deniedMethods of the server).
func (a *App) GetGKEOAuth() (GKEOAuthStatus, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return GKEOAuthStatus{}, err
	}
	if a.gkeOAuth == nil {
		return GKEOAuthStatus{}, errGKEOAuthUnavailable
	}
	saved, err := a.gkeOAuth.Load()
	if err != nil {
		return GKEOAuthStatus{}, err
	}
	return gkeOAuthStatus(saved, a.gkeOAuthActive), nil
}

// SaveGKEOAuth saves the client. An empty secret keeps the saved one for the same client id.
func (a *App) SaveGKEOAuth(clientID, clientSecret string) error {
	if err := a.authz(auth.PermRead); err != nil {
		return err
	}
	if a.gkeOAuth == nil {
		return errGKEOAuthUnavailable
	}
	return a.gkeOAuth.Save(clientID, clientSecret)
}

// ClearGKEOAuth removes the saved client.
func (a *App) ClearGKEOAuth() error {
	if err := a.authz(auth.PermRead); err != nil {
		return err
	}
	if a.gkeOAuth == nil {
		return errGKEOAuthUnavailable
	}
	return a.gkeOAuth.Clear()
}
