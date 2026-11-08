// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/yaml"
)

const helmReleaseFixtures = "testdata/flux/helmrelease"

func readHelmReleaseFixture(t testing.TB, name string) *unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(helmReleaseFixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	obj := map[string]any{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return &unstructured.Unstructured{Object: obj}
}

func allHelmReleaseFixtures(t testing.TB) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(helmReleaseFixtures, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures in %s: %v", helmReleaseFixtures, err)
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	return names
}

// newHelmDyn — fake dynamic client, который знает список HelmRelease v2.
func newHelmDyn(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	kind, _ := fluxKindByName(FluxKindHelmRelease)
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		kind.GVR("v2"): "HelmReleaseList",
	}, objs...)
}

func TestParseFluxHelmReleaseFixtures(t *testing.T) {
	type hist struct {
		version int64
		status  string
		chart   string
	}
	cases := []struct {
		file string
		want FluxHelmReleaseInfo // сравниваются перечисленные поля (Age и Created зависят от времени запуска)
		// history — ожидаемые (версия, статус, версия чарта), новые первыми.
		history []hist
		message string
	}{
		{
			file: "v2.8-ready-inventory.yaml",
			want: FluxHelmReleaseInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindHelmRelease, Name: "podinfo", Namespace: "apps", Interval: "15m0s", State: FluxStateReady,
					SourceRef: &FluxRef{Kind: FluxKindHelmRepository, Namespace: "flux-system", Name: "podinfo"},
					Revision:  "6.5.4", LastAttemptedRevision: "6.5.4", LastReconcileAt: "2026-11-05T09:00:03Z",
				},
				Chart: "podinfo", ChartVersion: "6.x", ReleaseName: "podinfo-podinfo", TargetNamespace: "podinfo", StorageNamespace: "helm-storage",
				HasInventory: true, InventoryCount: 5,
			},
			history: []hist{{3, "deployed", "6.5.4"}, {2, "superseded", "6.5.3"}, {1, "superseded", "6.5.2"}},
			message: "Helm upgrade succeeded",
		},
		{
			file: "v2.8-chartref.yaml",
			want: FluxHelmReleaseInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindHelmRelease, Name: "podinfo", Namespace: "team-a", Interval: "15m0s", State: FluxStateReady,
					// chartRef без namespace — namespace самого объекта; он же служит источником.
					SourceRef: &FluxRef{Kind: FluxKindOCIRepository, Namespace: "team-a", Name: "podinfo-chart"},
					Revision:  "6.5.4", LastAttemptedRevision: "6.5.4", LastReconcileAt: "2026-11-05T09:10:00Z",
				},
				// При chartRef чарт берётся из статуса (последняя запись истории).
				Chart: "podinfo", ChartVersion: "6.5.4",
				ChartRef:    &FluxRef{Kind: FluxKindOCIRepository, Namespace: "team-a", Name: "podinfo-chart"},
				ReleaseName: "podinfo", TargetNamespace: "team-a", StorageNamespace: "team-a",
				HasInventory: true, InventoryCount: 3,
			},
			history: []hist{{1, "deployed", "6.5.4"}},
		},
		{
			file: "v2.8-remote.yaml",
			want: FluxHelmReleaseInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindHelmRelease, Name: "edge", Namespace: "fleet", Interval: "15m0s", State: FluxStateReady,
					SourceRef: &FluxRef{Kind: FluxKindHelmRepository, Namespace: "flux-system", Name: "podinfo"},
					Revision:  "6.5.4", LastAttemptedRevision: "6.5.4", LastReconcileAt: "2026-11-05T09:10:00Z",
				},
				Chart: "podinfo", ChartVersion: "6.x", ReleaseName: "edge", TargetNamespace: "fleet", StorageNamespace: "fleet",
				Remote: true, HasInventory: true, InventoryCount: 2,
			},
			history: []hist{{1, "deployed", "6.5.4"}},
		},
		{
			file: "v2.8-suspended.yaml",
			want: FluxHelmReleaseInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindHelmRelease, Name: "podinfo", Namespace: "apps", Interval: "15m0s", Suspended: true, State: FluxStateSuspended,
					SourceRef: &FluxRef{Kind: FluxKindHelmRepository, Namespace: "flux-system", Name: "podinfo"},
					Revision:  "6.5.4", LastAttemptedRevision: "6.5.4", LastReconcileAt: "2026-11-04T09:00:03Z",
				},
				Chart: "podinfo", ChartVersion: "6.x", ReleaseName: "podinfo-podinfo", TargetNamespace: "podinfo", StorageNamespace: "apps",
				HasInventory: true, InventoryCount: 5,
			},
			history: []hist{{3, "deployed", "6.5.4"}},
		},
		{
			file: "v2.8-failed.yaml",
			want: FluxHelmReleaseInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindHelmRelease, Name: "podinfo", Namespace: "apps", Interval: "15m0s", State: FluxStateFailed,
					SourceRef: &FluxRef{Kind: FluxKindHelmRepository, Namespace: "flux-system", Name: "podinfo"},
					Revision:  "6.5.4", LastAttemptedRevision: "6.5.5", LastReconcileAt: "2026-11-07T10:05:00Z",
				},
				Chart: "podinfo", ChartVersion: "6.x", ReleaseName: "podinfo-podinfo", TargetNamespace: "podinfo", StorageNamespace: "apps",
				HasInventory: true, InventoryCount: 5,
			},
			history: []hist{{4, "failed", "6.5.5"}, {3, "deployed", "6.5.4"}},
			message: "Helm upgrade failed",
		},
		{
			file: "v2.8-stalled.yaml",
			want: FluxHelmReleaseInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindHelmRelease, Name: "podinfo", Namespace: "apps", Interval: "15m0s", State: FluxStateStalled,
					SourceRef: &FluxRef{Kind: FluxKindHelmRepository, Namespace: "flux-system", Name: "podinfo"},
					Revision:  "6.5.4", LastAttemptedRevision: "6.5.5", LastReconcileAt: "2026-11-07T10:20:00Z",
				},
				Chart: "podinfo", ChartVersion: "6.x", ReleaseName: "podinfo-podinfo", TargetNamespace: "podinfo", StorageNamespace: "apps",
				HasInventory: true, InventoryCount: 5,
			},
			history: []hist{{3, "deployed", "6.5.4"}},
			message: "Failed to upgrade",
		},
		{
			// Flux 2.3: status.inventory нет совсем — вкладка ресурсов недоступна.
			file: "v2.3-ready.yaml",
			want: FluxHelmReleaseInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindHelmRelease, Name: "podinfo", Namespace: "apps", Interval: "15m0s", State: FluxStateReady,
					SourceRef: &FluxRef{Kind: FluxKindHelmRepository, Namespace: "flux-system", Name: "podinfo"},
					Revision:  "6.5.3", LastAttemptedRevision: "6.5.3", LastReconcileAt: "2024-06-12T07:05:00Z",
				},
				Chart: "podinfo", ChartVersion: "6.x", ReleaseName: "podinfo-podinfo", TargetNamespace: "podinfo", StorageNamespace: "apps",
				HasInventory: false, InventoryCount: 0,
			},
			history: []hist{{3, "deployed", "6.5.4"}, {2, "superseded", "6.5.3"}},
		},
		{
			file: "v2.3-chartref.yaml",
			want: FluxHelmReleaseInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindHelmRelease, Name: "podinfo", Namespace: "team-a", Interval: "15m0s", State: FluxStateReady,
					SourceRef: &FluxRef{Kind: FluxKindOCIRepository, Namespace: "flux-system", Name: "podinfo-chart"},
					Revision:  "6.5.4", LastAttemptedRevision: "6.5.4", LastReconcileAt: "2024-06-12T07:10:00Z",
				},
				Chart: "podinfo", ChartVersion: "6.5.4",
				ChartRef:    &FluxRef{Kind: FluxKindOCIRepository, Namespace: "flux-system", Name: "podinfo-chart"},
				ReleaseName: "podinfo", TargetNamespace: "team-a", StorageNamespace: "team-a",
			},
			history: []hist{{1, "deployed", "6.5.4"}},
		},
		{
			// Ещё не устанавливался: истории нет, имя релиза считается по правилу releaseName.
			file: "v2.3-remote.yaml",
			want: FluxHelmReleaseInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindHelmRelease, Name: "api", Namespace: "fleet", Interval: "15m0s", State: FluxStateUnknown,
					SourceRef:       &FluxRef{Kind: FluxKindHelmRepository, Namespace: "flux-system", Name: "podinfo"},
					LastReconcileAt: "2024-06-12T07:00:10Z",
				},
				Chart: "podinfo", ChartVersion: "6.x", ReleaseName: "team-a-api", TargetNamespace: "team-a", StorageNamespace: "fleet",
				Remote: true,
			},
			history: nil,
			message: "Reconciliation in progress",
		},
		{
			file: "v2.3-suspended.yaml",
			want: FluxHelmReleaseInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindHelmRelease, Name: "podinfo", Namespace: "apps", Interval: "15m0s", Suspended: true, State: FluxStateSuspended,
					SourceRef: &FluxRef{Kind: FluxKindHelmRepository, Namespace: "flux-system", Name: "podinfo"},
					DependsOn: []FluxRef{{Namespace: "infra", Name: "ingress-nginx"}, {Namespace: "apps", Name: "cert-manager"}},
					Revision:  "6.5.4", LastAttemptedRevision: "6.5.4", LastReconcileAt: "2024-06-12T07:05:00Z",
				},
				Chart: "podinfo", ChartVersion: "6.x", ReleaseName: "podinfo-podinfo", TargetNamespace: "podinfo", StorageNamespace: "apps",
			},
			history: []hist{{3, "deployed", "6.5.4"}},
		},
		{
			file: "v2.3-failed.yaml",
			want: FluxHelmReleaseInfo{
				FluxObjectInfo: FluxObjectInfo{
					Kind: FluxKindHelmRelease, Name: "podinfo", Namespace: "apps", Interval: "15m0s", State: FluxStateFailed,
					SourceRef: &FluxRef{Kind: FluxKindHelmRepository, Namespace: "flux-system", Name: "podinfo"},
					Revision:  "", LastAttemptedRevision: "6.5.4", LastReconcileAt: "2024-06-13T07:05:00Z",
				},
				Chart: "podinfo", ChartVersion: "6.x", ReleaseName: "podinfo-podinfo", TargetNamespace: "podinfo", StorageNamespace: "apps",
			},
			history: []hist{{1, "failed", "6.5.4"}},
			message: "Helm install failed",
		},
	}

	covered := map[string]bool{}
	for _, tc := range cases {
		covered[tc.file] = true
		t.Run(tc.file, func(t *testing.T) {
			got := parseFluxHelmRelease(readHelmReleaseFixture(t, tc.file))
			w := tc.want

			if got.Kind != w.Kind || got.Name != w.Name || got.Namespace != w.Namespace {
				t.Errorf("identity = %s %s/%s, want %s %s/%s", got.Kind, got.Namespace, got.Name, w.Kind, w.Namespace, w.Name)
			}
			if got.Interval != w.Interval || got.Suspended != w.Suspended || got.State != w.State {
				t.Errorf("interval/suspended/state = %q/%v/%q, want %q/%v/%q", got.Interval, got.Suspended, got.State, w.Interval, w.Suspended, w.State)
			}
			if got.Revision != w.Revision || got.LastAttemptedRevision != w.LastAttemptedRevision || got.LastReconcileAt != w.LastReconcileAt {
				t.Errorf("revisions/time = %q/%q/%q, want %q/%q/%q",
					got.Revision, got.LastAttemptedRevision, got.LastReconcileAt, w.Revision, w.LastAttemptedRevision, w.LastReconcileAt)
			}
			if (got.SourceRef == nil) != (w.SourceRef == nil) || (w.SourceRef != nil && *got.SourceRef != *w.SourceRef) {
				t.Errorf("sourceRef = %+v, want %+v", got.SourceRef, w.SourceRef)
			}
			if (got.ChartRef == nil) != (w.ChartRef == nil) || (w.ChartRef != nil && *got.ChartRef != *w.ChartRef) {
				t.Errorf("chartRef = %+v, want %+v", got.ChartRef, w.ChartRef)
			}
			if !slices.Equal(got.DependsOn, w.DependsOn) {
				t.Errorf("dependsOn = %+v, want %+v", got.DependsOn, w.DependsOn)
			}
			if got.Chart != w.Chart || got.ChartVersion != w.ChartVersion {
				t.Errorf("chart = %q@%q, want %q@%q", got.Chart, got.ChartVersion, w.Chart, w.ChartVersion)
			}
			// Имя релиза и namespace хранилища — ключ сопоставления с Helm-релизом (задача X.1).
			if got.ReleaseName != w.ReleaseName || got.TargetNamespace != w.TargetNamespace || got.StorageNamespace != w.StorageNamespace {
				t.Errorf("release = %q target=%q storage=%q, want %q/%q/%q",
					got.ReleaseName, got.TargetNamespace, got.StorageNamespace, w.ReleaseName, w.TargetNamespace, w.StorageNamespace)
			}
			if got.Remote != w.Remote || got.HasInventory != w.HasInventory || got.InventoryCount != w.InventoryCount {
				t.Errorf("remote/inventory = %v/%v/%d, want %v/%v/%d",
					got.Remote, got.HasInventory, got.InventoryCount, w.Remote, w.HasInventory, w.InventoryCount)
			}
			if got.History == nil {
				t.Fatal("History must not be nil")
			}
			if len(got.History) != len(tc.history) {
				t.Fatalf("history = %+v, want %d entries", got.History, len(tc.history))
			}
			for i, h := range tc.history {
				e := got.History[i]
				if e.Version != h.version || e.Status != h.status || e.ChartVersion != h.chart || e.ChartName != "podinfo" || e.AppVersion != h.chart {
					t.Errorf("history[%d] = %+v, want version=%d status=%s chart=%s", i, e, h.version, h.status, h.chart)
				}
				if e.FirstDeployed == "" || e.LastDeployed == "" {
					t.Errorf("history[%d] has no deploy times: %+v", i, e)
				}
				if e.Digest != "" {
					t.Errorf("history[%d].Digest must stay empty (digests are not needed): %q", i, e.Digest)
				}
			}
			if tc.message != "" && !strings.Contains(got.Message, tc.message) {
				t.Errorf("message = %q, want to contain %q", got.Message, tc.message)
			}
			if got.Age == "" || got.Created == "" {
				t.Errorf("age/created are empty: %q/%q", got.Age, got.Created)
			}
			if got.Labels["helm.toolkit.fluxcd.io/name"] != got.Name || got.Annotations["team"] != "platform" {
				t.Errorf("labels/annotations = %v / %v", got.Labels, got.Annotations)
			}
		})
	}
	for _, f := range allHelmReleaseFixtures(t) {
		if !covered[f] {
			t.Errorf("fixture %s has no expectations in the table", f)
		}
	}
}

