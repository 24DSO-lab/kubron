// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/24DSO-labs/kubron/internal/pgstore"
)

type fakeLegacy struct {
	res      pgstore.LegacyMigration
	migErr   error
	count    int
	countErr error
}

func (f fakeLegacy) MigrateLegacyOIDC(context.Context) (pgstore.LegacyMigration, error) {
	return f.res, f.migErr
}
func (f fakeLegacy) CountLegacyCredentials(context.Context) (int, error) { return f.count, f.countErr }

func runCutover(t *testing.T, f fakeLegacy) string {
	t.Helper()
	var buf bytes.Buffer
	migrateLegacyCredentials(context.Background(), f, slog.New(slog.NewTextHandler(&buf, nil)))
	return buf.String()
}

func TestMigrateLegacyCredentials(t *testing.T) {
	out := runCutover(t, fakeLegacy{res: pgstore.LegacyMigration{Copied: 2}, count: 2})
	if !strings.Contains(out, "copied") || !strings.Contains(out, "legacy-v1") {
		t.Errorf("ожидались сообщение о копировании и предупреждение о legacy: %s", out)
	}
	if out := runCutover(t, fakeLegacy{}); strings.Contains(out, "legacy-v1") {
		t.Errorf("без legacy-записей предупреждения быть не должно: %s", out)
	}
	secret := "TOPSECRET-VALUE"
	out = runCutover(t, fakeLegacy{migErr: errors.New("boom " + secret)})
	if !strings.Contains(out, "retried") {
		t.Errorf("ошибка копирования должна логироваться: %s", out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("текст ошибки попал в журнал: %s", out)
	}
}
