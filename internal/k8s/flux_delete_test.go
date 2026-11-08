// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
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
	fluxdKustAPI = "kustomize.toolkit.fluxcd.io/v1"
	fluxdHelmAPI = "helm.toolkit.fluxcd.io/v2"
)

type fluxdDeleted struct {
	gvr      schema.GroupVersionResource
	ns, name string
	opts     metav1.DeleteOptions
}

type fluxdRecorder struct {
	mu   sync.Mutex
	dels []fluxdDeleted
}

func (r *fluxdRecorder) all() []fluxdDeleted {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]fluxdDeleted(nil), r.dels...)
}

func fluxdSetup(objs ...runtime.Object) (*Manager, *dynamicfake.FakeDynamicClient, *fluxdRecorder) {
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objs...)
	rec := &fluxdRecorder{}
	dyn.PrependReactor("delete", "*", func(a clienttesting.Action) (bool, runtime.Object, error) {
		da := a.(clienttesting.DeleteActionImpl)
		rec.mu.Lock()
		rec.dels = append(rec.dels, fluxdDeleted{gvr: a.GetResource(), ns: a.GetNamespace(), name: da.Name, opts: da.DeleteOptions})
		rec.mu.Unlock()
		return false, nil, nil
	})
	return fluxManager(dyn), dyn, rec
}

// fluxdLogs перехватывает slog.Default на время теста (тесты, которые его меняют, не запускаются параллельно).
func fluxdLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func fluxdExists(dyn *dynamicfake.FakeDynamicClient, kind, ns, name string) bool {
	k, _ := fluxKindByName(kind)
	_, err := dyn.Resource(k.GVR(k.Versions[0])).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
	return err == nil
}

func TestFluxDeleteKustomization(t *testing.T) {
	logs := fluxdLogs(t)
	m, dyn, rec := fluxdSetup(
		fluxObj(fluxdKustAPI, "Kustomization", "flux-system", "apps", map[string]any{"prune": true}),
		fluxObj(fluxdKustAPI, "Kustomization", "flux-system", "keep", nil),
	)
	if err := m.DeleteFlux(FluxRef{FluxKindKustomization, "flux-system", "apps"}); err != nil {
		t.Fatal(err)
	}
	if fluxdExists(dyn, FluxKindKustomization, "flux-system", "apps") || !fluxdExists(dyn, FluxKindKustomization, "flux-system", "keep") {
		t.Fatal("only the requested object must be deleted")
	}
	dels := rec.all()
	if len(dels) != 1 || dels[0].gvr.Resource != "kustomizations" || dels[0].name != "apps" || dels[0].ns != "flux-system" {
		t.Fatalf("deletes: %+v", dels)
	}
	// Простой DELETE: без propagationPolicy, preconditions, dryRun и grace period — каскад решает finalizer контроллера.
	if o := dels[0].opts; o.PropagationPolicy != nil || o.GracePeriodSeconds != nil || o.Preconditions != nil || len(o.DryRun) != 0 {
		t.Fatalf("delete must carry no options, got %+v", o)
	}
	line := logs.String()
	for _, want := range []string{"level=INFO", "kind=Kustomization", "namespace=flux-system", "name=apps", "prune=true"} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line lacks %q: %s", want, line)
		}
	}
}

func TestFluxDeleteHelmRelease(t *testing.T) {
	logs := fluxdLogs(t)
	m, dyn, rec := fluxdSetup(fluxObj(fluxdHelmAPI, "HelmRelease", "apps", "web", map[string]any{"values": map[string]any{"password": "S3CRET-del"}}))
	if err := m.DeleteFlux(FluxRef{FluxKindHelmRelease, "apps", "web"}); err != nil {
		t.Fatal(err)
	}
	if fluxdExists(dyn, FluxKindHelmRelease, "apps", "web") || len(rec.all()) != 1 {
		t.Fatalf("not deleted: %+v", rec.all())
	}
	// У HelmRelease нет spec.prune: в логе prune=false, значения чарта в лог не попадают.
	if s := logs.String(); !strings.Contains(s, "kind=HelmRelease") || !strings.Contains(s, "prune=false") || strings.Contains(s, "S3CRET-del") {
		t.Fatalf("log: %s", s)
	}
}