// shortHash — независимая запись правила из документации Flux: первые 40 символов имени, дефис,
// первые 12 символов hex SHA-256 полного имени.
func shortHash(name string) string {
	sum := sha256.Sum256([]byte(name))
	return name[:40] + "-" + hex.EncodeToString(sum[:])[:12]
}

func TestHelmReleaseNameRule(t *testing.T) {
	long := strings.Repeat("a", 30) + "-" + strings.Repeat("b", 30) // 61 символ
	exact53 := strings.Repeat("c", 53)
	over54 := strings.Repeat("d", 54)
	cases := []struct {
		name                   string
		hr, specName, targetNS string
		want                   string
	}{
		{"plain name", "web", "", "", "web"},
		{"target namespace prefixes the default", "web", "", "prod", "prod-web"},
		{"explicit releaseName wins", "web", "custom", "prod", "custom"},
		{"exactly 53 characters stays", exact53, "", "", exact53},
		{"54 characters are shortened", over54, "", "", shortHash(over54)},
		{"long composed name is shortened", long, "", "", shortHash(long)},
		{"prefix makes the name long", strings.Repeat("e", 51), "", "ns", shortHash("ns-" + strings.Repeat("e", 51))},
		{"prefix and name make exactly 53", strings.Repeat("e", 50), "", "ns", "ns-" + strings.Repeat("e", 50)},
		{"long explicit name is shortened too", "web", long, "", shortHash(long)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := helmReleaseName(tc.hr, tc.specName, tc.targetNS)
			if got != tc.want {
				t.Errorf("helmReleaseName = %q, want %q", got, tc.want)
			}
			if len(got) > 53 {
				t.Errorf("name is longer than 53 characters: %d", len(got))
			}
		})
	}
	// Фиксированный вектор: ловит случайную смену алгоритма. 12 hex-символов SHA-256 от 54 букв «d» посчитаны
	// независимо (python3, hashlib), а не тем же кодом, что и реализация.
	if got, want := helmReleaseName(over54, "", ""), strings.Repeat("d", 40)+"-198322b8dd96"; got != want {
		t.Errorf("fixed vector mismatch: %q, want %q", got, want)
	}
}

