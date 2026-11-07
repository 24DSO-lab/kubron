// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"log/slog"
	"maps"
	"runtime"
	"strings"
	"sync"
	"time"
	"weak"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

// Обнаружение Flux (ADR-0023, раздел 3): один вызов discovery на подключение, результат живёт fluxDiscoveryTTL.
// Из него строятся и FluxStatus для интерфейса, и карта «вид → обслуживаемая версия» для fluxList.

// fluxDiscoveryTTL — время жизни результата discovery. Короткое: установку или удаление Flux пользователь должен
// увидеть быстро, но десятки вызовов GetFluxStatus и fluxList за секунду не должны превращаться в десятки обходов API.
const fluxDiscoveryTTL = 30 * time.Second

// fluxDiscoveryEntry — кэшированный результат discovery одного подключения.
type fluxDiscoveryEntry struct {
	mu     sync.Mutex // удерживается на время самого discovery: параллельные вызовы ждут и берут свежий результат
	at     time.Time  // момент получения; нулевой — результата ещё нет
	status FluxStatus
}

// fluxDiscoveries — кэши по подключениям. ClusterConnection принадлежит client.go (контракт, не правится в этой
// задаче), поэтому кэш лежит рядом и ключуется слабой ссылкой: запись не удерживает подключение в памяти и
// удаляется, когда подключение собрано сборщиком мусора (runtime.AddCleanup).
var fluxDiscoveries = struct {
	mu sync.Mutex
	m  map[weak.Pointer[ClusterConnection]]*fluxDiscoveryEntry
}{m: make(map[weak.Pointer[ClusterConnection]]*fluxDiscoveryEntry)}

// fluxDiscoveryEntryFor возвращает запись кэша подключения, создавая её при первом обращении.
func fluxDiscoveryEntryFor(conn *ClusterConnection) *fluxDiscoveryEntry {
	key := weak.Make(conn)

	fluxDiscoveries.mu.Lock()
	defer fluxDiscoveries.mu.Unlock()
	if e, ok := fluxDiscoveries.m[key]; ok {
		return e
	}
	e := &fluxDiscoveryEntry{}
	fluxDiscoveries.m[key] = e
	// Замыкание не ссылается на conn: иначе подключение никогда не станет недостижимым.
	runtime.AddCleanup(conn, func(k weak.Pointer[ClusterConnection]) {
		fluxDiscoveries.mu.Lock()
		delete(fluxDiscoveries.m, k)
		fluxDiscoveries.mu.Unlock()
	}, key)
	return e
}

// emptyFluxStatus — «Flux не установлен». Versions всегда непустая карта: фронтенд читает её как объект.
func emptyFluxStatus() FluxStatus {
	return FluxStatus{Versions: map[string]string{}}
}

// cloneFluxStatus копирует статус, чтобы вызывающий не мог испортить кэш, изменив карту версий.
func cloneFluxStatus(s FluxStatus) FluxStatus {
	s.Versions = maps.Clone(s.Versions)
	if s.Versions == nil {
		s.Versions = map[string]string{}
	}
	return s
}

// fluxDiscoveryStatus возвращает статус Flux подключения: из кэша, а по истечении TTL — новым обходом discovery.
// Подключение без DiscoveryClient (бывает только в тестах) считается кластером без Flux.
func fluxDiscoveryStatus(conn *ClusterConnection) FluxStatus {
	if conn == nil || conn.DiscoveryClient == nil {
		return emptyFluxStatus()
	}
	e := fluxDiscoveryEntryFor(conn)

	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.at.IsZero() && time.Since(e.at) < fluxDiscoveryTTL {
		return cloneFluxStatus(e.status)
	}
	e.status = fluxDiscover(conn.DiscoveryClient)
	e.at = time.Now()
	return cloneFluxStatus(e.status)
}

