// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
	"sigs.k8s.io/yaml"
)

const imageFixtures = "testdata/flux/image"

func imageReadFixture(t testing.TB, name string) *unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(imageFixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	obj := map[string]any{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return &unstructured.Unstructured{Object: obj}
}

func imageAllFixtures(t testing.TB) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(imageFixtures, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures in %s: %v", imageFixtures, err)
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	return names
}

// imageNewDyn — fake dynamic client, знающий три вида image automation в v1.
func imageNewDyn(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	lists := map[schema.GroupVersionResource]string{}
	for _, k := range fluxKindsOf(fluxCtrlImage) {
		lists[k.GVR("v1")] = k.Kind + "List"
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, objs...)
}

func imageObjs(t testing.TB, files ...string) []runtime.Object {
	t.Helper()
	var objs []runtime.Object
	for _, f := range files {
		objs = append(objs, imageReadFixture(t, f))
	}
	return objs
}

func imageFailList(dyn *dynamicfake.FakeDynamicClient, plural string, err error) {
	dyn.PrependReactor("list", plural, func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, err })
}

func TestParseFluxImageRepositoryFixtures(t *testing.T) {
	cases := []struct {
		file    string
		want    FluxImageRepositoryInfo
		message string
	}{
		{
			file: "v2.8-imagerepository.yaml",
			want: FluxImageRepositoryInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindImageRepository, Name: "podinfo", Namespace: "flux-system", Interval: "5m0s",
					State: FluxStateReady, LastReconcileAt: "2026-11-05T09:00:03Z"},
				Image: "ghcr.io/stefanprodan/podinfo", TagCount: 42, LastScanAt: "2026-11-05T09:00:02Z",
			},
			message: "found 42 tags",
		},
		{
			// userinfo в адресе образа отбрасывается, порт реестра остаётся.
			file: "v2.8-imagerepository-userinfo.yaml",
			want: FluxImageRepositoryInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindImageRepository, Name: "private", Namespace: "team-a", Interval: "10m0s",
					State: FluxStateReady, LastReconcileAt: "2026-11-05T09:00:03Z"},
				Image: "registry.example.com:5000/team/app", TagCount: 3, LastScanAt: "2026-11-05T09:10:00Z",
			},
		},
		{
			file: "v2.8-imagerepository-suspended.yaml",
			want: FluxImageRepositoryInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindImageRepository, Name: "paused", Namespace: "team-a", Interval: "1h0m0s", Suspended: true,
					State: FluxStateSuspended, LastReconcileAt: "2026-11-04T09:00:03Z"},
				Image: "docker.io/library/nginx", TagCount: 100, LastScanAt: "2026-11-04T09:00:02Z",
			},
		},
		{
			// Скана ещё не было: счётчик и время нулевые.
			file: "v2.8-imagerepository-failed.yaml",
			want: FluxImageRepositoryInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindImageRepository, Name: "broken", Namespace: "team-a", Interval: "5m0s",
					State: FluxStateFailed, LastReconcileAt: "2026-11-07T10:05:00Z"},
				Image: "registry.example.com/missing/app",
			},
			message: "scan failed",
		},
		{
			file: "v2.3-imagerepository-v1beta2.yaml",
			want: FluxImageRepositoryInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindImageRepository, Name: "podinfo", Namespace: "flux-system", Interval: "5m0s",
					State: FluxStateReady, LastReconcileAt: "2024-06-12T07:05:00Z"},
				Image: "ghcr.io/stefanprodan/podinfo", TagCount: 40, LastScanAt: "2024-06-12T07:05:00Z",
			},
		},
	}
	covered := map[string]bool{}
	for _, tc := range cases {
		covered[tc.file] = true
		t.Run(tc.file, func(t *testing.T) {
			got := parseFluxImageRepository(imageReadFixture(t, tc.file))
			w := tc.want
			if got.Kind != w.Kind || got.Name != w.Name || got.Namespace != w.Namespace || got.Interval != w.Interval ||
				got.Suspended != w.Suspended || got.State != w.State || got.LastReconcileAt != w.LastReconcileAt {
				t.Errorf("object = %+v, want %+v", got.FluxObjectInfo, w.FluxObjectInfo)
			}
			if got.Image != w.Image || got.TagCount != w.TagCount || got.LastScanAt != w.LastScanAt {
				t.Errorf("image/tagCount/scan = %q/%d/%q, want %q/%d/%q", got.Image, got.TagCount, got.LastScanAt, w.Image, w.TagCount, w.LastScanAt)
			}
			if tc.message != "" && !strings.Contains(got.Message, tc.message) {
				t.Errorf("message = %q, want to contain %q", got.Message, tc.message)
			}
			imageCheckCommon(t, got.FluxObjectInfo)
		})
	}
	imageCheckCovered(t, "imagerepository", covered)
}

