// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

// Тестовый «вид» ресурса: показывает, что кэш не привязан к Argo и работает с любым T.
var (
	widgetGVR  = schema.GroupVersionResource{Group: "example.dev", Version: "v1", Resource: "widgets"}
	gadgetGVR  = schema.GroupVersionResource{Group: "example.dev", Version: "v1", Resource: "gadgets"}
	probeRetry = crdProbeRetry
)

type widgetInfo struct{ Namespace, Name, Value string }

func parseWidget(u *unstructured.Unstructured) widgetInfo {
	v, _, _ := unstructured.NestedString(u.Object, "spec", "value")
	return widgetInfo{Namespace: u.GetNamespace(), Name: u.GetName(), Value: v}
}

func crdObj(kind, ns, name string, spec map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.dev/v1",
		"kind":       kind,
		"metadata":   map[string]any{"namespace": ns, "name": name},
	}}
	if spec != nil {
		u.Object["spec"] = spec
	}
	return u
}

func newFakeCRDDyn(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		widgetGVR: "WidgetList",
		gadgetGVR: "GadgetList",
	}, objs...)
}

func newWidgetCache(t *testing.T, dyn *dynamicfake.FakeDynamicClient, cfg crdCacheConfig[widgetInfo]) *crdCache[widgetInfo] {
	t.Helper()
	cfg.GVR = widgetGVR
	if cfg.Parse == nil {
		cfg.Parse = parseWidget
	}
	c, err := newCRDCache(dyn, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCRDCacheListNotSyncedAndFilterBeforeParse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dyn := newFakeCRDDyn(
			crdObj("Widget", "ns-a", "x", nil),
			crdObj("Widget", "ns-a", "y", nil),
			crdObj("Widget", "ns-b", "z", nil),
		)
		var parsed atomic.Int32
		c := newWidgetCache(t, dyn, crdCacheConfig[widgetInfo]{
			Parse: func(u *unstructured.Unstructured) widgetInfo { parsed.Add(1); return parseWidget(u) },
		})
		defer c.stop()

		// Горутины informer в «пузыре» ещё не запускались: кэш гарантированно не синхронизирован.
		if got, ok := c.list(nsScope{}); ok || got != nil {
			t.Fatalf("before sync the list must be (nil, false): ok=%v %+v", ok, got)
		}

		synctest.Wait()
		got, ok := c.list(nsScope{names: []string{"ns-a"}})
		if !ok || len(got) != 2 {
			t.Fatalf("ns-a: ok=%v %+v", ok, got)
		}
		if parsed.Load() != 2 {
			t.Fatalf("objects outside the scope must be filtered before parsing: parsed %d, expected 2", parsed.Load())
		}
		all, ok := c.list(nsScope{})
		if !ok || len(all) != 3 {
			t.Fatalf("all namespaces: ok=%v %+v", ok, all)
		}
		none, ok := c.list(nsScope{names: []string{"ns-missing"}})
		if !ok || len(none) != 0 {
			t.Fatalf("empty namespace: ok=%v %+v", ok, none)
		}
	})
}

