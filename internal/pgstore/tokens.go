// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/24DSO-labs/kubron/pkg/ext"
)

func tokenAAD(clusterID string) []byte { return []byte("argo-token/" + clusterID) }

// ArgoTokens — ext.TokenStore: токены хранятся только зашифрованными, шифротекст привязан к кластеру.
func (s *Store) ArgoTokens() ext.TokenStore { return tokenStore{s} }

type tokenStore struct{ s *Store }

func (t tokenStore) Put(clusterID, token string) error {
	ct, keyID, err := t.s.keys.Seal([]byte(token), tokenAAD(clusterID))
	if err != nil {
		return err
	}
	ctx, cancel := opCtx()
	defer cancel()
	_, err = t.s.pool.Exec(ctx, `
		INSERT INTO argo_tokens (cluster_id, token_enc, key_id) VALUES ($1,$2,$3)
		ON CONFLICT (cluster_id) DO UPDATE SET token_enc = EXCLUDED.token_enc, key_id = EXCLUDED.key_id, updated_at = now()`,
		clusterID, ct, keyID)
	if pgCode(err) == "23503" { // нет такого кластера
		return fmt.Errorf("%w: %q", ErrClusterNotFound, clusterID)
	}
	return err
}

func (t tokenStore) Get(clusterID string) (string, error) {
	ctx, cancel := opCtx()
	defer cancel()
	var ct []byte
	var keyID string
	err := t.s.pool.QueryRow(ctx, `SELECT token_enc, key_id FROM argo_tokens WHERE cluster_id = $1`, clusterID).Scan(&ct, &keyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ext.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	plain, err := t.s.keys.Open(ct, keyID, tokenAAD(clusterID))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (t tokenStore) Delete(clusterID string) error {
	ctx, cancel := opCtx()
	defer cancel()
	_, err := t.s.pool.Exec(ctx, `DELETE FROM argo_tokens WHERE cluster_id = $1`, clusterID)
	return err
}
