// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/24DSO-labs/kubron/internal/netpolicy"
)

// memHelmStore — хранилище состояния Helm в памяти с изоляцией по командам, как у PostgreSQL.
type memHelmStore struct {
	mu    sync.Mutex
	repos map[string][]HelmRepoRecord // команда -> репозитории
	ops   map[string][]HelmOperationRecord
}

func newMemHelmStore() *memHelmStore {
	return &memHelmStore{repos: map[string][]HelmRepoRecord{}, ops: map[string][]HelmOperationRecord{}}
}

func (*memHelmStore) CredentialsInStore() bool { return true }

func (s *memHelmStore) ListRepos(team string) ([]HelmRepoRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]HelmRepoRecord(nil), s.repos[team]...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *memHelmStore) CreateRepo(team string, r *HelmRepoRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.repos[team] {
		if x.Name == r.Name {
			return ErrHelmRepoExists
		}
	}
	s.repos[team] = append(s.repos[team], *r)
	return nil
}

func (s *memHelmStore) SetOCICharts(team, name string, charts []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.repos[team] {
		if s.repos[team][i].Name == name {
			s.repos[team][i].Charts = append([]string(nil), charts...)
			return nil
		}
	}
	return ErrHelmNotFound
}

func (s *memHelmStore) DeleteRepo(team, name string) (*HelmRepoRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.repos[team] {
		if x.Name == name {
			s.repos[team] = append(s.repos[team][:i:i], s.repos[team][i+1:]...)
			x.Password = ""
			return &x, nil
		}
	}
	return nil, ErrHelmNotFound
}

func (s *memHelmStore) AddOperation(team, _ string, r HelmOperationRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops[team] = append([]HelmOperationRecord{r}, s.ops[team]...)
	return nil
}

func (s *memHelmStore) ListOperations(team string) ([]HelmOperationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]HelmOperationRecord(nil), s.ops[team]...), nil
}

func (s *memHelmStore) GetOperation(team, id string) (*HelmOperationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.ops[team] {
		if r.ID == id {
			return &r, nil
		}
	}
	return nil, ErrHelmNotFound
}

func (s *memHelmStore) ClearOperations(team string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.ops, team)
	return nil
}

// serviceMode включает хранилище сервиса и два кластера: red-1 принадлежит команде red, blue-1 — команде blue.
func serviceMode(t *testing.T) (red, blue *Manager, store *memHelmStore) {
	t.Helper()
	setupHelmHome(t)
	cs := newMemClusterStore()
	for id, team := range map[string]string{"red-1": "red", "blue-1": "blue"} {
		if err := cs.Save(&ClusterMeta{ID: id, DisplayName: id, TeamID: team}, []byte("kc")); err != nil {
			t.Fatal(err)
		}
	}
	useClusterStore(t, cs)
	store = newMemHelmStore()
	SetHelmStateStore(store)
	t.Cleanup(func() { SetHelmStateStore(nil); netpolicy.Set(netpolicy.ScopeHelm, nil) })
	red, blue = NewManager(), NewManager()
	red.activeCtx, blue.activeCtx = "red-1", "blue-1"
	return red, blue, store
}

func chartIndex(chart string) string {
	return "apiVersion: v1\nentries:\n  " + chart + ":\n    - name: " + chart + "\n      version: 1.0.0\n      urls: [" + chart + "-1.0.0.tgz]\n"
}

// chartRepo отдаёт index.yaml с одним чартом и запоминает заголовки и число запросов.
type chartRepo struct {
	*httptest.Server
	hits atomic.Int32
	auth atomic.Value // последний Authorization
}

