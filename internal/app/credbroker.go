// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"log/slog"

	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/credential/providers/aks"
	"github.com/24DSO-labs/kubron/internal/credential/providers/execplugin"
	"github.com/24DSO-labs/kubron/internal/credential/providers/manual"
	"github.com/24DSO-labs/kubron/internal/credential/providers/oidc"
	"github.com/24DSO-labs/kubron/internal/idpnet"
	"github.com/24DSO-labs/kubron/internal/secretbox"
	"github.com/24DSO-labs/kubron/internal/secrets"
)

// BrokerOptions is what the shell supplies to build the sign-in broker (ADR-0018).
type BrokerOptions struct {
	Desktop bool
	// Keyring is the master key ring from secrets.Resolve.
	Keyring *secretbox.Keyring
	// Store keeps the saved sign-ins; Challenges keeps the pending requests (memory is enough with one replica).
	Store      credential.CredentialStore
	Challenges credential.ChallengeStore
	// OpenURL opens the browser (desktop only).
	OpenURL func(url string) error
	DataDir string
	Logger  *slog.Logger
}

// ErrBrokerOptions means that BuildBroker was given an incomplete configuration.
var ErrBrokerOptions = errors.New("the sign-in broker is not configured completely")

// BuildBroker assembles the broker (a *credential.Service; the caller runs Sweep periodically): the oidc provider, the KindExec provider of the mode (execplugin on the
// desktop, manual in the web), and the single HTTP client for identity providers with the SSRF policy of the mode.
// It is a function of the package, not a method of App: it must not appear in the Wails bindings or the RPC.
func BuildBroker(o BrokerOptions) (*credential.Service, error) {
	if o.Keyring == nil || o.Store == nil || o.Challenges == nil {
		return nil, ErrBrokerOptions
	}
	env, err := secrets.NewEnvelope(o.Keyring)
	if err != nil {
		return nil, err
	}
	httpc, err := idpnet.NewClient(idpnet.Options{Desktop: o.Desktop})
	if err != nil {
		return nil, err
	}
	reg := credential.NewRegistry(o.Desktop)
	execProvider := credential.Provider(manual.New())
	if o.Desktop {
		execProvider = execplugin.New()
	}
	for _, p := range []credential.Provider{oidc.New(), aks.New(), execProvider} {
		if err := reg.Register(p); err != nil {
			return nil, err
		}
	}
	cenv := credential.Env{HTTP: httpc, Desktop: o.Desktop, DataDir: o.DataDir}
	if o.Desktop {
		cenv.OpenURL = o.OpenURL
	}
	return credential.NewService(credential.Config{
		Registry: reg, Store: o.Store, Challenges: o.Challenges, Envelope: env, Env: cenv, Logger: o.Logger,
	})
}
