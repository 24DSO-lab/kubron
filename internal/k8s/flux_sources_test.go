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

const sourceFixtures = "testdata/flux/source"

func readSourceFixture(t testing.TB, name string) *unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(sourceFixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	obj := map[string]any{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return &unstructured.Unstructured{Object: obj}
}

func allSourceFixtures(t testing.TB) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(sourceFixtures, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures in %s: %v", sourceFixtures, err)
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	return names
}

// newSourcesDyn — fake dynamic client, который знает списки всех пяти видов источников в версии v1
// (версию по умолчанию отдаёт fluxServedVersions для подключения без discovery).
func newSourcesDyn(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	lists := map[schema.GroupVersionResource]string{}
	for _, k := range fluxKindsOf(fluxCtrlSource) {
		lists[k.GVR("v1")] = k.Kind + "List"
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, objs...)
}

const (
	srcRev       = "main@sha1:6d1f6c3a8b0b9d9e0f6a1c3f0b2f1d8e4a5b6c7d"
	srcReadyTime = "2026-11-05T09:00:03Z"
)

func TestParseFluxSourceFixtures(t *testing.T) {
	cases := []struct {
		file string
		want FluxSourceInfo // сравниваются перечисленные поля (Age и Created зависят от времени запуска)
		// digest — ожидается ли непустой дайджест артефакта.
		digest  bool
		message string
	}{
		{
			file: "gitrepository-branch.yaml",
			want: FluxSourceInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindGitRepository, Name: "flux-system", Namespace: "flux-system", Interval: "5m0s",
					State: FluxStateReady, Revision: srcRev, LastReconcileAt: srcReadyTime},
				// userinfo («git@») из адреса убирается.
				URL: "ssh://github.com/acme/fleet", RefType: "branch", RefValue: "main", SecretRef: "flux-system",
			},
			digest: true, message: "stored artifact",
		},
		{
			file: "gitrepository-tag.yaml",
			want: FluxSourceInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindGitRepository, Name: "apps", Namespace: "team-a", Interval: "5m0s",
					State: FluxStateReady, Revision: "v1.4.2@sha1:aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44", LastReconcileAt: srcReadyTime},
				URL: "https://github.com/acme/apps.git", RefType: "tag", RefValue: "v1.4.2",
			},
			digest: true,
		},
		{
			file: "gitrepository-semver.yaml",
			want: FluxSourceInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindGitRepository, Name: "releases", Namespace: "team-a", Interval: "5m0s",
					State: FluxStateReady, Revision: "v1.9.0@sha1:bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55", LastReconcileAt: srcReadyTime},
				URL: "https://github.com/acme/releases.git", RefType: "semver", RefValue: ">=1.0.0 <2.0.0",
			},
			digest: true,
		},
		{
			// commit главнее branch (порядок разрешения в source-controller: commit, name, semver, tag, branch).
			file: "gitrepository-commit.yaml",
			want: FluxSourceInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindGitRepository, Name: "pinned", Namespace: "team-a", Interval: "5m0s",
					State: FluxStateReady, Revision: srcRev, LastReconcileAt: srcReadyTime},
				URL: "https://github.com/acme/pinned.git", RefType: "commit", RefValue: "6d1f6c3a8b0b9d9e0f6a1c3f0b2f1d8e4a5b6c7d",
			},
			digest: true,
		},
		{
			file: "gitrepository-failed.yaml",
			want: FluxSourceInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindGitRepository, Name: "broken", Namespace: "team-a", Interval: "5m0s",
					State: FluxStateFailed, LastReconcileAt: "2026-11-07T10:05:00Z"},
				URL: "https://github.com/acme/missing.git", RefType: "branch", RefValue: "main", SecretRef: "git-credentials",
			},
			digest: false, message: "authentication required",
		},
		{
			file: "gitrepository-suspended.yaml",
			want: FluxSourceInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindGitRepository, Name: "paused", Namespace: "team-a", Interval: "5m0s", Suspended: true,
					State: FluxStateSuspended, Revision: "dev@sha1:cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66", LastReconcileAt: "2026-11-04T09:00:03Z"},
				URL: "https://github.com/acme/paused.git", RefType: "branch", RefValue: "dev",
			},
			digest: true,
		},
		{
			file: "ocirepository-v1-tag.yaml",
			want: FluxSourceInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindOCIRepository, Name: "podinfo", Namespace: "flux-system", Interval: "5m0s",
					State: FluxStateReady, Revision: "6.5.4@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", LastReconcileAt: srcReadyTime},
				URL: "oci://ghcr.io/stefanprodan/manifests/podinfo", RefType: "tag", RefValue: "6.5.4", SecretRef: "ghcr-auth",
			},
			digest: true,
		},
		{
			// digest главнее tag.
			file: "ocirepository-v1-digest.yaml",
			want: FluxSourceInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindOCIRepository, Name: "pinned", Namespace: "flux-system", Interval: "5m0s",
					State: FluxStateReady, Revision: "latest@sha256:89abcdef0123456789abcdef0123456789abcdef0123456789abcdef01234567", LastReconcileAt: srcReadyTime},
				URL: "oci://ghcr.io/acme/manifests/app", RefType: "digest", RefValue: "sha256:89abcdef0123456789abcdef0123456789abcdef0123456789abcdef01234567",
			},
			digest: true,
		},
		{
			// Flux 2.3: OCIRepository в v1beta2, поля те же.
			file: "ocirepository-v1beta2-semver.yaml",
			want: FluxSourceInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindOCIRepository, Name: "podinfo", Namespace: "flux-system", Interval: "5m0s",
					State: FluxStateReady, Revision: "6.5.4@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", LastReconcileAt: "2024-06-12T07:05:00Z"},
				URL: "oci://ghcr.io/stefanprodan/manifests/podinfo", RefType: "semver", RefValue: ">=6.0.0",
			},
			digest: true,
		},
		{
			file: "helmrepository-default.yaml",
			want: FluxSourceInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindHelmRepository, Name: "bitnami", Namespace: "flux-system", Interval: "5m0s",
					State: FluxStateReady, Revision: "sha256:aaaa1111bbbb2222cccc3333dddd4444eeee5555ffff6666aaaa1111bbbb2222", LastReconcileAt: srcReadyTime},
				URL: "https://charts.bitnami.com/bitnami", SecretRef: "bitnami-auth",
			},
			digest: true,
		},
		{
			// OCI-репозиторий Helm: артефакта нет (чарты берутся напрямую из реестра), тип виден по схеме oci://.
			file: "helmrepository-oci.yaml",
			want: FluxSourceInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindHelmRepository, Name: "ghcr", Namespace: "flux-system", Interval: "5m0s",
					State: FluxStateReady, LastReconcileAt: srcReadyTime},
				URL: "oci://ghcr.io/acme/charts", SecretRef: "ghcr-auth",
			},
			digest: false,
		},
		{
			file: "helmchart.yaml",
			want: FluxSourceInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindHelmChart, Name: "apps-podinfo", Namespace: "flux-system", Interval: "5m0s",
					State: FluxStateReady, Revision: "6.5.4", LastReconcileAt: srcReadyTime,
					SourceRef: &FluxRef{Kind: FluxKindHelmRepository, Namespace: "flux-system", Name: "podinfo"}},
				Chart: "podinfo", ChartVersion: "6.x",
			},
			digest: true,
		},
		{
			file: "bucket-v1.yaml",
			want: FluxSourceInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindBucket, Name: "assets", Namespace: "flux-system", Interval: "5m0s",
					State: FluxStateReady, Revision: "sha256:bbbb1111cccc2222dddd3333eeee4444ffff5555aaaa6666bbbb1111cccc2222", LastReconcileAt: srcReadyTime},
				URL: "s3.amazonaws.com", BucketName: "acme-manifests", SecretRef: "s3-auth",
			},
			digest: true,
		},
		{
			// Flux 2.3: Bucket в v1beta2; endpoint с портом остаётся как есть.
			file: "bucket-v1beta2.yaml",
			want: FluxSourceInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindBucket, Name: "assets", Namespace: "flux-system", Interval: "5m0s",
					State: FluxStateReady, Revision: "sha256:cccc1111dddd2222eeee3333ffff4444aaaa5555bbbb6666cccc1111dddd2222", LastReconcileAt: "2024-06-12T07:05:00Z"},
				URL: "minio.example.com:9000", BucketName: "acme-manifests", SecretRef: "minio-auth",
			},
			digest: true,
		},
	}

	covered := map[string]bool{}
	for _, tc := range cases {
		covered[tc.file] = true
		t.Run(tc.file, func(t *testing.T) {
			w := tc.want
			got := parseFluxSource(w.Kind, readSourceFixture(t, tc.file))

			if got.Kind != w.Kind || got.Name != w.Name || got.Namespace != w.Namespace {
				t.Errorf("identity = %s %s/%s, want %s %s/%s", got.Kind, got.Namespace, got.Name, w.Kind, w.Namespace, w.Name)
			}
			if got.Interval != w.Interval || got.Suspended != w.Suspended || got.State != w.State {
				t.Errorf("interval/suspended/state = %q/%v/%q, want %q/%v/%q", got.Interval, got.Suspended, got.State, w.Interval, w.Suspended, w.State)
			}
			if got.Revision != w.Revision || got.LastReconcileAt != w.LastReconcileAt {
				t.Errorf("revision/time = %q/%q, want %q/%q", got.Revision, got.LastReconcileAt, w.Revision, w.LastReconcileAt)
			}
			if got.URL != w.URL || got.RefType != w.RefType || got.RefValue != w.RefValue {
				t.Errorf("url/ref = %q %q=%q, want %q %q=%q", got.URL, got.RefType, got.RefValue, w.URL, w.RefType, w.RefValue)
			}
			if got.SecretRef != w.SecretRef || got.Chart != w.Chart || got.ChartVersion != w.ChartVersion || got.BucketName != w.BucketName {
				t.Errorf("secretRef/chart/bucket = %q/%q@%q/%q, want %q/%q@%q/%q",
					got.SecretRef, got.Chart, got.ChartVersion, got.BucketName, w.SecretRef, w.Chart, w.ChartVersion, w.BucketName)
			}
			if (got.SourceRef == nil) != (w.SourceRef == nil) || (w.SourceRef != nil && *got.SourceRef != *w.SourceRef) {
				t.Errorf("sourceRef = %+v, want %+v", got.SourceRef, w.SourceRef)
			}
			if (got.Digest != "") != tc.digest {
				t.Errorf("digest = %q, want non-empty: %v", got.Digest, tc.digest)
			}
			if tc.digest && !strings.HasPrefix(got.Digest, "sha256:") {
				t.Errorf("digest = %q", got.Digest)
			}
			if tc.message != "" && !strings.Contains(got.Message, tc.message) {
				t.Errorf("message = %q, want to contain %q", got.Message, tc.message)
			}
			if got.Age == "" || got.Created == "" {
				t.Errorf("age/created are empty: %q/%q", got.Age, got.Created)
			}
			if got.Annotations["team"] != "platform" || got.Labels["app.kubernetes.io/part-of"] != "platform" {
				t.Errorf("labels/annotations = %v / %v", got.Labels, got.Annotations)
			}
			if got.DependsOn == nil || got.Conditions == nil {
				t.Errorf("collections must be non-nil: %+v", got)
			}
		})
	}
	for _, f := range allSourceFixtures(t) {
		if !covered[f] {
			t.Errorf("fixture %s has no expectations in the table", f)
		}
	}
}

