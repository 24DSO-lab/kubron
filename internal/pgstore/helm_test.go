// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/VadimIl224/kubron/internal/secretbox"
)

func newBlueTeam(t *testing.T, s *Store) string {
	t.Helper()
	blue, err := s.Directory().CreateTeam("Blue")
	if err != nil {
		t.Fatal(err)
	}
	return blue.ID
}

func helmRepo(name, secret string) HelmRepo {
	return HelmRepo{Name: name, Kind: "http", URL: "https://charts.example/" + name, Username: "ci", Secret: secret, CreatedAt: time.Now().UTC()}
}

func TestHelmReposAreEncryptedAndIsolatedByTeam(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	h := s.Helm()
	red, blue := s.teamID, newBlueTeam(t, s)

	if err := h.Create(red, helmRepo("stable", "s3cr3t-pass")); err != nil {
		t.Fatal(err)
	}
	// В БД пароля в открытом виде нет, ключ записан.
	var raw []byte
	var keyID string
	if err := s.pool.QueryRow(context.Background(), `SELECT secret_enc, secret_key_id FROM helm_repositories WHERE team_id=$1 AND name='stable'`, red).Scan(&raw, &keyID); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("s3cr3t-pass")) || keyID != "k1" {
		t.Errorf("the password must be stored encrypted (key %q)", keyID)
	}
	if err := h.Create(red, helmRepo("stable", "x")); !errors.Is(err, ErrHelmExists) {
		t.Errorf("the name is unique within a team: %v", err)
	}
	// То же имя в другой команде — другой репозиторий.
	if err := h.Create(blue, helmRepo("stable", "blue-pass")); err != nil {
		t.Fatal(err)
	}
	oci := HelmRepo{Name: "reg", Kind: "oci", URL: "oci://reg.example/charts", Options: HelmRepoOptions{PlainHTTP: true, Charts: []string{"a", "b"}}}
	if err := h.Create(red, oci); err != nil {
		t.Fatal(err)
	}

	redList, err := h.List(red)
	if err != nil || len(redList) != 2 || redList[0].Name != "reg" || redList[1].Secret != "s3cr3t-pass" {
		t.Fatalf("red: %+v %v", redList, err)
	}
	if !redList[0].Options.PlainHTTP || len(redList[0].Options.Charts) != 2 {
		t.Errorf("OCI options: %+v", redList[0].Options)
	}
	blueList, err := h.List(blue)
	if err != nil || len(blueList) != 1 || blueList[0].Secret != "blue-pass" {
		t.Fatalf("blue sees only its own repositories: %+v %v", blueList, err)
	}

	// Чужой репозиторий «не найден», как и несуществующий.
	if _, err := h.Delete(blue, "reg"); !errors.Is(err, ErrHelmNotFound) {
		t.Errorf("deleting another team's repository: %v", err)
	}
	if err := h.SetCharts(blue, "reg", []string{"x"}); !errors.Is(err, ErrHelmNotFound) {
		t.Errorf("changing another team's registry: %v", err)
	}
	if err := h.SetCharts(red, "reg", []string{"c", "a"}); err != nil {
		t.Fatal(err)
	}
	if l, _ := h.List(red); len(l[0].Options.Charts) != 2 || l[0].Options.Charts[0] != "c" || !l[0].Options.PlainHTTP {
		t.Errorf("SetCharts must keep the other options: %+v", l[0].Options)
	}
	if err := h.SetCharts(red, "stable", []string{"x"}); !errors.Is(err, ErrHelmNotFound) {
		t.Errorf("charts exist only on OCI registries: %v", err)
	}

	del, err := h.Delete(red, "stable")
	if err != nil || del.Name != "stable" || del.Secret != "" {
		t.Errorf("Delete returns the old record without the password: %+v %v", del, err)
	}
	if l, _ := h.List(blue); len(l) != 1 {
		t.Error("deleting in one team must not affect another")
	}
}

