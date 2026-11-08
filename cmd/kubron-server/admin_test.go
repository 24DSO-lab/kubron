// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/pgstore"
	"github.com/24DSO-lab/kubron/internal/secretbox"
	"github.com/jackc/pgx/v5"
)

func key(id string, fill byte) string {
	return id + ":" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}

// kubron-server rekey перешифровывает и секреты Helm. Тест работает в собственной схеме БД (search_path): остальные
// PG-тесты пересоздают public, и общая схема им мешала бы.
func TestRekeyCommandReencryptsHelmSecrets(t *testing.T) {
	base := os.Getenv("KUBRON_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("KUBRON_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	const schema = "rekey_cli_test"
	if _, err := conn.Exec(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE; CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`) })

	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	dbURL := u.String()

	oldKey, newKey := key("2026-01", 1), key("2026-07", 2)

	// Данные под старым ключом.
	keys, err := secretbox.ParseKeyring(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	s, err := pgstore.Open(ctx, dbURL, keys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	team, err := s.Directory().CreateTeam("Red")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Helm().Create(team.ID, pgstore.HelmRepo{Name: "stable", Kind: "http", URL: "https://charts.example", Username: "ci", Secret: "s3cr3t"}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Команда с новым ключом и прежним в KUBRON_SECRET_KEY_PREVIOUS.
	t.Setenv("KUBRON_DATABASE_URL", dbURL)
	t.Setenv("KUBRON_SECRET_KEY", newKey)
	t.Setenv("KUBRON_SECRET_KEY_PREVIOUS", oldKey)
	var out bytes.Buffer
	if err := runAdmin(ctx, "rekey", nil, &out); err != nil {
		t.Fatalf("rekey: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "1 Helm repository secrets") || !strings.Contains(out.String(), `"2026-07"`) {
		t.Errorf("the report must mention the Helm secrets and the new key: %q", out.String())
	}

	// Старый ключ больше не нужен: пароль расшифровывается только новым.
	only, _ := secretbox.ParseKeyring(newKey)
	m, err := pgstore.Open(ctx, dbURL, only)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	list, err := m.Helm().List(team.ID)
	if err != nil || len(list) != 1 || list[0].Secret != "s3cr3t" {
		t.Errorf("after rekey the password must open with the new key alone: %+v %v", list, err)
	}
}