func TestParseFluxImagePolicyFixtures(t *testing.T) {
	cases := []struct {
		file string
		want FluxImagePolicyInfo
	}{
		{
			file: "v2.8-imagepolicy-semver.yaml",
			want: FluxImagePolicyInfo{
				FluxObjectInfo:  FluxObjectInfo{Kind: FluxKindImagePolicy, Name: "podinfo", Namespace: "flux-system", State: FluxStateReady, LastReconcileAt: "2026-11-05T09:00:03Z"},
				ImageRepository: "podinfo", Policy: "semver: 6.x",
				LatestImage: "ghcr.io/stefanprodan/podinfo:6.5.4@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			},
		},
		{
			file: "v2.8-imagepolicy-alphabetical.yaml",
			want: FluxImagePolicyInfo{
				FluxObjectInfo:  FluxObjectInfo{Kind: FluxKindImagePolicy, Name: "alpha", Namespace: "team-a", State: FluxStateReady, LastReconcileAt: "2026-11-05T09:00:03Z"},
				ImageRepository: "private", Policy: "alphabetical: desc", LatestImage: "registry.example.com/team/app:20261105",
			},
		},
		{
			// userinfo в name отбрасывается; namespace репозитория в DTO нет, отдаётся только имя.
			file: "v2.8-imagepolicy-numerical.yaml",
			want: FluxImagePolicyInfo{
				FluxObjectInfo:  FluxObjectInfo{Kind: FluxKindImagePolicy, Name: "num", Namespace: "team-a", State: FluxStateReady, LastReconcileAt: "2026-11-05T09:00:03Z"},
				ImageRepository: "private", Policy: "numerical: asc", LatestImage: "registry.example.com/team/app:1234",
			},
		},
		{
			// Порядок не задан: по документации по умолчанию asc.
			file: "v2.8-imagepolicy-no-order.yaml",
			want: FluxImagePolicyInfo{
				FluxObjectInfo:  FluxObjectInfo{Kind: FluxKindImagePolicy, Name: "noorder", Namespace: "team-a", State: FluxStateReady, LastReconcileAt: "2026-11-05T09:00:03Z"},
				ImageRepository: "private", Policy: "numerical: asc",
			},
		},
		{
			file: "v2.8-imagepolicy-unresolved.yaml",
			want: FluxImagePolicyInfo{
				FluxObjectInfo:  FluxObjectInfo{Kind: FluxKindImagePolicy, Name: "pending", Namespace: "team-a", State: FluxStateFailed, LastReconcileAt: "2026-11-07T10:05:00Z"},
				ImageRepository: "private", Policy: "semver: >=99.0.0",
			},
		},
		{
			// Flux 2.3 (v1beta2): последний образ только в status.latestImage строкой.
			file: "v2.3-imagepolicy-v1beta2.yaml",
			want: FluxImagePolicyInfo{
				FluxObjectInfo:  FluxObjectInfo{Kind: FluxKindImagePolicy, Name: "podinfo", Namespace: "flux-system", State: FluxStateReady, LastReconcileAt: "2024-06-12T07:05:00Z"},
				ImageRepository: "podinfo", Policy: "semver: 6.x", LatestImage: "ghcr.io/stefanprodan/podinfo:6.5.3",
			},
		},
	}
	covered := map[string]bool{}
	for _, tc := range cases {
		covered[tc.file] = true
		t.Run(tc.file, func(t *testing.T) {
			got := parseFluxImagePolicy(imageReadFixture(t, tc.file))
			w := tc.want
			if got.Kind != w.Kind || got.Name != w.Name || got.Namespace != w.Namespace || got.State != w.State ||
				got.Suspended != w.Suspended || got.LastReconcileAt != w.LastReconcileAt {
				t.Errorf("object = %+v, want %+v", got.FluxObjectInfo, w.FluxObjectInfo)
			}
			if got.ImageRepository != w.ImageRepository || got.Policy != w.Policy || got.LatestImage != w.LatestImage {
				t.Errorf("repo/policy/latest = %q/%q/%q, want %q/%q/%q", got.ImageRepository, got.Policy, got.LatestImage, w.ImageRepository, w.Policy, w.LatestImage)
			}
			imageCheckCommon(t, got.FluxObjectInfo)
		})
	}
	imageCheckCovered(t, "imagepolicy", covered)
}

func TestParseFluxImageUpdateAutomationFixtures(t *testing.T) {
	cases := []struct {
		file    string
		want    FluxImageUpdateAutomationInfo
		message string
	}{
		{
			// Ветка push главнее ветки checkout.
			file: "v2.8-imageupdateautomation.yaml",
			want: FluxImageUpdateAutomationInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindImageUpdateAutomation, Name: "flux-system", Namespace: "flux-system", Interval: "30m0s",
					State: FluxStateReady, LastReconcileAt: "2026-11-05T09:00:03Z",
					SourceRef: &FluxRef{Kind: FluxKindGitRepository, Namespace: "flux-system", Name: "flux-system"}},
				Branch: "image-updates", LastRunAt: "2026-11-05T09:00:01Z", LastPushCommit: "6d1f6c3a8b0b9d9e0f6a1c3f0b2f1d8e4a5b6c7d",
			},
		},
		{
			// Нет push: ветка из checkout; namespace источника задан явно; пуша ещё не было.
			file: "v2.8-imageupdateautomation-checkout-only.yaml",
			want: FluxImageUpdateAutomationInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindImageUpdateAutomation, Name: "direct", Namespace: "team-a", Interval: "1h0m0s",
					State: FluxStateReady, LastReconcileAt: "2026-11-05T10:00:00Z",
					SourceRef: &FluxRef{Kind: FluxKindGitRepository, Namespace: "flux-system", Name: "apps"}},
				Branch: "dev", LastRunAt: "2026-11-05T10:00:00Z",
			},
		},
		{
			// Нет checkout: ветка из push; namespace источника по умолчанию — объекта.
			file: "v2.8-imageupdateautomation-suspended.yaml",
			want: FluxImageUpdateAutomationInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindImageUpdateAutomation, Name: "paused", Namespace: "team-a", Interval: "1h0m0s", Suspended: true,
					State: FluxStateSuspended, LastReconcileAt: "2026-11-04T09:00:03Z",
					SourceRef: &FluxRef{Kind: FluxKindGitRepository, Namespace: "team-a", Name: "apps"}},
				Branch: "bot", LastRunAt: "2026-11-04T09:00:00Z",
			},
		},
		{
			file: "v2.8-imageupdateautomation-failed.yaml",
			want: FluxImageUpdateAutomationInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindImageUpdateAutomation, Name: "broken", Namespace: "team-a", Interval: "1h0m0s",
					State: FluxStateFailed, LastReconcileAt: "2026-11-07T10:05:00Z",
					SourceRef: &FluxRef{Kind: FluxKindGitRepository, Namespace: "team-a", Name: "apps"}},
				Branch: "bot",
			},
			message: "failed to push",
		},
		{
			file: "v2.3-imageupdateautomation-v1beta2.yaml",
			want: FluxImageUpdateAutomationInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindImageUpdateAutomation, Name: "flux-system", Namespace: "flux-system", Interval: "30m0s",
					State: FluxStateReady, LastReconcileAt: "2024-06-12T07:05:00Z",
					SourceRef: &FluxRef{Kind: FluxKindGitRepository, Namespace: "flux-system", Name: "flux-system"}},
				Branch: "main", LastRunAt: "2024-06-12T07:05:00Z", LastPushCommit: "aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44",
			},
		},
	}
	covered := map[string]bool{}
	for _, tc := range cases {
		covered[tc.file] = true
		t.Run(tc.file, func(t *testing.T) {
			got := parseFluxImageUpdateAutomation(imageReadFixture(t, tc.file))
			w := tc.want
			if got.Kind != w.Kind || got.Name != w.Name || got.Namespace != w.Namespace || got.Interval != w.Interval ||
				got.Suspended != w.Suspended || got.State != w.State || got.LastReconcileAt != w.LastReconcileAt {
				t.Errorf("object = %+v, want %+v", got.FluxObjectInfo, w.FluxObjectInfo)
			}
			if (got.SourceRef == nil) != (w.SourceRef == nil) || (w.SourceRef != nil && *got.SourceRef != *w.SourceRef) {
				t.Errorf("sourceRef = %+v, want %+v", got.SourceRef, w.SourceRef)
			}
			if got.Branch != w.Branch || got.LastRunAt != w.LastRunAt || got.LastPushCommit != w.LastPushCommit {
				t.Errorf("branch/run/commit = %q/%q/%q, want %q/%q/%q", got.Branch, got.LastRunAt, got.LastPushCommit, w.Branch, w.LastRunAt, w.LastPushCommit)
			}
			if tc.message != "" && !strings.Contains(got.Message, tc.message) {
				t.Errorf("message = %q, want to contain %q", got.Message, tc.message)
			}
			imageCheckCommon(t, got.FluxObjectInfo)
		})
	}
	imageCheckCovered(t, "imageupdateautomation", covered)
}