func TestFluxSourceURL(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"user and password", "https://user:S3CRET-URLPASS@git.example.com/o/r.git", "https://git.example.com/o/r.git"},
		{"token only", "https://S3CRET-TOKEN@git.example.com/o/r.git", "https://git.example.com/o/r.git"},
		{"ssh user", "ssh://git@github.com/acme/fleet", "ssh://github.com/acme/fleet"},
		{"oci with password", "oci://user:S3CRET-URLPASS@ghcr.io/org/charts", "oci://ghcr.io/org/charts"},
		{"query and fragment (SAS tokens)", "https://acct.blob.core.windows.net/charts?sv=1&sig=S3CRET-QUERY#S3CRET-FRAG", "https://acct.blob.core.windows.net/charts"},
		{"plain https", "https://charts.example.com/stable", "https://charts.example.com/stable"},
		{"bucket endpoint without scheme", "s3.amazonaws.com", "s3.amazonaws.com"},
		{"bucket endpoint with port", "minio.example.com:9000", "minio.example.com:9000"},
		{"scheme-less endpoint with credentials", "user:S3CRET-URLPASS@minio.example.com:9000", ""},
		{"unparsable without credentials stays", "http://[::1", "http://[::1"},
		{"unparsable with credentials is dropped", "http://user:S3CRET-URLPASS@[::1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fluxSourceURL(tc.in)
			if got != tc.want {
				t.Errorf("fluxSourceURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.Contains(got, "S3CRET") {
				t.Errorf("secret leaked: %q", got)
			}
		})
	}
}