func TestParseFluxHelmReleaseHistoryOrderAndReleaseName(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "web", "namespace": "ns"},
		"spec":     map[string]any{"targetNamespace": "prod"},
		"status": map[string]any{
			"history": []any{
				map[string]any{"version": int64(1), "name": "old-name", "status": "superseded"},
				map[string]any{"version": int64(3), "name": "actual-name", "status": "deployed"},
				"garbage",
				map[string]any{"version": int64(2), "name": "mid-name", "status": "superseded"},
			},
		},
	}}
	got := parseFluxHelmRelease(u)
	var versions []int64
	for _, h := range got.History {
		versions = append(versions, h.Version)
	}
	if !slices.Equal(versions, []int64{3, 2, 1}) {
		t.Errorf("history order = %v, want newest first", versions)
	}
	// Имя берётся у самой новой записи истории: оно и есть реальное имя релиза в Helm.
	if got.ReleaseName != "actual-name" {
		t.Errorf("ReleaseName = %q, want the name of the newest history entry", got.ReleaseName)
	}

	// Новая запись без имени: откат к правилу из spec.
	u.Object["status"] = map[string]any{"history": []any{map[string]any{"version": int64(1)}}}
	if got := parseFluxHelmRelease(u); got.ReleaseName != "prod-web" {
		t.Errorf("ReleaseName without a history name = %q, want prod-web", got.ReleaseName)
	}
}