// imageCheckCommon проверяет общие свойства разобранного объекта: возраст, аннотации без last-applied, списки не nil.
func imageCheckCommon(t *testing.T, o FluxObjectInfo) {
	t.Helper()
	if o.Age == "" || o.Created == "" {
		t.Errorf("age/created are empty: %q/%q", o.Age, o.Created)
	}
	if o.Annotations["team"] != "platform" || o.Labels["app.kubernetes.io/part-of"] != "platform" {
		t.Errorf("labels/annotations = %v / %v", o.Labels, o.Annotations)
	}
	if _, ok := o.Annotations[fluxLastAppliedAnnotation]; ok {
		t.Error("last-applied-configuration must not be returned")
	}
	if o.Conditions == nil || o.DependsOn == nil {
		t.Errorf("nil collections: %+v", o)
	}
}

// imageCheckCovered требует, чтобы каждая фикстура вида участвовала в таблице.
func imageCheckCovered(t *testing.T, infix string, covered map[string]bool) {
	t.Helper()
	for _, f := range imageAllFixtures(t) {
		// «imagerepository» входит и в имя других видов только как префикс «image...»: сравниваем полное слово.
		if strings.Contains(f, "-"+infix+"-") || strings.HasSuffix(f, "-"+infix+".yaml") {
			if !covered[f] {
				t.Errorf("fixture %s has no expectations in the table", f)
			}
		}
	}
}

