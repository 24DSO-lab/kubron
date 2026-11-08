// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	goruntime "runtime"
	"slices"
	"strings"
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

const (
	fluxcKustAPI = "kustomize.toolkit.fluxcd.io/v1"
	fluxcHelmAPI = "helm.toolkit.fluxcd.io/v2"
)

// fluxcDyn — fake dynamic client, который знает списки всех видов Flux в предпочтительной версии.
func fluxcDyn(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	lists := map[schema.GroupVersionResource]string{}
	for _, k := range fluxKinds {
		lists[k.GVR(k.Versions[0])] = k.Kind + "List"
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, objs...)
}

func fluxcKind(t testing.TB, name string) (fluxKind, schema.GroupVersionResource) {
	t.Helper()
	k, ok := fluxKindByName(name)
	if !ok {
		t.Fatalf("unknown kind %s", name)
	}
	return k, k.GVR(k.Versions[0])
}

func fluxcNsOf(i fluxListed) string { return i.ns }

// fluxcWait ждёт условия до 20 с: под -race синхронизация informer на тысячах объектов занимает заметное время.
func fluxcWait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

// fluxcKustCache достаёт у подключения кэш Kustomization с типом fluxListed (тем же, что создаёт fluxList в тестах).
func fluxcKustCache(t *testing.T, conn *ClusterConnection) *fluxCache[fluxListed] {
	t.Helper()
	kind, gvr := fluxcKind(t, FluxKindKustomization)
	return fluxCacheOf(conn, kind, gvr, parseFluxListed, nil)
}