func TestFluxSourceRefPrecedence(t *testing.T) {
	git := func(ref map[string]any) FluxSourceInfo {
		return parseFluxSource(FluxKindGitRepository, &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{"name": "g", "namespace": "ns"}, "spec": map[string]any{"ref": ref},
		}})
	}
	oci := func(ref map[string]any) FluxSourceInfo {
		return parseFluxSource(FluxKindOCIRepository, &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{"name": "o", "namespace": "ns"}, "spec": map[string]any{"ref": ref},
		}})
	}
	cases := []struct {
		name         string
		got          FluxSourceInfo
		wantT, wantV string
	}{
		{"git: all set, commit wins", git(map[string]any{"commit": "c1", "name": "refs/x", "semver": "1.x", "tag": "t", "branch": "b"}), "commit", "c1"},
		{"git: name over semver", git(map[string]any{"name": "refs/x", "semver": "1.x", "tag": "t", "branch": "b"}), "name", "refs/x"},
		{"git: semver over tag", git(map[string]any{"semver": "1.x", "tag": "t", "branch": "b"}), "semver", "1.x"},
		{"git: tag over branch", git(map[string]any{"tag": "t", "branch": "b"}), "tag", "t"},
		{"git: branch", git(map[string]any{"branch": "b"}), "branch", "b"},
		{"git: empty ref", git(map[string]any{}), "", ""},
		{"git: empty strings are not set", git(map[string]any{"commit": "", "branch": "b"}), "branch", "b"},
		{"oci: all set, digest wins", oci(map[string]any{"digest": "sha256:1", "semver": "1.x", "tag": "t"}), "digest", "sha256:1"},
		{"oci: semver over tag", oci(map[string]any{"semver": "1.x", "tag": "t"}), "semver", "1.x"},
		{"oci: tag", oci(map[string]any{"tag": "t"}), "tag", "t"},
		{"oci: semverFilter alone is not a ref", oci(map[string]any{"semverFilter": ".*-rc"}), "", ""},
	}
	for _, tc := range cases {
		if tc.got.RefType != tc.wantT || tc.got.RefValue != tc.wantV {
			t.Errorf("%s: ref = %q=%q, want %q=%q", tc.name, tc.got.RefType, tc.got.RefValue, tc.wantT, tc.wantV)
		}
	}
}