func TestFluxImagePolicyRule(t *testing.T) {
	mk := func(policy any) *unstructured.Unstructured {
		spec := map[string]any{}
		if policy != nil {
			spec["policy"] = policy
		}
		return &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "p", "namespace": "ns"}, "spec": spec}}
	}
	cases := []struct {
		name   string
		policy any
		want   string
	}{
		{"semver", map[string]any{"semver": map[string]any{"range": ">=1.0.0"}}, "semver: >=1.0.0"},
		{"semver without range", map[string]any{"semver": map[string]any{}}, "semver"},
		{"alphabetical asc", map[string]any{"alphabetical": map[string]any{"order": "asc"}}, "alphabetical: asc"},
		{"alphabetical default order", map[string]any{"alphabetical": map[string]any{}}, "alphabetical: asc"},
		{"numerical desc", map[string]any{"numerical": map[string]any{"order": "desc"}}, "numerical: desc"},
		{"semver wins over numerical", map[string]any{"semver": map[string]any{"range": "1.x"}, "numerical": map[string]any{"order": "desc"}}, "semver: 1.x"},
		{"alphabetical wins over numerical", map[string]any{"alphabetical": map[string]any{"order": "desc"}, "numerical": map[string]any{"order": "asc"}}, "alphabetical: desc"},
		{"unknown rule", map[string]any{"custom": map[string]any{"x": "y"}}, ""},
		{"no policy", nil, ""},
		{"policy is a string", "semver", ""},
		{"rule is a string", map[string]any{"semver": "6.x"}, "semver"},
	}
	for _, tc := range cases {
		if got := parseFluxImagePolicy(mk(tc.policy)).Policy; got != tc.want {
			t.Errorf("%s: policy = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestFluxImageRef(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"plain", "ghcr.io/org/app", "ghcr.io/org/app"},
		{"with port", "registry.example.com:5000/team/app", "registry.example.com:5000/team/app"},
		{"user and password", "user:S3CRET-P@registry.example.com/team/app", "registry.example.com/team/app"},
		{"token as user", "S3CRET-T@registry.example.com/team/app", "registry.example.com/team/app"},
		{"with scheme", "https://user:S3CRET-P@registry.example.com/team/app", "registry.example.com/team/app"},
		{"with scheme and no credentials", "https://registry.example.com/team/app", "registry.example.com/team/app"},
		{"digest reference is not userinfo", "nginx@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "nginx@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		{"query is dropped", "https://registry.example.com/app?token=S3CRET-Q", "registry.example.com/app"},
		{"unparsable with scheme and credentials", "http://user:S3CRET-P@[::1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fluxImageRef(tc.in)
			if got != tc.want {
				t.Errorf("fluxImageRef(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.Contains(got, "S3CRET") {
				t.Errorf("secret leaked: %q", got)
			}
		})
	}
}

func TestFluxImageLatest(t *testing.T) {
	mk := func(status map[string]any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "p", "namespace": "ns"}, "status": status}}
	}
	cases := []struct {
		name   string
		status map[string]any
		want   string
	}{
		{"name and tag", map[string]any{"latestRef": map[string]any{"name": "a/b", "tag": "1"}}, "a/b:1"},
		{"with digest", map[string]any{"latestRef": map[string]any{"name": "a/b", "tag": "1", "digest": "sha256:abc"}}, "a/b:1@sha256:abc"},
		{"digest only", map[string]any{"latestRef": map[string]any{"name": "a/b", "digest": "sha256:abc"}}, "a/b@sha256:abc"},
		{"name only", map[string]any{"latestRef": map[string]any{"name": "a/b"}}, "a/b"},
		{"latestRef without name", map[string]any{"latestRef": map[string]any{"tag": "1"}}, ""},
		{"legacy latestImage", map[string]any{"latestImage": "a/b:2"}, "a/b:2"},
		{"latestRef wins over latestImage", map[string]any{"latestRef": map[string]any{"name": "a/b", "tag": "1"}, "latestImage": "a/b:2"}, "a/b:1"},
		{"legacy latestImage with userinfo", map[string]any{"latestImage": "u:S3CRET-P@a/b:2"}, "a/b:2"},
		{"nothing", map[string]any{}, ""},
	}
	for _, tc := range cases {
		if got := parseFluxImagePolicy(mk(tc.status)).LatestImage; got != tc.want {
			t.Errorf("%s: latest = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestFluxImageTagCountTypes(t *testing.T) {
	// После JSON-разбора число — float64, после YAML/typed — int64: оба вида читаются, остальное — ноль.
	for name, v := range map[string]any{"int64": int64(7), "float64": float64(7), "int": 7} {
		u := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{"name": "r", "namespace": "ns"},
			"status":   map[string]any{"lastScanResult": map[string]any{"tagCount": v}},
		}}
		if got := parseFluxImageRepository(u).TagCount; got != 7 {
			t.Errorf("%s: tagCount = %d, want 7", name, got)
		}
	}
	for name, v := range map[string]any{"string": "7", "list": []any{}, "huge": float64(1e30), "negative": int64(-4), "nan-like": nil} {
		u := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{"name": "r", "namespace": "ns"},
			"status":   map[string]any{"lastScanResult": map[string]any{"tagCount": v}},
		}}
		if got := parseFluxImageRepository(u).TagCount; got != 0 {
			t.Errorf("%s: tagCount = %d, want 0", name, got)
		}
	}
}

