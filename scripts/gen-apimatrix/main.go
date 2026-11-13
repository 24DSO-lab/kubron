// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// gen-apimatrix генерирует встроенную матрицу устаревших и удалённых Kubernetes API
// (internal/readiness/apimatrix/matrix.json, ADR-0025 §3).
//
// Источник — файлы zz_generated.prerelease-lifecycle.go репозитория kubernetes/kubernetes по тегам релизов
// (методы APILifecycleIntroduced/Deprecated/Removed/Replacement): это машиночитаемая форма тех же данных, из которых
// собраны deprecation guide и справочник API. Окно целей — три последние поддерживаемые минорные версии — берётся
// из расписания релизов kubernetes/website (data/releases/schedule.yaml).
//
// Запуск (нужен доступ к raw.githubusercontent.com; в сборке и при проверке отчёта сеть не используется):
//
//	go run ./scripts/gen-apimatrix                 # перегенерировать matrix.json
//	go run ./scripts/gen-apimatrix -check          # проверить, что файл совпадает с тем, что дал бы апстрим
//	go run ./scripts/gen-apimatrix -cache .cache   # кэшировать скачанные файлы между запусками
//
// Порядок обновления при выходе новой минорной версии описан в docs/lifecycle-readiness.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/24DSO-lab/kubron/internal/readiness/apimatrix"
	"github.com/24DSO-lab/kubron/internal/readiness/kver"
)

const (
	repoURL   = "https://github.com/kubernetes/kubernetes"
	guideURL  = "https://kubernetes.io/docs/reference/using-api/deprecation-guide/"
	scheduleP = "data/releases/schedule.yaml"
	swaggerP  = "api/openapi-spec/swagger.json"
)

// Адреса данных апстрима; переменные, чтобы тест подставил локальный сервер.
var (
	rawBase    = "https://raw.githubusercontent.com/kubernetes/kubernetes"
	rawWebsite = "https://raw.githubusercontent.com/kubernetes/website/main"
)

// pkg — каталог пакета с типами API: путь от корня репозитория, группа и версии, в которых когда-либо были
// предрелизные типы. Версии перечислены явно: список каталогов без GitHub API получить нельзя, а полноту
// таблицы проверяет сверка со swagger последнего релиза (checkCoverage): новая предрелизная группа или версия
// без записи здесь — ошибка генерации, а не молчаливый пропуск.
type pkg struct {
	dir      string
	group    string
	versions []string
}

const apiDir = "staging/src/k8s.io/api/"

var packages = []pkg{
	{apiDir + "admission", "admission.k8s.io", []string{"v1", "v1beta1"}},
	{apiDir + "admissionregistration", "admissionregistration.k8s.io", []string{"v1alpha1", "v1beta1"}},
	{apiDir + "apidiscovery", "apidiscovery.k8s.io", []string{"v2beta1"}},
	{apiDir + "apiserverinternal", "internal.apiserver.k8s.io", []string{"v1alpha1"}},
	{apiDir + "apps", "apps", []string{"v1beta1", "v1beta2"}},
	{apiDir + "authentication", "authentication.k8s.io", []string{"v1alpha1", "v1beta1"}},
	{apiDir + "authorization", "authorization.k8s.io", []string{"v1beta1"}},
	{apiDir + "autoscaling", "autoscaling", []string{"v2beta1", "v2beta2"}},
	{apiDir + "batch", "batch", []string{"v1beta1"}},
	{apiDir + "certificates", "certificates.k8s.io", []string{"v1alpha1", "v1beta1"}},
	{apiDir + "coordination", "coordination.k8s.io", []string{"v1alpha1", "v1alpha2", "v1beta1"}},
	{apiDir + "discovery", "discovery.k8s.io", []string{"v1beta1"}},
	{apiDir + "events", "events.k8s.io", []string{"v1beta1"}},
	{apiDir + "extensions", "extensions", []string{"v1beta1"}},
	{apiDir + "flowcontrol", "flowcontrol.apiserver.k8s.io", []string{"v1alpha1", "v1beta1", "v1beta2", "v1beta3"}},
	{apiDir + "imagepolicy", "imagepolicy.k8s.io", []string{"v1alpha1"}},
	{apiDir + "lifecycle", "lifecycle.k8s.io", []string{"v1alpha1"}},
	{apiDir + "networking", "networking.k8s.io", []string{"v1alpha1", "v1beta1"}},
	{apiDir + "node", "node.k8s.io", []string{"v1alpha1", "v1beta1"}},
	{apiDir + "policy", "policy", []string{"v1beta1"}},
	{apiDir + "rbac", "rbac.authorization.k8s.io", []string{"v1alpha1", "v1beta1"}},
	{apiDir + "resource", "resource.k8s.io", []string{"v1alpha1", "v1alpha2", "v1alpha3", "v1beta1", "v1beta2"}},
	{apiDir + "scheduling", "scheduling.k8s.io", []string{"v1alpha1", "v1alpha2", "v1alpha3", "v1beta1"}},
	{apiDir + "storage", "storage.k8s.io", []string{"v1alpha1", "v1beta1"}},
	{apiDir + "storagemigration", "storagemigration.k8s.io", []string{"v1alpha1", "v1beta1"}},
	{"staging/src/k8s.io/apiextensions-apiserver/pkg/apis/apiextensions", "apiextensions.k8s.io", []string{"v1beta1"}},
	{"staging/src/k8s.io/kube-aggregator/pkg/apis/apiregistration", "apiregistration.k8s.io", []string{"v1beta1"}},
}

