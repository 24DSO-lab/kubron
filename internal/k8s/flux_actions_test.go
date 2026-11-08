// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

const (
	fluxaKustAPI   = "kustomize.toolkit.fluxcd.io/v1"
	fluxaHelmAPI   = "helm.toolkit.fluxcd.io/v2"
	fluxaSourceAPI = "source.toolkit.fluxcd.io/v1"
	fluxaImageAPI  = "image.toolkit.fluxcd.io/v1"
)

// fluxaPatch — записанный запрос patch.
type fluxaPatch struct {
	gvr          schema.GroupVersionResource
	ns, name     string
	patchType    types.PatchType
	body         map[string]any
	fieldManager string
}

type fluxaRecorder struct {
	mu      sync.Mutex
	patches []fluxaPatch
}

func (r *fluxaRecorder) all() []fluxaPatch {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]fluxaPatch(nil), r.patches...)
}

// fluxaSetup — fake dynamic client с записью всех patch; запрос patch всё равно доходит до трекера.
func fluxaSetup(objs ...runtime.Object) (*Manager, *dynamicfake.FakeDynamicClient, *fluxaRecorder) {
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objs...)
	rec := &fluxaRecorder{}
	dyn.PrependReactor("patch", "*", func(a clienttesting.Action) (bool, runtime.Object, error) {
		pa := a.(clienttesting.PatchAction)
		var body map[string]any
		_ = json.Unmarshal(pa.GetPatch(), &body)
		impl, _ := a.(clienttesting.PatchActionImpl)
		rec.mu.Lock()
		rec.patches = append(rec.patches, fluxaPatch{
			gvr: a.GetResource(), ns: a.GetNamespace(), name: pa.GetName(), patchType: pa.GetPatchType(),
			body: body, fieldManager: impl.PatchOptions.FieldManager,
		})
		rec.mu.Unlock()
		return false, nil, nil
	})
	return fluxManager(dyn), dyn, rec
}

func fluxaKust(ns, name string, spec map[string]any) *unstructured.Unstructured {
	return fluxObj(fluxaKustAPI, "Kustomization", ns, name, spec)
}