func TestParseFluxHelmReleaseInlineOCIChartHidesUserinfo(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "web", "namespace": "ns"},
		"spec": map[string]any{"chart": map[string]any{"spec": map[string]any{
			"url": "oci://user:S3CRET-URLPASS@ghcr.io/org/charts/web", "version": "1.x",
			"sourceRef": map[string]any{"kind": "OCIRepository", "name": "x"},
		}}},
	}}
	got := parseFluxHelmRelease(u)
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "S3CRET") {
		t.Fatalf("userinfo leaked: %s", raw)
	}
	if got.Chart != "oci://ghcr.io/org/charts/web" || got.ChartVersion != "1.x" {
		t.Errorf("chart = %q@%q", got.Chart, got.ChartVersion)
	}
}

func TestParseFluxHelmReleaseInventoryFlag(t *testing.T) {
	mk := func(inv any) *unstructured.Unstructured {
		st := map[string]any{}
		if inv != nil {
			st["inventory"] = inv
		}
		return &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "h", "namespace": "ns"}, "status": st}}
	}
	cases := []struct {
		name      string
		inv       any
		has       bool
		wantCount int
	}{
		{"no inventory (Flux < 2.8)", nil, false, 0},
		{"inventory with entries", map[string]any{"entries": []any{map[string]any{"id": "a"}, map[string]any{"id": "b"}}}, true, 2},
		{"empty inventory object is still an inventory", map[string]any{}, true, 0},
		{"empty entries", map[string]any{"entries": []any{}}, true, 0},
		{"inventory is not an object", "none", false, 0},
		{"entries is not a list", map[string]any{"entries": "x"}, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseFluxHelmRelease(mk(tc.inv))
			if got.HasInventory != tc.has || got.InventoryCount != tc.wantCount {
				t.Errorf("has=%v count=%d, want %v/%d", got.HasInventory, got.InventoryCount, tc.has, tc.wantCount)
			}
		})
	}
}

