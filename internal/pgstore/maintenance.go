// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// RekeyReport — итог перешифрования.
type RekeyReport struct {
	Clusters int // kubeconfig'и кластеров, перешифрованные текущим ключом
	Tokens   int // токены Argo CD
	Secrets  int // секреты подключения кластеров (пароль oidc-password)
	Helm     int // пароли репозиториев и OCI-реестров Helm

	Credentials       int // записи входа в кластеры (cluster_credentials), перешифрованные или переведённые в envelope-v1
	LegacyCredentials int // записей legacy-v1, оставшихся после перешифрования (должно быть 0)

	IncidentFragments int // фрагменты логов расследований (incident_fragments), перешифрованные текущим ключом
}

// Rekey перешифровывает текущим ключом все секреты, зашифрованные другим ключом (ротация, задача 3.1c).
// Прежние ключи должны быть в связке. Запись обновляется, только если за время операции её не успели
// изменить (условие по key_id и шифротексту), поэтому Rekey безопасно запускать при работающем сервисе.
func (s *Store) Rekey(ctx context.Context) (RekeyReport, error) {
	var rep RekeyReport
	cur := s.keys.CurrentID()

	type row struct {
		id, keyID string
		ct        []byte
	}
	load := func(q string) ([]row, error) {
		rows, err := s.pool.Query(ctx, q, cur)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.ct, &r.keyID); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}

	cl, err := load(`SELECT id, kubeconfig_enc, key_id FROM clusters WHERE kubeconfig_enc IS NOT NULL AND key_id <> $1`)
	if err != nil {
		return rep, err
	}
	for _, r := range cl {
		plain, err := s.keys.Open(r.ct, r.keyID, kubeconfigAAD(r.id))
		if err != nil {
			return rep, err
		}
		ct, keyID, err := s.keys.Seal(plain, kubeconfigAAD(r.id))
		if err != nil {
			return rep, err
		}
		tag, err := s.pool.Exec(ctx, `UPDATE clusters SET kubeconfig_enc=$2, key_id=$3 WHERE id=$1 AND key_id=$4 AND kubeconfig_enc=$5`,
			r.id, ct, keyID, r.keyID, r.ct)
		if err != nil {
			return rep, err
		}
		rep.Clusters += int(tag.RowsAffected())
	}

	sec, err := load(`SELECT id, secret_enc, secret_key_id FROM clusters WHERE secret_enc IS NOT NULL AND secret_key_id <> $1`)
	if err != nil {
		return rep, err
	}
	for _, r := range sec {
		plain, err := s.keys.Open(r.ct, r.keyID, secretAAD(r.id))
		if err != nil {
			return rep, err
		}
		ct, keyID, err := s.keys.Seal(plain, secretAAD(r.id))
		if err != nil {
			return rep, err
		}
		tag, err := s.pool.Exec(ctx, `UPDATE clusters SET secret_enc=$2, secret_key_id=$3 WHERE id=$1 AND secret_key_id=$4 AND secret_enc=$5`,
			r.id, ct, keyID, r.keyID, r.ct)
		if err != nil {
			return rep, err
		}
		rep.Secrets += int(tag.RowsAffected())
	}

	tk, err := load(`SELECT cluster_id, token_enc, key_id FROM argo_tokens WHERE key_id <> $1`)
	if err != nil {
		return rep, err
	}
	for _, r := range tk {
		plain, err := s.keys.Open(r.ct, r.keyID, tokenAAD(r.id))
		if err != nil {
			return rep, err
		}
		ct, keyID, err := s.keys.Seal(plain, tokenAAD(r.id))
		if err != nil {
			return rep, err
		}
		tag, err := s.pool.Exec(ctx, `UPDATE argo_tokens SET token_enc=$2, key_id=$3, updated_at=now() WHERE cluster_id=$1 AND key_id=$4 AND token_enc=$5`,
			r.id, ct, keyID, r.keyID, r.ct)
		if err != nil {
			return rep, err
		}
		rep.Tokens += int(tag.RowsAffected())
	}

	if rep.Helm, err = s.rekeyHelm(ctx, cur); err != nil {
		return rep, err
	}

	cr, err := s.Credentials().RekeyCredentials(ctx)
	rep.Credentials, rep.LegacyCredentials = cr.Changed, cr.LegacyRemaining
	if err != nil {
		return rep, err
	}
	if rep.IncidentFragments, err = s.RekeyIncidentFragments(ctx); err != nil {
		return rep, err
	}
	return rep, nil
}

// Counts — сколько записей уже есть (импорт данных десктопа выполняется только в пустые таблицы).
type Counts struct{ Users, Clusters, AuditEvents int }

// Counts возвращает число пользователей, кластеров и записей аудита.
func (s *Store) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM clusters), (SELECT count(*) FROM audit_events)`).
			Scan(&c.Users, &c.Clusters, &c.AuditEvents)
	})
	return c, err
}