func fluxaGet(t *testing.T, dyn *dynamicfake.FakeDynamicClient, kind, ns, name string) *unstructured.Unstructured {
	t.Helper()
	k, _ := fluxKindByName(kind)
	u, err := dyn.Resource(k.GVR(k.Versions[0])).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func fluxaReq(t *testing.T, u *unstructured.Unstructured) string {
	t.Helper()
	return u.GetAnnotations()[fluxRequestedAtAnnotation]
}

func TestFluxReconcileSetsRequestedAt(t *testing.T) {
	m, dyn, rec := fluxaSetup(fluxaKust("flux-system", "apps", nil))
	before := time.Now().UTC().Add(-time.Second)
	res, err := m.ReconcileFlux([]FluxRef{{FluxKindKustomization, "flux-system", "apps"}}, false)
	if err != nil || len(res) != 1 || res[0].Error != "" {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if res[0].Kind != FluxKindKustomization || res[0].Namespace != "flux-system" || res[0].Name != "apps" {
		t.Fatalf("result must identify the object: %+v", res[0])
	}
	stamp := fluxaReq(t, fluxaGet(t, dyn, FluxKindKustomization, "flux-system", "apps"))
	at, perr := time.Parse(time.RFC3339Nano, stamp)
	if perr != nil || at.Before(before) || at.After(time.Now().Add(time.Second)) {
		t.Fatalf("requestedAt = %q (parse err %v)", stamp, perr)
	}
	ps := rec.all()
	if len(ps) != 1 || ps[0].patchType != types.MergePatchType || ps[0].fieldManager != fluxFieldManager {
		t.Fatalf("one merge patch with field manager %q expected: %+v", fluxFieldManager, ps)
	}
	// Патч трогает только аннотацию: spec не входит в тело.
	if _, ok := ps[0].body["spec"]; ok {
		t.Fatalf("reconcile must not patch spec: %v", ps[0].body)
	}
}

func TestFluxReconcileHelmReleaseAndSource(t *testing.T) {
	m, dyn, _ := fluxaSetup(
		fluxObj(fluxaHelmAPI, "HelmRelease", "apps", "web", nil),
		fluxObj(fluxaSourceAPI, "GitRepository", "apps", "repo", nil),
	)
	res, err := m.ReconcileFlux([]FluxRef{{FluxKindHelmRelease, "apps", "web"}, {FluxKindGitRepository, "apps", "repo"}}, false)
	if err != nil || len(res) != 2 || res[0].Error != "" || res[1].Error != "" {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	for _, kn := range [][3]string{{FluxKindHelmRelease, "apps", "web"}, {FluxKindGitRepository, "apps", "repo"}} {
		hr, _ := fluxKindByName(kn[0])
		u, err := dyn.Resource(hr.GVR(hr.Versions[0])).Namespace(kn[1]).Get(context.Background(), kn[2], metav1.GetOptions{})
		if err != nil || fluxaReq(t, u) == "" {
			t.Fatalf("%s: requestedAt not set (err=%v)", kn[0], err)
		}
	}
}

func TestFluxReconcileWithSourcePatchesSourceFirst(t *testing.T) {
	m, dyn, rec := fluxaSetup(
		fluxaKust("flux-system", "apps", map[string]any{"sourceRef": map[string]any{"kind": "GitRepository", "name": "repo"}}),
		fluxObj(fluxaSourceAPI, "GitRepository", "flux-system", "repo", nil),
	)
	res, err := m.ReconcileFlux([]FluxRef{{FluxKindKustomization, "flux-system", "apps"}}, true)
	if err != nil || len(res) != 1 || res[0].Error != "" {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	ps := rec.all()
	if len(ps) != 2 || ps[0].gvr.Resource != "gitrepositories" || ps[0].name != "repo" ||
		ps[1].gvr.Resource != "kustomizations" || ps[1].name != "apps" {
		t.Fatalf("source must be patched before the object: %+v", ps)
	}
	if fluxaReq(t, fluxaGet(t, dyn, FluxKindGitRepository, "flux-system", "repo")) == "" {
		t.Fatal("source requestedAt not set")
	}
	// Результат один — по запрошенному объекту; источник отдельной строкой не идёт.
}

func TestFluxReconcileWithSourceResolvesSourceRefs(t *testing.T) {
	cases := []struct {
		name    string
		obj     *unstructured.Unstructured
		src     *unstructured.Unstructured
		wantNS  string
		wantRes string
	}{
		{
			name: "kustomization source in another namespace",
			obj: fluxaKust("apps", "a", map[string]any{"sourceRef": map[string]any{
				"kind": "OCIRepository", "name": "oci", "namespace": "flux-system"}}),
			src:     fluxObj(fluxaSourceAPI, "OCIRepository", "flux-system", "oci", nil),
			wantNS:  "flux-system",
			wantRes: "ocirepositories",
		},
		{
			name: "helmrelease chart.spec.sourceRef defaults to own namespace",
			obj: fluxObj(fluxaHelmAPI, "HelmRelease", "apps", "web", map[string]any{"chart": map[string]any{
				"spec": map[string]any{"sourceRef": map[string]any{"kind": "HelmRepository", "name": "charts"}}}}),
			src:     fluxObj(fluxaSourceAPI, "HelmRepository", "apps", "charts", nil),
			wantNS:  "apps",
			wantRes: "helmrepositories",
		},
		{
			name: "helmrelease chartRef",
			obj: fluxObj(fluxaHelmAPI, "HelmRelease", "apps", "web", map[string]any{"chartRef": map[string]any{
				"kind": "OCIRepository", "name": "chart", "namespace": "oci-ns"}}),
			src:     fluxObj(fluxaSourceAPI, "OCIRepository", "oci-ns", "chart", nil),
			wantNS:  "oci-ns",
			wantRes: "ocirepositories",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _, rec := fluxaSetup(tc.obj, tc.src)
			ref := FluxRef{tc.obj.GetKind(), tc.obj.GetNamespace(), tc.obj.GetName()}
			res, err := m.ReconcileFlux([]FluxRef{ref}, true)
			if err != nil || res[0].Error != "" {
				t.Fatalf("err=%v res=%+v", err, res)
			}
			ps := rec.all()
			if len(ps) != 2 || ps[0].gvr.Resource != tc.wantRes || ps[0].ns != tc.wantNS {
				t.Fatalf("source patch expected first: %+v", ps)
			}
		})
	}
}

func TestFluxReconcileWithSourceEdgeCases(t *testing.T) {
	t.Run("no source reference reconciles the object only", func(t *testing.T) {
		m, _, rec := fluxaSetup(fluxaKust("ns", "a", nil))
		res, _ := m.ReconcileFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}}, true)
		if res[0].Error != "" || len(rec.all()) != 1 {
			t.Fatalf("res=%+v patches=%+v", res, rec.all())
		}
	})
	t.Run("unknown source kind reconciles the object only", func(t *testing.T) {
		m, _, rec := fluxaSetup(fluxaKust("ns", "a", map[string]any{"sourceRef": map[string]any{"kind": "ExternalArtifact", "name": "x"}}))
		res, _ := m.ReconcileFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}}, true)
		if res[0].Error != "" || len(rec.all()) != 1 {
			t.Fatalf("res=%+v patches=%+v", res, rec.all())
		}
	})
	t.Run("garbage in sourceRef does not panic", func(t *testing.T) {
		for _, spec := range []map[string]any{
			{"sourceRef": "oops"}, {"sourceRef": []any{int64(1)}}, {"sourceRef": map[string]any{"kind": int64(5), "name": true}},
			{"sourceRef": map[string]any{"kind": "GitRepository"}}, {"chart": "x"}, {"chart": map[string]any{"spec": int64(3)}},
		} {
			m, _, _ := fluxaSetup(fluxaKust("ns", "a", spec))
			res, err := m.ReconcileFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}}, true)
			if err != nil || len(res) != 1 {
				t.Fatalf("spec %v: err=%v res=%+v", spec, err, res)
			}
		}
	})
	t.Run("missing source stops the object", func(t *testing.T) {
		m, dyn, rec := fluxaSetup(fluxaKust("ns", "a", map[string]any{"sourceRef": map[string]any{"kind": "GitRepository", "name": "gone"}}))
		res, _ := m.ReconcileFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}}, true)
		if !strings.Contains(res[0].Error, "GitRepository ns/gone") || len(rec.all()) != 0 {
			t.Fatalf("res=%+v patches=%+v", res, rec.all())
		}
		if fluxaReq(t, fluxaGet(t, dyn, FluxKindKustomization, "ns", "a")) != "" {
			t.Fatal("the object must stay untouched when its source cannot be reconciled")
		}
	})
	t.Run("suspended source stops the object", func(t *testing.T) {
		m, _, rec := fluxaSetup(
			fluxaKust("ns", "a", map[string]any{"sourceRef": map[string]any{"kind": "GitRepository", "name": "r"}}),
			fluxObj(fluxaSourceAPI, "GitRepository", "ns", "r", map[string]any{"suspend": true}),
		)
		res, _ := m.ReconcileFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}}, true)
		if !strings.Contains(res[0].Error, errFluxSuspended.Error()) || len(rec.all()) != 0 {
			t.Fatalf("res=%+v patches=%+v", res, rec.all())
		}
	})
	t.Run("withSource is ignored for sources", func(t *testing.T) {
		m, _, rec := fluxaSetup(fluxObj(fluxaSourceAPI, "GitRepository", "ns", "r", map[string]any{"sourceRef": map[string]any{"kind": "GitRepository", "name": "other"}}))
		res, _ := m.ReconcileFlux([]FluxRef{{FluxKindGitRepository, "ns", "r"}}, true)
		if res[0].Error != "" || len(rec.all()) != 1 {
			t.Fatalf("res=%+v patches=%+v", res, rec.all())
		}
	})
}

