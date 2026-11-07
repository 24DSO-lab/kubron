// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/pgstore"
	"github.com/24DSO-lab/kubron/internal/store"
)

func testKey() string { return "k1:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)) }

func kubeconfigText(server string) []byte {
	return []byte("apiVersion: v1\nkind: Config\nclusters:\n- name: c\n  cluster:\n    server: " + server +
		"\n    insecure-skip-tls-verify: true\nusers:\n- name: u\n  user:\n    token: t\ncontexts:\n- name: ctx\n  context:\n    cluster: c\n    user: u\ncurrent-context: ctx\n")
}

func chartRepo(t *testing.T, chart string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.yaml" {
			_, _ = w.Write([]byte("apiVersion: v1\nentries:\n  " + chart + ":\n    - name: " + chart + "\n      version: 1.0.0\n      urls: [" + chart + "-1.0.0.tgz]\n"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type helmStater interface{ HelmState() k8s.HelmStateStore }

// pgSetup поднимает хранилище, подключает его к k8s так же, как сервер (app.initAuthWith), и создаёт две команды
// с одним кластером в каждой.
func pgSetup(t *testing.T) (s *pgstore.Store, st k8s.HelmStateStore, red, blue string) {
	t.Helper()
	s = pgstore.NewTestStore(t, testKey())
	full := store.FromPostgres(s)
	st = full.(helmStater).HelmState()

	t.Setenv("HOME", t.TempDir()) // кэш индексов Helm лежит в каталоге данных
	k8s.SetClusterStore(full.Clusters())
	k8s.SetHelmStateStore(st)
	t.Cleanup(func() { k8s.SetHelmStateStore(nil) })

	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{}")) }))
	t.Cleanup(api.Close)
	for name, id := range map[string]*string{"Red": &red, "Blue": &blue} {
		team, err := s.Directory().CreateTeam(name)
		if err != nil {
			t.Fatal(err)
		}
		*id = team.ID
		if err := s.Clusters().Save(&pgstore.Cluster{ID: strings.ToLower(name) + "-1", DisplayName: name, ContextName: "ctx", TeamID: team.ID}, kubeconfigText(api.URL)); err != nil {
			t.Fatal(err)
		}
	}
	return s, st, red, blue
}

func connect(t *testing.T, cluster string) *k8s.Manager {
	t.Helper()
	m := k8s.NewManager()
	if err := m.ConnectToCluster(cluster); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Detach)
	return m
}

// Менеджер работает с PostgreSQL через адаптер так же, как в сервисе: команды изолированы, пароль лежит
// зашифрованным, одноимённые репозитории не пересекаются.
func TestHelmThroughPostgresStore(t *testing.T) {
	s, st, red, blue := pgSetup(t)
	redM, blueM := connect(t, "red-1"), connect(t, "blue-1")
	redSrv, blueSrv := chartRepo(t, "red-app"), chartRepo(t, "blue-app")

	if err := redM.AddHelmRepo("stable", redSrv.URL, "ci", "s3cr3t-pass", false); err != nil {
		t.Fatal(err)
	}
	if l, _ := blueM.ListHelmRepos(); len(l) != 0 {
		t.Fatalf("blue must not see red's repositories: %+v", l)
	}
	if err := blueM.AddHelmRepo("stable", blueSrv.URL, "", "", false); err != nil {
		t.Fatalf("the same name in another team: %v", err)
	}
	if err := redM.AddHelmRepo("stable", redSrv.URL, "", "", false); err == nil {
		t.Error("a duplicate name within a team must be rejected")
	}

	for m, want := range map[*k8s.Manager]string{redM: "red-app", blueM: "blue-app"} {
		res, err := m.SearchHelmCharts("", "")
		if err != nil || len(res.Items) != 1 || res.Items[0].Name != want {
			t.Errorf("catalog for %s: %+v %v", want, res, err)
		}
	}

	// В БД пароль зашифрован, в API его нет.
	var enc []byte
	var keyID string
	if err := s.QueryRowForTest(`SELECT secret_enc, secret_key_id FROM helm_repositories WHERE team_id=$1 AND name='stable'`, red).Scan(&enc, &keyID); err != nil {
		t.Fatal(err)
	}
	if len(enc) == 0 || bytes.Contains(enc, []byte("s3cr3t-pass")) || keyID == "" {
		t.Errorf("the password must be stored encrypted with a key id: %d bytes, key %q", len(enc), keyID)
	}
	if raw, _ := json.Marshal(mustList(t, redM)); bytes.Contains(raw, []byte("s3cr3t-pass")) {
		t.Errorf("the API leaks the password: %s", raw)
	}
	if recs, err := st.ListRepos(red); err != nil || len(recs) != 1 || recs[0].Password != "s3cr3t-pass" || recs[0].CreatedAt.IsZero() {
		t.Errorf("the adapter returns the decrypted password for requests to the repository: %+v %v", recs, err)
	}

	// Удаление и обновление действуют только на свою команду.
	if err := blueM.RemoveHelmRepo("stable"); err != nil {
		t.Fatal(err)
	}
	if l := mustList(t, redM); len(l) != 1 {
		t.Errorf("removing in blue must not touch red: %+v", l)
	}
	if err := blueM.RemoveHelmRepo("stable"); err == nil {
		t.Error("a repository that is not in the team is «not found»")
	}
	if res, err := redM.UpdateHelmRepos(""); err != nil || len(res) != 1 || res[0].Error != "" {
		t.Errorf("update: %+v %v", res, err)
	}
	if err := redM.RemoveHelmRepo("stable"); err != nil {
		t.Fatal(err)
	}
	_ = blue
}

func mustList(t *testing.T, m *k8s.Manager) []k8s.HelmRepoInfo {
	t.Helper()
	l, err := m.ListHelmRepos()
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// Преобразования адаптера: OCI-реестр, журнал операций, ошибки хранилища → ошибки ядра.
func TestHelmAdapterConversions(t *testing.T) {
	_, st, red, blue := pgSetup(t)

	oci := &k8s.HelmRepoRecord{Name: "reg", Kind: "oci", URL: "oci://reg.example/charts", Username: "bot", Password: "pa55",
		PlainHTTP: true, Charts: []string{"a", "b"}, CreatedAt: time.Now().UTC()}
	if err := st.CreateRepo(red, oci); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRepo(red, oci); err != k8s.ErrHelmRepoExists {
		t.Errorf("a duplicate must map to ErrHelmRepoExists: %v", err)
	}
	recs, _ := st.ListRepos(red)
	if len(recs) != 1 || recs[0].Kind != "oci" || !recs[0].PlainHTTP || len(recs[0].Charts) != 2 || recs[0].Password != "pa55" {
		t.Fatalf("OCI record: %+v", recs)
	}
	if err := st.SetOCICharts(red, "reg", []string{"c"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetOCICharts(blue, "reg", []string{"x"}); err != k8s.ErrHelmNotFound {
		t.Errorf("a foreign registry is not found: %v", err)
	}
	if _, err := st.DeleteRepo(blue, "reg"); err != k8s.ErrHelmNotFound {
		t.Errorf("a foreign registry cannot be deleted: %v", err)
	}
	removed, err := st.DeleteRepo(red, "reg")
	if err != nil || removed.Password != "" || removed.Username != "bot" || len(removed.Charts) != 1 {
		t.Errorf("the removed record comes back without the password: %+v %v", removed, err)
	}

	// Журнал: запись целиком переживает JSON, новые первыми, чужая команда ничего не видит.
	now := time.Now().UTC().Truncate(time.Second)
	for i, id := range []string{"op-1", "op-2"} {
		rec := k8s.HelmOperationRecord{ID: id, Op: "install", Namespace: "apps", Release: "web", Status: "succeeded", Cluster: "red-1",
			FinishedAt: now.Add(time.Duration(i) * time.Second).Format(time.RFC3339), Log: []string{"line"}}
		if err := st.AddOperation(red, "red-1", rec); err != nil {
			t.Fatal(err)
		}
	}
	ops, err := st.ListOperations(red)
	if err != nil || len(ops) != 2 || ops[0].ID != "op-2" || ops[1].Release != "web" || len(ops[1].Log) != 1 {
		t.Fatalf("journal: %+v %v", ops, err)
	}
	if ops, _ := st.ListOperations(blue); len(ops) != 0 {
		t.Error("blue must not see red's journal")
	}
	if _, err := st.GetOperation(blue, "op-1"); err != k8s.ErrHelmNotFound {
		t.Errorf("a foreign operation is not found: %v", err)
	}
	if r, err := st.GetOperation(red, "op-1"); err != nil || r.Namespace != "apps" {
		t.Errorf("own operation: %+v %v", r, err)
	}
	if err := st.ClearOperations(red); err != nil {
		t.Fatal(err)
	}
	if ops, _ := st.ListOperations(red); len(ops) != 0 {
		t.Error("the journal must be empty after Clear")
	}
}