func TestFluxSourceKindsToRead(t *testing.T) {
	kinds := func(versions map[string]string) []string {
		var out []string
		for _, k := range fluxSourceKindsToRead(versions) {
			out = append(out, k.Kind+"/"+versions[k.Kind])
		}
		return out
	}
	all := []string{"GitRepository/v1", "OCIRepository/v1", "HelmRepository/v1", "HelmChart/v1", "Bucket/v1"}

	full := map[string]string{
		FluxKindGitRepository: "v1", FluxKindOCIRepository: "v1", FluxKindHelmRepository: "v1", FluxKindHelmChart: "v1", FluxKindBucket: "v1",
		FluxKindKustomization: "v1", // чужой вид в карте не мешает
	}
	if got := kinds(full); !slices.Equal(got, all) {
		t.Errorf("full install: %v, want %v", got, all)
	}

	// Flux 2.3: OCIRepository и Bucket в v1beta2 — версия берётся из allowlist вида, а не «v1» по умолчанию.
	v1beta2 := cloneVersions(full)
	v1beta2[FluxKindOCIRepository], v1beta2[FluxKindBucket] = "v1beta2", "v1beta2"
	if got, want := kinds(v1beta2), []string{"GitRepository/v1", "OCIRepository/v1beta2", "HelmRepository/v1", "HelmChart/v1", "Bucket/v1beta2"}; !slices.Equal(got, want) {
		t.Errorf("flux 2.3: %v, want %v", got, want)
	}

	// Вид без CRD (нет в карте) или с версией вне allowlist пропускается.
	partial := cloneVersions(full)
	delete(partial, FluxKindHelmChart)
	partial[FluxKindBucket] = "v1alpha1"
	if got, want := kinds(partial), []string{"GitRepository/v1", "OCIRepository/v1", "HelmRepository/v1"}; !slices.Equal(got, want) {
		t.Errorf("partial: %v, want %v", got, want)
	}

	if got := kinds(nil); len(got) != 0 {
		t.Errorf("no versions: %v, want none", got)
	}
}

