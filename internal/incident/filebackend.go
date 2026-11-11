// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/24DSO-lab/kubron/internal/tenant"
)

// FileBackend — файловая реализация Backend для desktop (ADR-0026 §4): один процесс, межпроцессных блокировок нет.
//
//	<root>/<org>/settings.json
//	<root>/<org>/<team>/<cluster>/incidents/<id>.json
//	<root>/<org>/<team>/<cluster>/fragments/<incident>/<fragment>.json
//
// Запись атомарна (временный файл в том же каталоге и rename). Права: каталоги 0700, файлы 0600 — не шире, чем у
// users.json и audit.log. Фрагменты здесь не шифруются: это каталог данных приложения с теми же правами, где уже
// лежат kubeconfig (ADR-0026 §4).
type FileBackend struct {
	root string
	mu   sync.Mutex // один процесс: сериализует чтение-изменение-запись
}

var _ Backend = (*FileBackend)(nil)

// NewFileBackend возвращает хранилище в каталоге root (создаётся при первой записи).
func NewFileBackend(root string) *FileBackend { return &FileBackend{root: root} }

func (b *FileBackend) clusterDir(s tenant.Scope) string {
	return filepath.Join(b.root, s.OrgID, s.TeamID, s.ClusterID)
}

func (b *FileBackend) incidentPath(s tenant.Scope, id string) string {
	return filepath.Join(b.clusterDir(s), "incidents", id+".json")
}

func (b *FileBackend) fragmentDir(s tenant.Scope, incidentID string) string {
	return filepath.Join(b.clusterDir(s), "fragments", incidentID)
}

func checkIDs(ids ...string) error {
	for _, id := range ids {
		if !ValidID(id) {
			return fmt.Errorf("%w", ErrInvalidID)
		}
	}
	return nil
}

func ctxErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("incident storage: %w", err)
	}
	return nil
}

// writeFile атомарно записывает data в path с правами 0600.
func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("corrupted incident data %s: %w", filepath.Base(path), err)
	}
	return nil
}

func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// PutIncident implements Backend.
func (b *FileBackend) PutIncident(ctx context.Context, in *Incident) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := CheckScope(in.Scope); err != nil {
		return err
	}
	if err := checkIDs(in.ID); err != nil {
		return err
	}
	in.OpenedAt, in.UpdatedAt = Micro(in.OpenedAt), Micro(in.UpdatedAt)
	b.mu.Lock()
	defer b.mu.Unlock()
	path := b.incidentPath(in.Scope, in.ID)
	var cur Incident
	switch err := readJSON(path, &cur); {
	case errors.Is(err, ErrNotFound):
		if in.Version != 0 {
			return ErrConflict
		}
	case err != nil:
		return err
	case cur.Version != in.Version:
		return ErrConflict
	}
	next := *in
	next.Version = in.Version + 1
	data, err := marshal(&next)
	if err != nil {
		return err
	}
	if err := writeFile(path, data); err != nil {
		return err
	}
	in.Version = next.Version
	return nil
}

// GetIncident implements Backend.
func (b *FileBackend) GetIncident(ctx context.Context, scope tenant.Scope, id string) (*Incident, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if err := CheckScope(scope); err != nil {
		return nil, err
	}
	if err := checkIDs(id); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var in Incident
	if err := readJSON(b.incidentPath(scope, id), &in); err != nil {
		return nil, err
	}
	return &in, nil
}