func TestParseFluxImageToleratesGarbage(t *testing.T) {
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
	objs := map[string]*unstructured.Unstructured{
		"nil object":            {},
		"spec is a string":      bad("oops", "oops"),
		"status is a list":      bad(map[string]any{}, []any{1}),
		"image is a number":     bad(map[string]any{"image": 5}, nil),
		"scan result garbage":   bad(nil, map[string]any{"lastScanResult": "x"}),
		"scan fields wrong":     bad(nil, map[string]any{"lastScanResult": map[string]any{"tagCount": []any{}, "scanTime": 3}}),
		"ref garbage":           bad(map[string]any{"imageRepositoryRef": "x", "policy": []any{1}}, nil),
		"latestRef garbage":     bad(nil, map[string]any{"latestRef": "x", "latestImage": 4}),
		"latestRef fields":      bad(nil, map[string]any{"latestRef": map[string]any{"name": 1, "tag": []any{}, "digest": map[string]any{}}}),
		"sourceRef garbage":     bad(map[string]any{"sourceRef": "x"}, nil),
		"git garbage":           bad(map[string]any{"git": []any{"x"}}, nil),
		"push/checkout garbage": bad(map[string]any{"git": map[string]any{"push": "x", "checkout": map[string]any{"ref": "main"}}}, nil),
		"run fields wrong":      bad(nil, map[string]any{"lastAutomationRunTime": 5, "lastPushCommit": []any{}}),
		"conditions garbage":    bad(nil, map[string]any{"conditions": []any{"x", 1, map[string]any{"type": 5}}}),
	}
	parsers := map[string]func(*unstructured.Unstructured) FluxObjectInfo{
		"ImageRepository": func(u *unstructured.Unstructured) FluxObjectInfo { return parseFluxImageRepository(u).FluxObjectInfo },
		"ImagePolicy":     func(u *unstructured.Unstructured) FluxObjectInfo { return parseFluxImagePolicy(u).FluxObjectInfo },
		"ImageUpdateAutomation": func(u *unstructured.Unstructured) FluxObjectInfo {
			return parseFluxImageUpdateAutomation(u).FluxObjectInfo
		},
	}
	for kind, parse := range parsers {
		for name, u := range objs {
			t.Run(kind+"/"+name, func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic: %v", r)
					}
				}()
				got := parse(u)
				if got.Conditions == nil || got.DependsOn == nil || got.Labels == nil || got.Annotations == nil {
					t.Errorf("nil collections: %+v", got)
				}
				if _, err := json.Marshal(got); err != nil {
					t.Errorf("json: %v", err)
				}
			})
		}
	}
}