func TestCRDCacheTransform(t *testing.T) {
	newObj := func(name string) *unstructured.Unstructured {
		u := crdObj("Widget", "ns", name, map[string]any{"value": "S3CRET-values", "keep": "visible"})
		u.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "kubectl", Operation: metav1.ManagedFieldsOperationApply}})
		return u
	}
	stored := func(c *crdCache[widgetInfo]) *unstructured.Unstructured {
		objs := c.inf.GetStore().List()
		if len(objs) != 1 {
			t.Fatalf("expected one object in the store, got %d", len(objs))
		}
		return objs[0].(*unstructured.Unstructured)
	}

	t.Run("managedFields are always removed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			c := newWidgetCache(t, newFakeCRDDyn(newObj("a")), crdCacheConfig[widgetInfo]{})
			defer c.stop()
			synctest.Wait()
			u := stored(c)
			if u.GetManagedFields() != nil {
				t.Errorf("managedFields must not stay in the cache: %+v", u.GetManagedFields())
			}
			if v, _, _ := unstructured.NestedString(u.Object, "spec", "value"); v != "S3CRET-values" {
				t.Errorf("without a Scrub hook the rest of the object is untouched, spec.value=%q", v)
			}
		})
	})

	t.Run("Scrub extension point cuts extra fields", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			c := newWidgetCache(t, newFakeCRDDyn(newObj("a")), crdCacheConfig[widgetInfo]{
				Scrub: func(u *unstructured.Unstructured) { unstructured.RemoveNestedField(u.Object, "spec", "value") },
			})
			defer c.stop()
			synctest.Wait()
			u := stored(c)
			if u.GetManagedFields() != nil {
				t.Error("managedFields must be removed together with Scrub")
			}
			if _, found, _ := unstructured.NestedString(u.Object, "spec", "value"); found {
				t.Error("Scrub must remove spec.value from the cache")
			}
			if k, _, _ := unstructured.NestedString(u.Object, "spec", "keep"); k != "visible" {
				t.Errorf("Scrub must not touch other fields, keep=%q", k)
			}
			got, ok := c.list(nsScope{})
			if !ok || len(got) != 1 || got[0].Value != "" {
				t.Errorf("parser sees the object after Scrub: ok=%v %+v", ok, got)
			}
		})
	})
}

func TestCRDCacheNotifiesOncePerBurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dyn := newFakeCRDDyn()
		var calls atomic.Int32
		c := newWidgetCache(t, dyn, crdCacheConfig[widgetInfo]{OnChange: func() { calls.Add(1) }, Debounce: 50 * time.Millisecond})
		defer c.stop()
		synctest.Wait()

		for _, name := range []string{"a", "b", "c"} {
			if _, err := dyn.Resource(widgetGVR).Namespace("ns").Create(context.Background(), crdObj("Widget", "ns", name, nil), metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		if calls.Load() != 0 {
			t.Fatalf("a notification must not come before the debounce window ends, got %d", calls.Load())
		}
		time.Sleep(49 * time.Millisecond)
		if calls.Load() != 0 {
			t.Fatalf("window is 50ms, a notification at 49ms is early: %d", calls.Load())
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("a burst of 3 changes must give 1 notification, got %d", got)
		}
		time.Sleep(time.Second)
		if got := calls.Load(); got != 1 {
			t.Fatalf("no extra notifications are expected, got %d", got)
		}
		if l, ok := c.list(nsScope{}); !ok || len(l) != 3 {
			t.Fatalf("objects in cache: ok=%v n=%d", ok, len(l))
		}

		// Удаление уведомляет отдельным окном.
		if err := dyn.Resource(widgetGVR).Namespace("ns").Delete(context.Background(), "a", metav1.DeleteOptions{}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
		if got := calls.Load(); got != 2 {
			t.Fatalf("delete must notify once more, got %d", got)
		}
	})
}

func TestCRDCacheStopSilencesNotifications(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dyn := newFakeCRDDyn()
		var calls atomic.Int32
		c := newWidgetCache(t, dyn, crdCacheConfig[widgetInfo]{OnChange: func() { calls.Add(1) }, Debounce: 30 * time.Millisecond})
		synctest.Wait()

		if _, err := dyn.Resource(widgetGVR).Namespace("ns").Create(context.Background(), crdObj("Widget", "ns", "a", nil), metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		c.mu.Lock()
		armed := c.timer != nil
		c.mu.Unlock()
		if !armed {
			t.Fatal("debounce timer must be armed after a change")
		}
		c.stop()
		time.Sleep(time.Second)
		if calls.Load() != 0 {
			t.Fatalf("no notifications are expected after stop, got %d", calls.Load())
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.stopped || c.timer != nil {
			t.Errorf("stop must reset the timer: stopped=%v timer=%v", c.stopped, c.timer)
		}
	})
}

func TestCRDCacheWithoutOnChangeArmsNoTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dyn := newFakeCRDDyn()
		c := newWidgetCache(t, dyn, crdCacheConfig[widgetInfo]{})
		defer c.stop()
		synctest.Wait()
		_, _ = dyn.Resource(widgetGVR).Namespace("ns").Create(context.Background(), crdObj("Widget", "ns", "a", nil), metav1.CreateOptions{})
		synctest.Wait()
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.timer != nil {
			t.Error("without onChange there is nothing to debounce")
		}
	})
}

