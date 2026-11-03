// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"errors"
	"os"
	"strings"

	"github.com/Masterminds/semver/v3"
	repo "helm.sh/helm/v4/pkg/repo/v1"
)

// helmUpdate — доступное обновление чарта установленного релиза.
type helmUpdate struct {
	Latest     string
	Repo       string
	Deprecated bool
}

type helmRepoIndex struct {
	name string
	idx  *repo.IndexFile
}

// helmAllIndexes снимает индексы всех репозиториев области (HTTP и OCI) один раз.
func (m *Manager) helmAllIndexes() []helmRepoIndex {
	sc, err := m.helmScope()
	if err != nil {
		return nil
	}
	recs, err := sc.repos()
	if err != nil {
		return nil
	}
	var repos []helmRepoIndex
	for _, r := range recs {
		if r.Kind != helmKindHTTP {
			continue
		}
		if idx, _, err := loadHelmIndex(sc, r.Name); err == nil {
			repos = append(repos, helmRepoIndex{name: r.Name, idx: idx})
		} else if errors.Is(err, os.ErrNotExist) {
			sc.warmIndex(r.Name) // список релизов не ждёт сеть
		}
	}
	return append(repos, sc.ociIndexes(recs)...)
}

func (m *Manager) helmUpdateFinder() func(chart, installed string) *helmUpdate {
	repos := m.helmAllIndexes()
	return func(chart, installed string) *helmUpdate {
		return findHelmUpdate(repos, chart, installed)
	}
}

func findHelmUpdate(repos []helmRepoIndex, chart, installed string) *helmUpdate {
	cur, err := semver.NewVersion(installed)
	if err != nil {
		return nil
	}
	for _, r := range repos {
		versions := r.idx.Entries[chart]
		if !helmHasVersion(versions, cur) {
			continue
		}
		// версии отсортированы по убыванию: берём первую подходящую
		for _, cv := range versions {
			if cv == nil || cv.Metadata == nil {
				continue
			}
			v, err := semver.NewVersion(cv.Version)
			if err != nil {
				continue
			}
			if v.Prerelease() != "" && cur.Prerelease() == "" {
				continue
			}
			if v.GreaterThan(cur) {
				return &helmUpdate{Latest: strings.TrimPrefix(cv.Version, "v"), Repo: r.name, Deprecated: cv.Deprecated}
			}
			break // дальше только более старые версии
		}
		return nil // релиз «принадлежит» этому репозиторию, обновлений нет
	}
	return nil
}

func helmHasVersion(versions repo.ChartVersions, cur *semver.Version) bool {
	for _, cv := range versions {
		if cv == nil || cv.Metadata == nil {
			continue
		}
		if v, err := semver.NewVersion(cv.Version); err == nil && v.Equal(cur) {
			return true
		}
	}
	return false
}
