// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	repo "helm.sh/helm/v4/pkg/repo/v1"
)

// Кэш индексов и тегов принадлежит реплике, а список репозиториев — БД (ADR-0015, ADR-0016). Репозиторий, добавленный
// на одной реплике, на другой не имеет кэша. В сервисе недостающее скачивается по первому обращению (lazyCache);
// список и поиск обновлений не ждут: они запускают скачивание в фоне и показывают прежнее сообщение «запустите
// Update», пока кэш не появится. Ограничение: «Update» обновляет кэш только той реплики, что приняла запрос.

var (
	helmFetchLocks sync.Map // ключ кэша -> *sync.Mutex: одна загрузка на ключ, остальные ждут её результат
	helmWarming    sync.Map // ключ кэша -> struct{}: фоновая загрузка уже идёт
)

func fetchLock(key string) *sync.Mutex {
	mu, _ := helmFetchLocks.LoadOrStore(key, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// fetchIndex скачивает индекс HTTP-репозитория, если его нет в кэше реплики.
func (sc *helmScope) fetchIndex(ctx context.Context, name string) error {
	key := sc.key(name)
	mu := fetchLock(key)
	mu.Lock()
	defer mu.Unlock()
	if _, err := os.Stat(sc.indexPath(name)); err == nil {
		return nil // успела другая загрузка
	}
	r, ok, err := sc.repo(name)
	if err != nil {
		return err
	}
	if !ok || r.Kind != helmKindHTTP {
		return fmt.Errorf("repository %q not found", name)
	}
	tmp, _, err := downloadHelmIndex(ctx, sc, entryOf(r))
	if err != nil {
		return fmt.Errorf("repository %q is unavailable: %w", name, err)
	}
	helmCacheMu.Lock()
	defer helmCacheMu.Unlock()
	return commitHelmIndex(sc, name, tmp)
}

// loadHelmIndexOrFetch — loadHelmIndex, который в сервисе докачивает отсутствующий индекс.
func loadHelmIndexOrFetch(ctx context.Context, sc *helmScope, name string) (*repo.IndexFile, time.Time, error) {
	idx, mod, err := loadHelmIndex(sc, name)
	if err == nil || !errors.Is(err, os.ErrNotExist) || !sc.lazyCache() {
		return idx, mod, err
	}
	if err := sc.fetchIndex(ctx, name); err != nil {
		return nil, time.Time{}, err
	}
	return loadHelmIndex(sc, name)
}

// warm запускает загрузку в фоне (не больше одной на ключ), чтобы список и поиск обновлений не ждали сеть.
func (sc *helmScope) warm(key string, fetch func(context.Context) error) {
	if _, busy := helmWarming.LoadOrStore(key, struct{}{}); busy {
		return
	}
	go func() {
		defer helmWarming.Delete(key)
		ctx, cancel := context.WithTimeout(context.Background(), helmRepoHTTPTimeout)
		defer cancel()
		_ = fetch(ctx)
	}()
}

func (sc *helmScope) warmIndex(name string) {
	if sc.lazyCache() {
		sc.warm(sc.key(name), func(ctx context.Context) error { return sc.fetchIndex(ctx, name) })
	}
}

// warmOCI докачивает теги всех чартов OCI-реестра, которых нет в кэше реплики.
func (sc *helmScope) warmOCI(rec HelmRepoRecord) {
	if !sc.lazyCache() {
		return
	}
	sc.warm(sc.key(rec.Name)+"/oci", func(context.Context) error {
		mu := fetchLock(sc.key(rec.Name) + "/oci")
		mu.Lock()
		defer mu.Unlock()
		if res := sc.ociUpdateOne(rec); res.Error != "" {
			return errors.New(res.Error)
		}
		return nil
	})
}

// ociTags возвращает теги чарта из кэша; в сервисе при промахе забирает их из реестра (кэш этой реплики мог быть пуст
// или не знать чарта, добавленного на другой реплике). Чужие чарты не запрашиваются: только перечисленные в реестре.
func (sc *helmScope) ociTags(r ociRepo, chart string) ([]string, error) {
	cache, err := sc.ociReadTags(r.Name)
	if err == nil && len(cache.Tags[chart]) > 0 {
		return cache.Tags[chart], nil
	}
	if !sc.lazyCache() || !slices.Contains(r.Charts, chart) {
		if err != nil {
			return nil, fmt.Errorf("tags of registry %q are not loaded: run Update", r.Name)
		}
		return nil, fmt.Errorf("chart %q not found in registry %q", chart, r.Name)
	}
	mu := fetchLock(sc.key(r.Name) + "/oci")
	mu.Lock()
	defer mu.Unlock()
	if cache, err = sc.ociReadTags(r.Name); err == nil && len(cache.Tags[chart]) > 0 {
		return cache.Tags[chart], nil // успела другая загрузка
	}
	tags, err := sc.ociFetchTags(&r, chart)
	if err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		return nil, fmt.Errorf("chart %q not found in registry %q", chart, r.Name)
	}
	if cache.Tags == nil {
		cache.Tags = map[string][]string{}
	}
	cache.Tags[chart] = tags
	cache.Updated = time.Now().UTC()
	helmCacheMu.Lock()
	defer helmCacheMu.Unlock()
	if err := sc.ociWriteTags(r.Name, cache); err != nil {
		return nil, err
	}
	return tags, nil
}
