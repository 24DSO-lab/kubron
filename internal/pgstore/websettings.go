// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

const settingsKeyWebOIDC = "web-oidc"

type webOIDCSettings struct {
	ClientID  string `json:"clientId"`
	IssuerURL string `json:"issuerUrl,omitempty"`
}

// WebOIDC is the administrator-configured public client of the web sign-in and the issuer it is registered at.
// A row of the first form has no issuer: IssuerURL is then empty and the client is not applied.
type WebOIDC struct {
	ClientID  string
	IssuerURL string
}

// WebOIDC reads the web sign-in client shared by all replicas.
func (s *Store) WebOIDC() (WebOIDC, error) {
	if err := s.ready(); err != nil {
		return WebOIDC{}, err
	}
	ctx, cancel := opCtx()
	defer cancel()
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, settingsKeyWebOIDC).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return WebOIDC{}, nil
	}
	if err != nil {
		return WebOIDC{}, err
	}
	var cfg webOIDCSettings
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return WebOIDC{}, err
	}
	return WebOIDC(cfg), nil
}

// SetWebOIDC saves the web sign-in client; empty values remove the setting.
func (s *Store) SetWebOIDC(v WebOIDC) error {
	if err := s.ready(); err != nil {
		return err
	}
	ctx, cancel := opCtx()
	defer cancel()
	if v.ClientID == "" && v.IssuerURL == "" {
		_, err := s.pool.Exec(ctx, `DELETE FROM settings WHERE key = $1`, settingsKeyWebOIDC)
		return err
	}
	raw, err := json.Marshal(webOIDCSettings(v))
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		settingsKeyWebOIDC, raw)
	return err
}
