// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/24DSO-labs/kubron/internal/netpolicy"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/registry"
	repo "helm.sh/helm/v4/pkg/repo/v1"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
	"sigs.k8s.io/yaml"
)

// OCI-реестры (H6).
//
// У OCI-реестра нет index.yaml, и список чартов получить нельзя (каталог реестра не стандартизован и часто
// закрыт). Поэтому «репозиторий» Kubron типа OCI — это имя, префикс oci://host/path и ЯВНЫЙ список имён чартов.
// Версии чарта — это теги реестра (semver, «+» в теге записывается как «_»). Теги кешируются в
// ~/.kubron/helm/cache/<имя>-oci.json и обновляются кнопкой Update; поиск работает по кешу без сети.
//
// Хранилище:
//
//	oci.yaml              — репозитории (без паролей), права 0600
//	registry-config.json  — учётные данные реестров в формате docker config (пишет Helm), права 0600
//
// БЕЗОПАСНОСТЬ: имя репозитория и чарта входят в имена файлов и ссылки, поэтому проверяются строго. Суммарный
// объём ответов реестра за операцию ограничен. Незащищённый HTTP разрешён только для localhost/127.0.0.0/8/::1;
// отключения проверки TLS для OCI нет. Как и helm CLI, клиент использует учётные данные из ~/.docker/config.json.
const (
	helmOCITagsMaxBytes  = 8 << 20
	helmOCIChartMaxBytes = 64 << 20
	helmOCITimeout       = 2 * time.Minute
	helmOCIMaxTags       = 500
	helmOCIMaxCharts     = 50
)

