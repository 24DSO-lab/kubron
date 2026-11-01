// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"helm.sh/helm/v4/pkg/action"
	ci "helm.sh/helm/v4/pkg/chart"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
)

/*
 * Апгрейд релиза на другую версию чарта.
 *
 * Helm не хранит, из какого репозитория поставлен релиз, поэтому UI получает список репозиториев,
 * где есть чарт с тем же именем (GetHelmUpgradeSources), и выбирает из них. Репозиторий, в индексе
 * которого есть установленная версия, помечен owns — он наиболее вероятный источник.
 *
 * Имя чарта клиент НЕ передаёт: оно берётся из самого релиза, а после загрузки чарта сверяется
 * с именем скачанного архива. Так релиз нельзя случайно перевести на чужой чарт с тем же
 * именем репозитория или версией.
 *
 * Values: user-supplied values передаются целиком (ResetValues), см. helmUpgrade. Редактор в UI
 * стартует с текущих user-values релиза, поэтому по умолчанию они переносятся на новую версию.
 */

// Положение версии относительно установленной (HelmUpgradeVersion.Relation).
const (
	helmVersionNewer   = "newer"
	helmVersionCurrent = "current"
	helmVersionOlder   = "older"
)

// HelmUpgradeVersion — версия чарта, доступная для апгрейда.
type HelmUpgradeVersion struct {
	Version     string `json:"version"`
	AppVersion  string `json:"appVersion"`
	Created     string `json:"created"`
	Description string `json:"description"`
	Deprecated  bool   `json:"deprecated"`
	Prerelease  bool   `json:"prerelease"`
	Relation    string `json:"relation"` // newer | current | older; пусто, если установленная версия не semver
}

// HelmUpgradeSource — репозиторий, в котором есть чарт установленного релиза.
type HelmUpgradeSource struct {
	Repo     string               `json:"repo"`
	Owns     bool                 `json:"owns"`     // установленная версия найдена в индексе этого репозитория
	Versions []HelmUpgradeVersion `json:"versions"` // новые первыми
}

// HelmUpgradeSources — установленный чарт и репозитории, из которых можно обновиться.
type HelmUpgradeSources struct {
	Chart   string              `json:"chart"`
	Current string              `json:"current"` // установленная версия чарта
	Sources []HelmUpgradeSource `json:"sources"`
}