// ListIncidents implements Backend.
func (b *FileBackend) ListIncidents(ctx context.Context, scope tenant.Scope, q ListQuery) (Page, error) {
	if err := ctxErr(ctx); err != nil {
		return Page{}, err
	}
	if err := CheckScope(scope); err != nil {
		return Page{}, err
	}
	curT, curID, hasCur, err := DecodeCursor(q.Cursor)
	if err != nil {
		return Page{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	entries, err := os.ReadDir(filepath.Join(b.clusterDir(scope), "incidents"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Page{}, err
	}
	var all []Summary
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		var in Incident
		if err := readJSON(filepath.Join(b.clusterDir(scope), "incidents", name), &in); err != nil {
			continue // повреждённая запись не должна скрывать остальную историю
		}
		if q.Status != "" && in.Status != q.Status {
			continue
		}
		all = append(all, in.Summarize())
	}
	slices.SortFunc(all, func(a, c Summary) int {
		return cmp.Or(c.OpenedAt.Compare(a.OpenedAt), cmp.Compare(c.ID, a.ID))
	})
	n := q.PageSize()
	page := Page{Items: []Summary{}}
	for _, s := range all {
		if hasCur && !Before(s.OpenedAt, s.ID, curT, curID) {
			continue
		}
		if len(page.Items) == n {
			last := page.Items[n-1]
			page.NextCursor = EncodeCursor(last.OpenedAt, last.ID)
			break
		}
		page.Items = append(page.Items, s)
	}
	return page, nil
}

// DeleteIncident implements Backend.
func (b *FileBackend) DeleteIncident(ctx context.Context, scope tenant.Scope, id string) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := CheckScope(scope); err != nil {
		return err
	}
	if err := checkIDs(id); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := os.Remove(b.incidentPath(scope, id)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	return os.RemoveAll(b.fragmentDir(scope, id))
}

// PutFragment implements Backend.
func (b *FileBackend) PutFragment(ctx context.Context, f *Fragment) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := CheckScope(f.Scope); err != nil {
		return err
	}
	if err := checkIDs(f.IncidentID, f.ID); err != nil {
		return err
	}
	f.CapturedAt, f.CreatedAt = Micro(f.CapturedAt), Micro(f.CreatedAt)
	data, err := marshal(f)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return writeFile(filepath.Join(b.fragmentDir(f.Scope, f.IncidentID), f.ID+".json"), data)
}

// GetFragment implements Backend.
func (b *FileBackend) GetFragment(ctx context.Context, scope tenant.Scope, incidentID, fragmentID string) (*Fragment, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if err := CheckScope(scope); err != nil {
		return nil, err
	}
	if err := checkIDs(incidentID, fragmentID); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var f Fragment
	if err := readJSON(filepath.Join(b.fragmentDir(scope, incidentID), fragmentID+".json"), &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// ListFragments implements Backend. Порядок — по времени создания, затем по ID.
func (b *FileBackend) ListFragments(ctx context.Context, scope tenant.Scope, incidentID string) ([]FragmentMeta, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if err := CheckScope(scope); err != nil {
		return nil, err
	}
	if err := checkIDs(incidentID); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.fragmentMetas(scope, incidentID)
}

func (b *FileBackend) fragmentMetas(scope tenant.Scope, incidentID string) ([]FragmentMeta, error) {
	dir := b.fragmentDir(scope, incidentID)
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	out := []FragmentMeta{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var f Fragment
		if err := readJSON(filepath.Join(dir, e.Name()), &f); err != nil {
			continue
		}
		out = append(out, f.FragmentMeta)
	}
	slices.SortFunc(out, func(a, c FragmentMeta) int {
		return cmp.Or(a.CreatedAt.Compare(c.CreatedAt), cmp.Compare(a.ID, c.ID))
	})
	return out, nil
}

// DeleteFragment implements Backend.
func (b *FileBackend) DeleteFragment(ctx context.Context, scope tenant.Scope, incidentID, fragmentID string) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := CheckScope(scope); err != nil {
		return err
	}
	if err := checkIDs(incidentID, fragmentID); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := os.Remove(filepath.Join(b.fragmentDir(scope, incidentID), fragmentID+".json")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// Settings implements Backend.
func (b *FileBackend) Settings(ctx context.Context, orgID string) (Settings, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return Settings{}, false, err
	}
	if err := checkIDs(orgID); err != nil {
		return Settings{}, false, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var s Settings
	switch err := readJSON(filepath.Join(b.root, orgID, "settings.json"), &s); {
	case errors.Is(err, ErrNotFound):
		return Settings{}, false, nil
	case err != nil:
		return Settings{}, false, err
	}
	return s.Normalize(), true, nil
}

// PutSettings implements Backend.
func (b *FileBackend) PutSettings(ctx context.Context, orgID string, s Settings) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := checkIDs(orgID); err != nil {
		return err
	}
	s = s.Normalize()
	if err := s.Validate(); err != nil {
		return err
	}
	data, err := marshal(s)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return writeFile(filepath.Join(b.root, orgID, "settings.json"), data)
}

// OrgIDs implements Backend.
func (b *FileBackend) OrgIDs(ctx context.Context) ([]string, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	entries, err := os.ReadDir(b.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() && ValidID(e.Name()) {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	return out, nil
}

// Purge implements Backend.
func (b *FileBackend) Purge(ctx context.Context, orgID string, fragmentsBefore, incidentsBefore time.Time) (PurgeResult, error) {
	var res PurgeResult
	if err := ctxErr(ctx); err != nil {
		return res, err
	}
	if err := checkIDs(orgID); err != nil {
		return res, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	teams, err := subdirs(filepath.Join(b.root, orgID))
	if err != nil {
		return res, err
	}
	for _, team := range teams {
		clusters, err := subdirs(filepath.Join(b.root, orgID, team))
		if err != nil {
			return res, err
		}
		for _, cluster := range clusters {
			if err := ctx.Err(); err != nil {
				return res, ctxErr(ctx)
			}
			scope := tenant.Scope{OrgID: orgID, TeamID: team, ClusterID: cluster}
			if err := b.purgeCluster(scope, fragmentsBefore, incidentsBefore, &res); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}

func subdirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && ValidID(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func (b *FileBackend) purgeCluster(scope tenant.Scope, fragmentsBefore, incidentsBefore time.Time, res *PurgeResult) error {
	incDir := filepath.Join(b.clusterDir(scope), "incidents")
	entries, err := os.ReadDir(incDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		var in Incident
		if e.IsDir() || readJSON(filepath.Join(incDir, e.Name()), &in) != nil {
			continue
		}
		if in.UpdatedAt.Before(incidentsBefore) {
			if err := os.Remove(filepath.Join(incDir, e.Name())); err != nil {
				return err
			}
			dir := b.fragmentDir(scope, in.ID)
			files, _ := os.ReadDir(dir)
			res.Fragments += len(files) // фрагменты удалённого инцидента входят в итог
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			res.Incidents++
		}
	}
	fragRoot := filepath.Join(b.clusterDir(scope), "fragments")
	incidents, err := subdirs(fragRoot)
	if err != nil {
		return err
	}
	for _, inc := range incidents {
		dir := filepath.Join(fragRoot, inc)
		files, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, f := range files {
			var frag Fragment
			if f.IsDir() || readJSON(filepath.Join(dir, f.Name()), &frag) != nil {
				continue
			}
			if frag.CreatedAt.Before(fragmentsBefore) {
				if err := os.Remove(filepath.Join(dir, f.Name())); err != nil {
					return err
				}
				res.Fragments++
			}
		}
		if left, _ := os.ReadDir(dir); len(left) == 0 {
			_ = os.Remove(dir) // пустой каталог осиротевшего инцидента
		}
	}
	return nil
}