func TestFluxReconcileSuspendedObject(t *testing.T) {
	m, dyn, rec := fluxaSetup(fluxaKust("ns", "a", map[string]any{"suspend": true}))
	res, err := m.ReconcileFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}}, false)
	if err != nil || !strings.Contains(res[0].Error, errFluxSuspended.Error()) {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if len(rec.all()) != 0 || fluxaReq(t, fluxaGet(t, dyn, FluxKindKustomization, "ns", "a")) != "" {
		t.Fatal("a suspended object must not be patched")
	}
}

func TestFluxActionsOnDeletingObject(t *testing.T) {
	del := fluxaKust("ns", "a", nil)
	del.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})
	for name, call := range map[string]func(*Manager, []FluxRef) ([]FluxActionResult, error){
		"reconcile": func(m *Manager, r []FluxRef) ([]FluxActionResult, error) { return m.ReconcileFlux(r, false) },
		"suspend":   func(m *Manager, r []FluxRef) ([]FluxActionResult, error) { return m.SuspendFlux(r) },
		"resume":    func(m *Manager, r []FluxRef) ([]FluxActionResult, error) { return m.ResumeFlux(r) },
	} {
		t.Run(name, func(t *testing.T) {
			m, _, rec := fluxaSetup(del.DeepCopy())
			res, err := call(m, []FluxRef{{FluxKindKustomization, "ns", "a"}})
			if err != nil || !strings.Contains(res[0].Error, errFluxDeleting.Error()) || len(rec.all()) != 0 {
				t.Fatalf("err=%v res=%+v patches=%+v", err, res, rec.all())
			}
		})
	}
}