func cloneVersions(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func sourceObjs(t testing.TB, files ...string) []runtime.Object {
	t.Helper()
	var objs []runtime.Object
	for _, f := range files {
		objs = append(objs, readSourceFixture(t, f))
	}
	return objs
}

var v1SourceFixtures = []string{
	"gitrepository-branch.yaml", "gitrepository-tag.yaml", "gitrepository-failed.yaml",
	"ocirepository-v1-tag.yaml", "helmrepository-default.yaml", "helmrepository-oci.yaml", "helmchart.yaml", "bucket-v1.yaml",
}

func TestGetFluxSources(t *testing.T) {
	m := fluxManager(newSourcesDyn(sourceObjs(t, v1SourceFixtures...)...))

	all, err := m.GetFluxSources(nil)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, s := range all {
		keys = append(keys, s.Kind+" "+s.Namespace+"/"+s.Name)
	}
	// Порядок: вид в порядке таблицы, затем namespace и имя.
	want := []string{
		"GitRepository flux-system/flux-system", "GitRepository team-a/apps", "GitRepository team-a/broken",
		"OCIRepository flux-system/podinfo",
		"HelmRepository flux-system/bitnami", "HelmRepository flux-system/ghcr",
		"HelmChart flux-system/apps-podinfo",
		"Bucket flux-system/assets",
	}
	if !slices.Equal(keys, want) {
		t.Fatalf("keys = %v\nwant   %v", keys, want)
	}

	one, err := m.GetFluxSources([]string{"team-a"})
	if err != nil || len(one) != 2 {
		t.Fatalf("team-a: err=%v items=%+v", err, one)
	}
	none, err := m.GetFluxSources([]string{"empty"})
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("empty namespace must give an empty non-nil list: err=%v items=%#v", err, none)
	}

	_, err = m.GetFluxSources([]string{"a/b"})
	if err == nil {
		t.Fatal("an invalid namespace in the scope must be rejected")
	}
	if strings.Count(err.Error(), "\n") > 0 {
		t.Errorf("a scope error must be reported once, not once per kind: %q", err)
	}
}

func failList(dyn *dynamicfake.FakeDynamicClient, plural string, err error) {
	dyn.PrependReactor("list", plural, func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, err })
}

