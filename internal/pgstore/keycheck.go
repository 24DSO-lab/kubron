// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"fmt"
)

// CheckKey проверяет, что настроенная связка ключей читает данные, уже лежащие в БД. Проверяется по одной
// записи на каждый идентификатор ключа среди зашифрованных kubeconfig и секретов подключения кластеров:
// этого достаточно, чтобы поймать пропущенный, перепутанный или повреждённый ключ (в том числе после
// восстановления из резервной копии), и это дёшево, а не полный проход по таблицам.
//
// Пустая БД или БД без зашифрованных данных — не ошибка. Ошибка оборачивает secretbox.ErrUnknownKey
// (в БД есть записи с ключом, которого нет в связке) или secretbox.ErrDecrypt (идентификатор совпал, а
// байты ключа другие). Сам ключ и расшифрованные данные в текст ошибки не попадают.
func (s *Store) CheckKey(ctx context.Context) error {
	for _, c := range []struct {
		what, query string
		aad         func(id string) []byte
	}{
		{"a cluster kubeconfig",
			`SELECT DISTINCT ON (key_id) id, kubeconfig_enc, key_id FROM clusters WHERE kubeconfig_enc IS NOT NULL ORDER BY key_id, id`,
			kubeconfigAAD},
		{"a cluster connection secret",
			`SELECT DISTINCT ON (secret_key_id) id, secret_enc, secret_key_id FROM clusters WHERE secret_enc IS NOT NULL ORDER BY secret_key_id, id`,
			secretAAD},
	} {
		if err := s.checkColumn(ctx, c.what, c.query, c.aad); err != nil {
			return err
		}
	}
	return s.checkBaselinesKey(ctx)
}

func (s *Store) checkColumn(ctx context.Context, what, query string, aad func(string) []byte) error {
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, keyID string
		var ct []byte
		if err := rows.Scan(&id, &ct, &keyID); err != nil {
			return err
		}
		if _, err := s.keys.Open(ct, keyID, aad(id)); err != nil {
			return fmt.Errorf("%s stored with encryption key %q cannot be read with the configured keys: %w", what, keyID, err)
		}
	}
	return rows.Err()
}
