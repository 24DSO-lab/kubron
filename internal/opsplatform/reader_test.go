// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package opsplatform

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	discfake "k8s.io/client-go/discovery/fake"
	dynfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

var (
	podGVK     = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	podGVR     = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	secretGVK  = schema.GroupVersionKind{Version: "v1", Kind: "Secret"}
	secretGVR  = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	deployGVR  = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	unknownGVK = schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
)

func pod(ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"namespace": ns, "name": name, "labels": map[string]any{"app": "web"}},
	}}
}

type fixture struct {
	dyn    *dynfake.FakeDynamicClient
	reader Reader
	lim    *ClusterLimiter
	calls  atomic.Int32 // обращения к API кластера
}

func newFixture(t *testing.T, limits ReaderLimits, objs ...runtime.Object) *fixture {
	t.Helper()
	f := &fixture{lim: NewClusterLimiter(4)}
	f.dyn = dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		podGVR: "PodList", secretGVR: "SecretList", deployGVR: "DeploymentList",
	}, objs...)
	f.dyn.PrependReactor("*", "*", func(clienttesting.Action) (bool, runtime.Object, error) {
		f.calls.Add(1)
		return false, nil, nil
	})
	f.reader = NewReader("c1", Clients{
		Dynamic:   f.dyn,
		Core:      kubefake.NewSimpleClientset(),
		Discovery: &discfake.FakeDiscovery{Fake: &clienttesting.Fake{}},
	}, f.lim, limits)
	return f
}

func TestReaderGet(t *testing.T) {
	t.Parallel()
	f := newFixture(t, ReaderLimits{}, pod("ns", "a"))
	got, err := f.reader.Get(t.Context(), podGVK, "ns", "a")
	if err != nil {
		t.Fatal(err)
	}
	if got.GetName() != "a" {
		t.Fatalf("name = %q", got.GetName())
	}
	if _, err := f.reader.Get(t.Context(), podGVK, "ns", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing pod: %v, want ErrNotFound", err)
	}
}

func TestReaderGetArguments(t *testing.T) {
	t.Parallel()
	f := newFixture(t, ReaderLimits{})
	tests := []struct {
		name, ns, obj string
		gvk           schema.GroupVersionKind
	}{
		{"no name", "ns", "", podGVK},
		{"namespaced without namespace", "", "a", podGVK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := f.reader.Get(t.Context(), tc.gvk, tc.ns, tc.obj); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
	if f.calls.Load() != 0 {
		t.Fatalf("invalid requests reached the cluster: %d calls", f.calls.Load())
	}
}

// Secret не читается ни одним методом и ни при каких параметрах: запрос даже не доходит до клиента кластера.
func TestReaderNeverReadsSecrets(t *testing.T) {
	t.Parallel()
	secret := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"namespace": "ns", "name": "s"},
		"data":     map[string]any{"password": "aHVudGVyMg=="},
	}}
	f := newFixture(t, ReaderLimits{}, secret)
	groups := []schema.GroupVersionKind{secretGVK, {Group: "custom.example", Version: "v1", Kind: "Secret"}}
	for _, gvk := range groups {
		t.Run(gvk.String(), func(t *testing.T) {
			t.Parallel()
			if _, err := f.reader.Get(t.Context(), gvk, "ns", "s"); !errors.Is(err, ErrForbidden) {
				t.Errorf("Get: %v, want ErrForbidden", err)
			}
			if _, err := f.reader.List(t.Context(), gvk, ListOptions{Namespace: "ns"}); !errors.Is(err, ErrForbidden) {
				t.Errorf("List: %v, want ErrForbidden", err)
			}
		})
	}
	if f.calls.Load() != 0 {
		t.Fatalf("a Secret request reached the cluster client (%d calls)", f.calls.Load())
	}
}