func fluxcListKust(t *testing.T, m *Manager, namespaces ...string) []fluxListed {
	t.Helper()
	kind, _ := fluxcKind(t, FluxKindKustomization)
	items, err := fluxList(m, kind, namespaces, parseFluxListed, fluxcNsOf)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func fluxcKeys(items []fluxListed) []string {
	out := make([]string, 0, len(items))
	for _, i := range items {
		out = append(out, i.ns+"/"+i.name)
	}
	return out
}

func TestFluxListUsesCacheAfterSync(t *testing.T) {
	dyn := fluxcDyn(
		fluxObj(fluxcKustAPI, "Kustomization", "team-b", "db", map[string]any{"path": "./db"}),
		fluxObj(fluxcKustAPI, "Kustomization", "flux-system", "apps", map[string]any{"path": "./apps"}),
		fluxObj(fluxcKustAPI, "Kustomization", "team-a", "web", map[string]any{"path": "./web"}),
	)
	var lists atomic.Int32
	dyn.PrependReactor("list", "kustomizations", func(clienttesting.Action) (bool, runtime.Object, error) {
		lists.Add(1)
		return false, nil, nil
	})
	m := fluxManager(dyn)
	conn, _ := m.GetActiveConnection()
	defer conn.Close()

	// Первый вызов стартует informer; до синхронизации ответ тот же, но через API.
	first := fluxcListKust(t, m)
	want := []string{"flux-system/apps", "team-a/web", "team-b/db"}
	if !slices.Equal(fluxcKeys(first), want) {
		t.Fatalf("first read = %v, want %v", fluxcKeys(first), want)
	}

	c := fluxcKustCache(t, conn)
	if c == nil {
		t.Fatal("the informer must be started lazily by the first read")
	}
	fluxcWait(t, "informer sync", c.inf.HasSynced)

	// После синхронизации список строится из памяти: ни одного LIST к API.
	lists.Store(0)
	got := fluxcListKust(t, m)
	if !slices.Equal(fluxcKeys(got), want) || got[0].path != "./apps" {
		t.Fatalf("cached read = %+v, want %v", got, want)
	}
	if n := lists.Load(); n != 0 {
		t.Fatalf("a synced cache must not call the API, LIST called %d times", n)
	}

	// Область namespace применяется к кэшу так же, как к API; порядок «namespace, имя».
	if got := fluxcListKust(t, m, "team-a", "team-b"); !slices.Equal(fluxcKeys(got), []string{"team-a/web", "team-b/db"}) {
		t.Errorf("scoped read = %v", fluxcKeys(got))
	}
	if got := fluxcListKust(t, m, "nothing"); got == nil || len(got) != 0 {
		t.Errorf("an empty namespace must give an empty non-nil list, got %#v", got)
	}
	if _, err := fluxList(m, mustKind(t, FluxKindKustomization), []string{"a/b"}, parseFluxListed, fluxcNsOf); err == nil {
		t.Error("an invalid namespace must be rejected on the cached path too")
	}

	// Изменения в кластере доходят до списка без повторного LIST.
	if _, err := dyn.Resource(mustGVR(t, FluxKindKustomization)).Namespace("team-a").Create(context.Background(),
		fluxObj(fluxcKustAPI, "Kustomization", "team-a", "api", map[string]any{"path": "./api"}), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	fluxcWait(t, "the new object in the cache", func() bool {
		return slices.Contains(fluxcKeys(fluxcListKust(t, m)), "team-a/api")
	})
	if err := dyn.Resource(mustGVR(t, FluxKindKustomization)).Namespace("team-b").Delete(context.Background(), "db", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	fluxcWait(t, "the deletion in the cache", func() bool {
		return !slices.Contains(fluxcKeys(fluxcListKust(t, m)), "team-b/db")
	})
}

func mustKind(t testing.TB, name string) fluxKind {
	k, _ := fluxcKind(t, name)
	return k
}

func mustGVR(t testing.TB, name string) schema.GroupVersionResource {
	_, gvr := fluxcKind(t, name)
	return gvr
}

func TestFluxListFallsBackToAPIUntilSynced(t *testing.T) {
	dyn := fluxcDyn(
		fluxObj(fluxcKustAPI, "Kustomization", "team-a", "web", map[string]any{"path": "./web"}),
		fluxObj(fluxcKustAPI, "Kustomization", "team-b", "db", nil),
	)
	// Проба права (первый LIST по всем namespace) проходит, а LIST самого informer (он тоже по всем namespace)
	// всегда падает: кэш создан, но никогда не синхронизируется. Чтение одного namespace идёт через API.
	var allNS atomic.Int32
	dyn.PrependReactor("list", "kustomizations", func(a clienttesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() == metav1.NamespaceAll && allNS.Add(1) > 1 {
			return true, nil, errors.New("the server is currently unable to handle the request")
		}
		return false, nil, nil
	})
	m := fluxManager(dyn)
	conn, _ := m.GetActiveConnection()
	defer conn.Close()

	got := fluxcListKust(t, m, "team-a")
	if !slices.Equal(fluxcKeys(got), []string{"team-a/web"}) {
		t.Fatalf("fallback read = %v", fluxcKeys(got))
	}
	c := fluxcKustCache(t, conn)
	if c == nil {
		t.Fatal("the cache must exist: only its sync fails")
	}
	if items, ok := c.list(nsScope{}); ok || items != nil {
		t.Fatalf("a not synced cache must answer (nil, false), got ok=%v %v", ok, items)
	}
	// И после повторного чтения результат прежний (через API).
	if got := fluxcListKust(t, m, "team-a"); !slices.Equal(fluxcKeys(got), []string{"team-a/web"}) {
		t.Fatalf("second fallback read = %v", fluxcKeys(got))
	}
}

func TestFluxListProbeDeniedReadsThroughAPI(t *testing.T) {
	dyn := fluxcDyn(
		fluxObj(fluxcKustAPI, "Kustomization", "team-a", "web", map[string]any{"path": "./web"}),
		fluxObj(fluxcKustAPI, "Kustomization", "team-b", "db", nil),
	)
	// Нет права list по всем namespace (так бывает у арендатора): проба отказывает, чтение по namespace работает.
	var probes atomic.Int32
	dyn.PrependReactor("list", "kustomizations", func(a clienttesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() == metav1.NamespaceAll {
			probes.Add(1)
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: fluxGroupKustomize, Resource: "kustomizations"}, "", errors.New("denied"))
		}
		return false, nil, nil
	})
	m := fluxManager(dyn)
	conn, _ := m.GetActiveConnection()
	defer conn.Close()
	_, gvr := fluxcKind(t, FluxKindKustomization)

	for i := range 3 {
		got := fluxcListKust(t, m, "team-a")
		if !slices.Equal(fluxcKeys(got), []string{"team-a/web"}) {
			t.Fatalf("read %d = %v", i, fluxcKeys(got))
		}
	}
	if conn.crdCaches.has(gvr) {
		t.Fatal("there must be no cache without the list permission")
	}
	// Пауза: повторная проба раньше crdProbeRetry не делается.
	if n := probes.Load(); n != 1 {
		t.Fatalf("the permission probe was repeated %d times within the pause, want 1", n)
	}

	// Без права на все namespace чтение «всех» — ошибка API, как и до кэша (кэш её не маскирует).
	kind := mustKind(t, FluxKindKustomization)
	if _, err := fluxList(m, kind, nil, parseFluxListed, fluxcNsOf); !apierrors.IsForbidden(err) {
		t.Fatalf("err = %v, want Forbidden", err)
	}
}

func TestFluxProbeDeniedOnOneKindDoesNotBlockOthers(t *testing.T) {
	dyn := fluxcDyn(
		fluxObj(fluxcKustAPI, "Kustomization", "ns", "k", nil),
		fluxObj(fluxcHelmAPI, "HelmRelease", "ns", "h", nil),
	)
	dyn.PrependReactor("list", "kustomizations", func(a clienttesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() == metav1.NamespaceAll {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: fluxGroupKustomize, Resource: "kustomizations"}, "", errors.New("denied"))
		}
		return false, nil, nil
	})
	m := fluxManager(dyn)
	conn, _ := m.GetActiveConnection()
	defer conn.Close()

	_ = fluxcListKust(t, m, "ns")
	helm := mustKind(t, FluxKindHelmRelease)
	if _, err := fluxList(m, helm, nil, parseFluxListed, fluxcNsOf); err != nil {
		t.Fatal(err)
	}
	if !conn.crdCaches.has(mustGVR(t, FluxKindHelmRelease)) {
		t.Fatal("a refusal on Kustomization must not stop the HelmRelease cache")
	}
	if conn.crdCaches.has(mustGVR(t, FluxKindKustomization)) {
		t.Fatal("no cache for the refused kind")
	}
}

