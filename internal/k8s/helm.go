// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"sort"
	"strings"
	"time"
)

/*
 * Helm хранит релизы в Secret типа helm.sh/release.v1 (по умолчанию) или в ConfigMap
 * (HELM_DRIVER=configmap), label owner=helm: data["release"] = base64(gzip(JSON релиза)).
 * Читаем оба хранилища напрямую через k8s API (см. helm_store.go), без helm CLI.
 */

const helmSecretType = "helm.sh/release.v1"

// HelmReleaseInfo — установленный helm-релиз (последняя ревизия)
type HelmReleaseInfo struct {
	Name         string `json:"name"`
	Namespace    string `json:"namespace"`
	Chart        string `json:"chart"`
	ChartVersion string `json:"chartVersion"`
	AppVersion   string `json:"appVersion"`
	Revision     int    `json:"revision"`
	Status       string `json:"status"`
	Updated      string `json:"updated"`
	Description  string `json:"description"`

	Storage          string `json:"storage"` // secret | configmap: где Helm хранит ревизии релиза
	LatestVersion    string `json:"latestVersion"`
	LatestRepo       string `json:"latestRepo"`
	LatestDeprecated bool   `json:"latestDeprecated"`
}

// HelmChartInfo — агрегация установленных релизов по chart-у
type HelmChartInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	AppVersion  string `json:"appVersion"`
	Releases    int    `json:"releases"`
	Namespaces  string `json:"namespaces"`
	Updated     string `json:"updated"`
	Description string `json:"description"`
}

// helmPayload — используемые поля JSON из секрета релиза
type helmPayload struct {
	Storage   string `json:"-"` // helmStorageSecret | helmStorageConfigMap (не из JSON релиза)
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Version   int    `json:"version"`
	Info      struct {
		Status       string    `json:"status"`
		Description  string    `json:"description"`
		LastDeployed time.Time `json:"last_deployed"`
	} `json:"info"`
	Chart struct {
		Metadata struct {
			Name        string `json:"name"`
			Version     string `json:"version"`
			AppVersion  string `json:"appVersion"`
			Description string `json:"description"`
		} `json:"metadata"`
	} `json:"chart"`
}

func helmUpdated(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return formatAge(t)
}

// listHelmPayloads: последняя ревизия каждого релиза из Secret и ConfigMap; superseded (старые ревизии) не входят
func (m *Manager) listHelmPayloads() ([]helmPayload, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	items, err := listHelmStored(ctx, conn.Client, "", helmReleaseSelector(""))
	if err != nil {
		return nil, err
	}
	return latestHelmPayloads(items), nil
}

func isSuperseded(status string) bool { return strings.EqualFold(status, "superseded") }

func (m *Manager) GetHelmReleases() ([]HelmReleaseInfo, error) {
	m.startHelmLive()

	payloads, err := m.listHelmPayloads()
	if err != nil {
		return nil, err
	}

	findUpdate := m.helmUpdateFinder()
	releases := make([]HelmReleaseInfo, 0, len(payloads))
	for _, p := range payloads {
		info := HelmReleaseInfo{
			Name:         p.Name,
			Namespace:    p.Namespace,
			Chart:        p.Chart.Metadata.Name,
			ChartVersion: p.Chart.Metadata.Version,
			AppVersion:   p.Chart.Metadata.AppVersion,
			Revision:     p.Version,
			Status:       p.Info.Status,
			Updated:      helmUpdated(p.Info.LastDeployed),
			Description:  p.Info.Description,
			Storage:      p.Storage,
		}
		if u := findUpdate(info.Chart, info.ChartVersion); u != nil {
			info.LatestVersion,
				info.LatestRepo,
				info.LatestDeprecated = u.Latest,
				u.Repo, u.Deprecated
		}
		releases = append(releases, info)
	}

	sort.Slice(releases, func(i, j int) bool {
		if releases[i].Namespace != releases[j].Namespace {
			return releases[i].Namespace < releases[j].Namespace
		}
		return releases[i].Name < releases[j].Name
	})
	return releases, nil
}

func (m *Manager) GetHelmCharts() ([]HelmChartInfo, error) {
	payloads, err := m.listHelmPayloads()
	if err != nil {
		return nil, err
	}

	type chartAgg struct {
		info    HelmChartInfo
		updated time.Time
		nsSet   map[string]bool
	}

	charts := make(map[string]*chartAgg)
	for _, p := range payloads {
		name := p.Chart.Metadata.Name
		if name == "" {
			continue
		}
		agg, ok := charts[name]
		if !ok {
			agg = &chartAgg{
				info: HelmChartInfo{
					Name:        name,
					Version:     p.Chart.Metadata.Version,
					AppVersion:  p.Chart.Metadata.AppVersion,
					Description: p.Chart.Metadata.Description,
					Updated:     helmUpdated(p.Info.LastDeployed),
				},
				updated: p.Info.LastDeployed,
				nsSet:   map[string]bool{},
			}
			charts[name] = agg
		}
		agg.info.Releases++
		agg.nsSet[p.Namespace] = true
		// версия chart-а — по самому свежему релизу
		if p.Info.LastDeployed.After(agg.updated) {
			agg.updated = p.Info.LastDeployed
			agg.info.Version = p.Chart.Metadata.Version
			agg.info.AppVersion = p.Chart.Metadata.AppVersion
			agg.info.Updated = helmUpdated(p.Info.LastDeployed)
		}
	}

	result := make([]HelmChartInfo, 0, len(charts))
	for _, agg := range charts {
		ns := make([]string, 0, len(agg.nsSet))
		for n := range agg.nsSet {
			ns = append(ns, n)
		}
		sort.Strings(ns)
		agg.info.Namespaces = strings.Join(ns, ", ")
		result = append(result, agg.info)
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