func TestReaderAllowlist(t *testing.T) {
	t.Parallel()
	f := newFixture(t, ReaderLimits{})
	if _, err := f.reader.Get(t.Context(), unknownGVK, "ns", "w"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unknown kind: %v, want ErrForbidden", err)
	}
	if _, err := f.reader.List(t.Context(), schema.GroupVersionKind{}, ListOptions{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("empty kind: %v, want ErrForbidden", err)
	}
	// Версия API не ограничена: проверке совместимости нужны устаревшие версии.
	if _, _, err := resolve(schema.GroupVersionKind{Group: "apps", Version: "v1beta1", Kind: "Deployment"}); err != nil {
		t.Fatalf("an old API version of an allowed kind must resolve: %v", err)
	}
	for gk, info := range allowedKinds {
		if isSecret(gk) || info.resource == "secrets" {
			t.Errorf("Secret is in the allowlist: %v", gk)
		}
	}
	if len(AllowedKinds()) != len(allowedKinds) {
		t.Error("AllowedKinds is out of sync")
	}
}

func TestReaderListTruncatesAtLimit(t *testing.T) {
	t.Parallel()
	var objs []runtime.Object
	for i := range 10 {
		objs = append(objs, pod("ns", fmt.Sprintf("p%02d", i)))
	}
	f := newFixture(t, ReaderLimits{PageSize: 3, MaxItems: 5}, objs...)

	res, err := f.reader.List(t.Context(), podGVK, ListOptions{Namespace: "ns", Limit: 100}) // Limit сверху ограничен MaxItems
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 5 || !res.Truncated {
		t.Fatalf("items=%d truncated=%v, want 5/true", len(res.Items), res.Truncated)
	}
	if res.ObservedAt.IsZero() {
		t.Fatal("ObservedAt is not set")
	}
	all, err := f.reader.List(t.Context(), podGVK, ListOptions{Namespace: "ns", Limit: 5})
	if err != nil || len(all.Items) != 5 {
		t.Fatalf("limit 5: %v, %d items", err, len(all.Items))
	}

	small := newFixture(t, ReaderLimits{}, pod("ns", "only"))
	res, err = small.reader.List(t.Context(), podGVK, ListOptions{Namespace: "ns"})
	if err != nil || len(res.Items) != 1 || res.Truncated {
		t.Fatalf("small list: err=%v items=%d truncated=%v", err, len(res.Items), res.Truncated)
	}
}

func TestReaderListTruncatesBySize(t *testing.T) {
	t.Parallel()
	var objs []runtime.Object
	for i := range 6 {
		objs = append(objs, pod("ns", fmt.Sprintf("p%d", i)))
	}
	f := newFixture(t, ReaderLimits{MaxListBytes: 200}, objs...)
	res, err := f.reader.List(t.Context(), podGVK, ListOptions{Namespace: "ns"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || len(res.Items) == 0 || len(res.Items) >= 6 {
		t.Fatalf("items=%d truncated=%v, want a size-based truncation", len(res.Items), res.Truncated)
	}
}

func TestReaderListSingleObjectTooLarge(t *testing.T) {
	t.Parallel()
	f := newFixture(t, ReaderLimits{MaxListBytes: 10}, pod("ns", "a"))
	if _, err := f.reader.List(t.Context(), podGVK, ListOptions{Namespace: "ns"}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

func TestReaderListSelectors(t *testing.T) {
	t.Parallel()
	f := newFixture(t, ReaderLimits{}, pod("ns", "a"))
	if _, err := f.reader.List(t.Context(), podGVK, ListOptions{LabelSelector: "app in (web"}); err == nil {
		t.Error("an invalid label selector must be rejected")
	}
	if _, err := f.reader.List(t.Context(), podGVK, ListOptions{FieldSelector: "==="}); err == nil {
		t.Error("an invalid field selector must be rejected")
	}
	if f.calls.Load() != 0 {
		t.Errorf("invalid selectors reached the cluster: %d", f.calls.Load())
	}
}

func TestReaderErrorTranslation(t *testing.T) {
	t.Parallel()
	gr := schema.GroupResource{Resource: "pods"}
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"not found", apierrors.NewNotFound(gr, "x"), ErrNotFound},
		{"forbidden", apierrors.NewForbidden(gr, "x", errors.New("no")), ErrForbidden},
		{"unauthorized", apierrors.NewUnauthorized("expired"), ErrForbidden},
		{"too large", apierrors.NewRequestEntityTooLargeError("big"), ErrTooLarge},
		{"timeout", apierrors.NewTimeoutError("slow", 1), ErrUnavailable},
		{"unavailable", apierrors.NewServiceUnavailable("down"), ErrUnavailable},
		{"too many requests", apierrors.NewTooManyRequests("later", 1), ErrUnavailable},
		{"deadline", context.DeadlineExceeded, ErrUnavailable},
		{"canceled", fmt.Errorf("wrapped: %w", context.Canceled), ErrUnavailable},
		{"unknown", errors.New("connection refused"), ErrUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, ReaderLimits{})
			f.dyn.PrependReactor("*", "*", func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, tc.err })
			_, getErr := f.reader.Get(t.Context(), podGVK, "ns", "x")
			_, listErr := f.reader.List(t.Context(), podGVK, ListOptions{})
			for op, err := range map[string]error{"Get": getErr, "List": listErr} {
				if !errors.Is(err, tc.want) {
					t.Errorf("%s: %v, want %v", op, err, tc.want)
				}
				// Контракт: результат ошибки не «пустой успех» и не несколько видов сразу.
				for _, other := range []error{ErrNotFound, ErrForbidden, ErrUnavailable, ErrTooLarge} {
					if other != tc.want && errors.Is(err, other) {
						t.Errorf("%s: %v also matches %v", op, err, other)
					}
				}
			}
		})
	}
}

