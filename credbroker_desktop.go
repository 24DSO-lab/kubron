// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/24DSO-lab/kubron/internal/app"
	"github.com/24DSO-lab/kubron/internal/credential"
	"github.com/24DSO-lab/kubron/internal/credential/filestore"
	"github.com/24DSO-lab/kubron/internal/k8s"
	wailsplatform "github.com/24DSO-lab/kubron/internal/platform/wails"
	"github.com/24DSO-lab/kubron/internal/secrets"
)

// setupDesktopBroker builds the sign-in broker of the desktop: the master key is in the system keyring (or, with a
// visible warning, in a 0600 file), the saved sign-ins are in the data directory. If it cannot be built the core
// keeps the old path, and the reason is logged: a failed keyring is never silent.
func setupDesktopBroker(ctx context.Context, a *app.App, platform *wailsplatform.Platform) {
	dir := k8s.GetKubronDir()
	// «Report a problem»: the report goes to the receiver of the project, and one that cannot be delivered waits in this
	// folder for a connection. It needs neither the master key nor the network, so it is set first.
	app.SetBugReportOutbox(a, filepath.Join(dir, "bugreports"))
	sel, err := secrets.Resolve(ctx, secrets.Options{Mode: secrets.ModeDesktop, DataDir: dir})
	if err != nil {
		slog.Error("sign-in: the master key is unavailable; clusters that need a sign-in will not connect", "error", err.Error())
		return
	}
	for _, w := range sel.Warnings {
		slog.Warn("secrets: master key warning", "code", w)
	}
	env, err := secrets.NewEnvelope(sel.Keyring)
	if err != nil {
		slog.Error("sign-in: the encryption is not available", "error", err.Error())
		return
	}
	// Mail settings for «Report a problem»: the password is sealed with the same envelope. Without the master key the
	// button still works from the environment (KUBRON_BUGREPORT_SMTP_*), but nothing can be saved.
	app.SetBugReport(a, app.NewBugReportStore(filepath.Join(dir, "bugreport.json"), env))
	fs, err := filestore.New(filepath.Join(dir, "credentials.json"), credential.NewSealer(env))
	if err != nil {
		slog.Error("sign-in: the credentials file cannot be used", "error", err.Error())
		return
	}
	// The Google OAuth client of the desktop (settings screen): without it GKE signs in with a key only. A client
	// that cannot be read is logged and left out; it never stops the broker.
	gkeStore := app.NewGKEOAuthStore(filepath.Join(dir, "gke-oauth.json"), env)
	gkeClient, err := gkeStore.Load()
	if err != nil {
		slog.Warn("sign-in: the saved Google OAuth client is not used", "error", err.Error())
		gkeClient = nil
	}
	opts := app.BrokerOptions{
		Desktop: true, Keyring: sel.Keyring, Store: fs, Challenges: credential.NewMemoryChallengeStore(), DataDir: dir,
		OpenURL:  func(u string) error { platform.OpenURL(u); return nil },
		GKEOAuth: gkeClient,
	}
	svc, err := app.BuildBroker(opts)
	if err != nil && gkeClient != nil {
		slog.Warn("sign-in: the saved Google OAuth client is not valid and is not used")
		gkeClient, opts.GKEOAuth = nil, nil
		svc, err = app.BuildBroker(opts)
	}
	if err != nil {
		slog.Error("sign-in: the broker was not built", "error", err.Error())
		return
	}
	app.SetBroker(a, svc)
	app.SetGKEOAuth(a, gkeStore, gkeClient)
}
