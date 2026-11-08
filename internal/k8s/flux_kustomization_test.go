// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

const kustomizationFixtures = "testdata/flux/kustomization"

// readKustomizationFixture читает фикстуру настоящего объекта Flux из testdata/flux/kustomization.
func readKustomizationFixture(t testing.TB, name string) *unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(kustomizationFixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	obj := map[string]any{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return &unstructured.Unstructured{Object: obj}
}

func allKustomizationFixtures(t testing.TB) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(kustomizationFixtures, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures in %s: %v", kustomizationFixtures, err)
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	return names
}

const (
	kustRevOld = "main@sha1:6d1f6c3a8b0b9d9e0f6a1c3f0b2f1d8e4a5b6c7d"
	kustRevNew = "main@sha1:b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6"
)

func TestParseFluxKustomizationFixtures(t *testing.T) {
	cases := []struct {
		file string
		want FluxKustomizationInfo // сравниваются только перечисленные поля (Age и Created зависят от времени запуска)
		// conditions — ожидаемое число условий.
		conditions int
		message    string // подстрока сообщения Ready; пусто — не проверять
	}{
		{
			file: "v2.8-ready.yaml",
			want: FluxKustomizationInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindKustomization, Name: "apps", Namespace: "flux-system", Interval: "10m0s", State: FluxStateReady,
					SourceRef: &FluxRef{Kind: FluxKindGitRepository, Namespace: "flux-system", Name: "flux-system"},
					Revision:  kustRevOld, LastAttemptedRevision: kustRevOld, LastReconcileAt: "2026-11-05T09:00:03Z",
				},
				Path: "./clusters/prod/apps", Prune: true, Wait: true, TargetNamespace: "apps", ServiceAccount: "kustomize-apps",
				InventoryCount: 4,
			},
			conditions: 2, message: "Applied revision",
		},
		{
			file: "v2.8-reconciling.yaml",
			want: FluxKustomizationInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindKustomization, Name: "apps", Namespace: "flux-system", Interval: "10m0s", State: FluxStateReconciling,
					SourceRef: &FluxRef{Kind: FluxKindGitRepository, Namespace: "flux-system", Name: "flux-system"},
					Revision:  kustRevOld, LastAttemptedRevision: kustRevNew, LastReconcileAt: "2026-11-07T10:00:00Z",
				},
				Path: "./clusters/prod/apps", Prune: true, Wait: true, TargetNamespace: "apps", ServiceAccount: "kustomize-apps",
				InventoryCount: 4,
			},
			conditions: 2, message: "Reconciliation in progress",
		},
		{
			file: "v2.8-failed.yaml",
			want: FluxKustomizationInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindKustomization, Name: "apps", Namespace: "flux-system", Interval: "10m0s", State: FluxStateFailed,
					SourceRef: &FluxRef{Kind: FluxKindGitRepository, Namespace: "flux-system", Name: "flux-system"},
					Revision:  kustRevOld, LastAttemptedRevision: kustRevNew, LastReconcileAt: "2026-11-07T10:05:00Z",
				},
				Path: "./clusters/prod/apps", Prune: true, Wait: true, TargetNamespace: "apps", ServiceAccount: "kustomize-apps",
				InventoryCount: 4,
			},
			conditions: 1, message: "kustomize build failed",
		},
		{
			file: "v2.8-stalled.yaml",
			want: FluxKustomizationInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindKustomization, Name: "apps", Namespace: "flux-system", Interval: "10m0s", State: FluxStateStalled,
					SourceRef: &FluxRef{Kind: FluxKindGitRepository, Namespace: "flux-system", Name: "flux-system"},
					Revision:  kustRevOld, LastAttemptedRevision: kustRevNew, LastReconcileAt: "2026-11-07T10:06:00Z",
				},
				Path: "./clusters/prod/apps", Prune: true, Wait: true, TargetNamespace: "apps", ServiceAccount: "kustomize-apps",
				InventoryCount: 4,
			},
			conditions: 2, message: "path not found",
		},
		{
			file: "v2.8-suspended.yaml",
			want: FluxKustomizationInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindKustomization, Name: "apps", Namespace: "flux-system", Interval: "10m0s", Suspended: true, State: FluxStateSuspended,
					SourceRef: &FluxRef{Kind: FluxKindGitRepository, Namespace: "flux-system", Name: "flux-system"},
					Revision:  kustRevOld, LastAttemptedRevision: kustRevOld, LastReconcileAt: "2026-11-04T09:00:03Z",
				},
				Path: "./clusters/prod/apps", Prune: true, Wait: true, TargetNamespace: "apps", ServiceAccount: "kustomize-apps",
				InventoryCount: 4,
			},
			conditions: 1,
		},
		{
			file: "v2.8-remote.yaml",
			want: FluxKustomizationInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindKustomization, Name: "edge-apps", Namespace: "fleet", Interval: "10m0s", State: FluxStateReady,
					SourceRef: &FluxRef{Kind: FluxKindGitRepository, Namespace: "fleet", Name: "flux-system"},
					Revision:  kustRevOld, LastAttemptedRevision: kustRevOld, LastReconcileAt: "2026-11-05T09:00:03Z",
				},
				Path: "./clusters/prod/edge-apps", Prune: true, Wait: true, TargetNamespace: "edge-apps", ServiceAccount: "kustomize-edge-apps",
				Remote: true, InventoryCount: 2,
			},
			conditions: 1,
		},
		{
			file: "v2.8-dependson.yaml",
			want: FluxKustomizationInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindKustomization, Name: "apps", Namespace: "flux-system", Interval: "10m0s", State: FluxStateReady,
					SourceRef: &FluxRef{Kind: FluxKindOCIRepository, Namespace: "team-a", Name: "apps"},
					// dependsOn: вид пуст; namespace по умолчанию — namespace самого объекта.
					DependsOn: []FluxRef{{Namespace: "flux-system", Name: "infra"}, {Namespace: "flux-system", Name: "crds"}},
					Revision:  kustRevOld, LastAttemptedRevision: kustRevOld, LastReconcileAt: "2026-11-05T09:00:03Z",
				},
				Path: "./clusters/prod/apps", Prune: true, Wait: true, TargetNamespace: "apps", ServiceAccount: "kustomize-apps",
				InventoryCount: 4,
			},
			conditions: 1,
		},
		{
			file: "v2.3-ready.yaml",
			want: FluxKustomizationInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindKustomization, Name: "infra", Namespace: "flux-system", Interval: "10m0s", State: FluxStateReady,
					SourceRef: &FluxRef{Kind: FluxKindGitRepository, Namespace: "flux-system", Name: "flux-system"},
					Revision:  kustRevOld, LastAttemptedRevision: kustRevOld, LastReconcileAt: "2024-06-12T07:05:00Z",
				},
				Path: "./clusters/prod/infra", Prune: true, Wait: true, TargetNamespace: "infra", ServiceAccount: "kustomize-infra",
				InventoryCount: 3,
			},
			conditions: 1,
		},
		{
			file: "v2.3-failed.yaml",
			want: FluxKustomizationInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindKustomization, Name: "infra", Namespace: "flux-system", Interval: "10m0s", State: FluxStateFailed,
					SourceRef: &FluxRef{Kind: FluxKindGitRepository, Namespace: "flux-system", Name: "flux-system"},
					Revision:  kustRevOld, LastAttemptedRevision: kustRevNew, LastReconcileAt: "2024-06-13T07:05:00Z",
				},
				Path: "./clusters/prod/infra", Prune: true, Wait: true, TargetNamespace: "infra", ServiceAccount: "kustomize-infra",
				InventoryCount: 3,
			},
			conditions: 1, message: "dry-run failed",
		},
		{
			file: "v2.3-suspended.yaml",
			want: FluxKustomizationInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindKustomization, Name: "infra", Namespace: "flux-system", Interval: "10m0s", Suspended: true, State: FluxStateSuspended,
					SourceRef: &FluxRef{Kind: FluxKindGitRepository, Namespace: "flux-system", Name: "flux-system"},
					Revision:  kustRevOld, LastAttemptedRevision: kustRevOld, LastReconcileAt: "2024-06-12T07:05:00Z",
				},
				Path: "./clusters/prod/infra", Prune: true, Wait: true, TargetNamespace: "infra", ServiceAccount: "kustomize-infra",
				InventoryCount: 3,
			},
			conditions: 1,
		},
		{
			// Нет status.inventory (объект ещё ни разу не применялся или Flux не записал его): ноль, не ошибка.
			file: "v2.3-dependson.yaml",
			want: FluxKustomizationInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindKustomization, Name: "infra-config", Namespace: "flux-system", Interval: "10m0s", State: FluxStateReady,
					SourceRef: &FluxRef{Kind: FluxKindGitRepository, Namespace: "flux-system", Name: "flux-system"},
					DependsOn: []FluxRef{{Namespace: "flux-system", Name: "infra"}},
					Revision:  kustRevOld, LastAttemptedRevision: kustRevOld, LastReconcileAt: "2024-06-12T07:06:00Z",
				},
				Path: "./clusters/prod/infra-config", Prune: true, Wait: true, TargetNamespace: "infra-config", ServiceAccount: "kustomize-infra-config",
				InventoryCount: 0,
			},
			conditions: 1,
		},
	}

	covered := map[string]bool{}
	for _, tc := range cases {
		covered[tc.file] = true
		t.Run(tc.file, func(t *testing.T) {
			got := parseFluxKustomization(readKustomizationFixture(t, tc.file))
			w := tc.want

			if got.Kind != w.Kind || got.Name != w.Name || got.Namespace != w.Namespace {
				t.Errorf("identity = %s %s/%s, want %s %s/%s", got.Kind, got.Namespace, got.Name, w.Kind, w.Namespace, w.Name)
			}
			if got.Interval != w.Interval || got.Suspended != w.Suspended || got.State != w.State {
				t.Errorf("interval/suspended/state = %q/%v/%q, want %q/%v/%q", got.Interval, got.Suspended, got.State, w.Interval, w.Suspended, w.State)
			}
			if got.Path != w.Path || got.Prune != w.Prune || got.Wait != w.Wait ||
				got.TargetNamespace != w.TargetNamespace || got.ServiceAccount != w.ServiceAccount {
				t.Errorf("spec = %q/%v/%v/%q/%q, want %q/%v/%v/%q/%q",
					got.Path, got.Prune, got.Wait, got.TargetNamespace, got.ServiceAccount,
					w.Path, w.Prune, w.Wait, w.TargetNamespace, w.ServiceAccount)
			}
			if got.Remote != w.Remote || got.InventoryCount != w.InventoryCount {
				t.Errorf("remote/inventory = %v/%d, want %v/%d", got.Remote, got.InventoryCount, w.Remote, w.InventoryCount)
			}
			if got.Revision != w.Revision || got.LastAttemptedRevision != w.LastAttemptedRevision || got.LastReconcileAt != w.LastReconcileAt {
				t.Errorf("revisions/time = %q/%q/%q, want %q/%q/%q",
					got.Revision, got.LastAttemptedRevision, got.LastReconcileAt, w.Revision, w.LastAttemptedRevision, w.LastReconcileAt)
			}
			if (got.SourceRef == nil) != (w.SourceRef == nil) || (w.SourceRef != nil && *got.SourceRef != *w.SourceRef) {
				t.Errorf("sourceRef = %+v, want %+v", got.SourceRef, w.SourceRef)
			}
			if !slices.Equal(got.DependsOn, w.DependsOn) {
				t.Errorf("dependsOn = %+v, want %+v", got.DependsOn, w.DependsOn)
			}
			if len(got.Conditions) != tc.conditions {
				t.Errorf("conditions = %d, want %d", len(got.Conditions), tc.conditions)
			}
			if tc.message != "" && !strings.Contains(got.Message, tc.message) {
				t.Errorf("message = %q, want to contain %q", got.Message, tc.message)
			}
			if got.Age == "" || got.Created == "" {
				t.Errorf("age/created are empty: %q/%q", got.Age, got.Created)
			}
			if got.Labels["kustomize.toolkit.fluxcd.io/name"] != got.Name || got.Annotations["team"] != "platform" {
				t.Errorf("labels/annotations = %v / %v", got.Labels, got.Annotations)
			}
		})
	}
	// Каждая фикстура участвует в таблице: новая фикстура без ожиданий — это пробел в тесте.
	for _, f := range allKustomizationFixtures(t) {
		if !covered[f] {
			t.Errorf("fixture %s has no expectations in the table", f)
		}
	}
}