func TestFluxCacheStartsLazilyPerKind(t *testing.T) {
	dyn := fluxcDyn(
		fluxObj(fluxcKustAPI, "Kustomization", "ns", "k", nil),
		fluxObj("source.toolkit.fluxcd.io/v1", "GitRepository", "ns", "g", nil),
	)
	m := fluxManager(dyn)
	conn, _ := m.GetActiveConnection()
	defer conn.Close()

	for _, name := range []string{FluxKindKustomization, FluxKindHelmRelease, FluxKindGitRepository, FluxKindProvider, FluxKindImagePolicy} {
		if conn.crdCaches.has(mustGVR(t, name)) {
			t.Fatalf("%s: informer started before the first read", name)
		}
	}
	_ = fluxcListKust(t, m)
	if !conn.crdCaches.has(mustGVR(t, FluxKindKustomization)) {
		t.Fatal("reading Kustomization must start its informer")
	}
	for _, name := range []string{FluxKindHelmRelease, FluxKindGitRepository, FluxKindProvider, FluxKindImagePolicy} {
		if conn.crdCaches.has(mustGVR(t, name)) {
			t.Fatalf("%s: informer started without a read of that kind", name)
		}
	}
	if _, err := fluxList(m, mustKind(t, FluxKindGitRepository), nil, parseFluxListed, fluxcNsOf); err != nil {
		t.Fatal(err)
	}
	if !conn.crdCaches.has(mustGVR(t, FluxKindGitRepository)) {
		t.Fatal("the first access to a source kind must start its informer")
	}
}

