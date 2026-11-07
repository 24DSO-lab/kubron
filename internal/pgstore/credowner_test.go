// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"testing"

	"github.com/24DSO-lab/kubron/internal/credential"
)

func putCred(t *testing.T, s *Store, cluster string, scope credential.Scope, owner string) {
	t.Helper()
	r := credential.Record{
		Meta:   credential.Meta{Key: credential.Key{ClusterID: cluster, Scope: scope, OwnerID: owner}, Kind: credential.KindOIDC, Principal: "p"},
		Secret: []byte("secret"),
	}
	if _, err := s.Credentials().Put(context.Background(), r, 0); err != nil {
		t.Fatal(err)
	}
}

func countCreds(t *testing.T, s *Store, ownerKind, owner string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM cluster_credentials WHERE owner_kind = $1 AND owner_id = $2`, ownerKind, owner).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Удаление пользователя или команды убирает их данные входа: у owner_id нет внешнего ключа.
func TestDeleteOwnerRemovesCredentials(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	ensureCluster(t, s, "c1")

	putCred(t, s, "c1", credential.ScopeUser, "gone-user")
	putCred(t, s, "c1", credential.ScopeUser, "other-user")
	if err := (userBackend{s}).DeleteUser("gone-user"); err != nil {
		t.Fatal(err)
	}
	if n := countCreds(t, s, "user", "gone-user"); n != 0 {
		t.Errorf("данные удалённого пользователя остались: %d", n)
	}
	if n := countCreds(t, s, "user", "other-user"); n != 1 {
		t.Errorf("данные другого пользователя затронуты: %d", n)
	}

	team, err := s.Directory().CreateTeam("Creds")
	if err != nil {
		t.Fatal(err)
	}
	putCred(t, s, "c1", credential.ScopeTeam, team.ID)
	if err := s.Directory().DeleteTeam(team.ID); err != nil {
		t.Fatal(err)
	}
	if n := countCreds(t, s, "team", team.ID); n != 0 {
		t.Errorf("данные удалённой команды остались: %d", n)
	}
}
