// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"log/slog"

	"github.com/24DSO-lab/kubron/internal/credential"
	"github.com/24DSO-lab/kubron/internal/credential/providers/aks"
	"github.com/24DSO-lab/kubron/internal/credential/providers/eks"
	"github.com/24DSO-lab/kubron/internal/credential/providers/execplugin"
	"github.com/24DSO-lab/kubron/internal/credential/providers/gke"
	"github.com/24DSO-lab/kubron/internal/credential/providers/manual"
	"github.com/24DSO-lab/kubron/internal/credential/providers/oidc"
	"github.com/24DSO-lab/kubron/internal/idpnet"
	"github.com/24DSO-lab/kubron/internal/secretbox"
	"github.com/24DSO-lab/kubron/internal/secrets"
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
	// GKEOAuth is the Google OAuth client for the sign-in of a Google user to GKE (optional). Without it GKE
	// is signed in with a service account key only. The client secret stays in this value: it is not put into
	// a Descriptor, an event or a log.
	GKEOAuth *gke.UserOAuth
	Logger   *slog.Logger
}

// ErrBrokerOptions means that BuildBroker was given an incomplete configuration.
var ErrBrokerOptions = errors.New("the sign-in broker is not configured completely")

// BuildBroker assembles the broker (a *credential.Service; the caller runs Sweep periodically): the oidc, AKS, EKS and GKE providers, the KindExec provider of the mode (execplugin on the
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
	gkeProvider := gke.New()
	if o.GKEOAuth != nil {
		if gkeProvider, err = gke.NewWithUserOAuth(*o.GKEOAuth); err != nil {
			return nil, ErrBrokerOptions
		}
	}
	for _, p := range []credential.Provider{oidc.New(), aks.New(), eks.New(), gkeProvider, execProvider} {
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