// HelmChartUpgradeRequest — параметры апгрейда релиза на другую версию чарта.
type HelmChartUpgradeRequest struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`    // имя релиза
	Repo      string `json:"repo"`    // репозиторий каталога (из GetHelmUpgradeSources)
	Version   string `json:"version"` // целевая версия чарта (точная)
	Values    string `json:"values"`  // YAML user-values для новой ревизии (полный набор, не дельта)
	OpID      string `json:"opId"`    // идентификатор операции для helm:progress и отмены (необязателен)
}

// HelmChartUpgradePreview — результат dry-run апгрейда на другую версию чарта.
// Поля продублированы, а не встроены: Wails не разворачивает embedded-структуры.
type HelmChartUpgradePreview struct {
	CurrentVersion    string `json:"currentVersion"`
	NewVersion        string `json:"newVersion"`
	CurrentAppVersion string `json:"currentAppVersion"`
	NewAppVersion     string `json:"newAppVersion"`
	Downgrade         bool   `json:"downgrade"`  // целевая версия старше установленной
	Deprecated        bool   `json:"deprecated"` // целевой чарт помечен deprecated
	CurrentValues     string `json:"currentValues"`
	NewValues         string `json:"newValues"`
	CurrentManifest   string `json:"currentManifest"` // значения Secret скрыты
	NewManifest       string `json:"newManifest"`     // значения Secret скрыты
	Notes             string `json:"notes"`           // NOTES.txt новой ревизии
}

// validateHelmChartUpgradeRequest проверяет вход до любых сетевых вызовов.
func validateHelmChartUpgradeRequest(r *HelmChartUpgradeRequest) error {
	if err := validateHelmRef(r.Namespace, r.Name); err != nil {
		return err
	}
	if err := validateHelmRepoName(r.Repo); err != nil {
		return err
	}
	if _, err := parseHelmChartVersion(r.Version); err != nil {
		return err
	}
	return nil
}

// parseHelmChartVersion разбирает ТОЧНУЮ версию. Пустая строка и диапазоны («^1.2», «>=1») не
// принимаются: релиз не должен уехать на «последнюю» версию, которой пользователь не видел.
func parseHelmChartVersion(v string) (*semver.Version, error) {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 128 {
		return nil, errors.New("target chart version is not specified")
	}
	sv, err := semver.StrictNewVersion(strings.TrimPrefix(v, "v"))
	if err != nil {
		return nil, fmt.Errorf("invalid chart version %q: %w", v, err)
	}
	return sv, nil
}

// findHelmUpgradeSources выбирает репозитории с чартом chart; в каждом версии отсортированы по
// убыванию. owns — установленная версия есть в индексе. Репозитории с owns идут первыми.
func findHelmUpgradeSources(repos []helmRepoIndex, chart, installed string) []HelmUpgradeSource {
	cur, _ := semver.NewVersion(installed) // nil — установленная версия не semver: owns всегда false
	var out []HelmUpgradeSource
	for _, r := range repos {
		if r.idx == nil {
			continue
		}
		cvs := r.idx.Entries[chart]
		type ver struct {
			sv   *semver.Version
			info HelmUpgradeVersion
		}
		vers := make([]ver, 0, len(cvs))
		for _, cv := range cvs {
			if cv == nil || cv.Metadata == nil {
				continue
			}
			sv, err := semver.StrictNewVersion(strings.TrimPrefix(cv.Version, "v"))
			if err != nil {
				continue // parseHelmChartVersion такую версию не примет: не предлагаем то, что нельзя выбрать
			}
			created := ""
			if !cv.Created.IsZero() {
				created = cv.Created.UTC().Format(time.RFC3339)
			}
			vers = append(vers, ver{sv, HelmUpgradeVersion{
				Version: strings.TrimPrefix(cv.Version, "v"), AppVersion: cv.AppVersion, Created: created,
				Description: cv.Description, Deprecated: cv.Deprecated, Prerelease: sv.Prerelease() != "",
			}})
		}
		if len(vers) == 0 {
			continue
		}
		sort.SliceStable(vers, func(i, j int) bool { return vers[i].sv.GreaterThan(vers[j].sv) })
		src := HelmUpgradeSource{Repo: r.name, Versions: make([]HelmUpgradeVersion, 0, len(vers))}
		for _, v := range vers {
			if cur != nil {
				switch {
				case v.sv.Equal(cur):
					v.info.Relation = helmVersionCurrent
					src.Owns = true
				case v.sv.GreaterThan(cur):
					v.info.Relation = helmVersionNewer
				default:
					v.info.Relation = helmVersionOlder
				}
			}
			src.Versions = append(src.Versions, v.info)
		}
		out = append(out, src)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Owns != out[j].Owns {
			return out[i].Owns
		}
		return out[i].Repo < out[j].Repo
	})
	return out
}

// latestHelmRevision — установленная (последняя) ревизия релиза: имя и версия чарта.
func (m *Manager) latestHelmRevision(namespace, name string) (*HelmRevisionInfo, error) {
	d, err := m.GetHelmReleaseDetail(namespace, name)
	if err != nil {
		return nil, err
	}
	if len(d.History) == 0 {
		return nil, fmt.Errorf("release %s/%s not found", namespace, name)
	}
	return &d.History[0], nil
}

// GetHelmUpgradeSources — репозитории, из которых можно обновить чарт релиза, и их версии.
// Читает только локальные индексы репозиториев и метаданные релиза, сеть не нужна.
func (m *Manager) GetHelmUpgradeSources(namespace, name string) (*HelmUpgradeSources, error) {
	if err := validateHelmRef(namespace, name); err != nil {
		return nil, err
	}
	rev, err := m.latestHelmRevision(namespace, name)
	if err != nil {
		return nil, err
	}
	return &HelmUpgradeSources{
		Chart:   rev.Chart,
		Current: rev.ChartVersion,
		Sources: findHelmUpgradeSources(m.helmAllIndexes(), rev.Chart, rev.ChartVersion),
	}, nil
}

// helmUpgradeTarget скачивает целевой чарт и проверяет, что на него можно перевести релиз.
// Проверки выполняются ДО обращения к кластеру: ошибка не должна стоить попытки апгрейда.
func (m *Manager) helmUpgradeTarget(ctx context.Context, req *HelmChartUpgradeRequest, cur *HelmRevisionInfo) (*chartv2.Chart, *semver.Version, error) {
	want, err := parseHelmChartVersion(req.Version)
	if err != nil {
		return nil, nil, err
	}
	if installed, err := semver.NewVersion(cur.ChartVersion); err == nil && installed.Equal(want) {
		return nil, nil, fmt.Errorf("release %s/%s already uses %s %s: change values on the Edit values tab instead",
			req.Namespace, req.Name, cur.Chart, cur.ChartVersion)
	}

	sc, err := m.helmScope()
	if err != nil {
		return nil, nil, err
	}
	chrt, err := loadHelmChart(ctx, sc, req.Repo, cur.Chart, want.Original())
	if err != nil {
		return nil, nil, err
	}
	if chrt == nil || chrt.Metadata == nil {
		return nil, nil, errors.New("the downloaded chart has no metadata")
	}
	md := chrt.Metadata
	if md.Name != cur.Chart {
		return nil, nil, fmt.Errorf("repository %q returned chart %q instead of %q", req.Repo, md.Name, cur.Chart)
	}
	if got, err := semver.NewVersion(md.Version); err != nil || !got.Equal(want) {
		return nil, nil, fmt.Errorf("repository %q returned version %q instead of %q", req.Repo, md.Version, want.Original())
	}
	ac, err := ci.NewAccessor(chrt)
	if err != nil {
		return nil, nil, err
	}
	if ac.IsLibraryChart() {
		return nil, nil, errors.New("a library chart cannot be installed as a release")
	}
	if deps := ac.MetaDependencies(); len(deps) > 0 {
		if err := action.CheckDependencies(chrt, deps); err != nil {
			return nil, nil, fmt.Errorf("chart dependencies are missing: %w", err)
		}
	}
	return chrt, want, nil
}

// PreviewHelmChartUpgrade — dry-run апгрейда на другую версию чарта: в кластер и в историю
// ничего не пишется. Нужен secrets.read: в ответе манифесты и values.
func (m *Manager) PreviewHelmChartUpgrade(req HelmChartUpgradeRequest) (*HelmChartUpgradePreview, error) {
	if err := validateHelmChartUpgradeRequest(&req); err != nil {
		return nil, err
	}
	vals, err := parseHelmValues(req.Values)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	rev, err := m.latestHelmRevision(req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	chrt, want, err := m.helmUpgradeTarget(ctx, &req, rev)
	if err != nil {
		return nil, err
	}
	cur, next, curVals, err := m.helmUpgrade(ctx, req.Namespace, req.Name, chrt, vals, true, nil)
	if err != nil {
		return nil, fmt.Errorf("preview %s/%s: %w", req.Namespace, req.Name, err)
	}
	curY, err := marshalHelmValues(curVals)
	if err != nil {
		return nil, err
	}
	newY, err := marshalHelmValues(vals)
	if err != nil {
		return nil, err
	}
	installed, _ := semver.NewVersion(rev.ChartVersion)
	return &HelmChartUpgradePreview{
		CurrentVersion:    rev.ChartVersion,
		NewVersion:        chrt.Metadata.Version,
		CurrentAppVersion: rev.AppVersion,
		NewAppVersion:     chrt.Metadata.AppVersion,
		Downgrade:         installed != nil && want.LessThan(installed),
		Deprecated:        chrt.Metadata.Deprecated,
		CurrentValues:     curY,
		NewValues:         newY,
		CurrentManifest:   redactHelmManifest(cur.Manifest()),
		NewManifest:       redactHelmManifest(next.Manifest()),
		Notes:             next.Notes(),
	}, nil
}

// UpgradeHelmChart переводит релиз на другую версию чарта и возвращает номер созданной ревизии.
// Блокируется до готовности ресурсов (до helmUpgradeTimeout); отменяется по opID.
func (m *Manager) UpgradeHelmChart(req HelmChartUpgradeRequest) (rev int, err error) {
	if err := validateHelmChartUpgradeRequest(&req); err != nil {
		return 0, err
	}
	vals, err := parseHelmValues(req.Values)
	if err != nil {
		return 0, err
	}
	ctx, tr, err := m.beginHelmOp(context.Background(), req.OpID, "upgrade", req.Namespace, req.Name, true)
	if err != nil {
		return 0, err
	}
	defer func() { tr.finish(err) }()

	tr.setPhase("Loading chart…")
	cur, err := m.latestHelmRevision(req.Namespace, req.Name)
	if err != nil {
		return 0, err
	}
	tr.note(fmt.Sprintf("%s %s → %s (%s)", cur.Chart, cur.ChartVersion, strings.TrimPrefix(req.Version, "v"), req.Repo))
	chrt, _, err := m.helmUpgradeTarget(ctx, &req, cur)
	if err != nil {
		return 0, err
	}

	ctx, cancel := context.WithTimeout(ctx, helmUpgradeTimeout+30*time.Second)
	defer cancel()
	tr.setPhase("Applying changes and waiting for readiness…")
	_, next, _, err := m.helmUpgrade(ctx, req.Namespace, req.Name, chrt, vals, false, tr.handler())
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return 0, fmt.Errorf("upgrade of %s/%s did not finish within %s", req.Namespace, req.Name, helmUpgradeTimeout)
		}
		return 0, fmt.Errorf("upgrade %s/%s: %w", req.Namespace, req.Name, err)
	}
	tr.setRevision(next.Version())
	return next.Version(), nil
}
