// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizeHelmOCIURL(t *testing.T) {
	got, host, err := normalizeHelmOCIURL(" oci://GHCR.io/Org/charts/ ")
	if err == nil {
		t.Fatalf("uppercase letters in the path must be rejected, got %q %q", got, host)
	}
	got, host, err = normalizeHelmOCIURL(" oci://GHCR.io/org/charts/ ")
	if err != nil || got != "oci://ghcr.io/org/charts" || host != "ghcr.io" {
		t.Fatalf("got %q %q %v", got, host, err)
	}
	if got, _, err := normalizeHelmOCIURL("oci://localhost:5000"); err != nil || got != "oci://localhost:5000" {
		t.Errorf("registry without a path: %q %v", got, err)
	}
	for _, bad := range []string{
		"", "https://ghcr.io/x", "ghcr.io/x", "oci://", "oci://u:p@ghcr.io/x", "oci://ghcr.io/x?a=1",
		"oci://ghcr.io/x#f", "oci://ghcr.io/../etc", "oci://ghcr.io/a b", "oci://ghcr.io/-x",
	} {
		if _, _, err := normalizeHelmOCIURL(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestOCIChartNames(t *testing.T) {
	list, err := normalizeOCIChartList([]string{" podinfo ", "podinfo", "", "my.chart_1"})
	if err != nil || len(list) != 2 || list[0] != "podinfo" || list[1] != "my.chart_1" {
		t.Fatalf("%v %v", list, err)
	}
	for _, bad := range [][]string{nil, {""}, {"Bad"}, {"a/b"}, {"../x"}, {strings.Repeat("a", 129)}} {
		if _, err := normalizeOCIChartList(bad); err == nil {
			t.Errorf("%v must be rejected", bad)
		}
	}
	many := make([]string, helmOCIMaxCharts+1)
	for i := range many {
		many[i] = "c" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	if _, err := normalizeOCIChartList(many); err == nil {
		t.Error("too many charts")
	}
}

func TestOCITagAndRef(t *testing.T) {
	if ociTag("1.2.3+build.5") != "1.2.3_build.5" || ociTag("1.2.3") != "1.2.3" {
		t.Error("'+' in a tag is replaced with '_'")
	}
	r := &ociRepo{URL: "oci://ghcr.io/org/charts"}
	if got := ociRef(r, "podinfo"); got != "ghcr.io/org/charts/podinfo" {
		t.Errorf("ref: %q", got)
	}
	for in, want := range map[string]bool{"localhost": true, "localhost:5000": true, "127.0.0.1:8080": true, "[::1]:5000": true, "ghcr.io": false, "10.0.0.5:5000": false} {
		if ociLoopbackHost(in) != want {
			t.Errorf("ociLoopbackHost(%q) != %v", in, want)
		}
	}
	if latestStable([]string{"2.0.0-rc.1", "1.2.0", "1.0.0"}) != "1.2.0" || latestStable([]string{"2.0.0-rc.1"}) != "2.0.0-rc.1" || latestStable(nil) != "" {
		t.Error("latestStable")
	}
}

func TestLimitTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 2<<20))
	}))
	defer srv.Close()
	b := &byteBudget{}
	b.left.Store(1 << 20)
	c := &http.Client{Transport: limitTransport{base: http.DefaultTransport, b: b}}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected a limit exceeded error: %v", err)
	}
}

