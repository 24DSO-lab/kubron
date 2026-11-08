// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/logging"
	"github.com/24DSO-lab/kubron/internal/pgstore"
)

type keyMismatchError struct{}

func (keyMismatchError) Error() string {
	return "KUBRON_SECRET_KEY does not match the encrypted data in the database " +
		"(pass the key the data was encrypted with and earlier keys in KUBRON_SECRET_KEY_PREVIOUS; " +
		"see docs/backup.md; KUBRON_ALLOW_KEY_MISMATCH=1 starts without access to cluster secrets)"
}

// checkSecretKey проверяет при старте, что KUBRON_SECRET_KEY (и прежние ключи) читают данные в БД. Без
// этого неверный ключ после восстановления из резервной копии или при ошибке в Secret выяснялся бы только
// при первом обращении к кластеру, с ошибкой «failed to decrypt» посреди работы пользователя. Сервер с
// неверным ключом не стартует: при выкладке с RollingUpdate старые реплики продолжают работать.
//
// KUBRON_ALLOW_KEY_MISMATCH=1 разрешает запуск вопреки проверке (например, чтобы выгрузить журнал аудита
// при потерянном ключе): секреты кластеров при этом недоступны.
func checkSecretKey(ctx context.Context, pg *pgstore.Store) error {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err := pg.CheckKey(cctx)
	if err == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("KUBRON_ALLOW_KEY_MISMATCH"))) {
	case "1", "true", "yes", "on":
		slog.Warn("KUBRON_ALLOW_KEY_MISMATCH is set: starting although the key does not read the database; cluster secrets are unavailable", "error_type", logging.ErrorKind(err))
		return nil
	}
	// Не оборачиваем исходную ошибку: драйвер/secretbox может включить в неё значения ключей.
	// Для диагностики достаточно безопасного сообщения и ссылки на инструкцию восстановления.
	return keyMismatchError{}
}
