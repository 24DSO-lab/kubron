// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/24DSO-labs/kubron/internal/netpolicy"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	repo "helm.sh/helm/v4/pkg/repo/v1"
	"sigs.k8s.io/yaml"
)

// Репозитории чартов (H3). Хранилище — ~/.kubron/helm/ (отдельно от ~/.config/helm пользователя):
//
//	repositories.yaml  — формат Helm (repo.File), права 0600: в нём лежит пароль, как и у helm CLI;
//	cache/<имя>-index.yaml — скачанные index.yaml.
//
// БЕЗОПАСНОСТЬ: имя репозитория входит в имя файла, поэтому проверяется строго (validateHelmRepoName).
// Учётные данные уходят только на хост репозитория (не на хост, куда ведёт ссылка из index.yaml
// или редирект). Размеры index.yaml и архива чарта ограничены.
const (
	helmIndexMaxBytes    = 64 << 20
	helmChartMaxBytes    = 32 << 20
	helmRepoHTTPTimeout  = 30 * time.Second
	helmSearchMaxResults = 500
	helmPackageTextMax   = 1 << 20
	helmUpdateParallel   = 4
	helmPackageCacheSize = 16
)

// helmHomeDir — каталог данных Helm в Kubron; переменная нужна тестам.
var helmHomeDir = func() string { return filepath.Join(GetKubronDir(), "helm") }

var (
	helmRepoMu     sync.Mutex // сериализует read-modify-write файлов состояния (fileHelmStore); хранилище целиком под ним, наружу не вызывать
	helmCacheMu    sync.Mutex // сериализует подмену файлов кэша (индексы, теги); под ним можно обращаться к хранилищу
	helmRepoNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9_.-]{0,61}[a-z0-9])?$`)

	helmIndexMu    sync.Mutex
	helmIndexCache = map[string]helmCachedIndex{}

	helmPkgMu    sync.Mutex
	helmPkgCache = map[string]*HelmChartPackage{}
	helmPkgOrder []string
)

type helmCachedIndex struct {
	mod time.Time
	idx *repo.IndexFile
}

// ─── типы для UI ───

type HelmRepoInfo struct {
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	Username    string   `json:"username"`
	InsecureTLS bool     `json:"insecureTLS"`
	Charts      int      `json:"charts"`
	Updated     string   `json:"updated"`    // RFC3339; "" — индекс ещё не скачан
	Error       string   `json:"error"`      // почему индекс не читается
	Kind        string   `json:"kind"`       // http | oci
	ChartNames  []string `json:"chartNames"` // только для oci: добавленные чарты
}

type HelmRepoUpdateResult struct {
	Name   string `json:"name"`
	Charts int    `json:"charts"`
	Error  string `json:"error"`
}

type HelmCatalogChart struct {
	Repo        string `json:"repo"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	AppVersion  string `json:"appVersion"`
	Description string `json:"description"`
	Deprecated  bool   `json:"deprecated"`
	Versions    int    `json:"versions"`
}

type HelmCatalogResult struct {
	Items []HelmCatalogChart `json:"items"`
	Total int                `json:"total"` // найдено всего (Items может быть обрезан)
}

type HelmChartVersionInfo struct {
	Version     string `json:"version"`
	AppVersion  string `json:"appVersion"`
	Created     string `json:"created"`
	Description string `json:"description"`
	Deprecated  bool   `json:"deprecated"`
}