const lifecycleFile = "zz_generated.prerelease-lifecycle.go"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "gen-apimatrix:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("gen-apimatrix", flag.ContinueOnError)
	outPath := fs.String("out", "internal/readiness/apimatrix/matrix.json", "output file")
	since := fs.String("since", "1.24", "oldest release whose lifecycle data is read")
	latest := fs.String("latest", "", "newest minor version (default: from the upstream release schedule)")
	date := fs.String("date", time.Now().UTC().Format(time.DateOnly), "generation date, YYYY-MM-DD")
	cache := fs.String("cache", "", "directory that caches downloaded files")
	check := fs.Bool("check", false, "compare the result with the existing file instead of writing it")
	skipCov := fs.Bool("skip-coverage-check", false, "do not compare the package table with the upstream OpenAPI spec")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sinceV, err := kver.ParseMinor(*since)
	if err != nil {
		return fmt.Errorf("-since: %w", err)
	}
	f := &fetcher{client: &http.Client{Timeout: 60 * time.Second}, cache: *cache}

	latestV, window, err := resolveWindow(ctx, f, *latest, *date)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "target window: %v\n", window); err != nil {
		return err
	}

	var tags []string
	for m := sinceV; !latestV.Less(m); m.Minor++ {
		tags = append(tags, fmt.Sprintf("v%s.0", m))
	}

	entries, err := collect(ctx, f, tags)
	if err != nil {
		return err
	}
	if !*skipCov {
		if err := checkCoverage(ctx, f, tags[len(tags)-1]); err != nil {
			return err
		}
	}
	apimatrix.SortEntries(entries)
	version, err := apimatrix.ComputeVersion(latestV, entries)
	if err != nil {
		return err
	}
	m := apimatrix.Matrix{
		SchemaVersion: apimatrix.SchemaVersion,
		Version:       version,
		GeneratedAt:   *date,
		Source:        apimatrix.Source{Repository: repoURL, Tags: tags, Guide: guideURL},
		Window:        window,
		Since:         sinceV,
		Entries:       entries,
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := apimatrix.Parse(data); err != nil {
		return fmt.Errorf("the generated matrix does not pass validation: %w", err)
	}
	if *check {
		cur, err := os.ReadFile(*outPath)
		if err != nil {
			return err
		}
		curM, err := apimatrix.Parse(cur)
		if err != nil {
			return fmt.Errorf("the committed matrix is invalid: %w", err)
		}
		if curM.Version != version {
			return fmt.Errorf("the committed matrix %s differs from upstream %s: run go run ./scripts/gen-apimatrix", curM.Version, version)
		}
		_, err = fmt.Fprintf(out, "matrix %s is up to date\n", version)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*outPath, data, 0o644); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "wrote %s: %d entries, version %s\n", *outPath, len(entries), version)
	return err
}

// fetcher скачивает файлы из raw.githubusercontent.com с повторами и необязательным кэшем на диске.
type fetcher struct {
	client *http.Client
	cache  string
}

var errNotFound = errors.New("not found")

func (f *fetcher) get(ctx context.Context, url string) ([]byte, error) {
	var cacheFile string
	if f.cache != "" {
		cacheFile = filepath.Join(f.cache, strings.NewReplacer("https://", "", "/", "_").Replace(url))
		if b, err := os.ReadFile(cacheFile); err == nil {
			if len(b) == 0 {
				return nil, errNotFound
			}
			return b, nil
		}
	}
	var lastErr error
	for attempt := range 4 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "kubron-gen-apimatrix")
		resp, err := f.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		switch {
		case err != nil:
			lastErr = err
		case resp.StatusCode == http.StatusNotFound:
			f.store(cacheFile, nil)
			return nil, errNotFound
		case resp.StatusCode != http.StatusOK:
			lastErr = fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
		default:
			f.store(cacheFile, body)
			return body, nil
		}
	}
	return nil, fmt.Errorf("failed to fetch %s: %w", url, lastErr)
}

func (f *fetcher) store(path string, b []byte) {
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, b, 0o644)
}

