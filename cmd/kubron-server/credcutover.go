// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/24DSO-labs/kubron/internal/logging"
	"github.com/24DSO-labs/kubron/internal/pgstore"
)

// legacyCredentialStore — часть pgstore.Store, нужная для переноса данных входа (ADR-0018, A.4).
type legacyCredentialStore interface {
	MigrateLegacyOIDC(ctx context.Context) (pgstore.LegacyMigration, error)
	CountLegacyCredentials(ctx context.Context) (int, error)
}

// migrateLegacyCredentials копирует секреты oidc-password из clusters.secret_enc в cluster_credentials (формат
// legacy-v1) и предупреждает, пока такие записи остаются. Копирование идемпотентно, его безопасно запускает
// каждая реплика при старте. Ошибка не мешает запуску: данные в clusters.secret_enc остаются рабочими, а
// повтор произойдёт при следующем старте; поэтому она только логируется (без значений секретов).
func migrateLegacyCredentials(ctx context.Context, st legacyCredentialStore, log *slog.Logger) {
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	res, err := st.MigrateLegacyOIDC(cctx)
	if err != nil {
		log.Warn("credentials: the copy of oidc-password secrets failed; it will be retried at the next start", "error_type", logging.ErrorKind(err))
		return
	}
	if res.Copied > 0 || res.Updated > 0 {
		log.Info("credentials: oidc-password secrets copied", "copied", res.Copied, "updated", res.Updated, "unchanged", res.Skipped)
	}
	n, err := st.CountLegacyCredentials(cctx)
	if err != nil {
		log.Warn("credentials: the legacy records were not counted", "error_type", logging.ErrorKind(err))
		return
	}
	if n > 0 {
		log.Warn("credentials: records in the legacy-v1 format remain; run `kubron-server rekey` to convert them to envelope-v1", "count", n)
	}
}
