// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

const (
	// crdDefaultDebounce — окно склейки серии изменений (например, массовый sync или reconcile) в одно уведомление.
	crdDefaultDebounce = time.Second

	// crdProbeRetry — как часто повторять пробу прав, если list был запрещён или кэш не удалось создать.
	crdProbeRetry = 30 * time.Second

	// crdProbeTimeout — предел одной пробы права list.
	crdProbeTimeout = 10 * time.Second
)

// crdCacheConfig — параметры кэша ресурсов одного вида (GVR).
type crdCacheConfig[T any] struct {
	// GVR — вид, за которым следит informer.
	GVR schema.GroupVersionResource
	// Parse защитно превращает объект в контракт для UI (отсутствующее поле — нулевое значение, не паника).
	Parse func(*unstructured.Unstructured) T
	// Scrub — необязательная точка расширения: убирает из объекта данные, которым не место в памяти процесса
	// (например, spec.values у HelmRelease). Вызывается после удаления managedFields, до попадания в хранилище.
	Scrub func(*unstructured.Unstructured)
	// OnChange вызывается после изменения набора объектов, не чаще раза за Debounce. nil — без уведомлений.
	OnChange func()
	// Debounce — окно склейки уведомлений; ноль означает crdDefaultDebounce.
	Debounce time.Duration
}

// crdCache — локальный кэш ресурсов одного вида на основе dynamic shared informer.
// После первичной синхронизации список строится из памяти, а об изменениях сообщаем через OnChange.
// Пока informer не синхронизирован, вызывающий код читает через API (fallback).
//
// Сортировка и событие фронтенду — забота конкретного вида (см. argoCache): кэш отдаёт объекты в порядке хранилища.
type crdCache[T any] struct {
	cancel context.CancelFunc
	inf    cache.SharedIndexInformer
	parse  func(*unstructured.Unstructured) T

	mu       sync.Mutex
	timer    *time.Timer
	stopped  bool
	debounce time.Duration
	onChange func()
}

func newCRDCache[T any](dyn dynamic.Interface, cfg crdCacheConfig[T]) (*crdCache[T], error) {
	ctx, cancel := context.WithCancel(context.Background())
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(dyn, 0, metav1.NamespaceAll, nil)
	inf := factory.ForResource(cfg.GVR).Informer()

	// managedFields тяжёлые и UI не нужны: экономим память при тысячах объектов.
	scrub := cfg.Scrub
	if err := inf.SetTransform(func(obj any) (any, error) {
		if u, ok := obj.(*unstructured.Unstructured); ok {
			// Не мутируем объект, переданный источником informer: fake-клиенты могут
			// разделять его с tracker и параллельными List-вызовами. Store получает
			// отдельную копию, которую безопасно очищать.
			u = u.DeepCopy()
			u.SetManagedFields(nil)
			if scrub != nil {
				scrub(u)
			}
			return u, nil
		}
		return obj, nil
	}); err != nil {
		cancel()
		return nil, err
	}

	debounce := cfg.Debounce
	if debounce <= 0 {
		debounce = crdDefaultDebounce
	}
	c := &crdCache[T]{cancel: cancel, inf: inf, parse: cfg.Parse, debounce: debounce, onChange: cfg.OnChange}
	handler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { c.changed() },
		UpdateFunc: func(_, _ any) { c.changed() },
		DeleteFunc: func(any) { c.changed() },
	}
	if _, err := inf.AddEventHandler(handler); err != nil {
		cancel()
		return nil, err
	}
	factory.Start(ctx.Done())
	return c, nil
}

// changed помечает кэш изменившимся; серия вызовов склеивается в одно уведомление.
func (c *crdCache[T]) changed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped || c.onChange == nil || c.timer != nil {
		return
	}
	c.timer = time.AfterFunc(c.debounce, func() {
		c.mu.Lock()
		c.timer = nil
		stopped := c.stopped
		c.mu.Unlock()
		if !stopped {
			c.onChange()
		}
	})
}

// stop останавливает informer и гасит ожидающее уведомление. Безопасен при повторном вызове.
func (c *crdCache[T]) stop() {
	c.mu.Lock()
	c.stopped = true
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.mu.Unlock()
	c.cancel()
}

// list возвращает объекты из кэша для набора namespace (пустой набор — все), разобранные через Parse.
// Фильтр по namespace применяется до разбора: чужие объекты не разбираются. Порядок не определён.
// false — informer ещё не синхронизирован.
func (c *crdCache[T]) list(scope nsScope) ([]T, bool) {
	if !c.inf.HasSynced() {
		return nil, false
	}
	objs := c.inf.GetStore().List()
	items := make([]T, 0, len(objs))
	for _, o := range objs {
		u, ok := o.(*unstructured.Unstructured)
		if !ok || !scope.has(u.GetNamespace()) {
			continue
		}
		items = append(items, c.parse(u))
	}
	return items, true
}

// crdStopper — то, что реестр умеет останавливать; crdCache[T] подходит для любого T.
type crdStopper interface{ stop() }

// crdRegistry — реестр informer-кэшей подключения, ключ — GVR.
// Один mutex на все ключи, свой retryAt на каждый: отказ в правах на один вид не блокирует остальные.
// Нулевое значение готово к работе.
type crdRegistry struct {
	mu      sync.Mutex
	caches  map[schema.GroupVersionResource]crdStopper
	retryAt map[schema.GroupVersionResource]time.Time
}

// has сообщает, запущен ли кэш для GVR.
func (r *crdRegistry) has(gvr schema.GroupVersionResource) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.caches[gvr]
	return ok
}

// stopAll останавливает все кэши и сбрасывает паузы проб (вызывается из ClusterConnection.Close).
func (r *crdRegistry) stopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.caches {
		c.stop()
	}
	r.caches = nil
	r.retryAt = nil
}

// acquireCRDCache лениво запускает кэш для GVR (informer стартует при первом обращении, не при подключении).
// Перед стартом проверяется право list: без него reflector бесконечно повторял бы запрещённые запросы.
// Если права нет (или CRD нет, или build вернул ошибку), возвращает (нулевое значение, false), а следующая
// проба состоится не раньше crdProbeRetry. Повторные вызовы возвращают тот же кэш.
func acquireCRDCache[C crdStopper](r *crdRegistry, dyn dynamic.Interface, gvr schema.GroupVersionResource, build func() (C, error)) (C, bool) {
	var zero C
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.caches[gvr]; ok {
		c, ok := existing.(C)
		return c, ok
	}
	if time.Now().Before(r.retryAt[gvr]) {
		return zero, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), crdProbeTimeout)
	defer cancel()
	if _, err := dyn.Resource(gvr).List(ctx, metav1.ListOptions{Limit: 1}); err != nil {
		r.pause(gvr)
		return zero, false
	}
	c, err := build()
	if err != nil {
		r.pause(gvr)
		return zero, false
	}
	if r.caches == nil {
		r.caches = make(map[schema.GroupVersionResource]crdStopper)
	}
	r.caches[gvr] = c
	return c, true
}

// pause откладывает следующую пробу для GVR; вызывается под r.mu.
func (r *crdRegistry) pause(gvr schema.GroupVersionResource) {
	if r.retryAt == nil {
		r.retryAt = make(map[schema.GroupVersionResource]time.Time)
	}
	r.retryAt[gvr] = time.Now().Add(crdProbeRetry)
}