// schedule — нужная часть data/releases/schedule.yaml репозитория kubernetes/website.
type schedule struct {
	Schedules []struct {
		Release       string `json:"release"`
		EndOfLifeDate string `json:"endOfLifeDate"`
	} `json:"schedules"`
}

// resolveWindow определяет три последние поддерживаемые минорные версии: релизы, чей endOfLifeDate ещё не наступил
// на дату генерации. Явный -latest не отменяет проверки: окно строится от него назад на три версии.
func resolveWindow(ctx context.Context, f *fetcher, latest, date string) (kver.Minor, []kver.Minor, error) {
	today, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return kver.Minor{}, nil, fmt.Errorf("-date: %w", err)
	}
	var supported []kver.Minor
	if raw, err := f.get(ctx, rawWebsite+"/"+scheduleP); err == nil {
		var s schedule
		if err := yaml.Unmarshal(raw, &s); err != nil {
			return kver.Minor{}, nil, fmt.Errorf("failed to parse the release schedule: %w", err)
		}
		for _, r := range s.Schedules {
			eol, err := time.Parse(time.DateOnly, r.EndOfLifeDate)
			if err != nil || eol.Before(today) {
				continue
			}
			v, err := kver.ParseMinor(r.Release)
			if err != nil {
				continue
			}
			supported = append(supported, v)
		}
	} else if latest == "" {
		return kver.Minor{}, nil, fmt.Errorf("failed to read the release schedule (pass -latest to set the window by hand): %w", err)
	}
	slices.SortFunc(supported, kver.Minor.Compare)
	var top kver.Minor
	if latest != "" {
		if top, err = kver.ParseMinor(latest); err != nil {
			return kver.Minor{}, nil, fmt.Errorf("-latest: %w", err)
		}
	} else {
		if len(supported) == 0 {
			return kver.Minor{}, nil, errors.New("the release schedule lists no supported release")
		}
		top = supported[len(supported)-1]
	}
	window := make([]kver.Minor, 0, apimatrix.MaxWindow)
	for i := apimatrix.MaxWindow - 1; i >= 0; i-- {
		window = append(window, kver.Minor{Major: top.Major, Minor: top.Minor - i})
	}
	// Расписание — источник истины об окне: если оно знает поддерживаемые версии, окно должно им совпасть.
	if len(supported) > 0 {
		supported = supported[max(0, len(supported)-apimatrix.MaxWindow):]
		if !slices.Equal(supported, window) {
			return kver.Minor{}, nil, fmt.Errorf("the supported releases %v differ from the window %v", supported, window)
		}
	}
	return top, window, nil
}

type fileJob struct {
	tag string
	p   pkg
	ver string
}

// collect читает файлы всех тегов и сливает записи: для вида API берётся запись самого нового тега, где он есть
// (в поздних тегах удалённые типы пропадают из исходников, и их жизненный цикл остаётся только в ранних).
func collect(ctx context.Context, f *fetcher, tags []string) ([]apimatrix.Entry, error) {
	var jobs []fileJob
	for _, tag := range tags {
		for _, p := range packages {
			for _, v := range p.versions {
				jobs = append(jobs, fileJob{tag, p, v})
			}
		}
	}
	type result struct {
		job     fileJob
		entries []apimatrix.Entry
		err     error
	}
	results := make([]result, len(jobs))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			url := fmt.Sprintf("%s/%s/%s/%s/%s", rawBase, j.tag, j.p.dir, j.ver, lifecycleFile)
			src, err := f.get(ctx, url)
			if errors.Is(err, errNotFound) {
				return
			}
			if err != nil {
				results[i] = result{job: j, err: err}
				cancel()
				return
			}
			es, err := parseLifecycle(src, j.p.group, j.ver)
			if err != nil {
				err = fmt.Errorf("%s: %w", url, err)
				cancel()
			}
			results[i] = result{job: j, entries: es, err: err}
		}()
	}
	wg.Wait()

	merged := map[apimatrix.GVK]apimatrix.Entry{}
	for tagIdx := range tags { // теги по возрастанию: поздний перекрывает ранний
		for _, r := range results {
			if r.err != nil {
				return nil, r.err
			}
			if r.job.tag != tags[tagIdx] {
				continue
			}
			for _, e := range r.entries {
				merged[e.GVK] = e
			}
		}
	}
	out := make([]apimatrix.Entry, 0, len(merged))
	for _, e := range merged {
		if e.Deprecated.IsZero() && e.Removed.IsZero() {
			continue
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, errors.New("no lifecycle data was found")
	}
	return out, nil
}

