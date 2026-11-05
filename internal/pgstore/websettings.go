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
	ClientID string `json:"clientId"`
}

// WebOIDCClientID reads the administrator-configured public client id shared by all replicas.
func (s *Store) WebOIDCClientID() (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	ctx, cancel := opCtx()
	defer cancel()
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, settingsKeyWebOIDC).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var cfg webOIDCSettings
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", err
	}
	return cfg.ClientID, nil
}

// SetWebOIDCClientID updates the public client id; an empty value removes the override.
func (s *Store) SetWebOIDCClientID(clientID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	ctx, cancel := opCtx()
	defer cancel()
	if clientID == "" {
		_, err := s.pool.Exec(ctx, `DELETE FROM settings WHERE key = $1`, settingsKeyWebOIDC)
		return err
	}
	raw, err := json.Marshal(webOIDCSettings{ClientID: clientID})
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		settingsKeyWebOIDC, raw)
	return err
}