func TestParseFluxHelmReleaseNeverReturnsNilCollections(t *testing.T) {
	got := parseFluxHelmRelease(&unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "bare", "namespace": "ns"},
	}})
	if got.Conditions == nil || got.DependsOn == nil || got.History == nil || got.Labels == nil || got.Annotations == nil {
		t.Fatalf("collections must be non-nil: %+v", got)
	}
	if got.State != FluxStateUnknown || got.SourceRef != nil || got.ChartRef != nil || got.ReleaseName != "bare" || got.StorageNamespace != "ns" {
		t.Errorf("bare object: %+v", got)
	}
	raw, _ := json.Marshal(got)
	for _, null := range []string{`"conditions":null`, `"dependsOn":null`, `"history":null`, `"labels":null`, `"annotations":null`} {
		if strings.Contains(string(raw), null) {
			t.Errorf("%s in %s", null, raw)
		}
	}
}

func TestParseFluxHelmReleaseToleratesGarbage(t *testing.T) {
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
		"nil object":           {},
		"spec is a string":     bad("oops", "oops"),
		"status is a list":     bad(map[string]any{}, []any{1, 2}),
		"chart is a string":    bad(map[string]any{"chart": "podinfo"}, nil),
		"chart.spec is a list": bad(map[string]any{"chart": map[string]any{"spec": []any{1}}}, nil),
		"chart fields wrong":   bad(map[string]any{"chart": map[string]any{"spec": map[string]any{"chart": 1, "version": []any{}, "sourceRef": "x"}}}, nil),
		"chartRef garbage":     bad(map[string]any{"chartRef": []any{"x"}}, nil),
		"flags wrong type":     bad(map[string]any{"suspend": "yes", "interval": 5, "releaseName": 7, "targetNamespace": []any{}, "storageNamespace": 1}, nil),
		"kubeConfig garbage":   bad(map[string]any{"kubeConfig": 5}, nil),
		"dependsOn garbage":    bad(map[string]any{"dependsOn": []any{nil, 1, "x", map[string]any{"name": 3}}}, nil),
		"history is a string":  bad(nil, map[string]any{"history": "none"}),
		"history garbage":      bad(nil, map[string]any{"history": []any{nil, 1, "x", map[string]any{"version": "7", "status": 3, "chartName": []any{}}}}),
		"history huge version": bad(nil, map[string]any{"history": []any{map[string]any{"version": float64(1e30)}}}),
		"inventory garbage":    bad(nil, map[string]any{"inventory": []any{"a"}}),
		"conditions garbage":   bad(nil, map[string]any{"conditions": []any{"x", 1, map[string]any{"type": 5}}}),
		"revision wrong types": bad(nil, map[string]any{"lastAppliedRevision": 7, "lastAttemptedRevision": []any{}}),
	}
	for name, u := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic: %v", r)
				}
			}()
			got := parseFluxHelmRelease(u)
			if got.Kind != FluxKindHelmRelease {
				t.Errorf("Kind = %q", got.Kind)
			}
			if got.Conditions == nil || got.DependsOn == nil || got.History == nil {
				t.Errorf("nil collections: %+v", got)
			}
			if _, err := json.Marshal(got); err != nil {
				t.Errorf("json: %v", err)
			}
		})
	}
}

