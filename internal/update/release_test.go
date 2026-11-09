// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const testRepo = "acme/kubron"

// releaseJSON — ответ GitHub с двумя файлами; base — адрес тестового «github.com».
func releaseJSON(base, tag string) string {
	dl := base + "/" + testRepo + "/releases/download/" + tag + "/"
	return `{
  "tag_name": "` + tag + `",
  "html_url": "` + base + `/` + testRepo + `/releases/tag/` + tag + `",
  "draft": false, "prerelease": false,
  "published_at": "2026-11-08T10:00:00Z",
  "assets": [
    {"name": "Kubron_` + tag + `_linux-amd64.tar.gz", "browser_download_url": "` + dl + `Kubron_` + tag + `_linux-amd64.tar.gz", "size": 1234},
    {"name": "SHA256SUMS.txt", "browser_download_url": "` + dl + `SHA256SUMS.txt", "size": 99}
  ]
}`
}

func newTestClient(srv *httptest.Server) *Client {
	return &Client{APIBase: srv.URL, WebBase: srv.URL, Repo: testRepo, UserAgent: "kubron/test"}
}

func TestLatestParsesReleaseAndSendsRequiredHeaders(t *testing.T) {
	var gotUA, gotAccept, gotPath string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotAccept, gotPath = r.Header.Get("User-Agent"), r.Header.Get("Accept"), r.URL.Path
		w.Header().Set("ETag", `"abc"`)
		_, _ = w.Write([]byte(releaseJSON(srv.URL, "v1.3.0")))
	}))
	defer srv.Close()

	rel, err := newTestClient(srv).Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/repos/"+testRepo+"/releases/latest" {
		t.Errorf("path = %q", gotPath)
	}
	if gotUA != "kubron/test" {
		t.Errorf("GitHub rejects requests without a User-Agent: got %q", gotUA)
	}
	if !strings.Contains(gotAccept, "application/vnd.github+json") {
		t.Errorf("Accept = %q", gotAccept)
	}
	if rel.Version != "1.3.0" || rel.Tag != "v1.3.0" {
		t.Errorf("release = %+v", rel)
	}
	if !strings.HasSuffix(rel.URL, "/releases/tag/v1.3.0") {
		t.Errorf("release URL = %q", rel.URL)
	}
	if len(rel.Assets) != 2 {
		t.Errorf("assets = %+v", rel.Assets)
	}
}

func TestLatestUsesETagAndReturnsCachedReleaseOn304(t *testing.T) {
	var calls, conditional atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("If-None-Match") == `"v1"` {
			conditional.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte(releaseJSON(srv.URL, "v1.3.0")))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	first, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if conditional.Load() != 1 || calls.Load() != 2 {
		t.Errorf("calls=%d conditional=%d, want the second request to be conditional", calls.Load(), conditional.Load())
	}
	if second.Version != first.Version {
		t.Errorf("a 304 must return the cached release: %+v", second)
	}
}

func TestLatestErrorCodes(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    Error
	}{
		{"not found", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }, ErrNotFound},
		{"429", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(429) }, ErrRateLimited},
		{"403 with exhausted quota", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(403)
		}, ErrRateLimited},
		{"403 secondary limit", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(403)
		}, ErrRateLimited},
		{"403 for another reason", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }, ErrUnexpected},
		{"500", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }, ErrUnexpected},
		{"not json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }, ErrUnexpected},
		{"too large", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"tag_name":"v1.0.0","pad":"` + strings.Repeat("x", maxReleaseBody) + `"}`))
		}, ErrUnexpected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			_, err := newTestClient(srv).Latest(context.Background())
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLatestNetworkErrorHidesDetails(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	c := newTestClient(srv)
	srv.Close() // соединение отклоняется

	_, err := c.Latest(context.Background())
	if !errors.Is(err, ErrNetwork) {
		t.Fatalf("err = %v, want %v", err, ErrNetwork)
	}
	if strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("the error leaks the address: %v", err)
	}
}

func TestLatestRejectsDraftsPrereleasesAndBadTags(t *testing.T) {
	for name, body := range map[string]string{
		"draft":       `{"tag_name":"v1.3.0","draft":true}`,
		"prerelease":  `{"tag_name":"v1.3.0","prerelease":true}`,
		"not semver":  `{"tag_name":"nightly"}`,
		"partial":     `{"tag_name":"v1.3"}`,
		"shell chars": `{"tag_name":"v1.3.0;reboot"}`,
		"spaces":      `{"tag_name":"v1.3.0 x"}`,
		"empty":       `{"tag_name":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			if _, err := newTestClient(srv).Latest(context.Background()); !errors.Is(err, ErrUnexpected) {
				t.Errorf("err = %v, want %v", err, ErrUnexpected)
			}
		})
	}
}

// Ссылки и имена файлов из ответа попадают в интерфейс и в команды: чужое отбрасывается.
func TestConvertDropsForeignLinksAndUnsafeAssetNames(t *testing.T) {
	c := &Client{WebBase: "https://github.com", Repo: "24DSO-lab/kubron"}
	rel, err := c.convert(apiRelease{
		TagName: "v1.3.0",
		HTMLURL: "https://evil.example/24DSO-lab/kubron/releases/tag/v1.3.0",
		Assets: []apiAsset{
			{Name: "Kubron_v1.3.0_linux-amd64.tar.gz", URL: "https://github.com/24DSO-lab/kubron/releases/download/v1.3.0/Kubron_v1.3.0_linux-amd64.tar.gz"},
			{Name: "kubron_1.3.0_amd64.deb", URL: "https://evil.example/kubron_1.3.0_amd64.deb"},
			{Name: "bad name; rm -rf.deb", URL: "https://github.com/24DSO-lab/kubron/releases/download/v1.3.0/x"},
			{Name: "../escape", URL: "https://github.com/24DSO-lab/kubron/releases/download/v1.3.0/x"},
			{Name: "other.zip", URL: "https://github.com/other/repo/releases/download/v1.3.0/other.zip"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rel.URL != "https://github.com/24DSO-lab/kubron/releases/tag/v1.3.0" {
		t.Errorf("a foreign html_url must be replaced by the repository page, got %q", rel.URL)
	}
	if len(rel.Assets) != 1 || rel.Assets[0].Name != "Kubron_v1.3.0_linux-amd64.tar.gz" {
		t.Errorf("assets = %+v, want only the safe one from this repository", rel.Assets)
	}
}

func TestConvertAcceptsGitHubURLCaseInsensitively(t *testing.T) {
	c := &Client{WebBase: "https://github.com", Repo: "24DSO-lab/kubron"}
	rel, err := c.convert(apiRelease{TagName: "v1.3.0", HTMLURL: "https://github.com/24dso-lab/kubron/releases/tag/v1.3.0"})
	if err != nil {
		t.Fatal(err)
	}
	if rel.URL != "https://github.com/24dso-lab/kubron/releases/tag/v1.3.0" {
		t.Errorf("URL = %q", rel.URL)
	}
}
