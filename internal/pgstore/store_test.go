// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/clusterhealth"
	"github.com/24DSO-labs/kubron/internal/secretbox"
	"github.com/24DSO-labs/kubron/pkg/ext"
)

func keyString(id string) string {
	b := make([]byte, secretbox.KeySize)
	_, _ = rand.Read(b)
	return id + ":" + base64.StdEncoding.EncodeToString(b)
}

// newTestStore подключается к БД из KUBRON_TEST_DATABASE_URL и пересоздаёт в ней схему public.
// База должна быть одноразовой: её имя обязано содержать «test».
func newTestStore(t *testing.T, keyStr string, prev ...string) *Store {
	t.Helper()
	url := os.Getenv("KUBRON_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("KUBRON_TEST_DATABASE_URL is not set")
	}
	if !strings.Contains(url[strings.LastIndex(url, "/"):], "test") {
		t.Fatal("the test database name must contain \"test\": the schema is dropped")
	}
	keys, err := secretbox.ParseKeyring(keyStr, prev...)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := Open(ctx, url, keys)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestInitIsIdempotent(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	org, team := s.orgID, s.teamID
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.orgID != org || s.teamID != team {
		t.Error("повторный Init создал новые организацию или команду")
	}
	if res, err := s.Migrate(context.Background()); err != nil || len(res.Applied) != 0 {
		t.Errorf("повторная миграция: %+v %v", res, err)
	}
}

func TestOpenRequiresKeys(t *testing.T) {
	if _, err := Open(context.Background(), "postgres://x", nil); !errors.Is(err, secretbox.ErrNoKey) {
		t.Fatal(err)
	}
}

func TestUsersRoundTrip(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	u := s.Users()
	d, err := u.Load()
	if err != nil || len(d.Users) != 0 || d.Settings.IdleTimeoutMinutes != nil {
		t.Fatalf("пустая БД: %+v %v", d, err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	idle := 0
	in := &ext.UserData{
		Users: []ext.UserRecord{
			{ID: "u1", Username: "Admin", Role: "admin", PasswordHash: "h1", CreatedAt: now, UpdatedAt: now},
			{ID: "u2", Username: "bob", Role: "operator", PasswordHash: "h2", Disabled: true, MustChangePassword: true, CreatedAt: now.Add(time.Second), UpdatedAt: now, LastLoginAt: &now},
		},
		Settings: ext.SecuritySettings{IdleTimeoutMinutes: &idle},
	}
	if err := u.Save(in); err != nil {
		t.Fatal(err)
	}
	got, err := u.Load()
	if err != nil || len(got.Users) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if got.Users[0].ID != "u1" || got.Users[1].Username != "bob" || !got.Users[1].Disabled || !got.Users[1].MustChangePassword ||
		got.Users[1].LastLoginAt == nil || !got.Users[1].LastLoginAt.Equal(now) {
		t.Errorf("поля не сохранились: %+v", got.Users)
	}
	if got.Settings.IdleTimeoutMinutes == nil || *got.Settings.IdleTimeoutMinutes != 0 {
		t.Errorf("явный 0 должен отличаться от «не задано»: %+v", got.Settings)
	}

	// Обновление и удаление: u2 пропадает из набора — удаляется; u1 меняется.
	in.Users = []ext.UserRecord{{ID: "u1", Username: "Admin", Role: "admin", PasswordHash: "h1-new", CreatedAt: now, UpdatedAt: now}}
	if err := u.Save(in); err != nil {
		t.Fatal(err)
	}
	got, _ = u.Load()
	if len(got.Users) != 1 || got.Users[0].PasswordHash != "h1-new" {
		t.Errorf("%+v", got.Users)
	}
}

func TestUsersValidation(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	u := s.Users()
	now := time.Now()
	if err := u.Save(&ext.UserData{Users: []ext.UserRecord{{ID: "u1", Username: "a", Role: "viewer", CreatedAt: now}}}); err == nil {
		t.Error("неизвестная роль должна отклоняться")
	}
	// Имена уникальны без учёта регистра.
	err := u.Save(&ext.UserData{Users: []ext.UserRecord{
		{ID: "u1", Username: "Bob", Role: "user", CreatedAt: now},
		{ID: "u2", Username: "bob", Role: "user", CreatedAt: now},
	}})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("повтор имени: %v", err)
	}
	// Неудачное сохранение не оставляет частичных данных (транзакция).
	if d, _ := u.Load(); len(d.Users) != 0 {
		t.Errorf("частичная запись: %+v", d.Users)
	}
}

func TestAuditAppendAndScan(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	a := s.Audit()
	if err := a.ScanNewestFirst(func([]byte) bool { t.Fatal("журнал должен быть пуст"); return false }); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	for i, act := range []string{"Login", "DeletePod", "CreateUser"} {
		line, _ := json.Marshal(map[string]any{"time": base.Add(time.Duration(i) * time.Second), "user": "alice", "role": "admin",
			"action": act, "result": "ok", "detail": "x=" + act, "source": "10.0.0.1"})
		if err := a.Append(line); err != nil {
			t.Fatal(err)
		}
	}
	// Неизвестный результат не ломает запись.
	if err := a.Append([]byte(`{"time":"` + base.Add(3*time.Second).Format(time.RFC3339) + `","user":"u","action":"Odd","result":"weird"}`)); err != nil {
		t.Fatal(err)
	}
	if err := a.Append([]byte(`not json`)); err == nil {
		t.Error("некорректная строка должна отклоняться")
	}
	var actions []string
	_ = a.ScanNewestFirst(func(line []byte) bool {
		var e struct{ Action, Result, Source string }
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, e.Action)
		return true
	})
	if strings.Join(actions, ",") != "Odd,CreateUser,DeletePod,Login" {
		t.Errorf("порядок от новых к старым: %v", actions)
	}
	n := 0
	_ = a.ScanNewestFirst(func([]byte) bool { n++; return n < 2 })
	if n != 2 {
		t.Errorf("досрочная остановка: %d", n)
	}
}

func saveCluster(t *testing.T, s *Store, id, name string, kc []byte) {
	t.Helper()
	if err := s.Clusters().Save(&Cluster{ID: id, DisplayName: name, Environment: "dev", ContextName: "ctx"}, kc); err != nil {
		t.Fatal(err)
	}
}

func TestClustersLifecycle(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	c := s.Clusters()
	kc := []byte("apiVersion: v1\nkind: Config\n")
	saveCluster(t, s, "prod", "Production", kc)

	got, err := c.Get("prod")
	if err != nil || got.DisplayName != "Production" || got.TeamID != s.teamID || got.Mode != "kubeconfig" || got.AddedAt == "" {
		t.Fatalf("%+v %v", got, err)
	}
	if b, err := c.Kubeconfig("prod"); err != nil || !bytes.Equal(b, kc) {
		t.Fatalf("%q %v", b, err)
	}
	// В БД лежит шифротекст, а не kubeconfig.
	var raw []byte
	_ = s.pool.QueryRow(context.Background(), `SELECT kubeconfig_enc FROM clusters WHERE id='prod'`).Scan(&raw)
	if len(raw) == 0 || bytes.Contains(raw, []byte("apiVersion")) {
		t.Fatal("kubeconfig хранится открытым текстом")
	}

	// Правка метаданных без kubeconfig оставляет его прежним.
	if err := c.Save(&Cluster{ID: "prod", DisplayName: "Prod EU", Environment: "prod"}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = c.Get("prod")
	if got.DisplayName != "Prod EU" || got.Environment != "prod" {
		t.Errorf("%+v", got)
	}
	if b, err := c.Kubeconfig("prod"); err != nil || !bytes.Equal(b, kc) {
		t.Errorf("kubeconfig потерян: %q %v", b, err)
	}
	// Замена kubeconfig.
	if err := c.Save(&Cluster{ID: "prod", DisplayName: "Prod EU"}, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := c.Kubeconfig("prod"); string(b) != "new" {
		t.Errorf("%q", b)
	}

	// Новый кластер без kubeconfig недопустим; одноимённый в той же команде — конфликт.
	if err := c.Save(&Cluster{ID: "x", DisplayName: "X"}, nil); err == nil {
		t.Error("кластер без kubeconfig должен отклоняться")
	}
	if err := c.Save(&Cluster{ID: "p2", DisplayName: "prod eu"}, kc); !errors.Is(err, ErrConflict) {
		t.Errorf("повтор имени: %v", err)
	}

	saveCluster(t, s, "dev", "Dev", kc)
	list, err := c.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("%+v %v", list, err)
	}

	if _, err := c.Get("nope"); !errors.Is(err, ErrClusterNotFound) {
		t.Errorf("%v", err)
	}
	if _, err := c.Kubeconfig("nope"); !errors.Is(err, ErrClusterNotFound) {
		t.Errorf("%v", err)
	}
	if err := c.Delete("dev"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete("dev"); err != nil {
		t.Errorf("повторное удаление не ошибка: %v", err)
	}
	if list, _ = c.List(); len(list) != 1 {
		t.Errorf("%+v", list)
	}
}

func TestInClusterModeNeedsNoKubeconfig(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	if err := s.Clusters().Save(&Cluster{ID: "self", DisplayName: "Self", Mode: "in-cluster"}, nil); err != nil {
		t.Fatal(err)
	}
	if b, err := s.Clusters().Kubeconfig("self"); err != nil || b != nil {
		t.Errorf("%q %v", b, err)
	}
}

func TestArgoTokens(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	saveCluster(t, s, "a", "A", []byte("kc"))
	saveCluster(t, s, "b", "B", []byte("kc"))
	ts := s.ArgoTokens()
	if _, err := ts.Get("a"); !errors.Is(err, ext.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if err := ts.Put("a", "tok-a"); err != nil {
		t.Fatal(err)
	}
	if err := ts.Put("a", "tok-a2"); err != nil { // замена
		t.Fatal(err)
	}
	if v, err := ts.Get("a"); err != nil || v != "tok-a2" {
		t.Fatalf("%q %v", v, err)
	}
	if err := ts.Put("ghost", "x"); !errors.Is(err, ErrClusterNotFound) {
		t.Errorf("токен для несуществующего кластера: %v", err)
	}
	var raw []byte
	_ = s.pool.QueryRow(context.Background(), `SELECT token_enc FROM argo_tokens WHERE cluster_id='a'`).Scan(&raw)
	if bytes.Contains(raw, []byte("tok-a2")) {
		t.Fatal("токен хранится открытым текстом")
	}
	// Шифротекст, перенесённый в строку другого кластера, не расшифровывается (AAD).
	if err := ts.Put("b", "tok-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(context.Background(),
		`UPDATE argo_tokens SET token_enc=(SELECT token_enc FROM argo_tokens WHERE cluster_id='a') WHERE cluster_id='b'`); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Get("b"); !errors.Is(err, secretbox.ErrDecrypt) {
		t.Errorf("подмена между строками: %v", err)
	}
	// Удаление кластера удаляет токен; Delete токена идемпотентен.
	_ = s.Clusters().Delete("a")
	if _, err := ts.Get("a"); !errors.Is(err, ext.ErrNotFound) {
		t.Errorf("%v", err)
	}
	if err := ts.Delete("a"); err != nil {
		t.Error(err)
	}
}

func TestKeyRotationAcrossStores(t *testing.T) {
	oldK, newK := keyString("2026-01"), keyString("2026-07")
	s := newTestStore(t, oldK)
	saveCluster(t, s, "a", "A", []byte("kc"))
	_ = s.ArgoTokens().Put("a", "tok")

	url := os.Getenv("KUBRON_TEST_DATABASE_URL")
	open := func(cur string, prev ...string) *Store {
		keys, err := secretbox.ParseKeyring(cur, prev...)
		if err != nil {
			t.Fatal(err)
		}
		n, err := Open(context.Background(), url, keys)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(n.Close)
		if err := n.Init(context.Background()); err != nil {
			t.Fatal(err)
		}
		return n
	}
	withBoth := open(newK, oldK)
	if b, err := withBoth.Clusters().Kubeconfig("a"); err != nil || string(b) != "kc" {
		t.Fatalf("%q %v", b, err)
	}
	if v, err := withBoth.ArgoTokens().Get("a"); err != nil || v != "tok" {
		t.Fatalf("%q %v", v, err)
	}
	// Новая запись шифруется новым ключом.
	if err := withBoth.Clusters().Save(&Cluster{ID: "a", DisplayName: "A"}, []byte("kc2")); err != nil {
		t.Fatal(err)
	}
	var kid string
	_ = withBoth.pool.QueryRow(context.Background(), `SELECT key_id FROM clusters WHERE id='a'`).Scan(&kid)
	if kid != "2026-07" {
		t.Errorf("key_id=%q", kid)
	}
	// Без прежнего ключа токен, зашифрованный им, не читается.
	if _, err := open(newK).ArgoTokens().Get("a"); !errors.Is(err, secretbox.ErrUnknownKey) {
		t.Errorf("%v", err)
	}
}

func TestRekeyAndCounts(t *testing.T) {
	oldK, newK := keyString("2026-01"), keyString("2026-07")
	s := newTestStore(t, oldK)
	saveCluster(t, s, "a", "A", []byte("kc-a"))
	saveCluster(t, s, "b", "B", []byte("kc-b"))
	_ = s.ArgoTokens().Put("a", "tok-a")
	c, err := s.Counts(context.Background())
	if err != nil || c.Clusters != 2 || c.Users != 0 || c.AuditEvents != 0 {
		t.Fatalf("%+v %v", c, err)
	}

	keys, _ := secretbox.ParseKeyring(newK, oldK)
	n, err := Open(context.Background(), os.Getenv("KUBRON_TEST_DATABASE_URL"), keys)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	rep, err := n.Rekey(context.Background())
	if err != nil || rep.Clusters != 2 || rep.Tokens != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if again, _ := n.Rekey(context.Background()); again != (RekeyReport{}) {
		t.Errorf("повторный Rekey ничего не должен менять: %+v", again)
	}
	// После перешифрования старый ключ больше не нужен.
	only, _ := secretbox.ParseKeyring(newK)
	m, err := Open(context.Background(), os.Getenv("KUBRON_TEST_DATABASE_URL"), only)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if b, err := m.Clusters().Kubeconfig("b"); err != nil || string(b) != "kc-b" {
		t.Errorf("%q %v", b, err)
	}
	if v, err := m.ArgoTokens().Get("a"); err != nil || v != "tok-a" {
		t.Errorf("%q %v", v, err)
	}
}

func TestAccessFromMemberships(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	zero := time.Duration(0)
	s.accessTTLOverride = &zero // без кэша: проверяем сам запрос
	acc := s.Access()
	now := time.Now()
	if err := s.Users().Save(&ext.UserData{Users: []ext.UserRecord{
		{ID: "adm", Username: "admin", Role: "admin", CreatedAt: now},
		{ID: "bob", Username: "bob", Role: "operator", CreatedAt: now},
		{ID: "off", Username: "off", Role: "user", Disabled: true, CreatedAt: now},
	}}); err != nil {
		t.Fatal(err)
	}
	saveCluster(t, s, "c1", "C1", []byte("kc"))

	if team, ok := acc.ClusterTeam("c1"); !ok || team != s.teamID {
		t.Errorf("команда кластера: %q %v", team, ok)
	}
	if _, ok := acc.ClusterTeam("nope"); ok {
		t.Error("несуществующий кластер")
	}
	if r, ok := acc.TeamRole("bob", s.teamID); !ok || r != "operator" {
		t.Errorf("bob: %q %v", r, ok)
	}
	if r, ok := acc.TeamRole("adm", s.teamID); !ok || r != "admin" {
		t.Errorf("admin → админ команды Default: %q %v", r, ok)
	}
	if !acc.IsOrgAdmin("adm") || acc.IsOrgAdmin("bob") {
		t.Error("администратор организации")
	}
	if _, ok := acc.TeamRole("off", s.teamID); ok || len(acc.TeamRoles("off")) != 0 {
		t.Error("отключённый пользователь не должен иметь доступа")
	}

	// Смена глобальной роли переносится на членство; повторное сохранение без изменений ничего не трогает.
	_, err := s.pool.Exec(context.Background(), `INSERT INTO teams (org_id, name) VALUES ($1, 'Other')`, s.orgID)
	if err != nil {
		t.Fatal(err)
	}
	var other string
	_ = s.pool.QueryRow(context.Background(), `SELECT id FROM teams WHERE name='Other'`).Scan(&other)
	_, _ = s.pool.Exec(context.Background(), `INSERT INTO memberships (user_id, team_id, role) VALUES ('bob', $1, 'user')`, other)
	d, _ := s.Users().Load()
	if err := s.Users().Save(d); err != nil {
		t.Fatal(err)
	}
	if r, ok := acc.TeamRole("bob", other); !ok || r != "user" {
		t.Errorf("Save без изменений не должен трогать чужие членства: %q %v", r, ok)
	}
	for i := range d.Users {
		if d.Users[i].ID == "bob" {
			d.Users[i].Role = "user"
		}
	}
	if err := s.Users().Save(d); err != nil {
		t.Fatal(err)
	}
	if r, _ := acc.TeamRole("bob", s.teamID); r != "user" {
		t.Errorf("смена роли: %q", r)
	}
	if r, _ := acc.TeamRole("bob", other); r != "user" {
		t.Errorf("членство в другой команде: %q", r)
	}
	if !acc.IsOrgAdmin("adm") {
		t.Error("org-admin пропал")
	}
	// Удаление пользователя убирает его членства (каскад).
	d.Users = d.Users[:1]
	if err := s.Users().Save(d); err != nil {
		t.Fatal(err)
	}
	if len(acc.TeamRoles("bob")) != 0 {
		t.Error("членства удалённого пользователя остались")
	}
}

func TestAccessSnapshotIsCachedAndInvalidated(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	acc := s.Access() // TTL по умолчанию: снимок кэшируется
	saveCluster(t, s, "c1", "C1", []byte("kc"))
	if _, ok := acc.ClusterTeam("c1"); !ok {
		t.Fatal("после Save кэш должен быть сброшен")
	}
	// Правка в обход Store (как сделала бы другая реплика) не видна, пока снимок свеж.
	_, _ = s.pool.Exec(context.Background(), `DELETE FROM clusters WHERE id='c1'`)
	if _, ok := acc.ClusterTeam("c1"); !ok {
		t.Error("снимок должен кэшироваться на accessTTL")
	}
	s.invalidateAccess()
	if _, ok := acc.ClusterTeam("c1"); ok {
		t.Error("после сброса кэша кластера быть не должно")
	}
}

func TestDirectory(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	zero := time.Duration(0)
	s.accessTTLOverride = &zero
	dir, acc := s.Directory(), s.Access()
	now := time.Now()
	if err := s.Users().Save(&ext.UserData{Users: []ext.UserRecord{
		{ID: "adm", Username: "admin", Role: "admin", CreatedAt: now},
		{ID: "bob", Username: "bob", Role: "user", CreatedAt: now},
	}}); err != nil {
		t.Fatal(err)
	}

	teams, err := dir.Teams()
	if err != nil || len(teams) != 1 || teams[0].Name != DefaultName || teams[0].Members != 2 || dir.DefaultTeam() != teams[0].ID {
		t.Fatalf("%+v %v", teams, err)
	}
	blue, err := dir.CreateTeam("  Blue ")
	if err != nil || blue.Name != "Blue" {
		t.Fatalf("%+v %v", blue, err)
	}
	if _, err := dir.CreateTeam("Blue"); !errors.Is(err, ErrConflict) {
		t.Errorf("точный повтор имени: %v", err)
	}
	if _, err := dir.CreateTeam("   "); err == nil {
		t.Error("пустое имя")
	}
	if err := dir.RenameTeam(blue.ID, "Azure"); err != nil {
		t.Fatal(err)
	}
	if err := dir.RenameTeam("nope", "X"); !errors.Is(err, auth.ErrTeamNotFound) {
		t.Errorf("%v", err)
	}

	// Участники.
	if err := dir.SetMember(blue.ID, "bob", "operator"); err != nil {
		t.Fatal(err)
	}
	if err := dir.SetMember(blue.ID, "bob", "admin"); err != nil { // смена роли
		t.Fatal(err)
	}
	if r, ok := acc.TeamRole("bob", blue.ID); !ok || r != "admin" {
		t.Errorf("доступ сразу после изменения: %q %v", r, ok)
	}
	if err := dir.SetMember(blue.ID, "ghost", "user"); !errors.Is(err, auth.ErrTeamNotFound) {
		t.Errorf("несуществующий пользователь: %v", err)
	}
	if err := dir.SetMember(blue.ID, "bob", "root"); !errors.Is(err, auth.ErrUnknownRole) {
		t.Errorf("неизвестная роль: %v", err)
	}
	ms, err := dir.Members(blue.ID)
	if err != nil || len(ms) != 1 || ms[0].Username != "bob" || ms[0].Role != "admin" {
		t.Fatalf("%+v %v", ms, err)
	}
	if _, err := dir.Members("nope"); !errors.Is(err, auth.ErrTeamNotFound) {
		t.Errorf("%v", err)
	}

	// Передача кластера и удаление команды.
	saveCluster(t, s, "c1", "C1", []byte("kc"))
	if err := dir.MoveCluster("c1", blue.ID); err != nil {
		t.Fatal(err)
	}
	if team, _ := acc.ClusterTeam("c1"); team != blue.ID {
		t.Errorf("владелец после передачи: %q", team)
	}
	if err := dir.MoveCluster("c1", "nope"); !errors.Is(err, auth.ErrTeamNotFound) {
		t.Errorf("%v", err)
	}
	if err := dir.MoveCluster("nope", blue.ID); !errors.Is(err, ErrClusterNotFound) {
		t.Errorf("%v", err)
	}
	saveCluster(t, s, "c2", "C1", []byte("kc")) // то же имя, но в Default — допустимо
	if err := dir.MoveCluster("c2", blue.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("одноимённый кластер в целевой команде: %v", err)
	}
	if err := dir.DeleteTeam(blue.ID); !errors.Is(err, ErrTeamNotEmpty) {
		t.Errorf("команда с кластерами: %v", err)
	}
	if err := dir.DeleteTeam(dir.DefaultTeam()); !errors.Is(err, ErrDefaultTeam) {
		t.Errorf("команда по умолчанию: %v", err)
	}
	_ = s.Clusters().Delete("c1")
	if err := dir.RemoveMember(blue.ID, "bob"); err != nil {
		t.Fatal(err)
	}
	if err := dir.DeleteTeam(blue.ID); err != nil {
		t.Fatal(err)
	}
	if err := dir.DeleteTeam(blue.ID); !errors.Is(err, auth.ErrTeamNotFound) {
		t.Errorf("повторное удаление: %v", err)
	}
}

func TestAuditKeepsTeamAndClusterContext(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	a := s.Audit()
	if err := a.Append([]byte(`{"time":"2026-10-30T01:00:00Z","user":"u","action":"DeletePod","result":"ok","teamId":"t1","clusterId":"c1","source":"10.0.0.7"}`)); err != nil {
		t.Fatal(err)
	}
	if err := a.Append([]byte(`{"time":"2026-10-30T02:00:00Z","user":"u","action":"Login","result":"ok"}`)); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	_ = a.ScanNewestFirst(func(line []byte) bool {
		var m map[string]any
		_ = json.Unmarshal(line, &m)
		got = append(got, m)
		return true
	})
	if len(got) != 2 || got[0]["action"] != "Login" || got[0]["teamId"] != nil {
		t.Fatalf("%+v", got)
	}
	if got[1]["teamId"] != "t1" || got[1]["clusterId"] != "c1" || got[1]["source"] != "10.0.0.7" {
		t.Errorf("контекст потерян: %+v", got[1])
	}
}

func TestHealthSaveLoadAndCascade(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	ctx := context.Background()
	saveCluster(t, s, "c1", "One", []byte("kc"))
	h := s.Health()

	at := time.Now().UTC().Truncate(time.Millisecond)
	if err := h.Save(ctx, "c1", clusterhealth.Health{State: clusterhealth.Unreachable, Message: "i/o timeout", LatencyMs: 12, CheckedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := h.Save(ctx, "c1", clusterhealth.Health{State: clusterhealth.OK, LatencyMs: 3, CheckedAt: at}); err != nil { // обновление той же записи
		t.Fatal(err)
	}
	// проверка удалённого или неизвестного кластера не создаёт строку и не падает
	if err := h.Save(ctx, "ghost", clusterhealth.Health{State: clusterhealth.OK}); err != nil {
		t.Fatalf("a check that finished after the cluster was deleted is not an error: %v", err)
	}
	if err := h.Save(ctx, "c1", clusterhealth.Health{State: "bogus"}); err == nil {
		t.Fatal("unknown state must be rejected by the schema")
	}

	all, err := h.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all["c1"].State != clusterhealth.OK || all["c1"].Message != "" || all["c1"].LatencyMs != 3 || !all["c1"].CheckedAt.Equal(at) {
		t.Fatalf("got %+v", all)
	}

	if err := s.Clusters().Delete("c1"); err != nil {
		t.Fatal(err)
	}
	if all, _ := h.Load(ctx); len(all) != 0 {
		t.Fatalf("health must disappear with the cluster: %+v", all)
	}
}

func TestLeaderIsExclusiveAndFailsOver(t *testing.T) {
	a := newTestStore(t, keyString("k1"))
	// вторая «реплика»: свой пул к той же БД
	b, err := Open(context.Background(), os.Getenv("KUBRON_TEST_DATABASE_URL"), a.keys)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	ctx := context.Background()
	la, lb := a.Leader(LeaderKey), b.Leader(LeaderKey)

	if !la.Acquire(ctx) {
		t.Fatal("the first replica must become the leader")
	}
	if !la.Acquire(ctx) {
		t.Fatal("the leader stays the leader on the next tick")
	}
	if lb.Acquire(ctx) {
		t.Fatal("two leaders at the same time")
	}
	la.Release()
	la.Release() // повторный вызов безопасен
	if !lb.Acquire(ctx) {
		t.Fatal("after release the other replica must take over")
	}
	lb.Release()
}

func TestLeaderLockDropsWithTheConnection(t *testing.T) {
	a := newTestStore(t, keyString("k1"))
	b, err := Open(context.Background(), os.Getenv("KUBRON_TEST_DATABASE_URL"), a.keys)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	ctx := context.Background()
	la, lb := a.Leader(LeaderKey), b.Leader(LeaderKey)
	if !la.Acquire(ctx) {
		t.Fatal("no leadership")
	}
	// реплика упала: её соединение закрыто, блокировку снимает сервер
	la.mu.Lock()
	_ = la.conn.Conn().Close(ctx)
	la.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for !lb.Acquire(ctx) {
		if time.Now().After(deadline) {
			t.Fatal("the lock was not released after the leader connection died")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// упавший лидер сам замечает потерю и не считает себя лидером
	if la.Acquire(ctx) {
		t.Fatal("the old leader must not reclaim a lock held by another replica")
	}
	lb.Release()
}

func TestClusterSecretIsEncryptedSeparatelyAndSurvivesUpdates(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	ctx := context.Background()
	c := s.Clusters()
	cl := &Cluster{ID: "oidc", DisplayName: "OIDC", Mode: "oidc-password"}
	secret := []byte(`{"username":"vasya","password":"s3cret-pass-word"}`)
	if err := c.SaveWithSecret(cl, []byte("kubeconfig-body"), secret); err != nil {
		t.Fatal(err)
	}

	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT secret_enc FROM clusters WHERE id='oidc'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("s3cret-pass-word")) || bytes.Contains(raw, []byte("vasya")) {
		t.Fatal("the secret is stored in plain text")
	}
	if got, err := c.Secret("oidc"); err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("got %q, %v", got, err)
	}

	// Обновление настроек и замена kubeconfig не трогают секрет
	if err := c.Save(&Cluster{ID: "oidc", DisplayName: "Renamed", Mode: "oidc-password"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(&Cluster{ID: "oidc", DisplayName: "Renamed", Mode: "oidc-password"}, []byte("kubeconfig-2")); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Secret("oidc"); !bytes.Equal(got, secret) {
		t.Fatalf("the secret changed after a plain update: %q", got)
	}
	// Явная замена секрета
	if err := c.SaveWithSecret(&Cluster{ID: "oidc", DisplayName: "Renamed", Mode: "oidc-password"}, nil, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Secret("oidc"); string(got) != "new" {
		t.Fatalf("got %q", got)
	}
	if b, _ := c.Kubeconfig("oidc"); string(b) != "kubeconfig-2" {
		t.Fatalf("kubeconfig must stay: %q", b)
	}

	// Подмена шифротекста секрета другим кластером не расшифровывается (AAD привязан к id)
	saveCluster(t, s, "other", "Other", []byte("kc"))
	if _, err := s.pool.Exec(ctx, `UPDATE clusters SET secret_enc=(SELECT secret_enc FROM clusters WHERE id='oidc'),
		secret_key_id=(SELECT secret_key_id FROM clusters WHERE id='oidc') WHERE id='other'`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Secret("other"); err == nil {
		t.Fatal("a secret copied to another cluster must not decrypt")
	}
	if _, err := c.Secret("missing"); !errors.Is(err, ErrClusterNotFound) {
		t.Fatalf("got %v", err)
	}
	if got, err := s.Clusters().Secret("oidc"); err != nil || got == nil {
		t.Fatal(err)
	}
}

func TestInClusterModeWithoutKubeconfigAndSecretRekey(t *testing.T) {
	oldK := keyString("old")
	s := newTestStore(t, oldK)
	ctx := context.Background()
	if err := s.Clusters().SaveWithSecret(&Cluster{ID: "o", DisplayName: "O", Mode: "oidc-password"}, []byte("kc"), []byte("pw")); err != nil {
		t.Fatal(err)
	}
	if err := s.Clusters().Save(&Cluster{ID: "self", DisplayName: "Self", Mode: "in-cluster"}, nil); err != nil {
		t.Fatal(err)
	}
	if sec, err := s.Clusters().Secret("self"); err != nil || sec != nil {
		t.Fatalf("a cluster without a secret returns nil, got %q %v", sec, err)
	}

	n, err := Open(ctx, os.Getenv("KUBRON_TEST_DATABASE_URL"), mustKeyring(t, keyString("new"), oldK))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(n.Close)
	n.orgID, n.teamID = s.orgID, s.teamID
	rep, err := n.Rekey(ctx)
	if err != nil || rep.Secrets != 1 || rep.Clusters != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if got, err := n.Clusters().Secret("o"); err != nil || string(got) != "pw" {
		t.Fatalf("got %q %v", got, err)
	}
	var kid string
	if err := n.pool.QueryRow(ctx, `SELECT secret_key_id FROM clusters WHERE id='o'`).Scan(&kid); err != nil || kid != "new" {
		t.Fatalf("secret must move to the current key: %q %v", kid, err)
	}
}

func mustKeyring(t *testing.T, cur string, prev ...string) *secretbox.Keyring {
	t.Helper()
	k, err := secretbox.ParseKeyring(cur, prev...)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