func TestParseFluxKustomizationNeverReturnsNilCollections(t *testing.T) {
	got := parseFluxKustomization(&unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kustomize.toolkit.fluxcd.io/v1", "kind": "Kustomization",
		"metadata": map[string]any{"name": "bare", "namespace": "ns"},
	}})
	if got.Conditions == nil || got.DependsOn == nil || got.Labels == nil || got.Annotations == nil {
		t.Fatalf("collections must be non-nil so that JSON has [] and {}: %+v", got)
	}
	if got.State != FluxStateUnknown || got.SourceRef != nil || got.InventoryCount != 0 || got.Remote {
		t.Errorf("bare object: %+v", got)
	}
	raw, _ := json.Marshal(got)
	for _, null := range []string{`"conditions":null`, `"dependsOn":null`, `"labels":null`, `"annotations":null`} {
		if strings.Contains(string(raw), null) {
			t.Errorf("%s in %s", null, raw)
		}
	}
}

func TestParseFluxKustomizationLastReconcileFallback(t *testing.T) {
	mk := func(status map[string]any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{"name": "k", "namespace": "ns"}, "status": status,
		}}
	}
	// Нет Ready-условия: берётся lastHandledReconcileAt, но только если это время (токен задаёт клиент).
	if got := parseFluxKustomization(mk(map[string]any{"lastHandledReconcileAt": "2026-11-05T09:00:01.123456789Z"})); got.LastReconcileAt != "2026-11-05T09:00:01.123456789Z" {
		t.Errorf("fallback to a valid timestamp: %q", got.LastReconcileAt)
	}
	if got := parseFluxKustomization(mk(map[string]any{"lastHandledReconcileAt": "not-a-time"})); got.LastReconcileAt != "" {
		t.Errorf("an opaque token must not be shown as a time: %q", got.LastReconcileAt)
	}
	// Есть Ready-условие: его время главнее.
	got := parseFluxKustomization(mk(map[string]any{
		"lastHandledReconcileAt": "2026-11-05T09:00:01Z",
		"conditions":             []any{map[string]any{"type": "Ready", "status": "True", "lastTransitionTime": "2026-11-06T00:00:00Z"}},
	}))
	if got.LastReconcileAt != "2026-11-06T00:00:00Z" {
		t.Errorf("Ready transition time must win: %q", got.LastReconcileAt)
	}
}