func TestGetFluxSourcesPartialFailure(t *testing.T) {
	boom := errors.New("the server is currently unable to handle the request")

	t.Run("one kind failed: the others are returned without an error", func(t *testing.T) {
		dyn := newSourcesDyn(sourceObjs(t, v1SourceFixtures...)...)
		failList(dyn, "buckets", boom)
		got, err := fluxManager(dyn).GetFluxSources(nil)
		if err != nil {
			t.Fatalf("a partial failure must not fail the call: %v", err)
		}
		if len(got) != 7 {
			t.Fatalf("got %d sources, want 7 (everything except the bucket): %+v", len(got), got)
		}
		for _, s := range got {
			if s.Kind == FluxKindBucket {
				t.Errorf("unexpected bucket: %+v", s)
			}
		}
	})

	t.Run("all kinds failed: an error naming them", func(t *testing.T) {
		dyn := newSourcesDyn(sourceObjs(t, v1SourceFixtures...)...)
		for _, k := range fluxKindsOf(fluxCtrlSource) {
			failList(dyn, k.Plural, boom)
		}
		got, err := fluxManager(dyn).GetFluxSources(nil)
		if err == nil || got != nil {
			t.Fatalf("expected an error, got err=%v items=%v", err, got)
		}
		if !errors.Is(err, boom) {
			t.Errorf("the cause is lost: %v", err)
		}
		for _, k := range fluxKindsOf(fluxCtrlSource) {
			if !strings.Contains(err.Error(), k.Kind) {
				t.Errorf("error does not name %s: %v", k.Kind, err)
			}
		}
	})

	t.Run("no permission or no CRD is skipped without an error", func(t *testing.T) {
		dyn := newSourcesDyn(sourceObjs(t, v1SourceFixtures...)...)
		failList(dyn, "buckets", apierrors.NewForbidden(schema.GroupResource{Group: fluxGroupSource, Resource: "buckets"}, "", errors.New("denied")))
		failList(dyn, "helmcharts", apierrors.NewNotFound(schema.GroupResource{Group: fluxGroupSource, Resource: "helmcharts"}, ""))
		got, err := fluxManager(dyn).GetFluxSources(nil)
		if err != nil || len(got) != 6 {
			t.Fatalf("err=%v items=%d, want 6 items and no error", err, len(got))
		}
	})

	t.Run("nothing readable and nothing broken: empty list", func(t *testing.T) {
		dyn := newSourcesDyn()
		for _, k := range fluxKindsOf(fluxCtrlSource) {
			failList(dyn, k.Plural, apierrors.NewForbidden(schema.GroupResource{Group: fluxGroupSource, Resource: k.Plural}, "", errors.New("denied")))
		}
		got, err := fluxManager(dyn).GetFluxSources(nil)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("err=%v items=%#v, want an empty non-nil list", err, got)
		}
	})

	t.Run("a real failure is not hidden by skipped kinds", func(t *testing.T) {
		dyn := newSourcesDyn()
		for _, k := range fluxKindsOf(fluxCtrlSource) {
			failList(dyn, k.Plural, apierrors.NewForbidden(schema.GroupResource{Group: fluxGroupSource, Resource: k.Plural}, "", errors.New("denied")))
		}
		failList(dyn, "gitrepositories", boom)
		if _, err := fluxManager(dyn).GetFluxSources(nil); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the real failure", err)
		}
	})
}

