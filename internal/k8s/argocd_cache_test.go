// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

func newFakeArgoDyn(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	gvrToList := map[schema.GroupVersionResource]string{
		kindTable["application"].gvr: "ApplicationList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToList, objs...)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func TestArgoCacheListAndNamespaceFilter(t *testing.T) {
	dyn := newFakeArgoDyn(
		argoObj("Application", "ns-b", "z", nil),
		argoObj("Application", "ns-a", "y", nil),
		argoObj("Application", "ns-a", "x", nil),
	)
	ac, err := newArgoCache(dyn, nil, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer ac.stop()

	waitFor(t, "informer sync", ac.inf.HasSynced)

	all, ok := ac.list(nsScope{})
	if !ok || len(all) != 3 || all[0].Name != "x" || all[2].Namespace != "ns-b" {
		t.Fatalf("all: ok=%v %+v", ok, all)
	}
	one, ok := ac.list(nsScope{names: []string{"ns-a"}})
	if !ok || len(one) != 2 {
		t.Fatalf("ns-a: ok=%v %+v", ok, one)
	}
}

func TestArgoCacheFiltersBySet(t *testing.T) {
	dyn := newFakeArgoDyn(
		argoObj("Application", "ns-c", "w", nil),
		argoObj("Application", "ns-b", "z", nil),
		argoObj("Application", "ns-a", "y", nil),
		argoObj("Application", "ns-a", "x", nil),
	)
	ac, err := newArgoCache(dyn, nil, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer ac.stop()
	waitFor(t, "informer sync", ac.inf.HasSynced)

	got, ok := ac.list(nsScope{names: []string{"ns-a", "ns-c"}})
	if !ok || len(got) != 3 {
		t.Fatalf("set of two namespaces: ok=%v %+v", ok, got)
	}
	// порядок детерминирован: по namespace, затем по имени; ns-b в ответ не попадает
	want := [][2]string{{"ns-a", "x"}, {"ns-a", "y"}, {"ns-c", "w"}}
	for i, w := range want {
		if got[i].Namespace != w[0] || got[i].Name != w[1] {
			t.Errorf("position %d: %s/%s, expected %s/%s", i, got[i].Namespace, got[i].Name, w[0], w[1])
		}
	}

	none, ok := ac.list(nsScope{names: []string{"ns-missing"}})
	if !ok || len(none) != 0 {
		t.Errorf("namespace without an Application: ok=%v %+v", ok, none)
	}
}

func TestArgoCacheNotifiesOncePerBurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dyn := newFakeArgoDyn()
		var calls atomic.Int32
		ac, err := newArgoCache(dyn, func() { calls.Add(1) }, 50*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		defer ac.stop()
		synctest.Wait()

		gvr := kindTable["application"].gvr
		for _, name := range []string{"a", "b", "c"} {
			if _, err := dyn.Resource(gvr).Namespace("argocd").Create(context.Background(), argoObj("Application", "argocd", name, nil), metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("a burst of 3 changes must give 1 notification, got %d", got)
		}
		time.Sleep(150 * time.Millisecond)
		if got := calls.Load(); got != 1 {
			t.Fatalf("a burst of 3 changes must give 1 notification, got %d", got)
		}

		if l, _ := ac.list(nsScope{}); len(l) != 3 {
			t.Fatalf("objects in cache: %d", len(l))
		}

		// Удаление тоже уведомляет.
		if err := dyn.Resource(gvr).Namespace("argocd").Delete(context.Background(), "a", metav1.DeleteOptions{}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
		if got := calls.Load(); got != 2 {
			t.Fatalf("delete must notify, got %d", got)
		}
	})
}

func TestArgoCacheStopSilencesNotifications(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dyn := newFakeArgoDyn()
		var calls atomic.Int32
		ac, err := newArgoCache(dyn, func() { calls.Add(1) }, 30*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()

		_, _ = dyn.Resource(kindTable["application"].gvr).Namespace("argocd").Create(context.Background(), argoObj("Application", "argocd", "a", nil), metav1.CreateOptions{})
		synctest.Wait()
		ac.mu.Lock()
		armed := ac.timer != nil
		ac.mu.Unlock()
		if !armed {
			t.Fatal("timer must be armed after a change")
		}
		ac.stop()
		time.Sleep(100 * time.Millisecond)
		if calls.Load() != 0 {
			t.Fatalf("no notifications are expected after stop, got %d", calls.Load())
		}
	})
}

func TestConnectionArgoCacheLifecycle(t *testing.T) {
	t.Run("has permissions: cache is created once, Close stops it", func(t *testing.T) {
		conn := &ClusterConnection{DynamicClient: newFakeArgoDyn(argoObj("Application", "argocd", "a", nil))}
		first := conn.argoCache(nil)
		if first == nil || conn.argoCache(nil) != first {
			t.Fatal("the cache must be created lazily and only once")
		}
		conn.Close()
		if conn.crdCaches.has(kindTable["application"].gvr) || !first.stopped {
			t.Fatal("Close must stop and reset the cache")
		}
	})

	t.Run("no list permission: nil and a pause until the next probe", func(t *testing.T) {
		dyn := newFakeArgoDyn()
		var lists atomic.Int32
		dyn.PrependReactor("list", "applications", func(clienttesting.Action) (bool, runtime.Object, error) {
			lists.Add(1)
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: argoGroup, Resource: "applications"}, "", nil)
		})
		conn := &ClusterConnection{DynamicClient: dyn}

		first, second := conn.argoCache(nil), conn.argoCache(nil)
		if first != nil || second != nil {
			t.Fatal("there must be no cache without permissions")

		}
		if lists.Load() != 1 {
			t.Fatalf("a repeated probe before the pause expires is not allowed, LIST called %d times", lists.Load())
		}
	})
}

func TestGetArgoApplicationsUsesCacheAfterSync(t *testing.T) {
	dyn := newFakeArgoDyn(argoObj("Application", "argocd", "a", nil))
	var lists atomic.Int32
	dyn.PrependReactor("list", "applications", func(clienttesting.Action) (bool, runtime.Object, error) {
		lists.Add(1)
		return false, nil, nil
	})
	conn := &ClusterConnection{Name: "c", DynamicClient: dyn}
	ac := conn.argoCache(nil)
	if ac == nil {
		t.Fatal("cache was not created")
	}
	defer conn.Close()
	waitFor(t, "informer sync", ac.inf.HasSynced)

	before := lists.Load()
	got, ok := ac.list(nsScope{})
	if !ok || len(got) != 1 || lists.Load() != before {
		t.Fatalf("reading from the cache must not call the API: ok=%v n=%d lists %d→%d", ok, len(got), before, lists.Load())
	}
}