func TestParseFluxKustomizationToleratesGarbage(t *testing.T) {
	bad := func(spec, status any) *unstructured.Unstructured {
		o := map[string]any{"metadata": map[string]any{"name": "g", "namespace": "ns"}}
		if spec != nil {
			o["spec"] = spec
		}
		if status != nil {
			o["status"] = status
		}
		return &unstructured.Unstructured{Object: o}
	}
	cases := map[string]*unstructured.Unstructured{
		"nil object":            {},
		"spec is a string":      bad("oops", "oops"),
		"status is a list":      bad(map[string]any{}, []any{1, 2}),
		"sourceRef is a string": bad(map[string]any{"sourceRef": "GitRepository/x"}, nil),
		"sourceRef wrong types": bad(map[string]any{"sourceRef": map[string]any{"kind": 1, "name": []any{}}}, nil),
		"dependsOn garbage":     bad(map[string]any{"dependsOn": []any{"x", 3, nil, map[string]any{"name": 4}, map[string]any{}}}, nil),
		"flags have wrong type": bad(map[string]any{"suspend": "yes", "prune": 1, "wait": "true", "path": 5, "interval": 10}, nil),
		"kubeConfig garbage":    bad(map[string]any{"kubeConfig": "x"}, nil),
		"conditions garbage":    bad(nil, map[string]any{"conditions": []any{"x", 1, map[string]any{"type": 5, "status": nil}}}),
		"inventory garbage":     bad(nil, map[string]any{"inventory": map[string]any{"entries": "none"}}),
		"inventory is a list":   bad(nil, map[string]any{"inventory": []any{"a"}}),
		"revision wrong types":  bad(nil, map[string]any{"lastAppliedRevision": 7, "lastAttemptedRevision": []any{}, "lastHandledReconcileAt": 3}),
	}
	for name, u := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic: %v", r)
				}
			}()
			got := parseFluxKustomization(u)
			if got.Kind != FluxKindKustomization {
				t.Errorf("Kind = %q", got.Kind)
			}
			if got.Conditions == nil || got.DependsOn == nil {
				t.Errorf("nil collections: %+v", got)
			}
			if _, err := json.Marshal(got); err != nil {
				t.Errorf("json: %v", err)
			}
		})
	}
	// Элементы dependsOn без имени пропускаются: вместе с мусором остаётся только корректная запись.
	got := parseFluxKustomization(bad(map[string]any{"dependsOn": []any{"x", map[string]any{}, map[string]any{"name": "ok"}}}, nil))
	if !slices.Equal(got.DependsOn, []FluxRef{{Namespace: "ns", Name: "ok"}}) {
		t.Errorf("dependsOn = %+v", got.DependsOn)
	}
}