func TestFluxCacheScrubsSecrets(t *testing.T) {
	managed := []metav1.ManagedFieldsEntry{{Manager: "kubectl", Operation: metav1.ManagedFieldsOperationApply}}
	helm := fluxObj(fluxcHelmAPI, "HelmRelease", "apps", "web", map[string]any{
		"interval":   "5m",
		"values":     map[string]any{"adminPassword": "S3CRET-VALUES", "nested": map[string]any{"token": "S3CRET-VALUES-TOKEN"}},
		"valuesFrom": []any{map[string]any{"kind": "Secret", "name": "web-values"}},
		"chart":      map[string]any{"spec": map[string]any{"chart": "web"}},
	})
	helm.SetManagedFields(managed)
	// kubectl apply кладёт копию манифеста, в том числе spec.values, в аннотацию: она тоже не должна лежать в памяти.
	helm.SetAnnotations(map[string]string{
		fluxLastAppliedAnnotation: `{"spec":{"values":{"adminPassword":"S3CRET-LASTAPPLIED"}}}`,
		"team":                    "platform",
	})
	kust := fluxObj(fluxcKustAPI, "Kustomization", "apps", "k", map[string]any{
		"path": "./k", "postBuild": map[string]any{"substitute": map[string]any{"A": "visible-to-parser"}},
	})
	kust.SetManagedFields(managed)
	kust.SetAnnotations(map[string]string{fluxLastAppliedAnnotation: `{"x":"S3CRET-LASTAPPLIED-K"}`})

	m := fluxManager(fluxcDyn(helm, kust))
	conn, _ := m.GetActiveConnection()
	defer conn.Close()

	helmKind, helmGVR := fluxcKind(t, FluxKindHelmRelease)
	if _, err := fluxList(m, helmKind, nil, parseFluxListed, fluxcNsOf); err != nil {
		t.Fatal(err)
	}
	_ = fluxcListKust(t, m)

	stored := func(kind fluxKind, gvr schema.GroupVersionResource) *unstructured.Unstructured {
		t.Helper()
		c := fluxCacheOf(conn, kind, gvr, parseFluxListed, nil)
		if c == nil {
			t.Fatalf("%s: no cache", kind.Kind)
		}
		fluxcWait(t, kind.Kind+" informer sync", c.inf.HasSynced)
		objs := c.inf.GetStore().List()
		if len(objs) != 1 {
			t.Fatalf("%s: %d objects in the store, want 1", kind.Kind, len(objs))
		}
		return objs[0].(*unstructured.Unstructured)
	}

	h := stored(helmKind, helmGVR)
	raw, _ := json.Marshal(h.Object)
	if strings.Contains(string(raw), "S3CRET") {
		t.Errorf("a HelmRelease in the cache still holds secret values: %s", raw)
	}
	if _, found, _ := unstructured.NestedMap(h.Object, "spec", "values"); found {
		t.Error("spec.values must be removed from the cached HelmRelease")
	}
	if len(h.GetManagedFields()) != 0 {
		t.Error("managedFields must be removed")
	}
	if _, ok := h.GetAnnotations()[fluxLastAppliedAnnotation]; ok || h.GetAnnotations()["team"] != "platform" {
		t.Errorf("annotations = %v: only last-applied-configuration goes", h.GetAnnotations())
	}
	// Остальное осталось: valuesFrom (только ссылки), chart, interval — парсеры их читают.
	if strAt(h.Object, "spec", "interval") != "5m" || strAt(h.Object, "spec", "chart", "spec", "chart") != "web" {
		t.Errorf("the rest of the spec was lost: %v", h.Object["spec"])
	}
	if _, ok, _ := unstructured.NestedSlice(h.Object, "spec", "valuesFrom"); !ok {
		t.Error("valuesFrom (references only) must stay for the parsers")
	}

	k := stored(mustKind(t, FluxKindKustomization), mustGVR(t, FluxKindKustomization))
	if _, ok := k.GetAnnotations()[fluxLastAppliedAnnotation]; ok {
		t.Error("last-applied-configuration must be removed from every Flux kind")
	}
	if len(k.GetManagedFields()) != 0 {
		t.Error("managedFields must be removed")
	}
	// Для Kustomization spec не режется: values есть только у HelmRelease.
	if strAt(k.Object, "spec", "path") != "./k" {
		t.Errorf("the Kustomization spec must stay intact: %v", k.Object["spec"])
	}
	if _, found, _ := unstructured.NestedMap(k.Object, "spec", "postBuild"); !found {
		t.Error("only HelmRelease spec.values is removed from the spec")
	}

	// Список из кэша не отдаёт значений и подавно.
	items, err := fluxList(m, helmKind, nil, parseFluxListed, fluxcNsOf)
	if err != nil || len(items) != 1 {
		t.Fatalf("err=%v items=%v", err, items)
	}
	if b, _ := json.Marshal(items); strings.Contains(string(b), "S3CRET") {
		t.Errorf("secret in the listed items: %s", b)
	}
}