func TestReaderUsesLimiter(t *testing.T) {
	t.Parallel()
	f := newFixture(t, ReaderLimits{}, pod("ns", "a"))
	f.lim = NewClusterLimiter(1)
	r := NewReader("c1", Clients{Dynamic: f.dyn, Core: kubefake.NewSimpleClientset(), Discovery: &discfake.FakeDiscovery{Fake: &clienttesting.Fake{}}}, f.lim, ReaderLimits{})

	hold, err := f.lim.Acquire(t.Context(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := r.Get(ctx, podGVK, "ns", "a"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a read with all slots busy must wait and fail on cancel: %v", err)
	}
	hold()
	if _, err := r.Get(t.Context(), podGVK, "ns", "a"); err != nil {
		t.Fatalf("slot not released by the read: %v", err)
	}
	if f.lim.tracked() != 0 {
		t.Fatal("limiter leaked an entry")
	}
}

func TestReaderCallDeadline(t *testing.T) {
	t.Parallel()
	f := newFixture(t, ReaderLimits{Timeout: 30 * time.Millisecond}, pod("ns", "a"))
	f.dyn.PrependReactor("get", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		time.Sleep(200 * time.Millisecond) // реактор fake не видит ctx: моделируем медленный ответ
		return true, nil, context.DeadlineExceeded
	})
	if _, err := f.reader.Get(t.Context(), podGVK, "ns", "a"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v, want ErrUnavailable", err)
	}
}

func TestReaderPodLogs(t *testing.T) {
	t.Parallel()
	f := newFixture(t, ReaderLimits{})
	data, err := f.reader.PodLogs(t.Context(), LogRequest{Namespace: "ns", Pod: "a", Container: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fake logs" {
		t.Fatalf("logs = %q", data)
	}
	if _, err := f.reader.PodLogs(t.Context(), LogRequest{Namespace: "ns"}); err == nil {
		t.Error("a request without a pod must be rejected")
	}
	// Лог больше лимита — ErrTooLarge, а не молчаливое усечение.
	if _, err := f.reader.PodLogs(t.Context(), LogRequest{Namespace: "ns", Pod: "a", LimitBytes: 4}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversized log: %v, want ErrTooLarge", err)
	}
	// Ровно по лимиту — помещается.
	if _, err := f.reader.PodLogs(t.Context(), LogRequest{Namespace: "ns", Pod: "a", LimitBytes: int64(len("fake logs"))}); err != nil {
		t.Errorf("log of exactly the limit: %v", err)
	}
}

// slowDiscovery блокирует вызовы до закрытия unblock.
type slowDiscovery struct {
	*discfake.FakeDiscovery
	unblock chan struct{}
}

func (d *slowDiscovery) ServerVersion() (*version.Info, error) {
	<-d.unblock
	return &version.Info{GitVersion: "v1.30.0"}, nil
}

func TestReaderServerVersion(t *testing.T) {
	t.Parallel()
	fd := &discfake.FakeDiscovery{Fake: &clienttesting.Fake{}, FakedServerVersion: &version.Info{GitVersion: "v1.31.2"}}
	r := NewReader("c1", Clients{Dynamic: dynfake.NewSimpleDynamicClient(runtime.NewScheme()), Core: kubefake.NewSimpleClientset(), Discovery: fd}, NewClusterLimiter(2), ReaderLimits{})
	v, err := r.ServerVersion(t.Context())
	if err != nil || v.GitVersion != "v1.31.2" {
		t.Fatalf("got %+v, %v", v, err)
	}
}

func TestReaderServerVersionCancel(t *testing.T) {
	t.Parallel()
	d := &slowDiscovery{FakeDiscovery: &discfake.FakeDiscovery{Fake: &clienttesting.Fake{}}, unblock: make(chan struct{})}
	defer close(d.unblock)
	lim := NewClusterLimiter(1)
	r := NewReader("c1", Clients{Dynamic: dynfake.NewSimpleDynamicClient(runtime.NewScheme()), Core: kubefake.NewSimpleClientset(), Discovery: d}, lim, ReaderLimits{})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := r.ServerVersion(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v, want ErrUnavailable", err)
	}
	if lim.tracked() != 0 {
		t.Fatal("the slot was not released after cancel")
	}
}

// partialDiscovery отвечает частично: одна группа не ответила.
type partialDiscovery struct{ *discfake.FakeDiscovery }

func (d *partialDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	lists := []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "pods", Kind: "Pod"}}}}
	return nil, lists, &discovery.ErrGroupDiscoveryFailed{Groups: map[schema.GroupVersion]error{
		{Group: "metrics.k8s.io", Version: "v1beta1"}: errors.New("503"),
		{Group: "custom.example", Version: "v1"}:      errors.New("timeout"),
	}}
}