func TestParseFluxKustomizationRemoteOnlyWhenKubeConfigSet(t *testing.T) {
	mk := func(kc any) *unstructured.Unstructured {
		spec := map[string]any{}
		if kc != nil {
			spec["kubeConfig"] = kc
		}
		return &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "k", "namespace": "ns"}, "spec": spec}}
	}
	if parseFluxKustomization(mk(nil)).Remote {
		t.Error("no kubeConfig: not remote")
	}
	if parseFluxKustomization(mk(map[string]any{})).Remote {
		t.Error("empty kubeConfig: not remote")
	}
	if !parseFluxKustomization(mk(map[string]any{"secretRef": map[string]any{"name": "kc"}})).Remote {
		t.Error("kubeConfig with secretRef: remote")
	}
}

func TestGetFluxKustomizations(t *testing.T) {
	var objs []runtime.Object
	for _, f := range []string{"v2.8-ready.yaml", "v2.8-remote.yaml", "v2.3-dependson.yaml", "v2.8-failed.yaml"} {
		objs = append(objs, readKustomizationFixture(t, f))
	}
	// В fixtures у v2.8-failed и v2.8-ready одинаковые namespace и name: второй объект переименовываем.
	objs[3].(*unstructured.Unstructured).SetName("apps-failed")
	m := fluxManager(newFluxDyn(objs...))

	all, err := m.GetFluxKustomizations(nil)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, k := range all {
		keys = append(keys, k.Namespace+"/"+k.Name)
	}
	// Порядок стабильный: namespace, затем имя.
	want := []string{"fleet/edge-apps", "flux-system/apps", "flux-system/apps-failed", "flux-system/infra-config"}
	if !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}

	one, err := m.GetFluxKustomizations([]string{"fleet"})
	if err != nil || len(one) != 1 || !one[0].Remote {
		t.Fatalf("fleet: err=%v items=%+v", err, one)
	}

	none, err := m.GetFluxKustomizations([]string{"empty"})
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("empty namespace must give an empty non-nil list: err=%v items=%#v", err, none)
	}

	if _, err := m.GetFluxKustomizations([]string{"a/b"}); err == nil {
		t.Error("an invalid namespace in the scope must be rejected")
	}
}