type HelmChartPackage struct {
	Repo        string   `json:"repo"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	AppVersion  string   `json:"appVersion"`
	Description string   `json:"description"`
	Home        string   `json:"home"`
	Sources     []string `json:"sources"`
	Maintainers []string `json:"maintainers"`
	Keywords    []string `json:"keywords"`
	Deprecated  bool     `json:"deprecated"`
	Readme      string   `json:"readme"`
	Values      string   `json:"values"`
}

// ─── валидация ───

func validateHelmRepoName(name string) error {
	if !helmRepoNameRe.MatchString(name) {
		return fmt.Errorf("invalid repository name %q: use lowercase Latin letters, digits, '-', '_' and '.' (up to 63 characters)", name)
	}
	return nil
}

// normalizeHelmRepoURL допускает только http(s) без логина в адресе, query и fragment.
func normalizeHelmRepoURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(raw), "oci://") {
		return "", errors.New("for oci:// use 'Add OCI registry'")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("repository URL must start with http:// or https://")
	}
	if u.Host == "" || u.Hostname() == "" {
		return "", errors.New("URL has no host")
	}
	if u.User != nil {
		return "", errors.New("username and password are set in separate fields, not in the URL")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("URL must not contain a query or fragment")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// ─── файлы ───

func helmRepoFilePath() string { return filepath.Join(helmHomeDir(), "repositories.yaml") }
func helmCacheDir() string     { return filepath.Join(helmHomeDir(), "cache") }

// helmIndexPath вызывается только с проверенным именем.
func helmIndexPath(name string) string {
	return filepath.Join(helmCacheDir(), name+"-index.yaml")
}

// loadHelmRepoFile читает repositories.yaml; отсутствие файла — пустой список.
func loadHelmRepoFile() (*repo.File, error) {
	b, err := os.ReadFile(helmRepoFilePath())
	if errors.Is(err, os.ErrNotExist) {
		return repo.NewFile(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the repository list: %w", err)
	}
	f := repo.NewFile()
	if err := yaml.Unmarshal(b, f); err != nil {
		return nil, fmt.Errorf("repositories.yaml is corrupted: %w", err)
	}
	return f, nil
}

func saveHelmRepoFile(f *repo.File) error {
	f.Generated = time.Now()
	b, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	// writeFileAtomic (cluster_settings.go) каталог не создаёт
	if err := os.MkdirAll(helmHomeDir(), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(helmRepoFilePath(), b, 0o600)
}

// ─── сеть ───

func sameHost(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	return err1 == nil && err2 == nil && strings.EqualFold(ua.Host, ub.Host) && ua.Scheme == ub.Scheme
}

// helmGet скачивает URL с ограничением размера. Basic-авторизация отправляется только на хост репозитория;
// Go сам убирает заголовок Authorization при редиректе на другой домен.
// Соединения и редиректы проходят через политику netpolicy (сервис: профиль strict, SSRF); в десктопе её нет.
func helmGet(ctx context.Context, e *repo.Entry, rawURL string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, helmRepoHTTPTimeout)
	defer cancel()

	pol := netpolicy.For(netpolicy.ScopeHelm)
	if err := pol.CheckURL(rawURL); err != nil {
		return nil, fmt.Errorf("request to %s: %w", redactURLUser(rawURL), err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if e.Username != "" && sameHost(rawURL, e.URL) {
		req.SetBasicAuth(e.Username, e.Password)
	}
	tr := pol.NewTransport()
	if e.InsecureSkipTLSVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // явный выбор пользователя в настройках репозитория
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport: tr,
		Timeout:   helmRepoHTTPTimeout,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			// каждый переход проверяется политикой заново (редирект на внутренний адрес)
			if err := pol.CheckRedirect(r, via); err != nil {
				return err
			}
			// https -> http отправил бы пароль открытым текстом
			if r.URL.Scheme == "http" && via[len(via)-1].URL.Scheme == "https" {
				return errors.New("redirect from https to http is not allowed")
			}
			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to %s: %w", redactURLUser(rawURL), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", redactURLUser(rawURL), resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", redactURLUser(rawURL), err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response from %s is larger than %d MiB", redactURLUser(rawURL), limit>>20)
	}
	return data, nil
}

func redactURLUser(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(invalid URL)"
	}
	u.User = nil
	return u.String()
}

// entryOf превращает запись хранилища в repo.Entry (тип Helm для HTTP-запросов).
func entryOf(r HelmRepoRecord) *repo.Entry {
	return &repo.Entry{Name: r.Name, URL: r.URL, Username: r.Username, Password: r.Password, InsecureSkipTLSVerify: r.InsecureTLS}
}

// downloadHelmIndex скачивает index.yaml во временный файл и проверяет его разбором.
// Подмена рабочего индекса — отдельным шагом (commitHelmIndex), под мьютексом.
func downloadHelmIndex(ctx context.Context, sc *helmScope, e *repo.Entry) (tmpPath string, idx *repo.IndexFile, err error) {
	data, err := helmGet(ctx, e, e.URL+"/index.yaml", helmIndexMaxBytes)
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(sc.cacheDir(), 0o700); err != nil {
		return "", nil, err
	}
	tmp, err := os.CreateTemp(sc.cacheDir(), ".idx-*")
	if err != nil {
		return "", nil, err
	}
	tmpPath = tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmpPath)
		return "", nil, errors.Join(werr, cerr)
	}
	idx, err = repo.LoadIndexFile(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", nil, fmt.Errorf("index.yaml is not a Helm repository index: %w", err)
	}
	return tmpPath, idx, nil
}

// commitHelmIndex подменяет рабочий индекс; вызывать под helmCacheMu.
func commitHelmIndex(sc *helmScope, name, tmpPath string) error {
	if err := os.Rename(tmpPath, sc.indexPath(name)); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	helmIndexMu.Lock()
	delete(helmIndexCache, sc.key(name))
	helmIndexMu.Unlock()
	return nil
}

// loadHelmIndex отдаёт индекс репозитория из памяти (по mtime файла) или с диска.
func loadHelmIndex(sc *helmScope, name string) (*repo.IndexFile, time.Time, error) {
	path := sc.indexPath(name)
	st, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	key := sc.key(name)
	helmIndexMu.Lock()
	c, ok := helmIndexCache[key]
	helmIndexMu.Unlock()
	if ok && c.mod.Equal(st.ModTime()) {
		return c.idx, c.mod, nil
	}
	idx, err := repo.LoadIndexFile(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	helmIndexMu.Lock()
	helmIndexCache[key] = helmCachedIndex{mod: st.ModTime(), idx: idx}
	helmIndexMu.Unlock()
	return idx, st.ModTime(), nil
}

func countHelmCharts(idx *repo.IndexFile) int {
	n := 0
	for _, v := range idx.Entries {
		if len(v) > 0 {
			n++
		}
	}
	return n
}

// ─── управление репозиториями ───

func (m *Manager) ListHelmRepos() ([]HelmRepoInfo, error) {
	sc, err := m.helmScope()
	if err != nil {
		return nil, err
	}
	recs, err := sc.repos()
	if err != nil {
		return nil, err
	}
	out := make([]HelmRepoInfo, 0, len(recs))
	for _, r := range recs {
		if r.Kind == helmKindOCI {
			out = append(out, sc.ociInfo(r))
			continue
		}
		info := HelmRepoInfo{Name: r.Name, URL: r.URL, Username: r.Username, InsecureTLS: r.InsecureTLS, Kind: "http"}
		idx, mod, err := loadHelmIndex(sc, r.Name)
		switch {
		case err == nil:
			info.Charts = countHelmCharts(idx)
			info.Updated = mod.UTC().Format(time.RFC3339)
		case errors.Is(err, os.ErrNotExist):
			info.Error = "index is not downloaded: run Update"
			sc.warmIndex(r.Name) // сервис: кэш этой реплики докачается в фоне, следующий список его покажет
		default:
			info.Error = err.Error()
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// AddHelmRepo добавляет репозиторий и сразу скачивает индекс: при ошибке ничего не сохраняется.
func (m *Manager) AddHelmRepo(name, rawURL, username, password string, insecureTLS bool) error {
	if err := validateHelmRepoName(name); err != nil {
		return err
	}
	u, err := normalizeHelmRepoURL(rawURL)
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
	rec := &HelmRepoRecord{Name: name, Kind: helmKindHTTP, URL: u, Username: username, Password: password, InsecureTLS: insecureTLS}

	tmp, _, err := downloadHelmIndex(context.Background(), sc, entryOf(*rec))
	if err != nil {
		return fmt.Errorf("repository %q is unavailable: %w", name, err)
	}
	// Имя занимает сама запись (атомарно, на стороне хранилища): между проверкой и записью список мог измениться.
	if err := sc.create(rec); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	helmCacheMu.Lock()
	err = commitHelmIndex(sc, name, tmp)
	helmCacheMu.Unlock()
	if err != nil {
		_, _ = sc.store.DeleteRepo(sc.team, name)
		return err
	}
	return nil
}

func (m *Manager) RemoveHelmRepo(name string) error {
	if err := validateHelmRepoName(name); err != nil {
		return err
	}
	sc, err := m.helmScope()
	if err != nil {
		return err
	}
	return sc.remove(name)
}

// remove удаляет репозиторий любого вида вместе с его кэшем.
func (sc *helmScope) remove(name string) error {
	// имена файлов кэша зависят от URL: считаем их до удаления записи
	indexFile, tagsFile, memKey := sc.indexPath(name), sc.tagsPath(name), sc.key(name)
	removed, err := sc.store.DeleteRepo(sc.team, name)
	sc.invalidate()
	if errors.Is(err, ErrHelmNotFound) {
		return fmt.Errorf("repository %q not found", name)
	}
	if err != nil {
		return err
	}
	helmCacheMu.Lock()
	_ = os.Remove(indexFile)
	_ = os.Remove(tagsFile)
	helmCacheMu.Unlock()
	helmIndexMu.Lock()
	delete(helmIndexCache, memKey)
	helmIndexMu.Unlock()
	if removed.Kind == helmKindOCI && removed.Username != "" && !sc.store.CredentialsInStore() {
		// учётные данные реестра лежат в registry-config.json: убираем, если на этот хост больше нет реестров
		host := ociRepoHost(ociFromRecord(*removed))
		if rest, err := sc.repos(); err == nil && !ociHostInUse(rest, host) {
			sc.ociLogout(host, removed.PlainHTTP)
		}
	}
	return nil
}

// nameTaken — быстрая проверка до скачивания индекса; окончательно имя занимает CreateRepo.
func (sc *helmScope) nameTaken(name string) error {
	if _, ok, err := sc.repo(name); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("repository %q is already added", name)
	}
	return nil
}

// UpdateHelmRepos обновляет индексы: name == "" — все репозитории (до 4 параллельно).
// Ошибка одного репозитория не мешает остальным и возвращается в его результате.
func (m *Manager) UpdateHelmRepos(name string) ([]HelmRepoUpdateResult, error) {
	if name != "" {
		if err := validateHelmRepoName(name); err != nil {
			return nil, err
		}
	}
	sc, err := m.helmScope()
	if err != nil {
		return nil, err
	}
	recs, err := sc.repos()
	if err != nil {
		return nil, err
	}
	var targets []HelmRepoRecord
	for _, r := range recs {
		if name == "" || r.Name == name {
			targets = append(targets, r)
		}
	}
	if name != "" && len(targets) == 0 {
		return nil, fmt.Errorf("repository %q not found", name)
	}

	results := make([]HelmRepoUpdateResult, len(targets))
	sem := make(chan struct{}, helmUpdateParallel)
	var wg sync.WaitGroup
	for i, r := range targets {
		wg.Add(1)
		go func(i int, r HelmRepoRecord) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if r.Kind == helmKindOCI {
				results[i] = sc.ociUpdateOne(r)
			} else {
				results[i] = sc.updateOneHelmRepo(entryOf(r))
			}
		}(i, r)
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	return results, nil
}

func (sc *helmScope) updateOneHelmRepo(e *repo.Entry) HelmRepoUpdateResult {
	res := HelmRepoUpdateResult{Name: e.Name}
	tmp, idx, err := downloadHelmIndex(context.Background(), sc, e)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	// репозиторий могли удалить или переназначить, пока шла загрузка
	sc.invalidate()
	cur, ok, err := sc.repo(e.Name)
	if err != nil || !ok || cur.Kind != helmKindHTTP || cur.URL != e.URL {
		_ = os.Remove(tmp)
		res.Error = "repository was changed during the update"
		return res
	}
	helmCacheMu.Lock()
	defer helmCacheMu.Unlock()
	if err := commitHelmIndex(sc, e.Name, tmp); err != nil {
		res.Error = err.Error()
		return res
	}
	res.Charts = countHelmCharts(idx)
	return res
}

// ─── каталог ───

func (m *Manager) SearchHelmCharts(query, repoName string) (*HelmCatalogResult, error) {
	if repoName != "" {
		if err := validateHelmRepoName(repoName); err != nil {
			return nil, err
		}
	}
	sc, err := m.helmScope()
	if err != nil {
		return nil, err
	}
	recs, err := sc.repos()
	if err != nil {
		return nil, err
	}
	terms := strings.Fields(strings.ToLower(query))
	var all []HelmCatalogChart
	for _, e := range recs {
		if e.Kind != helmKindHTTP || (repoName != "" && e.Name != repoName) {
			continue
		}
		idx, _, err := loadHelmIndexOrFetch(context.Background(), sc, e.Name)
		if err != nil {
			continue // индекс не скачан или повреждён: виден в списке репозиториев
		}
		for name, versions := range idx.Entries {
			if len(versions) == 0 || versions[0] == nil || versions[0].Metadata == nil {
				continue
			}
			latest := versions[0] // IndexFile сортирует версии по убыванию
			if !helmMatches(terms, name, latest.Description, latest.Keywords) {
				continue
			}
			all = append(all, HelmCatalogChart{
				Repo: e.Name, Name: name, Version: latest.Version, AppVersion: latest.AppVersion,
				Description: latest.Description, Deprecated: latest.Deprecated, Versions: len(versions),
			})
		}
	}
	all = append(all, sc.ociSearch(recs, terms, repoName)...)
	sort.Slice(all, func(i, j int) bool {
		if all[i].Name != all[j].Name {
			return all[i].Name < all[j].Name
		}
		return all[i].Repo < all[j].Repo
	})
	res := &HelmCatalogResult{Total: len(all), Items: all}
	if len(all) > helmSearchMaxResults {
		res.Items = all[:helmSearchMaxResults]
	}
	if res.Items == nil {
		res.Items = []HelmCatalogChart{}
	}
	return res, nil
}

func helmMatches(terms []string, name, desc string, keywords []string) bool {
	if len(terms) == 0 {
		return true
	}
	hay := strings.ToLower(name + " " + desc + " " + strings.Join(keywords, " "))
	for _, t := range terms {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

func helmRepoEntry(sc *helmScope, name string) (*repo.Entry, error) {
	if err := validateHelmRepoName(name); err != nil {
		return nil, err
	}
	r, ok, err := sc.repo(name)
	if err != nil {
		return nil, err
	}
	if !ok || r.Kind != helmKindHTTP {
		return nil, fmt.Errorf("repository %q not found", name)
	}
	return entryOf(r), nil
}

func (m *Manager) GetHelmChartVersions(repoName, chartName string) ([]HelmChartVersionInfo, error) {
	sc, err := m.helmScope()
	if err != nil {
		return nil, err
	}
	if r, isOCI, err := sc.ociFind(repoName); err != nil {
		return nil, err
	} else if isOCI {
		return sc.ociVersions(r, chartName)
	}
	if _, err := helmRepoEntry(sc, repoName); err != nil {
		return nil, err
	}
	idx, _, err := loadHelmIndexOrFetch(context.Background(), sc, repoName)
	if err != nil {
		return nil, fmt.Errorf("index of repository %q is unavailable: %w", repoName, err)
	}
	versions := idx.Entries[chartName]
	if len(versions) == 0 {
		return nil, fmt.Errorf("chart %q not found in repository %q", chartName, repoName)
	}
	out := make([]HelmChartVersionInfo, 0, len(versions))
	for _, v := range versions {
		if v == nil || v.Metadata == nil {
			continue
		}
		created := ""
		if !v.Created.IsZero() {
			created = v.Created.UTC().Format(time.RFC3339)
		}
		out = append(out, HelmChartVersionInfo{
			Version: v.Version, AppVersion: v.AppVersion, Created: created,
			Description: v.Description, Deprecated: v.Deprecated,
		})
	}
	return out, nil
}

// resolveChartURL превращает ссылку из index.yaml (может быть относительной) в абсолютную http(s).
func resolveChartURL(repoURL, ref string) (string, error) {
	base, err := url.Parse(repoURL + "/")
	if err != nil {
		return "", err
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("invalid chart URL: %w", err)
	}
	abs := base.ResolveReference(r)
	if abs.Scheme != "http" && abs.Scheme != "https" {
		return "", fmt.Errorf("chart URL with scheme %q is not supported", abs.Scheme)
	}
	abs.User = nil
	return abs.String(), nil
}

// lookupHelmChartVersion находит запись чарта в индексе репозитория (version == "" — последняя).
func lookupHelmChartVersion(sc *helmScope, repoName, chartName, version string) (*repo.Entry, *repo.ChartVersion, error) {
	e, err := helmRepoEntry(sc, repoName)
	if err != nil {
		return nil, nil, err
	}
	idx, _, err := loadHelmIndexOrFetch(context.Background(), sc, repoName)
	if err != nil {
		return nil, nil, fmt.Errorf("index of repository %q is unavailable: %w", repoName, err)
	}
	cv, err := idx.Get(chartName, version)
	if err != nil {
		return nil, nil, err
	}
	if len(cv.URLs) == 0 {
		return nil, nil, fmt.Errorf("chart %s %s has no archive URL", chartName, cv.Version)
	}
	return e, cv, nil
}

// downloadHelmChart скачивает архив чарта (с лимитом размера), сверяет digest из index.yaml и разбирает чарт.
func downloadHelmChart(ctx context.Context, e *repo.Entry, cv *repo.ChartVersion) (*chartv2.Chart, error) {
	chartURL, err := resolveChartURL(e.URL, cv.URLs[0])
	if err != nil {
		return nil, err
	}
	data, err := helmGet(ctx, e, chartURL, helmChartMaxBytes)
	if err != nil {
		return nil, err
	}
	if cv.Digest != "" {
		sum := sha256.Sum256(data)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), cv.Digest) {
			return nil, errors.New("archive checksum does not match index.yaml")
		}
	}
	c, err := loader.LoadArchive(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse chart archive: %w", err)
	}
	return c, nil
}

// GetHelmChartPackage скачивает архив чарта из репозитория и отдаёт README и values.yaml.
func (m *Manager) GetHelmChartPackage(repoName, chartName, version string) (*HelmChartPackage, error) {
	sc, err := m.helmScope()
	if err != nil {
		return nil, err
	}
	if r, isOCI, err := sc.ociFind(repoName); err != nil {
		return nil, err
	} else if isOCI {
		return sc.ociPackage(r, chartName, version)
	}
	e, cv, err := lookupHelmChartVersion(sc, repoName, chartName, version)
	if err != nil {
		return nil, err
	}

	// у каждой команды свой набор репозиториев: одноимённые репозитории разных команд — разные
	key := sc.key(repoName) + "/" + chartName + "/" + cv.Version + "/" + cv.Digest
	helmPkgMu.Lock()
	cached := helmPkgCache[key]
	helmPkgMu.Unlock()
	if cached != nil {
		return cached, nil
	}

	c, err := downloadHelmChart(context.Background(), e, cv)
	if err != nil {
		return nil, err
	}

	pkg := &HelmChartPackage{
		Repo: repoName, Name: chartName, Version: cv.Version, AppVersion: cv.AppVersion,
		Description: cv.Description, Home: cv.Home, Sources: cv.Sources, Keywords: cv.Keywords,
		Deprecated: cv.Deprecated,
	}
	for _, mt := range cv.Maintainers {
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

func clipText(b []byte) string {
	if len(b) > helmPackageTextMax {
		return string(b[:helmPackageTextMax]) + "\n… (truncated)"
	}
	return string(b)
}