func TestFluxSuspend(t *testing.T) {
	m, dyn, rec := fluxaSetup(fluxaKust("ns", "a", map[string]any{"interval": "5m", "path": "./x"}))
	res, err := m.SuspendFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}})
	if err != nil || res[0].Error != "" {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	u := fluxaGet(t, dyn, FluxKindKustomization, "ns", "a")
	if !boolAt(u.Object, "spec", "suspend") {
		t.Fatalf("spec.suspend must be true: %v", u.Object["spec"])
	}
	if strAt(u.Object, "spec", "path") != "./x" || strAt(u.Object, "spec", "interval") != "5m" {
		t.Fatalf("merge patch must keep the rest of spec: %v", u.Object["spec"])
	}
	if fluxaReq(t, u) != "" {
		t.Fatal("suspend must not request a reconcile")
	}
	ps := rec.all()
	if len(ps) != 1 || ps[0].patchType != types.MergePatchType || ps[0].fieldManager != fluxFieldManager {
		t.Fatalf("patches: %+v", ps)
	}
	// Повторное приостановление идемпотентно и не ошибка.
	res, _ = m.SuspendFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}})
	if res[0].Error != "" {
		t.Fatalf("second suspend: %+v", res)
	}
}

func TestFluxResume(t *testing.T) {
	m, dyn, rec := fluxaSetup(fluxObj(fluxaHelmAPI, "HelmRelease", "ns", "web", map[string]any{"suspend": true, "interval": "1h"}))
	res, err := m.ResumeFlux([]FluxRef{{FluxKindHelmRelease, "ns", "web"}})
	if err != nil || res[0].Error != "" {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	u := fluxaGet(t, dyn, FluxKindHelmRelease, "ns", "web")
	if _, found, _ := unstructured.NestedBool(u.Object, "spec", "suspend"); !found || boolAt(u.Object, "spec", "suspend") {
		t.Fatalf("spec.suspend must be present and false (merge patch sets false, not removes): %v", u.Object["spec"])
	}
	if _, err := time.Parse(time.RFC3339Nano, fluxaReq(t, u)); err != nil {
		t.Fatalf("resume must set requestedAt: %v", err)
	}
	if strAt(u.Object, "spec", "interval") != "1h" {
		t.Fatal("spec must be kept")
	}
	ps := rec.all()
	if len(ps) != 1 || ps[0].patchType != types.MergePatchType || ps[0].fieldManager != fluxFieldManager {
		t.Fatalf("one merge patch expected: %+v", ps)
	}
	// Объект не был приостановлен: возобновление всё равно допустимо (как у flux CLI).
	m2, _, _ := fluxaSetup(fluxaKust("ns", "a", nil))
	if res, _ := m2.ResumeFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}}); res[0].Error != "" {
		t.Fatalf("%+v", res)
	}
}