func TestFluxScrubIsSafeOnOddObjects(t *testing.T) {
	for name, u := range map[string]*unstructured.Unstructured{
		"empty":               {Object: map[string]any{}},
		"spec is a string":    {Object: map[string]any{"spec": "x"}},
		"values is a string":  {Object: map[string]any{"spec": map[string]any{"values": "x"}}},
		"no annotations":      {Object: map[string]any{"metadata": map[string]any{"name": "a"}}},
		"annotations garbage": {Object: map[string]any{"metadata": map[string]any{"annotations": "x"}}},
	} {
		for _, kind := range []string{FluxKindHelmRelease, FluxKindKustomization} {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s/%s: panic: %v", kind, name, r)
					}
				}()
				fluxScrub(kind)(u)
			}()
		}
	}
}

// fluxcEvents записывает события, которые получили виды.
type fluxcEvents struct {
	mu  sync.Mutex
	got map[string][]string
}

func (e *fluxcEvents) notifier(name string) func(string, ...any) {
	return func(ev string, data ...any) {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.got == nil {
			e.got = map[string][]string{}
		}
		arg := ""
		if len(data) > 0 {
			arg = fmt.Sprint(data[0])
		}
		e.got[name] = append(e.got[name], ev+":"+arg)
	}
}

func (e *fluxcEvents) of(name string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.got[name])
}

// fluxcPool — менеджер с пулом из кластеров a (dynA) и b (dynB); закрытие подключения останавливает его informers.
func fluxcPool(dynA, dynB *dynamicfake.FakeDynamicClient) *Manager {
	root := NewManager()
	root.pool.closeFn = func(c *ClusterConnection) { c.Close() }
	root.pool.put("a", &ClusterConnection{Name: "a", DynamicClient: dynA})
	root.pool.put("b", &ClusterConnection{Name: "b", DynamicClient: dynB})
	return root
}