func TestGetFluxHelmReleases(t *testing.T) {
	var objs []runtime.Object
	for _, f := range []string{"v2.8-ready-inventory.yaml", "v2.8-remote.yaml", "v2.3-chartref.yaml"} {
		objs = append(objs, readHelmReleaseFixture(t, f))
	}
	m := fluxManager(newHelmDyn(objs...))

	all, err := m.GetFluxHelmReleases(nil)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, h := range all {
		keys = append(keys, h.Namespace+"/"+h.Name)
	}
	if want := []string{"apps/podinfo", "fleet/edge", "team-a/podinfo"}; !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}

	one, err := m.GetFluxHelmReleases([]string{"fleet"})
	if err != nil || len(one) != 1 || !one[0].Remote {
		t.Fatalf("fleet: err=%v items=%+v", err, one)
	}
	none, err := m.GetFluxHelmReleases([]string{"empty"})
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("empty namespace must give an empty non-nil list: err=%v items=%#v", err, none)
	}
	if _, err := m.GetFluxHelmReleases([]string{"a/b"}); err == nil {
		t.Error("an invalid namespace in the scope must be rejected")
	}
}

// TestFluxHelmReleaseNoSecretLeak — по образцу TestArgoSettingsNoSecretLeak: запрещённые поля заполнены контрольными
// строками, ни одна не должна попасть в json.Marshal ответа. Новое поле DTO добавляется только через этот тест.
func TestFluxHelmReleaseNoSecretLeak(t *testing.T) {
	// 1) Все фикстуры: spec.values, valuesFrom, kubeConfig и last-applied-configuration несут маркеры.
	var objs []runtime.Object
	for i, f := range allHelmReleaseFixtures(t) {
		u := readHelmReleaseFixture(t, f)
		u.SetName(u.GetName() + "-" + string(rune('a'+i)))
		objs = append(objs, u)
	}
	// 2) Объект со всеми запрещёнными местами сразу.
	evil := fluxObj("helm.toolkit.fluxcd.io/v2", "HelmRelease", "team", "evil", map[string]any{
		"interval": "1m",
		"chart": map[string]any{"spec": map[string]any{
			"chart": "web", "version": "1.x",
			"sourceRef": map[string]any{"kind": "HelmRepository", "name": "repo"},
		}},
		"values": map[string]any{"password": "S3CRET-VALUES", "nested": map[string]any{"token": "S3CRET-VALUES-TOKEN"}},
		"valuesFrom": []any{
			map[string]any{"kind": "Secret", "name": "S3CRET-VALUESFROM", "valuesKey": "S3CRET-VALUESKEY", "targetPath": "S3CRET-TARGETPATH"},
		},
		"kubeConfig": map[string]any{"secretRef": map[string]any{"name": "S3CRET-KUBECONFIG", "key": "S3CRET-KUBECONFIG"}},
	})
	evil.SetAnnotations(map[string]string{
		fluxLastAppliedAnnotation: `{"spec":{"values":{"p":"S3CRET-LASTAPPLIED"}}}`,
		"keep":                    "me",
	})
	evil.Object["status"] = map[string]any{
		"conditions": []any{map[string]any{
			"type": "Ready", "status": "False", "reason": "InstallFailed", "lastTransitionTime": "2026-11-07T10:00:00Z",
			"message": "chart pull failed: https://git:S3CRET-URLPASS@example.com/c.tgz; Authorization: Bearer S3CRET-MSGTOKEN",
		}},
	}
	objs = append(objs, evil)

	m := fluxManager(newHelmDyn(objs...))
	got, err := m.GetFluxHelmReleases(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(objs) {
		t.Fatalf("got %d helm releases, want %d", len(got), len(objs))
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	all := string(raw)
	for _, marker := range []string{
		"S3CRET-VALUES", "S3CRET-VALUES-TOKEN", "S3CRET-VALUESFROM", "S3CRET-VALUESKEY", "S3CRET-TARGETPATH",
		"S3CRET-KUBECONFIG", "S3CRET-LASTAPPLIED", "S3CRET-URLPASS", "S3CRET-MSGTOKEN",
	} {
		if strings.Contains(all, marker) {
			t.Errorf("secret %q leaked into the response: %s", marker, all)
		}
	}
	if strings.Contains(all, "S3CRET") {
		t.Errorf("a control string leaked into the response: %s", all)
	}
	for _, h := range got {
		if h.Name != "evil" {
			continue
		}
		if _, ok := h.Annotations[fluxLastAppliedAnnotation]; ok {
			t.Error("last-applied-configuration must not be returned")
		}
		if h.Annotations["keep"] != "me" || !h.Remote || !strings.Contains(h.Message, "chart pull failed") {
			t.Errorf("allowed data was lost: %+v", h)
		}
	}
}