func TestFluxImageKindsToRead(t *testing.T) {
	names := func(v map[string]string) []string {
		var out []string
		for _, k := range fluxImageKindsToRead(v) {
			out = append(out, k.Kind+"/"+v[k.Kind])
		}
		return out
	}
	if got, want := names(map[string]string{FluxKindImageRepository: "v1", FluxKindImagePolicy: "v1", FluxKindImageUpdateAutomation: "v1"}),
		[]string{"ImageRepository/v1", "ImagePolicy/v1", "ImageUpdateAutomation/v1"}; !slices.Equal(got, want) {
		t.Errorf("v1: %v, want %v", got, want)
	}
	// Flux 2.3: все три вида в v1beta2 — версия из allowlist вида, а не «v1» по умолчанию.
	if got, want := names(map[string]string{FluxKindImageRepository: "v1beta2", FluxKindImagePolicy: "v1beta2", FluxKindImageUpdateAutomation: "v1beta2"}),
		[]string{"ImageRepository/v1beta2", "ImagePolicy/v1beta2", "ImageUpdateAutomation/v1beta2"}; !slices.Equal(got, want) {
		t.Errorf("v1beta2: %v, want %v", got, want)
	}
	// Вид без CRD, с неподдерживаемой версией или чужой пропускается.
	if got, want := names(map[string]string{FluxKindImageRepository: "v1", FluxKindImagePolicy: "v1alpha1", FluxKindKustomization: "v1"}),
		[]string{"ImageRepository/v1"}; !slices.Equal(got, want) {
		t.Errorf("partial: %v, want %v", got, want)
	}
	if got := names(nil); len(got) != 0 {
		t.Errorf("no versions: %v, want none", got)
	}
}

var imageV1Fixtures = []string{
	"v2.8-imagerepository.yaml", "v2.8-imagerepository-userinfo.yaml", "v2.8-imagerepository-failed.yaml",
	"v2.8-imagepolicy-semver.yaml", "v2.8-imagepolicy-alphabetical.yaml", "v2.8-imagepolicy-numerical.yaml",
	"v2.8-imageupdateautomation.yaml", "v2.8-imageupdateautomation-checkout-only.yaml",
}

func TestGetFluxImageAutomation(t *testing.T) {
	m := fluxManager(imageNewDyn(imageObjs(t, imageV1Fixtures...)...))

	got, err := m.GetFluxImageAutomation(nil)
	if err != nil {
		t.Fatal(err)
	}
	var repos, policies, autos []string
	for _, r := range got.Repositories {
		repos = append(repos, r.Namespace+"/"+r.Name)
	}
	for _, p := range got.Policies {
		policies = append(policies, p.Namespace+"/"+p.Name)
	}
	for _, a := range got.Automations {
		autos = append(autos, a.Namespace+"/"+a.Name)
	}
	// Порядок: namespace, затем имя.
	if want := []string{"flux-system/podinfo", "team-a/broken", "team-a/private"}; !slices.Equal(repos, want) {
		t.Errorf("repositories = %v, want %v", repos, want)
	}
	if want := []string{"flux-system/podinfo", "team-a/alpha", "team-a/num"}; !slices.Equal(policies, want) {
		t.Errorf("policies = %v, want %v", policies, want)
	}
	if want := []string{"flux-system/flux-system", "team-a/direct"}; !slices.Equal(autos, want) {
		t.Errorf("automations = %v, want %v", autos, want)
	}

	scoped, err := m.GetFluxImageAutomation([]string{"team-a"})
	if err != nil || len(scoped.Repositories) != 2 || len(scoped.Policies) != 2 || len(scoped.Automations) != 1 {
		t.Fatalf("team-a: err=%v %+v", err, scoped)
	}
	empty, err := m.GetFluxImageAutomation([]string{"nothing"})
	if err != nil || empty == nil || empty.Repositories == nil || empty.Policies == nil || empty.Automations == nil {
		t.Fatalf("empty scope must give non-nil empty lists: err=%v %#v", err, empty)
	}
	if _, err := m.GetFluxImageAutomation([]string{"a/b"}); err == nil || strings.Contains(err.Error(), "\n") {
		t.Errorf("an invalid scope must be one error, got %v", err)
	}
}