// fluxKustomizationSecretMarkers — контрольные строки, которых не должно быть ни в одном ответе.
var fluxKustomizationSecretMarkers = []string{
	"S3CRET-SUBSTITUTE", "S3CRET-SUBSTITUTE-TOKEN", "S3CRET-SUBFROM", "S3CRET-DECRYPT", "S3CRET-KUBECONFIG",
	"S3CRET-LASTAPPLIED", "S3CRET-URLPASS", "S3CRET-MSGTOKEN",
}

// TestFluxKustomizationNoSecretLeak — по образцу TestArgoSettingsNoSecretLeak: запрещённые поля заполнены
// контрольными строками, ни одна не должна попасть в json.Marshal ответа. Новое поле DTO добавляется только через этот тест.
func TestFluxKustomizationNoSecretLeak(t *testing.T) {
	// 1) Все фикстуры: в них postBuild, decryption, kubeConfig и last-applied-configuration несут маркеры.
	var objs []runtime.Object
	for i, f := range allKustomizationFixtures(t) {
		u := readKustomizationFixture(t, f)
		u.SetName(u.GetName() + "-" + string(rune('a'+i))) // имена фикстур повторяются
		objs = append(objs, u)
	}
	// 2) Объект со всеми запрещёнными местами сразу, включая маркеры в метках, сообщениях и URL с паролем.
	evil := fluxObj("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "team", "evil", map[string]any{
		"interval":  "1m",
		"path":      "./x",
		"sourceRef": map[string]any{"kind": "GitRepository", "name": "src"},
		"postBuild": map[string]any{
			"substitute":     map[string]any{"PASSWORD": "S3CRET-SUBSTITUTE", "TOKEN": "S3CRET-SUBSTITUTE-TOKEN"},
			"substituteFrom": []any{map[string]any{"kind": "Secret", "name": "S3CRET-SUBFROM"}},
		},
		"decryption": map[string]any{"provider": "sops", "secretRef": map[string]any{"name": "S3CRET-DECRYPT"}},
		"kubeConfig": map[string]any{"secretRef": map[string]any{"name": "S3CRET-KUBECONFIG", "key": "S3CRET-KUBECONFIG"}},
	})
	evil.SetAnnotations(map[string]string{
		fluxLastAppliedAnnotation: `{"spec":{"postBuild":{"substitute":{"A":"S3CRET-LASTAPPLIED"}}}}`,
		"keep":                    "me",
	})
	evil.Object["status"] = map[string]any{
		"conditions": []any{map[string]any{
			"type": "Ready", "status": "False", "reason": "BuildFailed", "lastTransitionTime": "2026-11-07T10:00:00Z",
			"message": "fetch failed: https://git:S3CRET-URLPASS@example.com/o/r.git; Authorization: Bearer S3CRET-MSGTOKEN",
		}},
	}
	objs = append(objs, evil)

	m := fluxManager(newFluxDyn(objs...))
	got, err := m.GetFluxKustomizations(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(objs) {
		t.Fatalf("got %d kustomizations, want %d", len(got), len(objs))
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	all := string(raw)
	for _, marker := range fluxKustomizationSecretMarkers {
		if strings.Contains(all, marker) {
			t.Errorf("secret %q leaked into the response: %s", marker, all)
		}
	}
	// Общий запрет на любые контрольные строки из фикстур, даже если список выше устарел.
	if strings.Contains(all, "S3CRET") {
		t.Errorf("a control string leaked into the response: %s", all)
	}

	// Разрешённое при этом не потерялось.
	for _, k := range got {
		if k.Name == "evil" {
			if _, ok := k.Annotations[fluxLastAppliedAnnotation]; ok {
				t.Error("last-applied-configuration must not be returned")
			}
			if k.Annotations["keep"] != "me" {
				t.Errorf("other annotations must stay: %v", k.Annotations)
			}
			if !k.Remote {
				t.Error("kubeConfig is set: Remote must be true")
			}
			if !strings.Contains(k.Message, "fetch failed") {
				t.Errorf("message was dropped instead of redacted: %q", k.Message)
			}
		}
	}
}