func TestParseFluxSourceToleratesGarbage(t *testing.T) {
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
		"nil object":          {},
		"spec is a string":    bad("oops", "oops"),
		"status is a list":    bad(map[string]any{}, []any{1}),
		"ref is a string":     bad(map[string]any{"ref": "main"}, nil),
		"ref values wrong":    bad(map[string]any{"ref": map[string]any{"branch": 1, "tag": []any{}, "digest": map[string]any{}}}, nil),
		"url is a number":     bad(map[string]any{"url": 5, "endpoint": []any{}, "bucketName": 7}, nil),
		"secretRef is a list": bad(map[string]any{"secretRef": []any{"x"}}, nil),
		"secretRef no name":   bad(map[string]any{"secretRef": map[string]any{"name": 1}}, nil),
		"sourceRef garbage":   bad(map[string]any{"sourceRef": "x"}, nil),
		"chart wrong types":   bad(map[string]any{"chart": 1, "version": []any{}}, nil),
		"artifact is a list":  bad(nil, map[string]any{"artifact": []any{"x"}}),
		"artifact wrong":      bad(nil, map[string]any{"artifact": map[string]any{"revision": 1, "digest": []any{}, "lastUpdateTime": 3}}),
		"conditions garbage":  bad(nil, map[string]any{"conditions": []any{"x", 1, map[string]any{"type": 5}}}),
	}
	for _, k := range fluxKindsOf(fluxCtrlSource) {
		for name, u := range objs {
			t.Run(k.Kind+"/"+name, func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic: %v", r)
					}
				}()
				got := parseFluxSource(k.Kind, u)
				if got.Kind != k.Kind {
					t.Errorf("Kind = %q, want %q", got.Kind, k.Kind)
				}
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

func TestParseFluxSourceLastReconcileFallbacks(t *testing.T) {
	mk := func(status map[string]any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "s", "namespace": "ns"}, "status": status}}
	}
	// Ready-условие главнее всего.
	got := parseFluxSource(FluxKindGitRepository, mk(map[string]any{
		"artifact":   map[string]any{"lastUpdateTime": "2026-11-01T00:00:00Z"},
		"conditions": []any{map[string]any{"type": "Ready", "status": "True", "lastTransitionTime": "2026-11-06T00:00:00Z"}},
	}))
	if got.LastReconcileAt != "2026-11-06T00:00:00Z" {
		t.Errorf("Ready time must win: %q", got.LastReconcileAt)
	}
	// Нет Ready-условия — время обновления артефакта.
	got = parseFluxSource(FluxKindGitRepository, mk(map[string]any{"artifact": map[string]any{"lastUpdateTime": "2026-11-01T00:00:00Z"}}))
	if got.LastReconcileAt != "2026-11-01T00:00:00Z" {
		t.Errorf("artifact update time is the fallback: %q", got.LastReconcileAt)
	}
	// Ни того ни другого, а lastHandledReconcileAt — токен, а не время: не показывается.
	got = parseFluxSource(FluxKindGitRepository, mk(map[string]any{"lastHandledReconcileAt": "opaque-token"}))
	if got.LastReconcileAt != "" {
		t.Errorf("an opaque token must not be shown as a time: %q", got.LastReconcileAt)
	}
	got = parseFluxSource(FluxKindGitRepository, mk(map[string]any{"lastHandledReconcileAt": "2026-11-05T09:00:01.5Z"}))
	if got.LastReconcileAt != "2026-11-05T09:00:01.5Z" {
		t.Errorf("a valid timestamp token is the last fallback: %q", got.LastReconcileAt)
	}
}

