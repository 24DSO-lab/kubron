// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/tools/cache"
)

const (
	// EventHelmChanged — событие фронтенду: секрет Helm-релиза создан, изменён или удалён
	// (установка, апгрейд, откат, удаление, в том числе из helm CLI или CI).
	EventHelmChanged = "helm:changed"

	// helmChangeDebounce — окно склейки серии изменений (один upgrade пишет несколько секретов).
	helmChangeDebounce = time.Second

	// helmProbeRetry — как часто повторять пробу прав, если list секретов был запрещён или упал.
	helmProbeRetry = 30 * time.Second
)

var (
	secretsGVR    = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	configMapsGVR = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
)

// helmCache следит за секретами и ConfigMap с меткой owner=helm (оба хранилища релизов Helm).
// Используется ТОЛЬКО metadata-informer: сами объекты (поле data с релизом, values и манифестом)
// в память Kubron не попадают, а нужен лишь факт изменения.
// Список релизов по-прежнему читается прямым запросом по событию.
type helmCache struct {
	cancel context.CancelFunc

	mu       sync.Mutex
	timer    *time.Timer
	stopped  bool
	debounce time.Duration
	onChange func()
}

func newHelmCache(md metadata.Interface, gvrs []schema.GroupVersionResource, onChange func(), debounce time.Duration) (*helmCache, error) {
	ctx, cancel := context.WithCancel(context.Background())
	factory := metadatainformer.NewFilteredSharedInformerFactory(md, 0, metav1.NamespaceAll,
		func(o *metav1.ListOptions) { o.LabelSelector = "owner=helm" })

	c := &helmCache{cancel: cancel, debounce: debounce, onChange: onChange}
	handler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { c.changed() },
		UpdateFunc: func(_, _ any) { c.changed() },
		DeleteFunc: func(any) { c.changed() },
	}
	for _, gvr := range gvrs {
		if _, err := factory.ForResource(gvr).Informer().AddEventHandler(handler); err != nil {
			cancel()
			return nil, err
		}
	}
	factory.Start(ctx.Done())
	return c, nil
}

// changed помечает кеш изменившимся; серия вызовов склеивается в одно уведомление.
func (c *helmCache) changed() {
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

func (c *helmCache) stop() {
	c.mu.Lock()
	c.stopped = true
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
	}
}

// helmCache лениво запускает informer у активного подключения. Перед стартом проверяется право list: без него
// reflector бесконечно повторял бы запрещённые запросы. Слежение включается за теми хранилищами (Secret,
// ConfigMap), к которым есть доступ; нет доступа ни к одному — nil, следующая проба не раньше helmProbeRetry
// (список всё равно обновляется таймером автообновления).
func (c *ClusterConnection) helmCache(onChange func()) *helmCache {
	c.helmMu.Lock()
	defer c.helmMu.Unlock()
	if c.helm != nil {
		return c.helm
	}
	if time.Now().Before(c.helmRetryAt) {
		return nil
	}
	fail := func() *helmCache {
		c.helmRetryAt = time.Now().Add(helmProbeRetry)
		return nil
	}

	md, err := metadata.NewForConfig(c.Config)
	if err != nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	gvrs := probeHelmWatchable(ctx, md)
	if len(gvrs) == 0 {
		return fail()
	}
	hc, err := newHelmCache(md, gvrs, onChange, helmChangeDebounce)
	if err != nil {
		return fail()
	}
	c.helm = hc
	return hc
}

// probeHelmWatchable возвращает хранилища релизов, список которых доступен текущему пользователю.
func probeHelmWatchable(ctx context.Context, md metadata.Interface) []schema.GroupVersionResource {
	var out []schema.GroupVersionResource
	for _, gvr := range []schema.GroupVersionResource{secretsGVR, configMapsGVR} {
		if _, err := md.Resource(gvr).List(ctx, metav1.ListOptions{LabelSelector: "owner=helm", Limit: 1}); err == nil {
			out = append(out, gvr)
		}
	}
	return out
}

// closeHelmCache останавливает informer (вызывается из ClusterConnection.Close).
func (c *ClusterConnection) closeHelmCache() {
	c.helmMu.Lock()
	defer c.helmMu.Unlock()
	if c.helm != nil {
		c.helm.stop()
		c.helm = nil
	}
	c.helmRetryAt = time.Time{}
}

// startHelmLive запускает слежение за релизами активного кластера (идемпотентно, дёшево после первого вызова).
func (m *Manager) startHelmLive() {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return
	}
	conn.helmCache(func() { m.emitCluster(EventHelmChanged, conn) })
}