var (
	helmOCISegmentRe = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*$`)
	errOCITooLarge   = errors.New("registry response exceeds the allowed size")
)

type ociRepo struct {
	Name      string   `json:"name"`
	URL       string   `json:"url"` // oci://host[:port][/path]
	Username  string   `json:"username,omitempty"`
	PlainHTTP bool     `json:"plainHTTP,omitempty"`
	Charts    []string `json:"charts"`
	// Password есть только у реестров из хранилища сервиса (пароль зашифрован в БД). В oci.yaml он не попадает никогда.
	Password string `json:"-"`
}

// ociFromRecord приводит запись хранилища к виду, в котором с реестром работает остальной код.
func ociFromRecord(r HelmRepoRecord) ociRepo {
	return ociRepo{Name: r.Name, URL: r.URL, Username: r.Username, PlainHTTP: r.PlainHTTP, Charts: r.Charts, Password: r.Password}
}

type ociFile struct {
	Repositories []ociRepo `json:"repositories"`
}

type ociTagCache struct {
	Updated time.Time           `json:"updated"`
	Tags    map[string][]string `json:"tags"` // чарт -> версии по убыванию
}

// ─── пути и файлы ───

func ociFilePath() string  { return filepath.Join(helmHomeDir(), "oci.yaml") }
func ociCredsPath() string { return filepath.Join(helmHomeDir(), "registry-config.json") }

func ociLoadLocked() (*ociFile, error) {
	b, err := os.ReadFile(ociFilePath())
	if errors.Is(err, os.ErrNotExist) {
		return &ociFile{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the OCI registry list: %w", err)
	}
	f := &ociFile{}
	if err := yaml.Unmarshal(b, f); err != nil {
		return nil, fmt.Errorf("oci.yaml is corrupted: %w", err)
	}
	return f, nil
}

func ociSaveLocked(f *ociFile) error {
	b, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(helmHomeDir(), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(ociFilePath(), b, 0o600)
}

func (f *ociFile) find(name string) *ociRepo {
	for i := range f.Repositories {
		if f.Repositories[i].Name == name {
			return &f.Repositories[i]
		}
	}
	return nil
}

// ociFind возвращает копию OCI-реестра по имени (для репозитория другого вида — ok == false).
func (sc *helmScope) ociFind(name string) (ociRepo, bool, error) {
	r, ok, err := sc.repo(name)
	if err != nil || !ok || r.Kind != helmKindOCI {
		return ociRepo{}, false, err
	}
	return ociFromRecord(r), true, nil
}

func (sc *helmScope) ociReadTags(name string) (ociTagCache, error) {
	var c ociTagCache
	b, err := os.ReadFile(sc.tagsPath(name))
	if err != nil {
		return c, err
	}
	err = yaml.Unmarshal(b, &c)
	return c, err
}

func (sc *helmScope) ociWriteTags(name string, c ociTagCache) error {
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(sc.cacheDir(), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(sc.tagsPath(name), b, 0o600)
}

// ─── валидация ───

// normalizeHelmOCIURL: oci://host[:port][/path] без логина, query и fragment; сегменты пути в нижнем регистре.
func normalizeHelmOCIURL(raw string) (normalized, host string, err error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(raw), "oci://") {
		return "", "", errors.New("OCI registry address must start with oci://")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid URL: %w", err)
	}
	if u.Hostname() == "" {
		return "", "", errors.New("URL has no host")
	}
	if u.User != nil {
		return "", "", errors.New("username and password are set in separate fields, not in the URL")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", "", errors.New("URL must not contain a query or fragment")
	}
	path := strings.Trim(u.Path, "/")
	if path != "" {
		for _, seg := range strings.Split(path, "/") {
			if !helmOCISegmentRe.MatchString(seg) {
				return "", "", fmt.Errorf("invalid path segment %q: use lowercase Latin letters, digits, '.', '_' and '-'", seg)
			}
		}
	}
	normalized = "oci://" + strings.ToLower(u.Host)
	if path != "" {
		normalized += "/" + path
	}
	return normalized, strings.ToLower(u.Host), nil
}

func validateHelmOCIChartName(name string) error {
	if len(name) > 128 || !helmOCISegmentRe.MatchString(name) {
		return fmt.Errorf("invalid chart name %q: use lowercase Latin letters, digits, '.', '_' and '-'", name)
	}
	return nil
}

func normalizeOCIChartList(in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if err := validateHelmOCIChartName(c); err != nil {
			return nil, err
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("specify at least one chart: an OCI registry has no chart list")
	}
	if len(out) > helmOCIMaxCharts {
		return nil, fmt.Errorf("at most %d charts per registry", helmOCIMaxCharts)
	}
	return out, nil
}

func ociLoopbackHost(hostport string) bool {
	h := hostport
	if hh, _, err := net.SplitHostPort(hostport); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func ociRef(r *ociRepo, chart string) string {
	return strings.TrimPrefix(r.URL, "oci://") + "/" + chart
}

// ociTag: «+» в версии записывается в теге как «_» (ограничение формата тегов OCI).
func ociTag(version string) string { return strings.ReplaceAll(version, "+", "_") }

// ─── клиент реестра с ограничением объёма ───

type byteBudget struct{ left atomic.Int64 }

type limitTransport struct {
	base http.RoundTripper
	b    *byteBudget
}

func (t limitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = &limitBody{rc: resp.Body, b: t.b}
	return resp, nil
}

type limitBody struct {
	rc io.ReadCloser
	b  *byteBudget
}

func (l *limitBody) Read(p []byte) (int, error) {
	n, err := l.rc.Read(p)
	if n > 0 && l.b.left.Add(-int64(n)) < 0 {
		return n, errOCITooLarge
	}
	return n, err
}

func (l *limitBody) Close() error { return l.rc.Close() }

// ociClient создаёт клиент реестра для одного запроса. Соединения и редиректы идут через политику netpolicy.
// Файловое хранилище: учётные данные берёт клиент из registry-config.json (туда их кладёт Login).
// Хранилище сервиса: пароль лежит зашифрованным в БД и передаётся клиенту из памяти, только хосту этого реестра;
// на диск он не попадает, а Docker-конфигурация сервера не читается.
func (sc *helmScope) ociClient(r *ociRepo, limit int64) (*registry.Client, error) {
	if err := os.MkdirAll(helmHomeDir(), 0o700); err != nil {
		return nil, err
	}
	b := &byteBudget{}
	b.left.Store(limit)
	pol := netpolicy.For(netpolicy.ScopeHelm)
	var base http.RoundTripper = registry.NewTransport(false)
	if pol != nil {
		base = retry.NewTransport(pol.NewTransport())
	}
	hc := &http.Client{Transport: limitTransport{base: base, b: b}, Timeout: helmOCITimeout, CheckRedirect: pol.CheckRedirect}
	opts := []registry.ClientOption{registry.ClientOptHTTPClient(hc), registry.ClientOptCredentialsFile(ociCredsPath())}
	if sc.store.CredentialsInStore() {
		az := auth.Client{Client: hc}
		az.SetUserAgent("kubron")
		if r != nil && r.Username != "" {
			az.Credential = auth.StaticCredential(ociRepoHost(*r), auth.Credential{Username: r.Username, Password: r.Password})
		}
		opts = append(opts, registry.ClientOptAuthorizer(az))
	}
	if r != nil && r.PlainHTTP {
		opts = append(opts, registry.ClientOptPlainHTTP())
	}
	return registry.NewClient(opts...)
}

// ociFetchTags возвращает версии чарта (по убыванию), не больше helmOCIMaxTags.
func (sc *helmScope) ociFetchTags(r *ociRepo, chart string) ([]string, error) {
	c, err := sc.ociClient(r, helmOCITagsMaxBytes)
	if err != nil {
		return nil, err
	}
	tags, err := c.Tags(ociRef(r, chart))
	if err != nil {
		return nil, fmt.Errorf("chart %q: %w", chart, err)
	}
	if len(tags) > helmOCIMaxTags {
		tags = tags[:helmOCIMaxTags]
	}
	return tags, nil
}

func ociRepoHost(r ociRepo) string {
	h := strings.TrimPrefix(r.URL, "oci://")
	if i := strings.Index(h, "/"); i >= 0 {
		h = h[:i]
	}
	return h
}

func ociHostInUse(list []HelmRepoRecord, host string) bool {
	for _, r := range list {
		if r.Kind == helmKindOCI && ociRepoHost(ociFromRecord(r)) == host {
			return true
		}
	}
	return false
}

func (sc *helmScope) ociLogout(host string, plainHTTP bool) {
	if c, err := sc.ociClient(&ociRepo{PlainHTTP: plainHTTP}, helmOCITagsMaxBytes); err == nil {
		_ = c.Logout(host)
	}
}

// ─── управление ───

// AddHelmOCIRepo добавляет OCI-реестр с набором чартов и проверяет доступ, получив теги каждого чарта.
func (m *Manager) AddHelmOCIRepo(name, rawURL string, charts []string, username, password string) error {
	if err := validateHelmRepoName(name); err != nil {
		return err
	}
	u, host, err := normalizeHelmOCIURL(rawURL)
	if err != nil {
		return err
	}
	list, err := normalizeOCIChartList(charts)
	if err != nil {
		return err
	}
	if password != "" && username == "" {
		return errors.New("password without a username")
	}
	sc, err := m.helmScope()
	if err != nil {
		return err
	}
	if err := sc.nameTaken(name); err != nil {
		return err
	}

	r := ociRepo{Name: name, URL: u, Username: username, PlainHTTP: ociLoopbackHost(host), Charts: list, Password: password}
	// Файловое хранилище: пароль проверяется и сохраняется входом в реестр (registry-config.json).
	// Хранилище сервиса: пароль уходит в БД зашифрованным, а доступ проверяет запрос тегов ниже.
	loggedIn := false
	if username != "" && !sc.store.CredentialsInStore() {
		c, err := sc.ociClient(&r, helmOCITagsMaxBytes)
		if err != nil {
			return err
		}
		if err := c.Login(host, registry.LoginOptBasicAuth(username, password)); err != nil {
			return fmt.Errorf("login to registry %s failed: %w", host, err)
		}
		loggedIn = true
	}
	fail := func(err error) error {
		if loggedIn {
			if rest, lerr := sc.repos(); lerr == nil && !ociHostInUse(rest, host) {
				sc.ociLogout(host, r.PlainHTTP)
			}
		}
		return err
	}

	cache := ociTagCache{Updated: time.Now().UTC(), Tags: map[string][]string{}}
	for _, ch := range list {
		tags, err := sc.ociFetchTags(&r, ch)
		if err != nil {
			return fail(fmt.Errorf("registry %q is unavailable: %w", name, err))
		}
		if len(tags) == 0 {
			return fail(fmt.Errorf("chart %q has no versions (semver tags) or access is denied", ch))
		}
		cache.Tags[ch] = tags
	}

	rec := &HelmRepoRecord{Name: name, Kind: helmKindOCI, URL: u, Username: username, PlainHTTP: r.PlainHTTP, Charts: list}
	if sc.store.CredentialsInStore() {
		rec.Password = password
	}
	if err := sc.create(rec); err != nil {
		return fail(err)
	}
	helmCacheMu.Lock()
	defer helmCacheMu.Unlock()
	if err := sc.ociWriteTags(name, cache); err != nil {
		_, _ = sc.store.DeleteRepo(sc.team, name)
		return fail(err)
	}
	return nil
}

// AddHelmOCIChart добавляет чарт в существующий OCI-реестр (после проверки, что у него есть версии).
func (m *Manager) AddHelmOCIChart(repoName, chart string) error {
	if err := validateHelmOCIChartName(chart); err != nil {
		return err
	}
	sc, err := m.helmScope()
	if err != nil {
		return err
	}
	r, ok, err := sc.ociFind(repoName)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("OCI registry %q not found", repoName)
	}
	if len(r.Charts) >= helmOCIMaxCharts {
		return fmt.Errorf("at most %d charts per registry", helmOCIMaxCharts)
	}
	tags, err := sc.ociFetchTags(&r, chart)
	if err != nil {
		return err
	}
	if len(tags) == 0 {
		return fmt.Errorf("chart %q has no versions (semver tags) or access is denied", chart)
	}

	helmCacheMu.Lock()
	defer helmCacheMu.Unlock()
	cur, ok, err := sc.ociFind(repoName) // набор чартов мог измениться, пока шёл запрос тегов
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("OCI registry %q not found", repoName)
	}
	for _, c := range cur.Charts {
		if c == chart {
			return fmt.Errorf("chart %q is already added", chart)
		}
	}
	charts := append(append([]string(nil), cur.Charts...), chart)
	sort.Strings(charts)
	cache, _ := sc.ociReadTags(repoName)
	if cache.Tags == nil {
		cache.Tags = map[string][]string{}
	}
	cache.Tags[chart] = tags
	cache.Updated = time.Now().UTC()
	if err := sc.ociWriteTags(repoName, cache); err != nil {
		return err
	}
	if err := sc.store.SetOCICharts(sc.team, repoName, charts); err != nil {
		return err
	}
	return nil
}

func (m *Manager) RemoveHelmOCIChart(repoName, chart string) error {
	if err := validateHelmOCIChartName(chart); err != nil {
		return err
	}
	sc, err := m.helmScope()
	if err != nil {
		return err
	}
	helmCacheMu.Lock()
	defer helmCacheMu.Unlock()
	cur, ok, err := sc.ociFind(repoName)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("OCI registry %q not found", repoName)
	}
	kept := make([]string, 0, len(cur.Charts))
	found := false
	for _, c := range cur.Charts {
		if c == chart {
			found = true
			continue
		}
		kept = append(kept, c)
	}
	if !found {
		return fmt.Errorf("chart %q not found in registry %q", chart, repoName)
	}
	if cache, err := sc.ociReadTags(repoName); err == nil {
		delete(cache.Tags, chart)
		_ = sc.ociWriteTags(repoName, cache)
	}
	return sc.store.SetOCICharts(sc.team, repoName, kept)
}

// ociUpdateOne обновляет теги одного реестра. Сбой одного чарта не стирает его прежние теги.
func (sc *helmScope) ociUpdateOne(rec HelmRepoRecord) HelmRepoUpdateResult {
	r := ociFromRecord(rec)
	res := HelmRepoUpdateResult{Name: r.Name}
	cache, _ := sc.ociReadTags(r.Name)
	if cache.Tags == nil {
		cache.Tags = map[string][]string{}
	}
	var errs []string
	for _, ch := range r.Charts {
		tags, err := sc.ociFetchTags(&r, ch)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		cache.Tags[ch] = tags
		res.Charts++
	}
	if res.Charts > 0 {
		cache.Updated = time.Now().UTC()
		helmCacheMu.Lock()
		// реестр могли удалить, пока шло обновление
		if _, ok, err := sc.ociFind(r.Name); err == nil && ok {
			if err := sc.ociWriteTags(r.Name, cache); err != nil {
				errs = append(errs, err.Error())
			}
		}
		helmCacheMu.Unlock()
	}
	res.Error = strings.Join(errs, "; ")
	return res
}

// ─── чтение каталога ───

// ociInfo — строка списка репозиториев для OCI-реестра (пароль наружу не отдаётся).
func (sc *helmScope) ociInfo(r HelmRepoRecord) HelmRepoInfo {
	info := HelmRepoInfo{Name: r.Name, URL: r.URL, Username: r.Username, Kind: "oci", Charts: len(r.Charts), ChartNames: r.Charts}
	if c, err := sc.ociReadTags(r.Name); err == nil {
		info.Updated = c.Updated.UTC().Format(time.RFC3339)
	} else {
		info.Error = "tags are not loaded: run Update"
	}
	return info
}

func latestStable(tags []string) string {
	for _, t := range tags {
		if v, err := semver.NewVersion(t); err == nil && v.Prerelease() == "" {
			return t
		}
	}
	if len(tags) > 0 {
		return tags[0]
	}
	return ""
}

func (sc *helmScope) ociSearch(recs []HelmRepoRecord, terms []string, repoName string) []HelmCatalogChart {
	var out []HelmCatalogChart
	for _, r := range recs {
		if r.Kind != helmKindOCI || (repoName != "" && r.Name != repoName) {
			continue
		}
		cache, err := sc.ociReadTags(r.Name)
		if err != nil {
			continue
		}
		for _, ch := range r.Charts {
			tags := cache.Tags[ch]
			if len(tags) == 0 || !helmMatches(terms, ch, "", nil) {
				continue
			}
			out = append(out, HelmCatalogChart{Repo: r.Name, Name: ch, Version: latestStable(tags), Description: "OCI: " + r.URL, Versions: len(tags)})
		}
	}
	return out
}

func (sc *helmScope) ociVersions(r ociRepo, chart string) ([]HelmChartVersionInfo, error) {
	cache, err := sc.ociReadTags(r.Name)
	if err != nil {
		return nil, fmt.Errorf("tags of registry %q are not loaded: run Update", r.Name)
	}
	tags := cache.Tags[chart]
	if len(tags) == 0 {
		return nil, fmt.Errorf("chart %q not found in registry %q", chart, r.Name)
	}
	out := make([]HelmChartVersionInfo, 0, len(tags))
	for _, t := range tags {
		out = append(out, HelmChartVersionInfo{Version: t})
	}
	return out, nil
}

// ociIndexes строит из кеша тегов «индексы» для поиска обновлений установленных релизов.
func (sc *helmScope) ociIndexes(recs []HelmRepoRecord) []helmRepoIndex {
	var out []helmRepoIndex
	for _, r := range recs {
		if r.Kind != helmKindOCI {
			continue
		}
		cache, err := sc.ociReadTags(r.Name)
		if err != nil {
			continue
		}
		idx := &repo.IndexFile{Entries: map[string]repo.ChartVersions{}}
		for ch, tags := range cache.Tags {
			cvs := make(repo.ChartVersions, 0, len(tags))
			for _, t := range tags {
				cvs = append(cvs, &repo.ChartVersion{Metadata: &chartv2.Metadata{Name: ch, Version: t}})
			}
			idx.Entries[ch] = cvs
		}
		out = append(out, helmRepoIndex{name: r.Name, idx: idx})
	}
	return out
}

// ─── загрузка чарта ───

// ociPull скачивает чарт по тегу. Pull не принимает контекст, поэтому ждём его в горутине и отпускаем
// вызывающего при отмене (загрузка завершится сама по таймауту клиента).
func (sc *helmScope) ociPull(ctx context.Context, r ociRepo, chart, version string) (*chartv2.Chart, error) {
	if err := validateHelmOCIChartName(chart); err != nil {
		return nil, err
	}
	known := false
	for _, c := range r.Charts {
		if c == chart {
			known = true
		}
	}
	if !known {
		return nil, fmt.Errorf("chart %q is not added to registry %q", chart, r.Name)
	}
	if version == "" {
		cache, err := sc.ociReadTags(r.Name)
		if err != nil || len(cache.Tags[chart]) == 0 {
			return nil, fmt.Errorf("versions of chart %q are not loaded: run Update", chart)
		}
		version = latestStable(cache.Tags[chart])
	}
	if _, err := semver.StrictNewVersion(version); err != nil {
		return nil, fmt.Errorf("invalid version %q: %w", version, err)
	}

	c, err := sc.ociClient(&r, helmOCIChartMaxBytes)
	if err != nil {
		return nil, err
	}
	ref := ociRef(&r, chart) + ":" + ociTag(version)

	type pulled struct {
		data []byte
		err  error
	}
	ch := make(chan pulled, 1)
	go func() {
		res, err := c.Pull(ref, registry.PullOptWithChart(true))
		if err != nil {
			ch <- pulled{err: fmt.Errorf("download %s: %w", ref, err)}
			return
		}
		if res.Chart == nil || len(res.Chart.Data) == 0 {
			ch <- pulled{err: fmt.Errorf("%s has no chart layer", ref)}
			return
		}
		ch <- pulled{data: res.Chart.Data}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case p := <-ch:
		if p.err != nil {
			return nil, p.err
		}
		chrt, err := loader.LoadArchive(bytes.NewReader(p.data))
		if err != nil {
			return nil, fmt.Errorf("failed to parse chart archive: %w", err)
		}
		return chrt, nil
	}
}

// loadHelmChart скачивает чарт из репозитория любого типа (HTTP или OCI).
func loadHelmChart(ctx context.Context, sc *helmScope, repoName, chartName, version string) (*chartv2.Chart, error) {
	r, isOCI, err := sc.ociFind(repoName)
	if err != nil {
		return nil, err
	}
	if isOCI {
		return sc.ociPull(ctx, r, chartName, version)
	}
	e, cv, err := lookupHelmChartVersion(sc, repoName, chartName, version)
	if err != nil {
		return nil, err
	}
	return downloadHelmChart(ctx, e, cv)
}

// helmPackageFromChart собирает карточку чарта по самому архиву (метаданные из Chart.yaml).
func helmPackageFromChart(repoName string, c *chartv2.Chart) *HelmChartPackage {
	md := c.Metadata
	pkg := &HelmChartPackage{
		Repo: repoName, Name: md.Name, Version: md.Version, AppVersion: md.AppVersion, Description: md.Description,
		Home: md.Home, Sources: md.Sources, Keywords: md.Keywords, Deprecated: md.Deprecated,
	}
	for _, mt := range md.Maintainers {
		if mt == nil {
			continue
		}
		s := mt.Name
		if mt.Email != "" {
			s += " <" + mt.Email + ">"
		}
		pkg.Maintainers = append(pkg.Maintainers, s)
	}
	for _, f := range c.Raw {
		switch {
		case f.Name == "values.yaml":
			pkg.Values = clipText(f.Data)
		case pkg.Readme == "" && strings.EqualFold(f.Name, "README.md"):
			pkg.Readme = clipText(f.Data)
		}
	}
	return pkg
}

func (sc *helmScope) ociPackage(r ociRepo, chart, version string) (*HelmChartPackage, error) {
	if version == "" {
		if cache, err := sc.ociReadTags(r.Name); err == nil {
			version = latestStable(cache.Tags[chart])
		}
	}
	key := sc.key(r.Name) + "/" + chart + "/" + version + "/oci"
	helmPkgMu.Lock()
	cached := helmPkgCache[key]
	helmPkgMu.Unlock()
	if cached != nil {
		return cached, nil
	}
	c, err := sc.ociPull(context.Background(), r, chart, version)
	if err != nil {
		return nil, err
	}
	pkg := helmPackageFromChart(r.Name, c)
	pkg.Name = chart // имя из реестра, а не из Chart.yaml: так чарт ищется в каталоге
	helmPkgMu.Lock()
	if _, ok := helmPkgCache[key]; !ok {
		helmPkgCache[key] = pkg
		helmPkgOrder = append(helmPkgOrder, key)
		if len(helmPkgOrder) > helmPackageCacheSize {
			delete(helmPkgCache, helmPkgOrder[0])
			helmPkgOrder = helmPkgOrder[1:]
		}
	}
	helmPkgMu.Unlock()
	return pkg, nil
}