// TestFluxSourcesNoSecretLeak — по образцу TestArgoSettingsNoSecretLeak: запрещённые поля заполнены контрольными
// строками, ни одна не должна попасть в json.Marshal ответа. Новое поле DTO добавляется только через этот тест.
func TestFluxSourcesNoSecretLeak(t *testing.T) {
	// 1) Фикстуры v1: last-applied-configuration и spec.ignore несут маркеры.
	objs := sourceObjs(t, v1SourceFixtures...)

	// 2) По объекту каждого вида со всеми запрещёнными местами: userinfo, токены в query, секретные ссылки сверх
	// допустимого имени secretRef, шаблоны ignore, адрес корзины с паролем, сообщение с токеном.
	evil := func(kind, name string, spec map[string]any) *unstructured.Unstructured {
		spec["interval"] = "1m"
		spec["secretRef"] = map[string]any{"name": "creds"} // имя отдаётся, содержимое секрета не читается
		spec["ignore"] = "/*\n# S3CRET-IGNORE"
		spec["verify"] = map[string]any{"provider": "cosign", "secretRef": map[string]any{"name": "S3CRET-VERIFY"}}
		spec["proxySecretRef"] = map[string]any{"name": "S3CRET-PROXY"}
		spec["certSecretRef"] = map[string]any{"name": "S3CRET-CERT"}
		u := fluxObj("source.toolkit.fluxcd.io/v1", kind, "team", name, spec)
		u.SetAnnotations(map[string]string{
			fluxLastAppliedAnnotation: `{"spec":{"url":"https://u:S3CRET-LASTAPPLIED@h/r"}}`,
			"keep":                    "me",
		})
		u.Object["status"] = map[string]any{
			"artifact": map[string]any{
				"revision": "main@sha1:abc", "digest": "sha256:abc",
				"url": "http://u:S3CRET-ARTIFACTURL@source-controller/x.tgz", "lastUpdateTime": "2026-11-07T10:00:00Z",
			},
			"conditions": []any{map[string]any{
				"type": "Ready", "status": "False", "reason": "Failed", "lastTransitionTime": "2026-11-07T10:00:00Z",
				"message": "fetch failed: https://git:S3CRET-URLPASS@example.com/o/r.git; Authorization: Bearer S3CRET-MSGTOKEN",
			}},
		}
		return u
	}
	objs = append(objs,
		evil(FluxKindGitRepository, "evil-git", map[string]any{
			"url": "https://user:S3CRET-URLPASS@git.example.com/o/r.git?token=S3CRET-QUERY", "ref": map[string]any{"branch": "main"}}),
		evil(FluxKindOCIRepository, "evil-oci", map[string]any{
			"url": "oci://user:S3CRET-URLPASS@ghcr.io/org/app", "ref": map[string]any{"tag": "1"}}),
		evil(FluxKindHelmRepository, "evil-helm", map[string]any{
			"url": "https://acct.blob.core.windows.net/charts?sv=1&sig=S3CRET-QUERY"}),
		evil(FluxKindHelmChart, "evil-chart", map[string]any{
			"chart": "web", "version": "1.x", "sourceRef": map[string]any{"kind": "HelmRepository", "name": "repo"}}),
		evil(FluxKindBucket, "evil-bucket", map[string]any{
			"bucketName": "b", "endpoint": "user:S3CRET-URLPASS@minio.example.com:9000"}),
	)

	m := fluxManager(newSourcesDyn(objs...))
	got, err := m.GetFluxSources(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(objs) {
		t.Fatalf("got %d sources, want %d", len(got), len(objs))
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	all := string(raw)
	for _, marker := range []string{
		"S3CRET-URLPASS", "S3CRET-QUERY", "S3CRET-IGNORE", "S3CRET-VERIFY", "S3CRET-PROXY", "S3CRET-CERT",
		"S3CRET-LASTAPPLIED", "S3CRET-ARTIFACTURL", "S3CRET-MSGTOKEN",
	} {
		if strings.Contains(all, marker) {
			t.Errorf("secret %q leaked into the response: %s", marker, all)
		}
	}
	if strings.Contains(all, "S3CRET") {
		t.Errorf("a control string leaked into the response: %s", all)
	}

	// Разрешённое не потерялось: имя секрета, остальные аннотации, сообщение (отредактированное).
	for _, s := range got {
		if !strings.HasPrefix(s.Name, "evil-") {
			continue
		}
		if s.Kind != FluxKindHelmChart && s.SecretRef != "creds" {
			t.Errorf("%s: only the secret name must be returned, got %q", s.Name, s.SecretRef)
		}
		if _, ok := s.Annotations[fluxLastAppliedAnnotation]; ok || s.Annotations["keep"] != "me" {
			t.Errorf("%s: annotations = %v", s.Name, s.Annotations)
		}
		if !strings.Contains(s.Message, "fetch failed") {
			t.Errorf("%s: message was dropped instead of redacted: %q", s.Name, s.Message)
		}
	}
}