func TestFluxDeleteLogsBeforeDelete(t *testing.T) {
	logs := fluxdLogs(t)
	m, dyn, _ := fluxdSetup(fluxObj(fluxdKustAPI, "Kustomization", "ns", "a", map[string]any{"prune": false}))
	var logged bool
	dyn.PrependReactor("delete", "kustomizations", func(clienttesting.Action) (bool, runtime.Object, error) {
		logged = strings.Contains(logs.String(), "name=a")
		return false, nil, nil
	})
	if err := m.DeleteFlux(FluxRef{FluxKindKustomization, "ns", "a"}); err != nil {
		t.Fatal(err)
	}
	if !logged {
		t.Fatal("the audit line must be written before the DELETE is sent")
	}
	if !strings.Contains(logs.String(), "prune=false") {
		t.Fatalf("log: %s", logs.String())
	}
}

func TestFluxDeleteOnlyKustomizationAndHelmRelease(t *testing.T) {
	for _, k := range fluxKinds {
		wantOK := k.Kind == FluxKindKustomization || k.Kind == FluxKindHelmRelease
		obj := fluxObj(k.Group+"/"+k.Versions[0], k.Kind, "ns", "o", nil)
		m, dyn, rec := fluxdSetup(obj)
		err := m.DeleteFlux(FluxRef{k.Kind, "ns", "o"})
		if wantOK {
			if err != nil || fluxdExists(dyn, k.Kind, "ns", "o") {
				t.Fatalf("%s: err=%v", k.Kind, err)
			}
			continue
		}
		if !errors.Is(err, errFluxKindUnsupported) || len(rec.all()) != 0 || !fluxdExists(dyn, k.Kind, "ns", "o") {
			t.Fatalf("%s must not be deletable: err=%v", k.Kind, err)
		}
	}
	m, _, rec := fluxdSetup()
	for _, ref := range []FluxRef{{"Deployment", "ns", "a"}, {"kustomization", "ns", "a"}, {"Namespace", "", "x"}} {
		if err := m.DeleteFlux(ref); err == nil || len(rec.all()) != 0 {
			t.Fatalf("%+v: err=%v", ref, err)
		}
	}
}

func TestFluxDeleteInvalidRef(t *testing.T) {
	m, _, rec := fluxdSetup(fluxObj(fluxdKustAPI, "Kustomization", "ns", "a", nil))
	for _, ref := range []FluxRef{
		{"", "ns", "a"}, {FluxKindKustomization, "", "a"}, {FluxKindKustomization, "ns", ""},
		{FluxKindKustomization, " ", "a"}, {FluxKindKustomization, "ns", "\t"},
		{FluxKindKustomization, strings.Repeat("n", 201), "a"}, {FluxKindKustomization, "ns", strings.Repeat("a", 201)},
	} {
		if err := m.DeleteFlux(ref); !errors.Is(err, errFluxInvalidRef) {
			t.Fatalf("%+v: err = %v", ref, err)
		}
	}
	if len(rec.all()) != 0 {
		t.Fatal("nothing may be sent for invalid refs")
	}
	// Пробелы по краям срезаются, как у действий reconcile/suspend/resume.
	if err := m.DeleteFlux(FluxRef{FluxKindKustomization, " ns ", " a\n"}); err != nil {
		t.Fatalf("trimmed ref: %v", err)
	}
}