func TestFluxActionsAllowlist(t *testing.T) {
	// Ни один из этих видов не принимает действия: Alert, Provider, Receiver и ImagePolicy только читаются.
	for _, kind := range []string{FluxKindAlert, FluxKindProvider, FluxKindReceiver, FluxKindImagePolicy, "Deployment", "kustomization", ""} {
		m, _, rec := fluxaSetup()
		for name, call := range map[string]func() ([]FluxActionResult, error){
			"reconcile": func() ([]FluxActionResult, error) { return m.ReconcileFlux([]FluxRef{{kind, "ns", "a"}}, false) },
			"suspend":   func() ([]FluxActionResult, error) { return m.SuspendFlux([]FluxRef{{kind, "ns", "a"}}) },
			"resume":    func() ([]FluxActionResult, error) { return m.ResumeFlux([]FluxRef{{kind, "ns", "a"}}) },
		} {
			res, err := call()
			if err == nil || res != nil {
				t.Fatalf("%s %q: want call-level error, got res=%+v err=%v", name, kind, res, err)
			}
			if !errors.Is(err, errFluxKindUnsupported) && !errors.Is(err, errFluxInvalidRef) {
				t.Fatalf("%s %q: unexpected error %v", name, kind, err)
			}
		}
		if len(rec.all()) != 0 {
			t.Fatalf("kind %q: nothing may be sent to the cluster", kind)
		}
	}
	// Виды, которым действия разрешены.
	for _, k := range fluxKinds {
		if !k.Actions.has(fluxActToggle) {
			continue
		}
		obj := fluxObj(k.Group+"/"+k.Versions[0], k.Kind, "ns", "o", nil)
		m, _, _ := fluxaSetup(obj)
		for _, call := range []func() ([]FluxActionResult, error){
			func() ([]FluxActionResult, error) { return m.ReconcileFlux([]FluxRef{{k.Kind, "ns", "o"}}, false) },
			func() ([]FluxActionResult, error) { return m.SuspendFlux([]FluxRef{{k.Kind, "ns", "o"}}) },
			func() ([]FluxActionResult, error) { return m.ResumeFlux([]FluxRef{{k.Kind, "ns", "o"}}) },
		} {
			if res, err := call(); err != nil || len(res) != 1 || res[0].Error != "" {
				t.Fatalf("%s: err=%v res=%+v", k.Kind, err, res)
			}
		}
	}
}