// fakeRegistry отдаёт только список тегов (GET /v2/<repo>/tags/list), этого достаточно для каталога.
func fakeRegistry(t *testing.T, tags map[string][]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/v2/")
		if !strings.HasSuffix(p, "/tags/list") {
			http.NotFound(w, r)
			return
		}
		repoPath := strings.TrimSuffix(p, "/tags/list")
		list, ok := tags[repoPath]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": repoPath, "tags": list})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOCIRepoLifecycle(t *testing.T) {
	setupHelmHome(t)
	m := NewManager()
	srv := fakeRegistry(t, map[string][]string{
		"org/charts/podinfo": {"1.0.0", "1.2.0", "2.0.0-rc.1", "latest", "1.1.0_build"},
		"org/charts/redis":   {"3.0.0"},
	})
	host := strings.TrimPrefix(srv.URL, "http://")
	base := "oci://" + host + "/org/charts"

	// некорректный ввод и недоступный чарт ничего не сохраняют
	if err := m.AddHelmOCIRepo("Bad Name", base, []string{"podinfo"}, "", ""); err == nil {
		t.Error("bad name")
	}
	if err := m.AddHelmOCIRepo("demo", "https://x", []string{"podinfo"}, "", ""); err == nil {
		t.Error("bad scheme")
	}
	if err := m.AddHelmOCIRepo("demo", base, nil, "", ""); err == nil {
		t.Error("at least one chart is required")
	}
	if err := m.AddHelmOCIRepo("demo", base, []string{"podinfo", "missing"}, "", ""); err == nil {
		t.Error("a nonexistent chart must reject the whole registry")
	}
	if repos, _ := m.ListHelmRepos(); len(repos) != 0 {
		t.Fatalf("the list must be empty after failed additions: %v", repos)
	}

	if err := m.AddHelmOCIRepo("demo", base, []string{"podinfo"}, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.AddHelmOCIRepo("demo", base, []string{"podinfo"}, "", ""); err == nil {
		t.Error("duplicate addition")
	}
	if err := m.AddHelmRepo("demo", "https://charts.example.com", "", "", false); err == nil {
		t.Error("the name is taken by an OCI registry")
	}
	// В Windows Unix-прав нет (Perm() всегда 0666): проверяем только там, где они есть.
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(ociFilePath()); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("oci.yaml: %v %v", st, err)
		}
	}

	repos, err := m.ListHelmRepos()
	if err != nil || len(repos) != 1 || repos[0].Kind != "oci" || repos[0].Charts != 1 || repos[0].Updated == "" || repos[0].Error != "" {
		t.Fatalf("list: %+v %v", repos, err)
	}

	res, err := m.SearchHelmCharts("podinfo", "")
	if err != nil || res.Total != 1 || res.Items[0].Version != "1.2.0" || res.Items[0].Repo != "demo" || res.Items[0].Versions != 4 {
		t.Fatalf("search: %+v %v", res, err)
	}
	vers, err := m.GetHelmChartVersions("demo", "podinfo")
	if err != nil || len(vers) != 4 || vers[0].Version != "2.0.0-rc.1" || vers[2].Version != "1.1.0+build" {
		t.Fatalf("versions: %+v %v", vers, err)
	}
	if _, err := m.GetHelmChartVersions("demo", "redis"); err == nil {
		t.Error("chart is not added to the registry")
	}

	// добавление и удаление чарта
	if err := m.AddHelmOCIChart("demo", "redis"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddHelmOCIChart("demo", "redis"); err == nil {
		t.Error("duplicate chart")
	}
	if err := m.AddHelmOCIChart("demo", "nope"); err == nil {
		t.Error("chart without tags")
	}
	if res, _ := m.SearchHelmCharts("", "demo"); res.Total != 2 {
		t.Errorf("after adding redis: %+v", res)
	}
	if err := m.RemoveHelmOCIChart("demo", "redis"); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveHelmOCIChart("demo", "redis"); err == nil {
		t.Error("removing a chart again")
	}

	// обновление индекса тегов
	upd, err := m.UpdateHelmRepos("demo")
	if err != nil || len(upd) != 1 || upd[0].Error != "" || upd[0].Charts != 1 {
		t.Fatalf("update: %+v %v", upd, err)
	}
}

func TestOCIRemoveCleansUp(t *testing.T) {
	setupHelmHome(t)
	m := NewManager()
	srv := fakeRegistry(t, map[string][]string{"org/charts/podinfo": {"1.0.0"}})
	base := "oci://" + strings.TrimPrefix(srv.URL, "http://") + "/org/charts"
	if err := m.AddHelmOCIRepo("demo", base, []string{"podinfo"}, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveHelmRepo("demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat((&helmScope{store: fileHelmStore{}}).tagsPath("demo")); !os.IsNotExist(err) {
		t.Errorf("the tag cache must be deleted: %v", err)
	}
	if repos, _ := m.ListHelmRepos(); len(repos) != 0 {
		t.Errorf("list after deletion: %v", repos)
	}
	if err := m.RemoveHelmRepo("demo"); err == nil {
		t.Error("removing again")
	}
}

// Версия OCI-чарта участвует в поиске обновлений установленных релизов.
func TestOCIIndexesFeedUpdateFinder(t *testing.T) {
	setupHelmHome(t)
	m := NewManager()
	srv := fakeRegistry(t, map[string][]string{"org/charts/podinfo": {"2.0.0", "1.5.0", "1.0.0"}})
	base := "oci://" + strings.TrimPrefix(srv.URL, "http://") + "/org/charts"
	if err := m.AddHelmOCIRepo("demo", base, []string{"podinfo"}, "", ""); err != nil {
		t.Fatal(err)
	}
	u := m.helmUpdateFinder()("podinfo", "1.5.0")
	if u == nil || u.Latest != "2.0.0" || u.Repo != "demo" {
		t.Errorf("update: %+v", u)
	}
	if u := m.helmUpdateFinder()("podinfo", "9.9.9"); u != nil {
		t.Errorf("unknown version: %+v", u)
	}
}