// parseLifecycle разбирает один zz_generated.prerelease-lifecycle.go.
func parseLifecycle(src []byte, group, version string) ([]apimatrix.Entry, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, lifecycleFile, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	byKind := map[string]*apimatrix.Entry{}
	get := func(kind string) *apimatrix.Entry {
		e := byKind[kind]
		if e == nil {
			e = &apimatrix.Entry{GVK: apimatrix.GVK{Group: group, Version: version, Kind: kind}}
			byKind[kind] = e
		}
		return e
	}
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Body == nil || len(fn.Body.List) != 1 {
			continue
		}
		kind := receiverName(fn.Recv.List[0].Type)
		ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
		if kind == "" || !ok {
			continue
		}
		switch fn.Name.Name {
		case "APILifecycleIntroduced", "APILifecycleDeprecated", "APILifecycleRemoved":
			v, err := minorFromReturn(ret)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", kind, fn.Name.Name, err)
			}
			e := get(kind)
			switch fn.Name.Name {
			case "APILifecycleIntroduced":
				e.Introduced = v
			case "APILifecycleDeprecated":
				e.Deprecated = v
			default:
				e.Removed = v
			}
		case "APILifecycleReplacement":
			g, err := gvkFromReturn(ret)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", kind, fn.Name.Name, err)
			}
			get(kind).Replacement = g
		}
	}
	// Списки (FooList) наследуют жизненный цикл вида и отдельной записи не заслуживают.
	var out []apimatrix.Entry
	for kind, e := range byKind {
		if base, ok := strings.CutSuffix(kind, "List"); ok && byKind[base] != nil {
			continue
		}
		out = append(out, *e)
	}
	return out, nil
}

func receiverName(t ast.Expr) string {
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func minorFromReturn(r *ast.ReturnStmt) (kver.Minor, error) {
	if len(r.Results) != 2 {
		return kver.Minor{}, errors.New("expected two return values")
	}
	var n [2]int
	for i, e := range r.Results {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.INT {
			return kver.Minor{}, errors.New("expected integer literals")
		}
		v, err := strconv.Atoi(lit.Value)
		if err != nil {
			return kver.Minor{}, err
		}
		n[i] = v
	}
	return kver.Minor{Major: n[0], Minor: n[1]}, nil
}

func gvkFromReturn(r *ast.ReturnStmt) (*apimatrix.GVK, error) {
	if len(r.Results) != 1 {
		return nil, errors.New("expected one return value")
	}
	lit, ok := r.Results[0].(*ast.CompositeLit)
	if !ok {
		return nil, errors.New("expected a composite literal")
	}
	var g apimatrix.GVK
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			return nil, errors.New("expected keyed fields")
		}
		key, ok := kv.Key.(*ast.Ident)
		val, ok2 := kv.Value.(*ast.BasicLit)
		if !ok || !ok2 || val.Kind != token.STRING {
			return nil, errors.New("expected string fields")
		}
		s, err := strconv.Unquote(val.Value)
		if err != nil {
			return nil, err
		}
		switch key.Name {
		case "Group":
			g.Group = s
		case "Version":
			g.Version = s
		case "Kind":
			g.Kind = s
		}
	}
	if g.Version == "" || g.Kind == "" {
		return nil, nil // пустая замена: апстрим её не указал
	}
	return &g, nil
}

// checkCoverage сверяет таблицу packages со swagger последнего релиза: каждая пара «группа, предрелизная версия»
// из спецификации должна быть в таблице, иначе генератор пропустил бы новый API.
func checkCoverage(ctx context.Context, f *fetcher, tag string) error {
	raw, err := f.get(ctx, fmt.Sprintf("%s/%s/%s", rawBase, tag, swaggerP))
	if err != nil {
		return fmt.Errorf("failed to read the OpenAPI spec of %s: %w", tag, err)
	}
	var spec struct {
		Definitions map[string]struct {
			GVK []struct {
				Group   string `json:"group"`
				Version string `json:"version"`
			} `json:"x-kubernetes-group-version-kind"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("failed to parse the OpenAPI spec: %w", err)
	}
	known := map[[2]string]bool{}
	for _, p := range packages {
		for _, v := range p.versions {
			known[[2]string{p.group, v}] = true
		}
	}
	missing := map[string]bool{}
	for _, d := range spec.Definitions {
		for _, g := range d.GVK {
			if (strings.Contains(g.Version, "alpha") || strings.Contains(g.Version, "beta")) && !known[[2]string{g.Group, g.Version}] {
				missing[g.Group+"/"+g.Version] = true
			}
		}
	}
	if len(missing) > 0 {
		list := make([]string, 0, len(missing))
		for k := range missing {
			list = append(list, k)
		}
		sort.Strings(list)
		return fmt.Errorf("the package table misses pre-release API versions of %s: add them to packages in scripts/gen-apimatrix/main.go (or pass -skip-coverage-check)", tag+": "+strings.Join(list, ", "))
	}
	return nil
}