// Шифротекст привязан к команде и имени: скопированный в другую строку он не расшифровывается.
func TestHelmSecretIsBoundToTeamAndName(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	h := s.Helm()
	red, blue := s.teamID, newBlueTeam(t, s)
	if err := h.Create(red, helmRepo("stable", "red-pass")); err != nil {
		t.Fatal(err)
	}
	if err := h.Create(blue, helmRepo("stable", "blue-pass")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(context.Background(), `
		UPDATE helm_repositories SET secret_enc = (SELECT secret_enc FROM helm_repositories WHERE team_id=$1 AND name='stable')
		WHERE team_id=$2 AND name='stable'`, red, blue); err != nil {
		t.Fatal(err)
	}
	if _, err := h.List(blue); err == nil {
		t.Error("a ciphertext copied to another team must not decrypt")
	}
}

func TestHelmRekey(t *testing.T) {
	oldK, newK := keyString("2026-01"), keyString("2026-07")
	s := newTestStore(t, oldK)
	red, blue := s.teamID, newBlueTeam(t, s)
	_ = s.Helm().Create(red, helmRepo("a", "pass-a"))
	_ = s.Helm().Create(blue, helmRepo("a", "pass-b"))
	_ = s.Helm().Create(red, HelmRepo{Name: "anon", Kind: "http", URL: "https://x.example"}) // без пароля

	keys, _ := secretbox.ParseKeyring(newK, oldK)
	n, err := Open(context.Background(), os.Getenv("KUBRON_TEST_DATABASE_URL"), keys)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	rep, err := n.Rekey(context.Background())
	if err != nil || rep.Helm != 2 {
		t.Fatalf("Rekey must re-encrypt both passwords: %+v %v", rep, err)
	}
	if again, _ := n.Rekey(context.Background()); again != (RekeyReport{}) {
		t.Errorf("a second Rekey changes nothing: %+v", again)
	}
	// Старый ключ больше не нужен.
	only, _ := secretbox.ParseKeyring(newK)
	m, err := Open(context.Background(), os.Getenv("KUBRON_TEST_DATABASE_URL"), only)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	for team, want := range map[string]string{red: "pass-a", blue: "pass-b"} {
		l, err := m.Helm().List(team)
		if err != nil {
			t.Fatalf("team %s: %v", team, err)
		}
		got := map[string]string{}
		for _, r := range l {
			got[r.Name] = r.Secret
		}
		if got["a"] != want {
			t.Errorf("team %s: the password must survive the re-encryption: %q", team, got["a"])
		}
	}
	var keyID string
	_ = m.pool.QueryRow(context.Background(), `SELECT secret_key_id FROM helm_repositories WHERE name='a' AND team_id=$1`, red).Scan(&keyID)
	if keyID != "2026-07" {
		t.Errorf("the key id must follow the re-encryption: %q", keyID)
	}
}

func opJSON(id string) []byte { return []byte(fmt.Sprintf(`{"id":%q,"op":"install"}`, id)) }

func TestHelmJournalIsolationAndLimit(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	h := s.Helm()
	red, blue := s.teamID, newBlueTeam(t, s)
	base := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < helmJournalMax+5; i++ {
		id := fmt.Sprintf("op-%03d", i)
		if err := h.AddOperation(red, "", id, base.Add(time.Duration(i)*time.Second), opJSON(id)); err != nil {
			t.Fatal(err)
		}
	}
	list, err := h.Operations(red)
	if err != nil || len(list) != helmJournalMax {
		t.Fatalf("the journal keeps the last %d: %d %v", helmJournalMax, len(list), err)
	}
	if !bytes.Contains(list[0], []byte("op-204")) || !bytes.Contains(list[len(list)-1], []byte("op-005")) {
		t.Errorf("newest first, oldest dropped: %s ... %s", list[0], list[len(list)-1])
	}
	if l, _ := h.Operations(blue); len(l) != 0 {
		t.Error("another team must not see the journal")
	}
	if _, err := h.Operation(blue, "op-204"); !errors.Is(err, ErrHelmNotFound) {
		t.Errorf("another team's operation is not found: %v", err)
	}
	if _, err := h.Operation(red, "op-204"); err != nil {
		t.Errorf("own operation: %v", err)
	}
	// Тот же id в другой команде — отдельная запись.
	if err := h.AddOperation(blue, "", "op-204", base, opJSON("op-204")); err != nil {
		t.Fatal(err)
	}
	if err := h.ClearOperations(blue); err != nil {
		t.Fatal(err)
	}
	if l, _ := h.Operations(red); len(l) != helmJournalMax {
		t.Error("clearing one team's journal must not touch another's")
	}
	if err := h.ClearOperations(red); err != nil {
		t.Fatal(err)
	}
	if l, _ := h.Operations(red); len(l) != 0 {
		t.Error("the journal must be empty after Clear")
	}
}

func TestHelmJournalSurvivesClusterDeletion(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	h := s.Helm()
	blue := newBlueTeam(t, s)
	saveCluster(t, s, "prod", "Prod", []byte("kc"))
	now := time.Now().UTC()
	if err := h.AddOperation(s.teamID, "prod", "op-1", now, opJSON("op-1")); err != nil {
		t.Fatal(err)
	}
	// Кластер чужой команды в запись не попадает.
	if err := h.AddOperation(blue, "prod", "op-2", now, opJSON("op-2")); err != nil {
		t.Fatal(err)
	}
	cluster := func(team, id string) *string {
		var c *string
		if err := s.pool.QueryRow(context.Background(), `SELECT cluster_id FROM helm_operations WHERE team_id=$1 AND id=$2`, team, id).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	if c := cluster(s.teamID, "op-1"); c == nil || *c != "prod" {
		t.Errorf("cluster id: %v", c)
	}
	if c := cluster(blue, "op-2"); c != nil {
		t.Errorf("another team's cluster must not be recorded: %v", *c)
	}
	if err := s.Clusters().Delete("prod"); err != nil {
		t.Fatal(err)
	}
	if c := cluster(s.teamID, "op-1"); c != nil {
		t.Error("the journal outlives the cluster: cluster_id becomes empty")
	}
	if _, err := h.Operation(s.teamID, "op-1"); err != nil {
		t.Errorf("the record must stay: %v", err)
	}
}

func TestHelmStateIsDeletedWithTeam(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	blue := newBlueTeam(t, s)
	_ = s.Helm().Create(blue, helmRepo("x", "p"))
	_ = s.Helm().AddOperation(blue, "", "op-1", time.Now(), opJSON("op-1"))
	if err := s.Directory().DeleteTeam(blue); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM helm_repositories) + (SELECT count(*) FROM helm_operations)`).Scan(&n)
	if n != 0 {
		t.Errorf("a deleted team must leave no Helm state: %d rows", n)
	}
}

func TestHelmRepoNameConstraint(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	for _, bad := range []string{"", "UPPER", "../x", "a b"} {
		if err := s.Helm().Create(s.teamID, helmRepo(bad, "")); err == nil {
			t.Errorf("name %q must be rejected by the database", bad)
		}
	}
}
