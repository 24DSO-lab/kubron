// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"cmp"
	"log/slog"
	"slices"
)

// Пометка релизов Helm, которыми управляет Flux (ADR-0023, раздел 9; задача X.1). Ручной upgrade, rollback или
// uninstall такого релиза расходится с Git: helm-controller при очередном reconcile вернёт состояние из HelmRelease.
// Интерфейс по метке предупреждает об этом перед действием.
//
// Сопоставление идёт по HelmRelease из Flux (его читает GetFluxHelmReleases), а не по меткам хранилища релиза:
// Helm пишет в Secret только свои метки, а helm.toolkit.fluxcd.io/* helm-controller ставит на отрендеренные
// ресурсы. Из объекта Flux берутся только вид, namespace и имя — остальные поля (сообщения контроллера, чарт,
// условия) в ответ Helm не попадают.

// fluxHelmLister — источник HelmRelease; в работе это Manager.GetFluxHelmReleases, в тестах — подмена.
type fluxHelmLister func(namespaces []string) ([]FluxHelmReleaseInfo, error)

// helmReleaseKey — идентификатор релиза Helm в кластере: namespace релиза и его имя.
type helmReleaseKey struct{ namespace, name string }

// fluxHelmReleaseKey — какому релизу Helm соответствует HelmRelease. По умолчанию релиз ставится в namespace
// самого объекта и называется как он; spec.targetNamespace и spec.releaseName это меняют (B.3 уже отдаёт значения
// с учётом умолчаний, пустые поля — защита от неполных данных).
func fluxHelmReleaseKey(h FluxHelmReleaseInfo) helmReleaseKey {
	return helmReleaseKey{
		namespace: cmp.Or(h.TargetNamespace, h.Namespace),
		name:      cmp.Or(h.ReleaseName, h.Name),
	}
}

// markFluxManaged заполняет FluxManagedBy у релизов, на которые указывает HelmRelease.
// HelmRelease с kubeConfig (Remote) пропускаются: их релиз живёт в другом кластере, а не в этом. Если на один
// релиз указывают несколько HelmRelease (ошибка конфигурации), побеждает первый по namespace и имени объекта:
// результат не зависит от порядка чтения.
func markFluxManaged(releases []HelmReleaseInfo, hrs []FluxHelmReleaseInfo) {
	if len(releases) == 0 || len(hrs) == 0 {
		return
	}
	sorted := slices.SortedFunc(slices.Values(hrs), func(a, b FluxHelmReleaseInfo) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	index := make(map[helmReleaseKey]FluxRef, len(sorted))
	for _, h := range sorted {
		if h.Remote || h.Name == "" {
			continue
		}
		key := fluxHelmReleaseKey(h)
		if _, taken := index[key]; !taken {
			index[key] = FluxRef{Kind: "HelmRelease", Namespace: h.Namespace, Name: h.Name}
		}
	}
	for i := range releases {
		if r, ok := index[helmReleaseKey{releases[i].Namespace, releases[i].Name}]; ok {
			releases[i].FluxManagedBy = &r // r — копия значения из map, указатели релизов не пересекаются
		}
	}
}

// markFluxManagedHelm помечает релизы, управляемые Flux. Используется GetHelmReleases.
func (m *Manager) markFluxManagedHelm(releases []HelmReleaseInfo) {
	m.markFluxManagedHelmWith(releases, m.GetFluxHelmReleases)
}

// markFluxManagedHelmWith — markFluxManagedHelm с подменяемым источником HelmRelease.
//
// Без Flux лишних запросов нет: сначала смотрится кэшированный результат discovery (общий с GetFluxStatus, TTL
// fluxDiscoveryTTL), и объекты Flux читаются, только когда есть helm-controller и поддерживаемая версия API.
// Метка — дополнение к списку Helm, поэтому любая ошибка (нет прав на helmreleases, таймаут) не ломает список:
// релизы остаются без метки, причина пишется в отладочный журнал.
func (m *Manager) markFluxManagedHelmWith(releases []HelmReleaseInfo, list fluxHelmLister) {
	if len(releases) == 0 {
		return
	}
	conn, err := m.GetActiveConnection()
	if err != nil {
		return
	}
	st := fluxDiscoveryStatus(conn)
	if !st.Installed || st.State != FluxStatusOK || !st.Controllers.Helm {
		return
	}
	hrs, err := list(nil)
	if err != nil {
		slog.Debug("Flux HelmRelease unavailable, Helm releases are listed without the Flux marker", "error", err)
		return
	}
	markFluxManaged(releases, hrs)
}
