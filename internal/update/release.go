// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package update

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultRepo — репозиторий, в котором выходят релизы. Образ и чарт публикуются под тем же владельцем
	// (ghcr.io/<владелец>/kubron, oci://ghcr.io/<владелец>/charts/kubron), см. publish.yml.
	DefaultRepo = "24DSO-lab/kubron"

	defaultAPIBase = "https://api.github.com"
	defaultWebBase = "https://github.com"

	// Описание релиза с десятком файлов занимает единицы килобайт; больше мегабайта — не то, что мы ждём.
	maxReleaseBody = 1 << 20
	requestTimeout = 15 * time.Second
	maxRedirects   = 3
)

var (
	// Тег релиза попадает в ссылки и команды, поэтому берётся только из безопасного набора символов;
	// сам он при этом ещё и обязан быть SemVer (parseVersion).
	tagRe = regexp.MustCompile(`^v?[0-9A-Za-z.+-]{1,64}$`)
	// Имя файла релиза попадает в команду (apt install ./имя): без пробелов, кавычек, слэшей и «$».
	assetNameRe = regexp.MustCompile(`^[A-Za-z0-9._~+-]{1,200}$`)
)

// Asset — файл, приложенный к релизу.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// Release — опубликованный релиз после проверки полей.
type Release struct {
	Tag         string // как в репозитории: v1.3.0
	Version     string // SemVer без «v»: 1.3.0
	URL         string // страница релиза на github.com
	PublishedAt time.Time
	Assets      []Asset
}

// Client получает последний релиз из GitHub. Безопасен для одновременного использования.
type Client struct {
	// Поля задаются до первого вызова; в тестах APIBase и WebBase указывают на httptest-сервер.
	APIBase   string
	WebBase   string
	Repo      string
	UserAgent string
	HTTP      *http.Client

	mu     sync.Mutex
	etag   string
	cached *Release
}

// NewClient создаёт клиент GitHub Releases для DefaultRepo. buildVersion попадает в User-Agent
// (GitHub отклоняет запросы без него).
func NewClient(buildVersion string) *Client {
	return &Client{
		APIBase:   defaultAPIBase,
		WebBase:   defaultWebBase,
		Repo:      DefaultRepo,
		UserAgent: "kubron/" + buildVersion,
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	// Транспорт по умолчанию учитывает HTTPS_PROXY/NO_PROXY: в корпоративных сетях выход наружу обычно через прокси.
	return &http.Client{
		Timeout: requestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Редирект не должен понижать схему (https → http) и уводить в цепочку длиннее нескольких переходов.
			if len(via) >= maxRedirects || req.URL.Scheme != via[0].URL.Scheme {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

type apiAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// apiRelease — нужные поля ответа GitHub (https://docs.github.com/rest/releases/releases#get-the-latest-release).
type apiRelease struct {
	TagName     string     `json:"tag_name"`
	HTMLURL     string     `json:"html_url"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
	PublishedAt time.Time  `json:"published_at"`
	Assets      []apiAsset `json:"assets"`
}

// Latest возвращает последний стабильный релиз (черновики и предрелизы GitHub в /releases/latest не отдаёт).
// Повторные запросы условные (If-None-Match): ответ 304 не расходует лимит GitHub и возвращает прежний релиз.
// Ошибки — только значения Error; подробности пишутся в журнал на уровне debug.
func (c *Client) Latest(ctx context.Context) (*Release, error) {
	endpoint := strings.TrimRight(c.APIBase, "/") + "/repos/" + c.Repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ErrUnexpected
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", c.UserAgent)

	c.mu.Lock()
	etag, cached := c.etag, c.cached
	c.mu.Unlock()
	if etag != "" && cached != nil {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		slog.Debug("update: release request failed", "error", err)
		return nil, ErrNetwork
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotModified:
		if cached != nil {
			return cached, nil
		}
		return nil, ErrUnexpected
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusTooManyRequests:
		return nil, ErrRateLimited
	case http.StatusForbidden:
		// 403 у GitHub — и исчерпанный лимит, и запрет по другой причине; по заголовкам различаем.
		if resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "" {
			return nil, ErrRateLimited
		}
		return nil, ErrUnexpected
	default:
		slog.Debug("update: unexpected release status", "status", resp.StatusCode)
		return nil, ErrUnexpected
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseBody+1))
	if err != nil {
		slog.Debug("update: release body read failed", "error", err)
		return nil, ErrNetwork
	}
	if len(body) > maxReleaseBody {
		return nil, ErrUnexpected
	}
	var raw apiRelease
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, ErrUnexpected
	}
	rel, err := c.convert(raw)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.etag, c.cached = resp.Header.Get("ETag"), rel
	c.mu.Unlock()
	return rel, nil
}

// convert проверяет ответ и оставляет только то, чему можно доверять в интерфейсе и в командах:
// тег — SemVer из безопасных символов, ссылки — на страницы этого репозитория, имена файлов — по белому списку.
func (c *Client) convert(raw apiRelease) (*Release, error) {
	if raw.Draft || raw.Prerelease || !tagRe.MatchString(raw.TagName) {
		return nil, ErrUnexpected
	}
	version, ok := NormalizeVersion(raw.TagName)
	if !ok {
		return nil, ErrUnexpected
	}
	web := strings.TrimRight(c.WebBase, "/") + "/" + c.Repo + "/releases/"
	rel := &Release{Tag: raw.TagName, Version: version, PublishedAt: raw.PublishedAt}

	rel.URL = web + "tag/" + url.PathEscape(raw.TagName)
	if hasPrefixFold(raw.HTMLURL, web) {
		rel.URL = raw.HTMLURL
	}
	for _, a := range raw.Assets {
		if !assetNameRe.MatchString(a.Name) || !hasPrefixFold(a.URL, web+"download/") {
			continue // чужой хост или странное имя: файл просто не предлагаем
		}
		rel.Assets = append(rel.Assets, Asset(a))
	}
	return rel, nil
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// asset ищет файл релиза по условию.
func (r *Release) asset(match func(name string) bool) (Asset, bool) {
	for _, a := range r.Assets {
		if match(a.Name) {
			return a, true
		}
	}
	return Asset{}, false
}