// fluxDiscover делает один вызов discovery и разбирает его результат.
//
// Частичная ошибка группы (ErrGroupDiscoveryFailed: например, недоступен metrics-server) — это частичный успех:
// в ответе остаются все остальные группы, и Flux из-за чужого сбоя не пропадает. Любая другая ошибка (кластер
// недоступен, нет прав на discovery) значит «Flux не обнаружен»: раздел просто скрыт, интерфейс ошибки не видит.
func fluxDiscover(d discovery.DiscoveryInterface) FluxStatus {
	_, lists, err := d.ServerGroupsAndResources()
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		slog.Debug("Flux discovery failed, treating Flux as not installed", "error", err)
		return emptyFluxStatus()
	}

	// served: какие версии API каждого (группа, plural) обслуживает кластер. Подресурсы («kustomizations/status»)
	// отдельным CRD не считаются.
	type resKey struct{ group, plural string }
	served := make(map[resKey]map[string]struct{})
	for _, list := range lists {
		if list == nil {
			continue
		}
		gv, perr := schema.ParseGroupVersion(list.GroupVersion)
		if perr != nil {
			continue
		}
		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") {
				continue
			}
			k := resKey{gv.Group, r.Name}
			if served[k] == nil {
				served[k] = make(map[string]struct{})
			}
			served[k][gv.Version] = struct{}{}
		}
	}

	st := emptyFluxStatus()
	unsupported := false
	for _, k := range fluxKinds {
		vs := served[resKey{k.Group, k.Plural}]
		if len(vs) == 0 {
			continue // CRD нет: вид не установлен
		}
		switch k.Controller {
		case fluxCtrlKustomize:
			st.Controllers.Kustomize = true
		case fluxCtrlHelm:
			st.Controllers.Helm = true
		case fluxCtrlSource:
			st.Controllers.Source = true
		case fluxCtrlNotification:
			st.Controllers.Notification = true
		case fluxCtrlImage:
			st.Controllers.Image = true
		}

		// Версия — первая из allowlist вида (порядок предпочтения), которую обслуживает кластер.
		found := false
		for _, v := range k.Versions {
			if _, ok := vs[v]; ok {
				st.Versions[k.Kind] = v
				found = true
				break
			}
		}
		if !found {
			unsupported = true // CRD есть, но только устаревшие версии (Flux < 2.3 или не выполнен flux migrate)
		}
	}

	st.Installed = st.Controllers.Kustomize || st.Controllers.Helm
	switch {
	case !st.Installed:
		// State пуст: Flux не установлен.
	case unsupported:
		st.State = FluxStatusUnsupportedVersion
	default:
		st.State = FluxStatusOK
	}
	return st
}

// GetFluxStatus сообщает, какие контроллеры Flux есть в активном кластере и какие версии API они обслуживают.
// Нет CRD, нет прав или кластер недоступен — это «не установлен», а не ошибка: раздел скрыт.
func (m *Manager) GetFluxStatus() (*FluxStatus, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	st := fluxDiscoveryStatus(conn)
	return &st, nil
}

// fluxServedVersions возвращает обслуживаемую версию API по виду («Kustomization» → «v1») из того же кэша discovery,
// что и GetFluxStatus. Видов без CRD или без версии из allowlist в карте нет: fluxList отвечает на них
// errFluxVersionUnsupported, а B.4–B.6 пропускают такие виды.
//
// Подключение без DiscoveryClient (в продакшене не бывает, так устроены тесты соседних задач с fake dynamic client)
// получает предпочтительные версии всех видов, чтобы fluxList работал без discovery.
func fluxServedVersions(conn *ClusterConnection) (map[string]string, error) {
	if conn == nil {
		return nil, errFluxNotInstalled
	}
	if conn.DiscoveryClient == nil {
		out := make(map[string]string, len(fluxKinds))
		for _, k := range fluxKinds {
			out[k.Kind] = k.Versions[0]
		}
		return out, nil
	}
	return fluxDiscoveryStatus(conn).Versions, nil
}
