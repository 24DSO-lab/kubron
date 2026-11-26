// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Права как у users.json и audit.log: читает и пишет только владелец.
const (
	fileMode = 0o600
	dirMode  = 0o700
)

// staleTempAge — через сколько прерванная запись (временный файл) убирается задачей retention.
const staleTempAge = time.Hour

// FileBackend — файловое хранилище отчётов (desktop): <dir>/<org>/<team>/<cluster>/<id>.json.
// Запись атомарна: временный файл в том же каталоге и rename. Каталог не обязан существовать заранее.
type FileBackend struct {
	dir string
	mu  sync.Mutex
}

// NewFileBackend возвращает хранилище в каталоге dir (подкаталог домена, например <data>/readiness).
func NewFileBackend(dir string) *FileBackend { return &FileBackend{dir: dir} }

var _ Backend = (*FileBackend)(nil)

var plainName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// segment превращает часть области в безопасное имя каталога. Допустимое имя остаётся как есть, остальное
// заменяется отпечатком; префикс «~» не встречается у допустимых имён, поэтому две формы не сталкиваются.
func segment(s string) string {
	if plainName.MatchString(s) && !strings.Contains(s, "..") {
		return s
	}
	sum := sha256.Sum256([]byte(s))
	return "~" + hex.EncodeToString(sum[:16])
}

func (b *FileBackend) clusterDir(s tenant.Scope) string {
	return filepath.Join(b.dir, segment(s.OrgID), segment(s.TeamID), segment(s.ClusterID))
}

func (b *FileBackend) Save(ctx context.Context, scope tenant.Scope, r *Report) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateForSave(scope, r); err != nil {
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("failed to encode the readiness report: %w", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	dir := b.clusterDir(scope)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("failed to create the readiness directory: %w", err)
	}
	return writeAtomic(filepath.Join(dir, r.ID+".json"), raw)
}

// writeAtomic пишет файл через временный файл и rename: читатель видит старую или новую версию целиком.
func writeAtomic(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to write the readiness report: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if err = tmp.Chmod(fileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write the readiness report: %w", err)
	}
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write the readiness report: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write the readiness report: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("failed to write the readiness report: %w", err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("failed to write the readiness report: %w", err)
	}
	return nil
}

func readReport(path string) (*Report, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the readiness report: %w", err)
	}
	var r Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("failed to decode the readiness report: %w", err)
	}
	return &r, nil
}

func (b *FileBackend) Get(ctx context.Context, scope tenant.Scope, id string) (*Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	if !ValidID(id) {
		return nil, ErrNotFound
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	r, err := readReport(filepath.Join(b.clusterDir(scope), id+".json"))
	if err != nil {
		return nil, err
	}
	// Файл лежит в каталоге области, но его содержимое не должно ей противоречить.
	if r.ClusterID != scope.ClusterID || r.ID != id {
		return nil, ErrNotFound
	}
	return r, nil
}

// entry — отчёт на диске с временем создания.
type entry struct {
	path    string
	summary Summary
}

// scan читает сводки отчётов каталога кластера; повреждённые файлы пропускаются с предупреждением.
func scan(dir string) []entry {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []entry
	for _, de := range des {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		p := filepath.Join(dir, name)
		r, err := readReport(p)
		if err != nil {
			slog.Warn("readiness report skipped", "file", name, "err", err)
			continue
		}
		out = append(out, entry{path: p, summary: r.Summary()})
	}
	return out
}

func (b *FileBackend) List(ctx context.Context, scope tenant.Scope, q HistoryQuery) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	if err := ValidateScope(scope); err != nil {
		return Page{}, err
	}
	if _, _, err := DecodeCursor(q.Cursor); err != nil {
		return Page{}, err
	}
	b.mu.Lock()
	entries := scan(b.clusterDir(scope))
	b.mu.Unlock()
	all := make([]Summary, 0, len(entries))
	for _, e := range entries {
		if e.summary.ClusterID == scope.ClusterID {
			all = append(all, e.summary)
		}
	}
	SortNewestFirst(all)
	return PageFrom(all, q)
}

func (b *FileBackend) Prune(ctx context.Context, now time.Time, r Retention) (int, error) {
	if r.KeepPerCluster <= 0 && r.MaxAge <= 0 {
		return 0, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	removed := 0
	for _, dir := range b.clusterDirs() {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		entries := scan(dir)
		sums := make([]Summary, len(entries))
		path := make(map[string]string, len(entries))
		for i, e := range entries {
			sums[i] = e.summary
			path[e.summary.ID] = e.path
		}
		SortNewestFirst(sums)
		for i, s := range sums {
			expired := r.MaxAge > 0 && now.Sub(s.CreatedAt) > r.MaxAge
			excess := r.KeepPerCluster > 0 && i >= r.KeepPerCluster
			if !expired && !excess {
				continue
			}
			if err := os.Remove(path[s.ID]); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return removed, fmt.Errorf("failed to remove the readiness report: %w", err)
			}
			removed++
		}
		removeStaleTemps(dir, now)
	}
	return removed, nil
}

// clusterDirs перечисляет каталоги кластеров всех областей: <org>/<team>/<cluster>.
func (b *FileBackend) clusterDirs() []string {
	var out []string
	orgs, _ := os.ReadDir(b.dir)
	for _, o := range orgs {
		teams, _ := os.ReadDir(filepath.Join(b.dir, o.Name()))
		for _, t := range teams {
			clusters, _ := os.ReadDir(filepath.Join(b.dir, o.Name(), t.Name()))
			for _, c := range clusters {
				if c.IsDir() {
					out = append(out, filepath.Join(b.dir, o.Name(), t.Name(), c.Name()))
				}
			}
		}
	}
	return out
}

func removeStaleTemps(dir string, now time.Time) {
	des, _ := os.ReadDir(dir)
	for _, de := range des {
		if !strings.HasPrefix(de.Name(), ".tmp-") {
			continue
		}
		if info, err := de.Info(); err == nil && now.Sub(info.ModTime()) > staleTempAge {
			_ = os.Remove(filepath.Join(dir, de.Name()))
		}
	}
}