func newChartRepo(t *testing.T, chart string) *chartRepo {
	t.Helper()
	rs := &chartRepo{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.hits.Add(1)
		rs.auth.Store(r.Header.Get("Authorization"))
		if r.URL.Path == "/index.yaml" {
			_, _ = w.Write([]byte(chartIndex(chart)))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func repoNames(t *testing.T, m *Manager) []string {
	t.Helper()
	list, err := m.ListHelmRepos()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range list {
		out = append(out, r.Name)
	}
	return out
}

func searchNames(t *testing.T, m *Manager) []string {
	t.Helper()
	res, err := m.SearchHelmCharts("", "")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range res.Items {
		out = append(out, c.Repo+"/"+c.Name)
	}
	return out
}

// Команды не видят репозитории друг друга; одно и то же имя у двух команд — два разных репозитория со своим кэшем.
func TestHelmReposAreIsolatedByTeam(t *testing.T) {
	red, blue, _ := serviceMode(t)
	redSrv, blueSrv := newChartRepo(t, "red-app"), newChartRepo(t, "blue-app")

	if err := red.AddHelmRepo("stable", redSrv.URL, "", "", false); err != nil {
		t.Fatal(err)
	}
	if got := repoNames(t, blue); len(got) != 0 {
		t.Fatalf("blue must not see red's repositories: %v", got)
	}
	if err := blue.AddHelmRepo("stable", blueSrv.URL, "", "", false); err != nil {
		t.Fatalf("the same name in another team is a different repository: %v", err)
	}
	if err := red.AddHelmRepo("stable", redSrv.URL, "", "", false); err == nil {
		t.Error("a duplicate name within a team must be rejected")
	}

	// Кэш индексов и пакетов у каждой команды свой: каталоги не смешиваются.
	if got := searchNames(t, red); len(got) != 1 || got[0] != "stable/red-app" {
		t.Errorf("red catalog: %v", got)
	}
	if got := searchNames(t, blue); len(got) != 1 || got[0] != "stable/blue-app" {
		t.Errorf("blue catalog: %v", got)
	}
	if v, err := blue.GetHelmChartVersions("stable", "red-app"); err == nil {
		t.Errorf("blue must not see a chart of red's repository: %v", v)
	}
	dirs := map[string]bool{}
	for team, m := range map[string]*Manager{"red": red, "blue": blue} {
		sc, err := m.helmScope()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(sc.indexPath("stable")); err != nil {
			t.Errorf("the index of %s must be cached in its own directory: %v", team, err)
		}
		if !strings.Contains(sc.indexPath("stable"), string(filepath.Separator)+team+string(filepath.Separator)) {
			t.Errorf("the cache path of %s must contain the team: %s", team, sc.indexPath("stable"))
		}
		dirs[sc.indexPath("stable")] = true
	}
	if len(dirs) != 2 {
		t.Error("two teams with a repository of the same name must not share a cache file")
	}

	// Обновление и удаление действуют только на свою команду.
	if res, err := blue.UpdateHelmRepos("stable"); err != nil || len(res) != 1 || res[0].Error != "" {
		t.Errorf("update: %+v %v", res, err)
	}
	if err := blue.RemoveHelmRepo("stable"); err != nil {
		t.Fatal(err)
	}
	if got := repoNames(t, red); len(got) != 1 {
		t.Errorf("removing in blue must not touch red: %v", got)
	}
	if err := blue.RemoveHelmRepo("stable"); err == nil {
		t.Error("blue has nothing left to remove: a foreign name is «not found»")
	}
	if got := searchNames(t, red); len(got) != 1 {
		t.Errorf("red catalog after blue's removal: %v", got)
	}
}

func TestHelmServiceModeNeedsAnActiveCluster(t *testing.T) {
	_, _, _ = serviceMode(t)
	if _, err := NewManager().ListHelmRepos(); err == nil || !strings.Contains(err.Error(), "no active connection") {
		t.Errorf("without a cluster there is no team and no state: %v", err)
	}
}

func TestHelmJournalIsIsolatedByTeam(t *testing.T) {
	red, blue, store := serviceMode(t)
	_, tr, err := red.beginHelmOp(t.Context(), "op-red-1", "install", "apps", "web", false)
	if err != nil {
		t.Fatal(err)
	}
	tr.finish(nil)

	if got, err := red.ListHelmOperations(HelmOperationQuery{}); err != nil || len(got) != 1 || got[0].Release != "web" {
		t.Fatalf("red journal: %+v %v", got, err)
	}
	if got, err := blue.ListHelmOperations(HelmOperationQuery{}); err != nil || len(got) != 0 {
		t.Errorf("blue must not see red's operations: %+v %v", got, err)
	}
	if _, err := blue.GetHelmOperation("op-red-1"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("a foreign operation ID is «not found»: %v", err)
	}
	if _, err := red.GetHelmOperation("op-red-1"); err != nil {
		t.Errorf("own operation: %v", err)
	}
	if err := blue.ClearHelmOperations(); err != nil {
		t.Fatal(err)
	}
	if len(store.ops["red"]) != 1 {
		t.Error("clearing blue's journal must not touch red's")
	}
}

// Пароли репозиториев не возвращаются через API и не попадают в журнал и в ошибки.
func TestHelmSecretsAreNeverExposed(t *testing.T) {
	red, _, store := serviceMode(t)
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const secret = "hunter2-very-secret"
	srv := newChartRepo(t, "app")
	if err := red.AddHelmRepo("private", srv.URL, "ci", secret, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := srv.auth.Load().(string); !strings.HasPrefix(got, "Basic ") {
		t.Fatalf("the index request must carry the credentials: %q", got)
	}
	if store.repos["red"][0].Password != secret {
		t.Fatal("the password must reach the store (it encrypts it)")
	}

	list, err := red.ListHelmRepos()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(list)
	if bytes.Contains(raw, []byte(secret)) || bytes.Contains(bytes.ToLower(raw), []byte("password")) {
		t.Errorf("the repository list leaks the password: %s", raw)
	}
	if list[0].Username != "ci" {
		t.Errorf("the username stays visible: %+v", list[0])
	}

	// Ошибки и логи при сбое: репозиторий, который отвечает отказом, тоже не раскрывает пароль.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer bad.Close()
	err = red.AddHelmRepo("denied", strings.Replace(bad.URL, "http://", "http://ci:"+secret+"@", 1), "ci", secret, false)
	if err == nil {
		t.Fatal("a repository that answers 401 must be rejected")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the error leaks the password: %v", err)
	}
	if strings.Contains(logs.String(), secret) {
		t.Errorf("the log leaks the password: %s", logs.String())
	}
}

// Профиль strict: сервер не ходит на loopback и в частные сети, в том числе по редиректу; ответ не отличается от
// обычной недоступности, а в хранилище ничего не попадает.
func TestHelmNetworkPolicyBlocksSSRF(t *testing.T) {
	red, _, store := serviceMode(t)
	srv := newChartRepo(t, "app")
	netpolicy.Set(netpolicy.ScopeHelm, netpolicy.New("helm", netpolicy.ProfileStrict, nil))

	err := red.AddHelmRepo("local", srv.URL, "", "", false)
	if err == nil {
		t.Fatal("loopback must be blocked in the strict profile")
	}
	if !netpolicy.IsBlocked(err) {
		t.Errorf("the failure must come from the policy: %v", err)
	}
	if msg := strings.ToLower(err.Error()); strings.Contains(msg, "policy") || strings.Contains(msg, "blocked") || !strings.Contains(msg, "connection refused") {
		t.Errorf("a block must look like an ordinary refused connection: %v", err)
	}
	if srv.hits.Load() != 0 {
		t.Error("the repository must not receive a request")
	}
	if len(store.repos["red"]) != 0 {
		t.Error("nothing may be saved for an unreachable repository")
	}

	// Редирект с разрешённого адреса на metadata-сервис проверяется заново.
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer meta.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer redir.Close()
	netpolicy.Set(netpolicy.ScopeHelm, netpolicy.New("helm", netpolicy.ProfileStrict, mustPrefixes(t, "127.0.0.0/8")))
	if err := red.AddHelmRepo("redir", redir.URL, "", "", false); err == nil || !netpolicy.IsBlocked(err) {
		t.Errorf("a redirect to the metadata address must be blocked: %v", err)
	}

	// Исключение оператора открывает свою сеть.
	if err := red.AddHelmRepo("ok", srv.URL, "", "", false); err != nil {
		t.Errorf("an address from the allow list must work: %v", err)
	}
	// Схемы, отличные от http(s), отклоняются.
	for _, u := range []string{"file:///etc/passwd", "gopher://127.0.0.1:70/"} {
		if err := red.AddHelmRepo("x", u, "", "", false); err == nil {
			t.Errorf("%s must be rejected", u)
		}
	}
}

func mustPrefixes(t *testing.T, s string) []netip.Prefix {
	t.Helper()
	p, err := netpolicy.ParseAllowList(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// OCI в сервисе: пароль приходит из хранилища, уходит реестру в заголовке и на диск не пишется
// (registry-config.json не создаётся, Docker-конфигурация сервера не читается).
func TestOCICredentialsStayInMemory(t *testing.T) {
	red, _, store := serviceMode(t)
	inner := fakeRegistry(t, map[string][]string{"org/charts/podinfo": {"1.0.0"}})
	target, _ := url.Parse(inner.URL)
	var seen atomic.Value
	proxy := httputil.NewSingleHostReverseProxy(target)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		seen.Store(user + ":" + pass)
		if !ok || user != "bot" || pass != "pa55" {
			w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer front.Close()
	base := "oci://" + strings.TrimPrefix(front.URL, "http://") + "/org/charts"

	if err := red.AddHelmOCIRepo("reg", base, []string{"podinfo"}, "bot", "wrong"); err == nil {
		t.Fatal("a wrong password must be rejected")
	}
	if len(store.repos["red"]) != 0 {
		t.Fatal("a registry that rejects the credentials must not be saved")
	}
	if err := red.AddHelmOCIRepo("reg", base, []string{"podinfo"}, "bot", "pa55"); err != nil {
		t.Fatal(err)
	}
	if got := store.repos["red"][0]; got.Password != "pa55" || got.Kind != helmKindOCI {
		t.Errorf("the store receives the password to encrypt it: %+v", got)
	}
	if _, err := os.Stat(ociCredsPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("registry-config.json must not be created in the service: %v", err)
	}
	if res, err := red.UpdateHelmRepos("reg"); err != nil || res[0].Error != "" || res[0].Charts != 1 {
		t.Errorf("update with stored credentials: %+v %v", res, err)
	}
	list, _ := red.ListHelmRepos()
	if raw, _ := json.Marshal(list); bytes.Contains(raw, []byte("pa55")) {
		t.Errorf("the list leaks the registry password: %s", raw)
	}
}

func TestFileStoreKeepsOCIPasswordOutOfOCIYAML(t *testing.T) {
	setupHelmHome(t)
	r := ociRepo{Name: "x", URL: "oci://h/p", Username: "u", Password: "never-written"}
	b, err := json.Marshal(r)
	if err != nil || bytes.Contains(b, []byte("never-written")) {
		t.Errorf("the password must not be serialized: %s %v", b, err)
	}
}
