// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

// otherReplica имитирует вторую реплику: состояние в БД то же, а каталог кэша (emptyDir) пуст.
func otherReplica(t *testing.T, m *Manager) {
	t.Helper()
	sc, err := m.helmScope()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(sc.cacheDir()); err != nil {
		t.Fatal(err)
	}
	helmIndexMu.Lock()
	helmIndexCache = map[string]helmCachedIndex{}
	helmIndexMu.Unlock()
}

// Репозиторий добавлен на одной реплике, запрос пришёл на другую: индекс и теги докачиваются сами.
func TestHelmCacheIsRebuiltOnAnotherReplica(t *testing.T) {
	red, _, _ := serviceMode(t)
	repo := newChartRepo(t, "red-app")
	reg := fakeRegistry(t, map[string][]string{"org/charts/podinfo": {"1.0.0", "1.1.0"}})
	if err := red.AddHelmRepo("stable", repo.URL, "", "", false); err != nil {
		t.Fatal(err)
	}
	base := "oci://" + strings.TrimPrefix(reg.URL, "http://") + "/org/charts"
	if err := red.AddHelmOCIRepo("reg", base, []string{"podinfo"}, "", ""); err != nil {
		t.Fatal(err)
	}
	hits := repo.hits.Load()

	// Список не ждёт сеть: сначала подсказка, затем кэш появляется в фоне.
	otherReplica(t, red)
	list, err := red.ListHelmRepos()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range list {
		if r.Error == "" {
			t.Errorf("the first list on a replica without a cache shows a hint: %+v", r)
		}
	}
	eventually(t, "the background fill of both caches", func() bool {
		l, _ := red.ListHelmRepos()
		return len(l) == 2 && l[0].Error == "" && l[1].Error == "" && l[0].Charts+l[1].Charts == 2
	})

	// Поиск и версии при пустом кэше докачивают недостающее и отвечают сразу.
	otherReplica(t, red)
	res, err := red.SearchHelmCharts("", "stable")
	if err != nil || len(res.Items) != 1 || res.Items[0].Name != "red-app" {
		t.Fatalf("search on a replica without a cache: %+v %v", res, err)
	}
	otherReplica(t, red)
	vs, err := red.GetHelmChartVersions("reg", "podinfo")
	if err != nil || len(vs) != 2 {
		t.Fatalf("OCI versions on a replica without a cache: %+v %v", vs, err)
	}
	if _, err := red.GetHelmChartVersions("reg", "not-in-the-registry"); err == nil {
		t.Error("only charts listed in the registry are requested")
	}
	if repo.hits.Load() <= hits {
		t.Error("the repository must have been asked again")
	}
}

// Репозиторий удалили и добавили под тем же именем с другим адресом: на реплике со старым кэшем он не подставляется.
func TestHelmStaleCacheOfAnotherURLIsIgnored(t *testing.T) {
	red, _, _ := serviceMode(t)
	oldSrv, newSrv := newChartRepo(t, "old-app"), newChartRepo(t, "new-app")
	if err := red.AddHelmRepo("stable", oldSrv.URL, "", "", false); err != nil {
		t.Fatal(err)
	}
	sc, _ := red.helmScope()
	oldFile := sc.indexPath("stable")
	stale, err := os.ReadFile(oldFile)
	if err != nil {
		t.Fatal(err)
	}

	if err := red.RemoveHelmRepo("stable"); err != nil {
		t.Fatal(err)
	}
	if err := red.AddHelmRepo("stable", newSrv.URL, "", "", false); err != nil {
		t.Fatal(err)
	}
	// «Другая реплика»: у неё остался кэш прежнего адреса и нет кэша нового.
	otherReplica(t, red)
	if err := os.MkdirAll(filepath.Dir(oldFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldFile, stale, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := red.SearchHelmCharts("", "")
	if err != nil || len(res.Items) != 1 || res.Items[0].Name != "new-app" {
		t.Errorf("the cache of the old URL must not be used: %+v %v", res, err)
	}
}

// Одновременные запросы к реплике без кэша скачивают индекс один раз.
func TestHelmLazyFetchIsSingleFlight(t *testing.T) {
	red, _, _ := serviceMode(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte(chartIndex("app")))
	}))
	defer srv.Close()
	if err := red.AddHelmRepo("stable", srv.URL, "", "", false); err != nil {
		t.Fatal(err)
	}
	otherReplica(t, red)
	hits.Store(0)

	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { _, err := red.GetHelmChartVersions("stable", "app"); done <- err }()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Errorf("request %d: %v", i, err)
		}
	}
	if hits.Load() != 1 {
		t.Errorf("eight concurrent requests must download the index once, got %d downloads", hits.Load())
	}
}

// Файловый режим (десктоп, один пользователь) не докачивает ничего сам: кэш появляется только по «Обновить».
func TestHelmFileModeDoesNotFetchLazily(t *testing.T) {
	setupHelmHome(t)
	repo := newChartRepo(t, "app")
	m := NewManager()
	if err := m.AddHelmRepo("stable", repo.URL, "", "", false); err != nil {
		t.Fatal(err)
	}
	sc, _ := m.helmScope()
	if err := os.Remove(sc.indexPath("stable")); err != nil {
		t.Fatal(err)
	}
	helmIndexMu.Lock()
	helmIndexCache = map[string]helmCachedIndex{}
	helmIndexMu.Unlock()
	hits := repo.hits.Load()

	if _, err := m.GetHelmChartVersions("stable", "app"); err == nil {
		t.Error("without an index the file mode answers with an error, as before")
	}
	if l, _ := m.ListHelmRepos(); len(l) != 1 || l[0].Error == "" {
		t.Errorf("the list shows the hint: %+v", l)
	}
	time.Sleep(200 * time.Millisecond)
	if repo.hits.Load() != hits {
		t.Error("the file mode must not download anything on its own")
	}
}