func fluxcCreateKust(t *testing.T, dyn *dynamicfake.FakeDynamicClient, ns, name string) {
	t.Helper()
	if _, err := dyn.Resource(mustGVR(t, FluxKindKustomization)).Namespace(ns).Create(context.Background(),
		fluxObj(fluxcKustAPI, "Kustomization", ns, name, nil), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestFluxChangedEventIsAddressedAndDebounced(t *testing.T) {
	dynA, dynB := fluxcDyn(), fluxcDyn()
	root := fluxcPool(dynA, dynB)
	var ev fluxcEvents
	v1, v2, v3 := root.ForSession(), root.ForSession(), root.ForSession()
	v1.SetNotifier(ev.notifier("v1"))
	v2.SetNotifier(ev.notifier("v2"))
	v3.SetNotifier(ev.notifier("v3"))
	mustActivate(t, v1, "a")
	mustActivate(t, v2, "a")
	mustActivate(t, v3, "b")

	synctest.Test(t, func(t *testing.T) {
		connA, _ := v1.GetActiveConnection()
		defer connA.Close()
		connB, _ := v3.GetActiveConnection()
		defer connB.Close()

		// Оба кластера читаются (informer стартует у каждого), но менять будем только «a».
		_ = fluxcListKust(t, v1)
		_ = fluxcListKust(t, v3)
		synctest.Wait()

		// Серия из трёх изменений за окно debounce даёт одно событие.
		for _, n := range []string{"x", "y", "z"} {
			fluxcCreateKust(t, dynA, "ns", n)
		}
		synctest.Wait()
		time.Sleep(crdDefaultDebounce - time.Millisecond)
		synctest.Wait()
		if got := ev.of("v1"); len(got) != 0 {
			t.Fatalf("the event must wait for the debounce window, got %v", got)
		}
		time.Sleep(2 * time.Millisecond)
		synctest.Wait()
		want := []string{EventFluxChanged + ":a"}
		if got := ev.of("v1"); !slices.Equal(got, want) {
			t.Fatalf("v1 events = %v, want %v (one event per burst, addressed to the connection)", got, want)
		}
		// Событие получают все виды, у которых кластер «a» активен, и только они; свой тип события — не cluster:changed.
		if got := ev.of("v2"); !slices.Equal(got, want) {
			t.Fatalf("v2 events = %v, want %v", got, want)
		}
		if got := ev.of("v3"); len(got) != 0 {
			t.Fatalf("a view of another cluster must not receive the event, got %v", got)
		}

		// Тишина без изменений.
		time.Sleep(5 * crdDefaultDebounce)
		synctest.Wait()
		if got := ev.of("v1"); len(got) != 1 {
			t.Fatalf("no events without changes, got %v", got)
		}

		// Следующее изменение даёт следующее событие; удаление тоже.
		fluxcCreateKust(t, dynA, "ns", "w")
		synctest.Wait()
		time.Sleep(crdDefaultDebounce)
		synctest.Wait()
		if err := dynA.Resource(mustGVR(t, FluxKindKustomization)).Namespace("ns").Delete(context.Background(), "x", metav1.DeleteOptions{}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		time.Sleep(crdDefaultDebounce)
		synctest.Wait()
		if got := ev.of("v1"); len(got) != 3 {
			t.Fatalf("create and delete must each notify, got %v", got)
		}

		// Изменение в чужом кластере доходит только до его видов.
		fluxcCreateKust(t, dynB, "ns", "other")
		synctest.Wait()
		time.Sleep(crdDefaultDebounce)
		synctest.Wait()
		if got := ev.of("v3"); !slices.Equal(got, []string{EventFluxChanged + ":b"}) {
			t.Fatalf("v3 events = %v", got)
		}
		if got := ev.of("v1"); len(got) != 3 {
			t.Fatalf("cluster a views must not see changes of b, got %v", got)
		}

		// Вид, покинувший кластер, событий больше не получает.
		beforeDetach := len(ev.of("v2"))
		v2.Detach()
		fluxcCreateKust(t, dynA, "ns", "after-detach")
		synctest.Wait()
		time.Sleep(crdDefaultDebounce)
		synctest.Wait()
		if got := ev.of("v2"); len(got) != beforeDetach {
			t.Fatalf("a detached view must not receive events, got %v", got)
		}
		if got := ev.of("v1"); len(got) != 4 {
			t.Fatalf("v1 events = %v", got)
		}
	})
}

func TestFluxCacheStopsOnConnectionClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dyn := fluxcDyn()
		var ev fluxcEvents
		m := fluxManager(dyn)
		m.SetNotifier(ev.notifier("m"))
		conn, _ := m.GetActiveConnection()
		_, gvr := fluxcKind(t, FluxKindKustomization)

		_ = fluxcListKust(t, m)
		synctest.Wait()
		c := fluxcKustCache(t, conn)
		if c == nil || !conn.crdCaches.has(gvr) {
			t.Fatal("the cache must be running")
		}

		// Изменение взводит таймер debounce; остановка гасит ожидающее событие.
		fluxcCreateKust(t, dyn, "ns", "a")
		synctest.Wait()
		c.mu.Lock()
		armed := c.timer != nil
		c.mu.Unlock()
		if !armed {
			t.Fatal("the debounce timer must be armed after a change")
		}
		conn.Close()
		if conn.crdCaches.has(gvr) || !c.stopped {
			t.Fatal("Close must stop the informer and reset the registry")
		}
		time.Sleep(5 * crdDefaultDebounce)
		synctest.Wait()
		if got := ev.of("m"); len(got) != 0 {
			t.Fatalf("no events are expected after Close, got %v", got)
		}
		fluxcCreateKust(t, dyn, "ns", "b")
		synctest.Wait()
		time.Sleep(5 * crdDefaultDebounce)
		synctest.Wait()
		if got := ev.of("m"); len(got) != 0 {
			t.Fatalf("a stopped informer must stay silent, got %v", got)
		}
	})
}