func TestFluxDeleteNotFound(t *testing.T) {
	m, _, rec := fluxdSetup()
	err := m.DeleteFlux(FluxRef{FluxKindKustomization, "ns", "gone"})
	if err == nil || !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want the API NotFound error", err)
	}
	if len(rec.all()) != 0 {
		t.Fatal("DELETE must not be sent for a missing object")
	}
	// Объект исчез между чтением и DELETE: та же NotFound, без паники.
	m2, dyn, _ := fluxdSetup(fluxObj(fluxdKustAPI, "Kustomization", "ns", "a", nil))
	dyn.PrependReactor("delete", "kustomizations", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: "kustomizations"}, "a")
	})
	if err := m2.DeleteFlux(FluxRef{FluxKindKustomization, "ns", "a"}); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestFluxDeleteAlreadyDeleting(t *testing.T) {
	obj := fluxObj(fluxdKustAPI, "Kustomization", "ns", "a", nil)
	obj.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})
	m, _, rec := fluxdSetup(obj)
	if err := m.DeleteFlux(FluxRef{FluxKindKustomization, "ns", "a"}); !errors.Is(err, errFluxDeleting) {
		t.Fatalf("err = %v", err)
	}
	if len(rec.all()) != 0 {
		t.Fatal("DELETE must not be repeated for an object that is being deleted")
	}
}

func TestFluxDeleteVersionAndInstall(t *testing.T) {
	orig := fluxDeleteVersions
	t.Cleanup(func() { fluxDeleteVersions = orig })
	m, _, rec := fluxdSetup(fluxObj(fluxdKustAPI, "Kustomization", "ns", "a", nil))

	fluxDeleteVersions = func(*ClusterConnection) (map[string]string, error) { return map[string]string{}, nil }
	if err := m.DeleteFlux(FluxRef{FluxKindKustomization, "ns", "a"}); !errors.Is(err, errFluxNotInstalled) {
		t.Fatalf("no kind: %v", err)
	}
	fluxDeleteVersions = func(*ClusterConnection) (map[string]string, error) {
		return map[string]string{FluxKindKustomization: "v1beta2"}, nil
	}
	if err := m.DeleteFlux(FluxRef{FluxKindKustomization, "ns", "a"}); !errors.Is(err, errFluxVersionUnsupported) {
		t.Fatalf("old version: %v", err)
	}
	fluxDeleteVersions = func(*ClusterConnection) (map[string]string, error) { return nil, errFluxNotInstalled }
	if err := m.DeleteFlux(FluxRef{FluxKindKustomization, "ns", "a"}); !errors.Is(err, errFluxNotInstalled) {
		t.Fatalf("discovery error: %v", err)
	}
	if len(rec.all()) != 0 {
		t.Fatal("nothing may be sent")
	}
}

func TestFluxDeleteNoActiveConnection(t *testing.T) {
	if err := NewManager().DeleteFlux(FluxRef{FluxKindKustomization, "ns", "a"}); err == nil {
		t.Fatal("expected an error without an active connection")
	}
}

func TestFluxDeleteOddObjects(t *testing.T) {
	// prune неожиданного типа не ломает удаление.
	for _, prune := range []any{"yes", int64(1), map[string]any{}, nil} {
		m, dyn, _ := fluxdSetup(fluxObj(fluxdKustAPI, "Kustomization", "ns", "a", map[string]any{"prune": prune}))
		if err := m.DeleteFlux(FluxRef{FluxKindKustomization, "ns", "a"}); err != nil || fluxdExists(dyn, FluxKindKustomization, "ns", "a") {
			t.Fatalf("prune=%v: err=%v", prune, err)
		}
	}
}

func TestFluxDeleteErrorsDoNotLeakObject(t *testing.T) {
	const marker = "S3CRET-delete"
	m, dyn, _ := fluxdSetup(&unstructured.Unstructured{Object: map[string]any{
		"apiVersion": fluxdKustAPI, "kind": "Kustomization",
		"metadata": map[string]any{"namespace": "ns", "name": "a"},
		"spec":     map[string]any{"postBuild": map[string]any{"substitute": map[string]any{"P": marker}}},
	}})
	dyn.PrependReactor("delete", "kustomizations", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "kustomizations"}, "a", errors.New("denied"))
	})
	logs := fluxdLogs(t)
	err := m.DeleteFlux(FluxRef{FluxKindKustomization, "ns", "a"})
	if err == nil || strings.Contains(err.Error(), marker) || strings.Contains(logs.String(), marker) {
		t.Fatalf("err=%v logs=%s", err, logs.String())
	}
}