func TestFluxActionsNormalizeRefs(t *testing.T) {
	obj := fluxaKust("ns", "a", nil)
	call := func(t *testing.T, refs []FluxRef) ([]FluxActionResult, *fluxaRecorder, error) {
		m, _, rec := fluxaSetup(obj.DeepCopy())
		res, err := m.SuspendFlux(refs)
		return res, rec, err
	}
	ref := FluxRef{FluxKindKustomization, "ns", "a"}

	t.Run("empty list", func(t *testing.T) {
		for _, refs := range [][]FluxRef{nil, {}} {
			if _, _, err := call(t, refs); !errors.Is(err, errFluxNoObjects) {
				t.Fatalf("err = %v", err)
			}
		}
	})
	t.Run("too many", func(t *testing.T) {
		refs := make([]FluxRef, fluxMaxActionObjects+1)
		for i := range refs {
			refs[i] = FluxRef{FluxKindKustomization, "ns", fmt.Sprintf("o%d", i)}
		}
		if _, rec, err := call(t, refs); !errors.Is(err, errFluxTooManyObjects) || len(rec.all()) != 0 {
			t.Fatalf("err = %v", err)
		}
		// Ровно предел допустим.
		if res, _, err := call(t, refs[:fluxMaxActionObjects]); err != nil || len(res) != fluxMaxActionObjects {
			t.Fatalf("limit: err=%v n=%d", err, len(res))
		}
	})
	t.Run("the limit counts the input, not the unique refs", func(t *testing.T) {
		refs := make([]FluxRef, fluxMaxActionObjects+1)
		for i := range refs {
			refs[i] = ref
		}
		if _, _, err := call(t, refs); !errors.Is(err, errFluxTooManyObjects) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("invalid fields", func(t *testing.T) {
		long := strings.Repeat("x", 201)
		for _, r := range []FluxRef{
			{FluxKindKustomization, "", "a"}, {FluxKindKustomization, "ns", ""}, {FluxKindKustomization, "  ", "a"},
			{FluxKindKustomization, "ns", "\t"}, {FluxKindKustomization, long, "a"}, {FluxKindKustomization, "ns", long},
		} {
			if _, rec, err := call(t, []FluxRef{ref, r}); !errors.Is(err, errFluxInvalidRef) || len(rec.all()) != 0 {
				t.Fatalf("%+v: err=%v (one bad ref must fail the whole call before anything is sent)", r, err)
			}
		}
		// Длина ровно 200 допустима.
		edge := strings.Repeat("x", 200)
		if _, _, err := call(t, []FluxRef{{FluxKindKustomization, "ns", edge}}); err != nil {
			t.Fatalf("200 chars: %v", err)
		}
	})
	t.Run("trim and dedupe", func(t *testing.T) {
		res, rec, err := call(t, []FluxRef{ref, {" Kustomization", " ns ", "\ta\n"}, ref})
		if err == nil {
			// «Kustomization» с пробелом — вид не из таблицы: регистр и пробелы вида не нормализуются.
			t.Fatalf("kind must not be normalized, got %+v", res)
		}
		_ = rec
		res, rec, err = call(t, []FluxRef{ref, {FluxKindKustomization, " ns ", "\ta\n"}, ref})
		if err != nil || len(res) != 1 || len(rec.all()) != 1 {
			t.Fatalf("err=%v res=%+v patches=%d", err, res, len(rec.all()))
		}
		if res[0].Namespace != "ns" || res[0].Name != "a" {
			t.Fatalf("result must carry trimmed refs: %+v", res[0])
		}
	})
	t.Run("order is kept", func(t *testing.T) {
		m, _, _ := fluxaSetup(fluxaKust("ns", "b", nil), fluxaKust("ns", "a", nil), fluxaKust("ns", "c", nil))
		res, _ := m.SuspendFlux([]FluxRef{{FluxKindKustomization, "ns", "c"}, {FluxKindKustomization, "ns", "a"}, {FluxKindKustomization, "ns", "b"}})
		if len(res) != 3 || res[0].Name != "c" || res[1].Name != "a" || res[2].Name != "b" {
			t.Fatalf("%+v", res)
		}
	})
}

func TestFluxActionsPerObjectErrors(t *testing.T) {
	m, dyn, rec := fluxaSetup(fluxaKust("ns", "ok1", nil), fluxaKust("ns", "ok2", nil))
	dyn.PrependReactor("patch", "kustomizations", func(a clienttesting.Action) (bool, runtime.Object, error) {
		if a.(clienttesting.PatchAction).GetName() == "forbidden" {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "kustomizations"}, "forbidden", errors.New("denied"))
		}
		return false, nil, nil
	})
	if _, err := dyn.Resource(mustFluxaGVR(FluxKindKustomization)).Namespace("ns").Create(context.Background(),
		fluxaKust("ns", "forbidden", nil), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	res, err := m.SuspendFlux([]FluxRef{
		{FluxKindKustomization, "ns", "ok1"}, {FluxKindKustomization, "ns", "missing"},
		{FluxKindKustomization, "ns", "forbidden"}, {FluxKindKustomization, "ns", "ok2"},
	})
	if err != nil || len(res) != 4 {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if res[0].Error != "" || res[3].Error != "" {
		t.Fatalf("one failure must not cancel the others: %+v", res)
	}
	if !strings.Contains(res[1].Error, "not found") || !strings.Contains(res[2].Error, "forbidden") {
		t.Fatalf("errors: %+v", res)
	}
	if got := len(rec.all()); got != 2 { // forbidden перехвачен раньше записи
		t.Fatalf("patches sent: %d", got)
	}
}

func mustFluxaGVR(kind string) schema.GroupVersionResource {
	k, _ := fluxKindByName(kind)
	return k.GVR(k.Versions[0])
}

func TestFluxActionsVersionAndInstall(t *testing.T) {
	// Нет такого вида в кластере (Flux не установлен) или версия вне списка — ошибка по объекту.
	orig := fluxActionVersions
	t.Cleanup(func() { fluxActionVersions = orig })
	fluxActionVersions = func(*ClusterConnection) (map[string]string, error) {
		return map[string]string{FluxKindHelmRelease: "v2beta2"}, nil // Kustomization нет, HelmRelease — старая версия
	}
	m, _, rec := fluxaSetup(fluxaKust("ns", "a", nil))
	res, err := m.SuspendFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}, {FluxKindHelmRelease, "ns", "h"}})
	if err != nil || len(res) != 2 {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if !strings.Contains(res[0].Error, errFluxNotInstalled.Error()) {
		t.Fatalf("missing kind: %+v", res[0])
	}
	if !strings.Contains(res[1].Error, errFluxVersionUnsupported.Error()) {
		t.Fatalf("old version: %+v", res[1])
	}
	if len(rec.all()) != 0 {
		t.Fatal("nothing may be sent")
	}
	// Ошибка discovery — ошибка вызова.
	fluxActionVersions = func(*ClusterConnection) (map[string]string, error) { return nil, errFluxNotInstalled }
	if _, err := m.SuspendFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}}); !errors.Is(err, errFluxNotInstalled) {
		t.Fatalf("err = %v", err)
	}
}