func TestFluxListConcurrentReadsAndChanges(t *testing.T) {
	dyn := fluxcDyn(fluxObj(fluxcKustAPI, "Kustomization", "ns", "seed", nil))
	m := fluxManager(dyn)
	conn, _ := m.GetActiveConnection()
	defer conn.Close()
	kind := mustKind(t, FluxKindKustomization)

	var wg sync.WaitGroup
	var created atomic.Int32
	for i := range 4 {
		wg.Go(func() {
			for j := range 15 {
				fluxcCreateKust(t, dyn, "ns", fmt.Sprintf("o-%d-%d", i, j))
				created.Add(1)
			}
		})
	}
	for range 12 {
		wg.Go(func() {
			for range 40 {
				items, err := fluxList(m, kind, nil, parseFluxListed, fluxcNsOf)
				if err != nil {
					t.Errorf("fluxList: %v", err)
					return
				}
				if !slices.IsSortedFunc(items, func(a, b fluxListed) int { return strings.Compare(a.ns+"/"+a.name, b.ns+"/"+b.name) }) {
					t.Errorf("a list is not sorted by namespace and name")
					return
				}
			}
		})
	}
	wg.Wait()

	// Одно подключение — один кэш на вид, сколько бы вызовов ни было параллельно.
	c := fluxcKustCache(t, conn)
	fluxcWait(t, "the cache catches up with all changes", func() bool {
		items, ok := c.list(nsScope{})
		return ok && len(items) == 1+int(created.Load())
	})
}

