// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"cmp"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Проблемы лежат в том же корне хранилища, что и расследования, отдельным подкаталогом кластера в том же формате
// и с той же атомарной записью (ADR-0033 §6):
//
//	<root>/<org>/<team>/<cluster>/problems/<id>.json
var _ ProblemBackend = (*FileBackend)(nil)

func (b *FileBackend) problemPath(s tenant.Scope, id string) string {
	return filepath.Join(b.clusterDir(s), "problems", id+".json")
}

// PutProblem implements ProblemBackend.
func (b *FileBackend) PutProblem(ctx context.Context, p *Problem) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := CheckScope(p.Scope); err != nil {
		return err
	}
	if err := checkIDs(p.ID); err != nil {
		return err
	}
	if !p.Status.ValidStatus() {
		return ErrInvalidProblem
	}
	NormalizeProblem(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	path := b.problemPath(p.Scope, p.ID)
	var cur Problem
	switch err := readJSON(path, &cur); {
	case errors.Is(err, ErrNotFound):
		if p.Version != 0 {
			return ErrConflict
		}
	case err != nil:
		return err
	case cur.Version != p.Version:
		return ErrConflict
	}
	next := *p
	next.Version = p.Version + 1
	data, err := marshal(&next)
	if err != nil {
		return err
	}
	if err := writeFile(path, data); err != nil {
		return err
	}
	p.Version = next.Version
	return nil
}

// GetProblem implements ProblemBackend.
func (b *FileBackend) GetProblem(ctx context.Context, scope tenant.Scope, id string) (*Problem, error) {
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
	var p Problem
	if err := readJSON(b.problemPath(scope, id), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ListProblems implements ProblemBackend. Порядок — «время обновления, ID» по убыванию.
func (b *FileBackend) ListProblems(ctx context.Context, scope tenant.Scope, q ProblemQuery) (ProblemPage, error) {
	if err := ctxErr(ctx); err != nil {
		return ProblemPage{}, err
	}
	if err := CheckScope(scope); err != nil {
		return ProblemPage{}, err
	}
	curT, curID, hasCur, err := DecodeCursor(q.Cursor)
	if err != nil {
		return ProblemPage{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	dir := filepath.Join(b.clusterDir(scope), "problems")
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return ProblemPage{}, err
	}
	var all []Problem
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var p Problem
		if err := readJSON(filepath.Join(dir, e.Name()), &p); err != nil {
			continue // повреждённая запись не должна скрывать остальные
		}
		if q.Status != "" && p.Status != q.Status {
			continue
		}
		all = append(all, p)
	}
	slices.SortFunc(all, func(a, c Problem) int {
		return cmp.Or(c.UpdatedAt.Compare(a.UpdatedAt), cmp.Compare(c.ID, a.ID))
	})
	n := q.PageSize()
	page := ProblemPage{Items: []Problem{}}
	for _, p := range all {
		if hasCur && !Before(p.UpdatedAt, p.ID, curT, curID) {
			continue
		}
		if len(page.Items) == n {
			last := page.Items[n-1]
			page.NextCursor = EncodeCursor(last.UpdatedAt, last.ID)
			break
		}
		page.Items = append(page.Items, p)
	}
	return page, nil
}

// DeleteProblem implements ProblemBackend.
func (b *FileBackend) DeleteProblem(ctx context.Context, scope tenant.Scope, id string) error {
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
	if err := os.Remove(b.problemPath(scope, id)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// PurgeProblems implements ProblemBackend: удаляет проблемы всех команд и кластеров организации, обновлённые
// раньше before.
func (b *FileBackend) PurgeProblems(ctx context.Context, orgID string, before time.Time) (int, error) {
	if err := ctxErr(ctx); err != nil {
		return 0, err
	}
	if err := checkIDs(orgID); err != nil {
		return 0, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	teams, err := subdirs(filepath.Join(b.root, orgID))
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, team := range teams {
		clusters, err := subdirs(filepath.Join(b.root, orgID, team))
		if err != nil {
			return removed, err
		}
		for _, cluster := range clusters {
			if err := ctxErr(ctx); err != nil {
				return removed, err
			}
			dir := filepath.Join(b.root, orgID, team, cluster, "problems")
			entries, err := os.ReadDir(dir)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return removed, err
			}
			for _, e := range entries {
				var p Problem
				if e.IsDir() || readJSON(filepath.Join(dir, e.Name()), &p) != nil {
					continue
				}
				if p.UpdatedAt.Before(before) {
					if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
						return removed, err
					}
					removed++
				}
			}
		}
	}
	return removed, nil
}