func TestFluxActionsTimeout(t *testing.T) {
	m, dyn, _ := fluxaSetup(fluxaKust("ns", "a", nil), fluxaKust("ns", "b", nil))
	var gotDeadline bool
	dyn.PrependReactor("get", "kustomizations", func(a clienttesting.Action) (bool, runtime.Object, error) {
		return false, nil, nil
	})
	dyn.PrependReactor("patch", "kustomizations", func(a clienttesting.Action) (bool, runtime.Object, error) {
		gotDeadline = true
		return false, nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn, _ := m.GetActiveConnection()
	// Отменённый контекст: объекты получают ошибку, вызов не паникует и в кластер ничего не уходит.
	res, err := fluxSuspend(ctx, conn, []FluxRef{{FluxKindKustomization, "ns", "a"}, {FluxKindKustomization, "ns", "b"}}, true, time.Now)
	if err != nil || len(res) != 2 || res[0].Error == "" || res[1].Error == "" || gotDeadline {
		t.Fatalf("err=%v res=%+v patched=%v", err, res, gotDeadline)
	}
	if fluxActionTimeout != 60*time.Second {
		t.Fatalf("timeout of an action call must be 60s, got %v", fluxActionTimeout)
	}
}

func TestFluxActionsNoActiveConnection(t *testing.T) {
	m := NewManager()
	if _, err := m.ReconcileFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}}, false); err == nil {
		t.Fatal("expected an error without an active connection")
	}
	if _, err := m.SuspendFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}}); err == nil {
		t.Fatal("expected an error without an active connection")
	}
	if _, err := m.ResumeFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}}); err == nil {
		t.Fatal("expected an error without an active connection")
	}
}

// Ошибки и результаты не несут содержимого объекта: в spec лежат поля, которые Kubron наружу не отдаёт.
func TestFluxActionsNoSecretLeak(t *testing.T) {
	const marker = "S3CRET-actions"
	m, _, _ := fluxaSetup(
		fluxaKust("ns", "a", map[string]any{
			"postBuild":  map[string]any{"substitute": map[string]any{"PASSWORD": marker}},
			"sourceRef":  map[string]any{"kind": "GitRepository", "name": "r"},
			"kubeConfig": map[string]any{"secretRef": map[string]any{"name": marker}},
		}),
		fluxObj(fluxaSourceAPI, "GitRepository", "ns", "r", map[string]any{"url": "https://u:" + marker + "@host/r.git", "suspend": true}),
		fluxObj(fluxaHelmAPI, "HelmRelease", "ns", "h", map[string]any{"values": map[string]any{"password": marker}, "suspend": true}),
	)
	var all []FluxActionResult
	r1, _ := m.ReconcileFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}}, true) // источник приостановлен: ошибка
	r2, _ := m.ReconcileFlux([]FluxRef{{FluxKindHelmRelease, "ns", "h"}}, false)
	r3, _ := m.SuspendFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}, {FluxKindKustomization, "ns", "nope"}})
	r4, _ := m.ResumeFlux([]FluxRef{{FluxKindHelmRelease, "ns", "h"}})
	all = append(append(append(append(all, r1...), r2...), r3...), r4...)
	if len(all) == 0 {
		t.Fatal("no results")
	}
	raw, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), marker) {
		t.Fatalf("object content leaked into the action results: %s", raw)
	}
}

func TestFluxActionsConcurrent(t *testing.T) {
	m, _, _ := fluxaSetup(fluxaKust("ns", "a", nil))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				_, _ = m.ReconcileFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}}, true)
				_, _ = m.SuspendFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}})
				_, _ = m.ResumeFlux([]FluxRef{{FluxKindKustomization, "ns", "a"}})
			}
		})
	}
	wg.Wait()
}