func TestReaderServerResourcesPartial(t *testing.T) {
	t.Parallel()
	d := &partialDiscovery{&discfake.FakeDiscovery{Fake: &clienttesting.Fake{}}}
	r := NewReader("c1", Clients{Dynamic: dynfake.NewSimpleDynamicClient(runtime.NewScheme()), Core: kubefake.NewSimpleClientset(), Discovery: d}, NewClusterLimiter(2), ReaderLimits{})
	lists, err := r.ServerResources(t.Context())
	if !errors.Is(err, ErrPartial) {
		t.Fatalf("err = %v, want ErrPartial", err)
	}
	if len(lists) != 1 {
		t.Fatalf("partial data must still be returned: %d lists", len(lists))
	}
	failed := PartialDetails(err)
	if len(failed) != 2 || failed[0] != "custom.example/v1" || failed[1] != "metrics.k8s.io/v1beta1" {
		t.Fatalf("failed = %v", failed)
	}
	if strings.Contains(err.Error(), "503") {
		t.Errorf("the error text must not carry upstream details: %v", err)
	}
}

func TestReaderServerResourcesOK(t *testing.T) {
	t.Parallel()
	fd := &discfake.FakeDiscovery{Fake: &clienttesting.Fake{}}
	fd.Resources = []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "pods", Kind: "Pod"}}}}
	r := NewReader("c1", Clients{Dynamic: dynfake.NewSimpleDynamicClient(runtime.NewScheme()), Core: kubefake.NewSimpleClientset(), Discovery: fd}, NewClusterLimiter(2), ReaderLimits{})
	lists, err := r.ServerResources(t.Context())
	if err != nil || len(lists) != 1 {
		t.Fatalf("got %d lists, %v", len(lists), err)
	}
}

func TestReaderLimitsDefaults(t *testing.T) {
	t.Parallel()
	l := ReaderLimits{MaxItems: 10, DefaultItems: 50}.withDefaults()
	if l.DefaultItems != 10 {
		t.Fatalf("DefaultItems must not exceed MaxItems: %d", l.DefaultItems)
	}
	if d := DefaultReaderLimits(); d.withDefaults() != d {
		t.Fatal("defaults are not a fixed point")
	}
}
