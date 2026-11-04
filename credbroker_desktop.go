// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/24DSO-labs/kubron/internal/app"
	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/credential/filestore"
	"github.com/24DSO-labs/kubron/internal/k8s"
	wailsplatform "github.com/24DSO-labs/kubron/internal/platform/wails"
	"github.com/24DSO-labs/kubron/internal/secrets"
)

// setupDesktopBroker builds the sign-in broker of the desktop: the master key is in the system keyring (or, with a
// visible warning, in a 0600 file), the saved sign-ins are in the data directory. If it cannot be built the core
// keeps the old path, and the reason is logged: a failed keyring is never silent.
func setupDesktopBroker(ctx context.Context, a *app.App, platform *wailsplatform.Platform) {
	dir := k8s.GetKubronDir()
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
	fs, err := filestore.New(filepath.Join(dir, "credentials.json"), credential.NewSealer(env))
	if err != nil {
		slog.Error("sign-in: the credentials file cannot be used", "error", err.Error())
		return
	}
	svc, err := app.BuildBroker(app.BrokerOptions{
		Desktop: true, Keyring: sel.Keyring, Store: fs, Challenges: credential.NewMemoryChallengeStore(), DataDir: dir,
		OpenURL: func(u string) error { platform.OpenURL(u); return nil },
	})
	if err != nil {
		slog.Error("sign-in: the broker was not built", "error", err.Error())
		return
	}
	app.SetBroker(a, svc)
}