func TestGetFluxImageAutomationPartialFailure(t *testing.T) {
	boom := errors.New("the server is currently unable to handle the request")
	gr := func(res string) schema.GroupResource {
		return schema.GroupResource{Group: fluxGroupImage, Resource: res}
	}
	plurals := []string{"imagerepositories", "imagepolicies", "imageupdateautomations"}

	t.Run("one kind failed: the others are returned without an error", func(t *testing.T) {
		dyn := imageNewDyn(imageObjs(t, imageV1Fixtures...)...)
		imageFailList(dyn, "imagepolicies", boom)
		got, err := fluxManager(dyn).GetFluxImageAutomation(nil)
		if err != nil {
			t.Fatalf("a partial failure must not fail the call: %v", err)
		}
		if len(got.Repositories) != 3 || len(got.Automations) != 2 || len(got.Policies) != 0 || got.Policies == nil {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("all kinds failed: an error naming them", func(t *testing.T) {
		dyn := imageNewDyn(imageObjs(t, imageV1Fixtures...)...)
		for _, p := range plurals {
			imageFailList(dyn, p, boom)
		}
		got, err := fluxManager(dyn).GetFluxImageAutomation(nil)
		if err == nil || got != nil || !errors.Is(err, boom) {
			t.Fatalf("err=%v got=%v", err, got)
		}
		for _, k := range []string{FluxKindImageRepository, FluxKindImagePolicy, FluxKindImageUpdateAutomation} {
			if !strings.Contains(err.Error(), k) {
				t.Errorf("error does not name %s: %v", k, err)
			}
		}
	})
	t.Run("no permission or no CRD is skipped without an error", func(t *testing.T) {
		dyn := imageNewDyn(imageObjs(t, imageV1Fixtures...)...)
		imageFailList(dyn, "imageupdateautomations", apierrors.NewForbidden(gr("imageupdateautomations"), "", errors.New("denied")))
		imageFailList(dyn, "imagepolicies", apierrors.NewNotFound(gr("imagepolicies"), ""))
		got, err := fluxManager(dyn).GetFluxImageAutomation(nil)
		if err != nil || len(got.Repositories) != 3 || len(got.Policies) != 0 || len(got.Automations) != 0 {
			t.Fatalf("err=%v got=%+v", err, got)
		}
	})
	t.Run("everything forbidden: empty, no error", func(t *testing.T) {
		dyn := imageNewDyn()
		for _, p := range plurals {
			imageFailList(dyn, p, apierrors.NewForbidden(gr(p), "", errors.New("denied")))
		}
		got, err := fluxManager(dyn).GetFluxImageAutomation(nil)
		if err != nil || got == nil || got.Repositories == nil {
			t.Fatalf("err=%v got=%#v", err, got)
		}
	})
	t.Run("a real failure is not hidden by skipped kinds", func(t *testing.T) {
		dyn := imageNewDyn()
		imageFailList(dyn, "imagerepositories", boom)
		imageFailList(dyn, "imagepolicies", apierrors.NewForbidden(gr("imagepolicies"), "", errors.New("denied")))
		imageFailList(dyn, "imageupdateautomations", apierrors.NewForbidden(gr("imageupdateautomations"), "", errors.New("denied")))
		if _, err := fluxManager(dyn).GetFluxImageAutomation(nil); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the real failure", err)
		}
	})
}

// TestGetFluxImageAutomationOnlyReadsClusterObjects: чтение использует только list/watch объектов кластера,
// не обращаясь к реестрам образов и не выполняя никаких операций записи.
func TestGetFluxImageAutomationOnlyReadsClusterObjects(t *testing.T) {
	dyn := imageNewDyn(imageObjs(t, imageV1Fixtures...)...)
	if _, err := fluxManager(dyn).GetFluxImageAutomation(nil); err != nil {
		t.Fatal(err)
	}

	lists := map[string]int{}
	watches := map[string]int{}
	for _, a := range dyn.Actions() {
		if a.GetResource().Group != fluxGroupImage {
			t.Errorf("unexpected resource: %s %s", a.GetVerb(), a.GetResource())
			continue
		}
		switch a.GetVerb() {
		case "list":
			lists[a.GetResource().Resource]++
		case "watch":
			watches[a.GetResource().Resource]++
		default:
			t.Errorf("unexpected API call: %s %s", a.GetVerb(), a.GetResource())
		}
	}

	plurals := []string{"imagerepositories", "imagepolicies", "imageupdateautomations"}
	for _, plural := range plurals {
		if lists[plural] == 0 {
			t.Errorf("%s: no LIST request", plural)
		}
	}
	// WATCH появляется только для видов, informer которых удалось синхронизировать.
	// Частичная ошибка LIST допустима (GetFluxImageAutomation возвращает доступные виды),
	// поэтому отсутствие WATCH у одного вида не означает лишнего API-вызова.
	if len(lists) != len(plurals) {
		t.Errorf("LIST resources = %v; want exactly the three image automation kinds", lists)
	}
	for plural := range watches {
		if !slices.Contains(plurals, plural) {
			t.Errorf("unexpected WATCH resource %q", plural)
		}
	}
}

// TestFluxImageNoSecretLeak — по образцу TestArgoSettingsNoSecretLeak: запрещённые поля заполнены контрольными
// строками, ни одна не должна попасть в json.Marshal ответа. Новое поле DTO добавляется только через этот тест.
func TestFluxImageNoSecretLeak(t *testing.T) {
	// 1) Все фикстуры v1 через GetFluxImageAutomation.
	objs := imageObjs(t, imageV1Fixtures...)
	// 2) Объекты со всеми запрещёнными местами: userinfo в image и latestRef.name, токены в сообщениях, почта
	// автора, шаблоны, ключ подписи, refspec.
	repo := fluxObj("image.toolkit.fluxcd.io/v1", "ImageRepository", "team", "evil-repo", map[string]any{
		"image": "user:S3CRET-IMGPASS@registry.example.com/app", "interval": "1m",
		"secretRef": map[string]any{"name": "S3CRET-REGSECRET"}, "certSecretRef": map[string]any{"name": "S3CRET-CERTSECRET"},
	})
	repo.Object["status"] = map[string]any{"conditions": []any{map[string]any{
		"type": "Ready", "status": "False", "reason": "Failed", "lastTransitionTime": "2026-11-07T10:00:00Z",
		"message": "scan failed: https://u:S3CRET-MSGPASS@registry.example.com/v2/; Authorization: Bearer S3CRET-MSGTOKEN",
	}}}
	repo.SetAnnotations(map[string]string{fluxLastAppliedAnnotation: `{"x":"S3CRET-LASTAPPLIED"}`, "keep": "me"})
	pol := fluxObj("image.toolkit.fluxcd.io/v1", "ImagePolicy", "team", "evil-policy", map[string]any{
		"imageRepositoryRef": map[string]any{"name": "evil-repo"},
		"policy":             map[string]any{"semver": map[string]any{"range": "1.x"}},
		"filterTags":         map[string]any{"pattern": "S3CRET-FILTER", "extract": "S3CRET-FILTER"},
	})
	pol.Object["status"] = map[string]any{"latestRef": map[string]any{"name": "u:S3CRET-REFPASS@registry.example.com/app", "tag": "1"}}
	iua := fluxObj("image.toolkit.fluxcd.io/v1", "ImageUpdateAutomation", "team", "evil-auto", map[string]any{
		"interval":  "1m",
		"sourceRef": map[string]any{"kind": "GitRepository", "name": "src"},
		"git": map[string]any{
			"push": map[string]any{"branch": "bot", "refspec": "refs/heads/bot:refs/heads/S3CRET-REFSPEC"},
			"commit": map[string]any{
				"author":          map[string]any{"email": "S3CRET-EMAIL@example.com", "name": "S3CRET-AUTHOR"},
				"messageTemplate": "S3CRET-TEMPLATE",
				"signingKey":      map[string]any{"secretRef": map[string]any{"name": "S3CRET-SIGNKEY"}},
			},
		},
	})
	objs = append(objs, repo, pol, iua)

	got, err := fluxManager(imageNewDyn(objs...)).GetFluxImageAutomation(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Repositories) != 4 || len(got.Policies) != 4 || len(got.Automations) != 3 {
		t.Fatalf("got %d repositories, %d policies, %d automations", len(got.Repositories), len(got.Policies), len(got.Automations))
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	all := string(raw)
	for _, marker := range []string{
		"S3CRET-IMGPASS", "S3CRET-REGSECRET", "S3CRET-CERTSECRET", "S3CRET-SA", "S3CRET-MSGPASS", "S3CRET-MSGTOKEN", "S3CRET-LASTAPPLIED",
		"S3CRET-FILTER", "S3CRET-REFPASS", "S3CRET-EMAIL", "S3CRET-AUTHOR", "S3CRET-SIGNKEY", "S3CRET-TEMPLATE", "S3CRET-REFSPEC",
	} {
		if strings.Contains(all, marker) {
			t.Errorf("secret %q leaked into the response: %s", marker, all)
		}
	}
	if strings.Contains(all, "S3CRET") {
		t.Errorf("a control string leaked into the response: %s", all)
	}

	// То же для v1beta2-фикстур, которые через v1-список не читаются: разбираются напрямую.
	for _, f := range imageAllFixtures(t) {
		u := imageReadFixture(t, f)
		var v any
		switch u.GetKind() {
		case FluxKindImageRepository:
			v = parseFluxImageRepository(u)
		case FluxKindImagePolicy:
			v = parseFluxImagePolicy(u)
		case FluxKindImageUpdateAutomation:
			v = parseFluxImageUpdateAutomation(u)
		}
		b, _ := json.Marshal(v)
		if strings.Contains(string(b), "S3CRET") && !strings.Contains(f, "failed") {
			t.Errorf("%s: a control string leaked: %s", f, b)
		}
		if strings.Contains(f, "failed") && (strings.Contains(string(b), "S3CRET-MSGPASS") || strings.Contains(string(b), "S3CRET-MSGPASS2")) {
			t.Errorf("%s: a message password leaked: %s", f, b)
		}
	}

	// Разрешённое не потерялось.
	for _, r := range got.Repositories {
		if r.Name == "evil-repo" {
			if r.Image != "registry.example.com/app" || r.Annotations["keep"] != "me" || !strings.Contains(r.Message, "scan failed") {
				t.Errorf("allowed data was lost: %+v", r)
			}
		}
	}
}