// fluxcHeap возвращает занятую кучу после полной сборки мусора.
func fluxcHeap() uint64 {
	goruntime.GC()
	goruntime.GC()
	var ms goruntime.MemStats
	goruntime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// fluxcHeavyHelmRelease — реалистично тяжёлый HelmRelease: значения чарта, managedFields, last-applied-configuration
// с копией значений, история релизов.
func fluxcHeavyHelmRelease(i int) *unstructured.Unstructured {
	values := map[string]any{}
	for k := range 40 {
		values[fmt.Sprintf("key%02d", k)] = strings.Repeat("v", 60)
	}
	hist := make([]any, 0, 5)
	for v := 5; v >= 1; v-- {
		hist = append(hist, map[string]any{
			"version": int64(v), "status": "superseded", "chartName": "web", "chartVersion": "1.2.3", "appVersion": "1.2.3",
			"firstDeployed": "2026-02-10T08:20:00Z", "lastDeployed": "2026-11-05T09:00:03Z", "name": "web", "namespace": "apps",
		})
	}
	u := fluxObj(fluxcHelmAPI, "HelmRelease", fmt.Sprintf("ns-%02d", i%20), fmt.Sprintf("hr-%04d", i), map[string]any{
		"interval": "10m", "values": values,
		"chart": map[string]any{"spec": map[string]any{"chart": "web", "version": "1.x", "sourceRef": map[string]any{"kind": "HelmRepository", "name": "repo"}}},
	})
	u.Object["status"] = map[string]any{"history": hist, "lastAppliedRevision": "1.2.3"}
	u.SetManagedFields([]metav1.ManagedFieldsEntry{
		{Manager: "helm-controller", Operation: metav1.ManagedFieldsOperationUpdate, APIVersion: fluxcHelmAPI, FieldsType: "FieldsV1",
			FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:spec":{"f:values":{"f:key00":{}}},"f:status":{"f:history":{}}}`)}},
	})
	lastApplied, _ := json.Marshal(map[string]any{"spec": map[string]any{"values": values}})
	u.SetAnnotations(map[string]string{fluxLastAppliedAnnotation: string(lastApplied)})
	return u
}

// TestFluxCacheMemory2000 замеряет память informer-кэша на 2000 тяжёлых HelmRelease: с очисткой (как в продакшене) и без
// неё. Результат записывается в лог теста (go test -v -run TestFluxCacheMemory2000 ./internal/k8s/) и в описание PR.
func TestFluxCacheMemory2000(t *testing.T) {
	if testing.Short() {
		t.Skip("memory measurement is skipped in -short mode")
	}
	const n = 2000
	kind, gvr := fluxcKind(t, FluxKindHelmRelease)

	measure := func(scrubbed bool) (bytes uint64, objects int) {
		objs := make([]runtime.Object, 0, n)
		for i := range n {
			objs = append(objs, fluxcHeavyHelmRelease(i))
		}
		dyn := fluxcDyn(objs...)
		objs = nil // оригиналы остаются только у fake-клиента, а не у теста
		before := fluxcHeap()

		var inf interface {
			HasSynced() bool
			count() int
			stop()
		}
		if scrubbed {
			c, err := newFluxCache(dyn, kind, gvr, parseFluxListed, nil)
			if err != nil {
				t.Fatal(err)
			}
			inf = fluxcProbe{c.crdCache, func() int { return len(c.inf.GetStore().List()) }}
		} else {
			c, err := newCRDCache(dyn, crdCacheConfig[fluxListed]{GVR: gvr, Parse: parseFluxListed}) // только managedFields
			if err != nil {
				t.Fatal(err)
			}
			inf = fluxcProbe{c, func() int { return len(c.inf.GetStore().List()) }}
		}
		defer inf.stop()
		fluxcWait(t, "informer sync", inf.HasSynced)
		got := inf.count()
		after := fluxcHeap()
		goruntime.KeepAlive(inf)
		if after < before {
			return 0, got
		}
		return after - before, got
	}

	plainBytes, plainN := measure(false)
	scrubBytes, scrubN := measure(true)
	if plainN != n || scrubN != n {
		t.Fatalf("objects in the cache: unscrubbed=%d scrubbed=%d, want %d", plainN, scrubN, n)
	}
	t.Logf("2000 HelmRelease in the informer cache: with scrub (production) %.1f MiB (%.1f KiB/object); "+
		"without spec.values and last-applied scrub %.1f MiB (%.1f KiB/object); saved %.0f%%",
		float64(scrubBytes)/(1<<20), float64(scrubBytes)/n/1024,
		float64(plainBytes)/(1<<20), float64(plainBytes)/n/1024,
		100*(1-float64(scrubBytes)/float64(max(plainBytes, 1))))
	if scrubBytes == 0 || plainBytes == 0 {
		t.Skip("the heap measurement is not reliable on this run")
	}
	if float64(scrubBytes) > 0.7*float64(plainBytes) {
		t.Errorf("the scrubbed cache (%d B) must be clearly smaller than the unscrubbed one (%d B)", scrubBytes, plainBytes)
	}
}

// fluxcProbe даёт тесту памяти единый интерфейс над crdCache.
type fluxcProbe struct {
	*crdCache[fluxListed]
	counter func() int
}

func (p fluxcProbe) count() int { return p.counter() }
func (p fluxcProbe) HasSynced() bool {
	return p.inf.HasSynced()
}