func TestCRDCacheDefaultDebounce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dyn := newFakeCRDDyn()
		var calls atomic.Int32
		c := newWidgetCache(t, dyn, crdCacheConfig[widgetInfo]{OnChange: func() { calls.Add(1) }}) // Debounce не задан
		defer c.stop()
		synctest.Wait()
		_, _ = dyn.Resource(widgetGVR).Namespace("ns").Create(context.Background(), crdObj("Widget", "ns", "a", nil), metav1.CreateOptions{})
		synctest.Wait()
		time.Sleep(crdDefaultDebounce - time.Millisecond)
		if calls.Load() != 0 {
			t.Fatal("early notification")
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("default debounce is %s, notifications: %d", crdDefaultDebounce, calls.Load())
		}
	})
}

// --- реестр кэшей в ClusterConnection ---

func buildWidgetCache(conn *ClusterConnection, builds *atomic.Int32) func() (*crdCache[widgetInfo], error) {
	return func() (*crdCache[widgetInfo], error) {
		builds.Add(1)
		return newCRDCache(conn.DynamicClient, crdCacheConfig[widgetInfo]{GVR: widgetGVR, Parse: parseWidget})
	}
}

func TestCRDRegistryCreatesOnceAndCloseStopsAll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn := &ClusterConnection{DynamicClient: newFakeCRDDyn(crdObj("Widget", "ns", "a", nil), crdObj("Gadget", "ns", "g", nil))}
		var wBuilds, gBuilds atomic.Int32

		w1, ok := acquireCRDCache(&conn.crdCaches, conn.DynamicClient, widgetGVR, buildWidgetCache(conn, &wBuilds))
		if !ok || w1 == nil {
			t.Fatal("cache must be created when list is allowed")
		}
		w2, _ := acquireCRDCache(&conn.crdCaches, conn.DynamicClient, widgetGVR, buildWidgetCache(conn, &wBuilds))
		if w2 != w1 || wBuilds.Load() != 1 {
			t.Fatalf("the cache must be created lazily and once per GVR: same=%v builds=%d", w2 == w1, wBuilds.Load())
		}
		g, ok := acquireCRDCache(&conn.crdCaches, conn.DynamicClient, gadgetGVR, func() (*crdCache[widgetInfo], error) {
			gBuilds.Add(1)
			return newCRDCache(conn.DynamicClient, crdCacheConfig[widgetInfo]{GVR: gadgetGVR, Parse: parseWidget})
		})
		if !ok || g == nil || gBuilds.Load() != 1 {
			t.Fatal("another GVR gets its own cache")
		}
		if !conn.crdCaches.has(widgetGVR) || !conn.crdCaches.has(gadgetGVR) {
			t.Fatal("registry must hold both caches")
		}

		conn.Close()
		for name, c := range map[string]*crdCache[widgetInfo]{"widgets": w1, "gadgets": g} {
			if !c.stopped {
				t.Errorf("Close must stop the %s cache", name)
			}
		}
		if conn.crdCaches.has(widgetGVR) || conn.crdCaches.has(gadgetGVR) {
			t.Error("Close must empty the registry")
		}
		// После Close подключение можно использовать снова: кэш создаётся заново.
		w3, ok := acquireCRDCache(&conn.crdCaches, conn.DynamicClient, widgetGVR, buildWidgetCache(conn, &wBuilds))
		if !ok || w3 == nil || w3 == w1 {
			t.Fatal("after Close a new cache is created")
		}
		conn.Close()
	})
}

func TestCRDRegistryRetryAtIsPerKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dyn := newFakeCRDDyn(crdObj("Gadget", "ns", "g", nil))
		var widgetLists atomic.Int32
		forbidden := atomic.Bool{}
		forbidden.Store(true)
		dyn.PrependReactor("list", "widgets", func(clienttesting.Action) (bool, runtime.Object, error) {
			widgetLists.Add(1)
			if forbidden.Load() {
				return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "example.dev", Resource: "widgets"}, "", nil)
			}
			return false, nil, nil
		})
		conn := &ClusterConnection{DynamicClient: dyn}
		var builds atomic.Int32

		// Отказ по widgets: nil, пауза; повторная проба до истечения паузы не допускается.
		for range 2 {
			if c, ok := acquireCRDCache(&conn.crdCaches, dyn, widgetGVR, buildWidgetCache(conn, &builds)); ok || c != nil {
				t.Fatal("there must be no cache without list permission")
			}
		}
		if widgetLists.Load() != 1 || builds.Load() != 0 {
			t.Fatalf("one probe before the pause expires: LIST=%d builds=%d", widgetLists.Load(), builds.Load())
		}

		// Пауза у widgets не мешает gadgets.
		g, ok := acquireCRDCache(&conn.crdCaches, dyn, gadgetGVR, func() (*crdCache[widgetInfo], error) {
			return newCRDCache(dyn, crdCacheConfig[widgetInfo]{GVR: gadgetGVR, Parse: parseWidget})
		})
		if !ok || g == nil {
			t.Fatal("retryAt is kept per GVR: the pause on widgets must not block gadgets")
		}

		// Ровно через crdProbeRetry проба повторяется; права появились — кэш создаётся.
		time.Sleep(probeRetry - time.Second)
		if _, ok := acquireCRDCache(&conn.crdCaches, dyn, widgetGVR, buildWidgetCache(conn, &builds)); ok || widgetLists.Load() != 1 {
			t.Fatalf("the pause (%s) has not expired yet: LIST=%d", probeRetry, widgetLists.Load())
		}
		time.Sleep(time.Second)
		forbidden.Store(false)
		w, ok := acquireCRDCache(&conn.crdCaches, dyn, widgetGVR, buildWidgetCache(conn, &builds))
		if !ok || w == nil || widgetLists.Load() != 2 || builds.Load() != 1 {
			t.Fatalf("after the pause the probe repeats: ok=%v LIST=%d builds=%d", ok, widgetLists.Load(), builds.Load())
		}
		conn.Close()
	})
}

func TestCRDRegistryBuildErrorPausesRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dyn := newFakeCRDDyn()
		var lists atomic.Int32
		dyn.PrependReactor("list", "widgets", func(clienttesting.Action) (bool, runtime.Object, error) {
			lists.Add(1)
			return false, nil, nil
		})
		conn := &ClusterConnection{DynamicClient: dyn}
		var builds atomic.Int32
		failing := func() (*crdCache[widgetInfo], error) { builds.Add(1); return nil, errors.New("boom") }

		for range 2 {
			if c, ok := acquireCRDCache(&conn.crdCaches, dyn, widgetGVR, failing); ok || c != nil {
				t.Fatal("a failed build gives no cache")
			}
		}
		if builds.Load() != 1 || lists.Load() != 1 {
			t.Fatalf("a failed build also starts the pause: builds=%d LIST=%d", builds.Load(), lists.Load())
		}
		time.Sleep(probeRetry)
		if _, ok := acquireCRDCache(&conn.crdCaches, dyn, widgetGVR, failing); ok || builds.Load() != 2 {
			t.Fatalf("after the pause the build is retried: builds=%d", builds.Load())
		}
	})
}

// Параллельные вызовы создают ровно один кэш (проверяется под -race).
func TestCRDRegistryConcurrentAcquire(t *testing.T) {
	conn := &ClusterConnection{DynamicClient: newFakeCRDDyn(crdObj("Widget", "ns", "a", nil))}
	defer conn.Close()
	var builds atomic.Int32
	var wg sync.WaitGroup
	caches := make([]*crdCache[widgetInfo], 16)
	for i := range caches {
		wg.Go(func() {
			caches[i], _ = acquireCRDCache(&conn.crdCaches, conn.DynamicClient, widgetGVR, buildWidgetCache(conn, &builds))
		})
	}
	wg.Wait()
	if builds.Load() != 1 {
		t.Fatalf("expected one build, got %d", builds.Load())
	}
	for i, c := range caches {
		if c == nil || c != caches[0] {
			t.Fatalf("goroutine %d got a different cache", i)
		}
	}
}
