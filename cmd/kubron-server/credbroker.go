// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/24DSO-labs/kubron/internal/app"
	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/credential/filestore"
	"github.com/24DSO-labs/kubron/internal/credential/providers/gke"
	"github.com/24DSO-labs/kubron/internal/logging"
	"github.com/24DSO-labs/kubron/internal/pgstore"
	"github.com/24DSO-labs/kubron/internal/secrets"
	"github.com/24DSO-labs/kubron/internal/server"
)

// Limits of AnswerChallenge (ADR-0018, section 7): a user may answer 5 times at once and then once in 5 s; all
// users together may answer 30 times at once and then once a second at one identity provider.
const (
	answerUserRate, answerUserBurst = 0.2, 5
	answerIdPRate, answerIdPBurst   = 1.0, 30
	challengeSweepEvery             = time.Minute
)

// setupBroker builds the sign-in broker and gives it to the core. With PostgreSQL the saved sign-ins and the
// pending requests are shared by the replicas; without it they live in the data directory and in memory.
func setupBroker(ctx context.Context, cfg server.Config, pg *pgstore.Store, core *app.App) error {
	app.SetWebOIDCRedirectURL(core, cfg.PublicURL)
	mode := secrets.ModeServerLocal
	if pg != nil {
		mode = secrets.ModeServerPG
	}
	sel, err := secrets.Resolve(ctx, secrets.Options{
		Mode: mode, EnvKey: cfg.SecretKey, EnvPrevious: cfg.PreviousSecretKeys, DataDir: cfg.DataDir,
	})
	if err != nil {
		return err
	}
	for _, w := range sel.Warnings {
		slog.Warn("secrets: master key warning", "code", w)
	}
	o := app.BrokerOptions{Keyring: sel.Keyring, DataDir: cfg.DataDir}
	if cfg.GKEOAuthClientID != "" {
		o.GKEOAuth = &gke.UserOAuth{ClientID: cfg.GKEOAuthClientID, ClientSecret: cfg.GKEOAuthClientSecret}
		if cfg.PublicURL != "" { // the callback of this server; without it the user pastes the address
			o.GKEOAuth.WebRedirectURL = strings.TrimRight(cfg.PublicURL, "/") + gke.WebCallbackPath
		}
	}
	if pg != nil {
		o.Store, o.Challenges = pg.Credentials(), pg.Challenges()
	} else {
		env, err := secrets.NewEnvelope(sel.Keyring)
		if err != nil {
			return err
		}
		fs, err := filestore.New(filepath.Join(cfg.DataDir, "credentials.json"), credential.NewSealer(env))
		if err != nil {
			return err
		}
		o.Store, o.Challenges = fs, credential.NewMemoryChallengeStore()
	}
	svc, err := app.BuildBroker(o)
	if err != nil {
		return err
	}
	app.SetBroker(core, svc)
	app.SetAnswerLimiters(core, server.NewRateLimiter(answerUserRate, answerUserBurst), server.NewRateLimiter(answerIdPRate, answerIdPBurst))
	go sweepChallenges(ctx, svc)
	return nil
}

// sweepChallenges removes expired pending requests.
func sweepChallenges(ctx context.Context, svc *credential.Service) {
	t := time.NewTicker(challengeSweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = svc.Close() // release what the providers hold
			return
		case <-t.C:
			if _, err := svc.Sweep(ctx); err != nil {
				slog.Warn("credentials: expired sign-in requests were not removed", "error_type", logging.ErrorKind(err))
			}
		}
	}
}
