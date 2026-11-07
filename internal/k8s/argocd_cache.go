// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"sort"
	"time"

	"k8s.io/client-go/dynamic"
)

const (
	// EventArgoChanged — событие фронтенду: Application создан, изменён или удалён.
	// Отдельное от cluster:changed, чтобы Argo-списки не перезагружались от любых изменений в кластере.
	EventArgoChanged = "argocd:changed"

	// argoChangeDebounce — окно склейки серии изменений (например, массовый sync) в одно уведомление.
	argoChangeDebounce = crdDefaultDebounce
)

// argoCache — кэш Application: экземпляр обобщённого crdCache (informer, debounce, фильтр namespace)
// с разбором через parseArgoApplication. Здесь остаётся то, что относится только к Argo: сортировка списка
// и событие argocd:changed (его эмитит вызывающий код через onChange).
type argoCache struct {
	*crdCache[ArgoApplicationInfo]
}

func newArgoCache(dyn dynamic.Interface, onChange func(), debounce time.Duration) (*argoCache, error) {
	c, err := newCRDCache(dyn, crdCacheConfig[ArgoApplicationInfo]{
		GVR:      kindTable["application"].gvr,
		Parse:    parseArgoApplication,
		OnChange: onChange,
		Debounce: debounce,
	})
	if err != nil {
		return nil, err
	}
	return &argoCache{crdCache: c}, nil
}

// list возвращает Application из кэша для набора namespace (пустой набор — все) в порядке «namespace, имя»;
// false — informer ещё не синхронизирован.
func (c *argoCache) list(scope nsScope) ([]ArgoApplicationInfo, bool) {
	apps, ok := c.crdCache.list(scope)
	if !ok {
		return nil, false
	}
	sortArgoApplications(apps)
	return apps, true
}

func sortArgoApplications(apps []ArgoApplicationInfo) {
	sort.SliceStable(apps, func(i, j int) bool {
		if apps[i].Namespace != apps[j].Namespace {
			return apps[i].Namespace < apps[j].Namespace
		}
		return apps[i].Name < apps[j].Name
	})
}

// argoCache лениво запускает informer Application у активного подключения через реестр кэшей.
// Если права list нет (или CRD нет), возвращает nil, а следующая проба состоится не раньше crdProbeRetry.
func (c *ClusterConnection) argoCache(onChange func()) *argoCache {
	ac, _ := acquireCRDCache(&c.crdCaches, c.DynamicClient, kindTable["application"].gvr, func() (*argoCache, error) {
		return newArgoCache(c.DynamicClient, onChange, argoChangeDebounce)
	})
	return ac
}
