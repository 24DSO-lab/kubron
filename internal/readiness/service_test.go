// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"

	"github.com/24DSO-lab/kubron/internal/readiness"
	"github.com/24DSO-lab/kubron/internal/readiness/apimatrix"
	"github.com/24DSO-lab/kubron/internal/readiness/kver"
	"github.com/24DSO-lab/kubron/internal/readiness/readinesstest"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// fakeReader — двойник Reader: состав кластера задаётся в тесте. Записывает все обращения.
type fakeReader struct {
	mu        sync.Mutex
	info      version.Info
	infoErr   error
	resources []*metav1.APIResourceList
	resErr    error
	lists     map[string]listAnswer // ключ — «group/Kind@namespace» (namespace может быть пусто)
	calls     []string
}

type listAnswer struct {
	items     []unstructured.Unstructured
	truncated bool
	err       error
}

func (f *fakeReader) ServerVersion(context.Context) (version.Info, error) { return f.info, f.infoErr }

func (f *fakeReader) ServerResources(context.Context) ([]*metav1.APIResourceList, error) {
	return f.resources, f.resErr
}

func (f *fakeReader) List(_ context.Context, gvk schema.GroupVersionKind, o readiness.ListOptions) (*readiness.ListResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := listKey(gvk.Group, gvk.Kind, o.Namespace)
	f.calls = append(f.calls, "list "+gvk.String()+" ns="+o.Namespace)
	a, ok := f.lists[key]
	if !ok {
		return &readiness.ListResult{ObservedAt: readinesstest.Base}, nil
	}
	if a.err != nil {
		return nil, a.err
	}
	return &readiness.ListResult{Items: a.items, Truncated: a.truncated, ObservedAt: readinesstest.Base}, nil
}

func listKey(group, kind, ns string) string { return group + "/" + kind + "@" + ns }

func newFake(gitVersion string) *fakeReader {
	m, err := kver.ParseGitVersion(gitVersion)
	if err != nil {
		panic(err)
	}
	return &fakeReader{
		info:  version.Info{GitVersion: gitVersion, Major: "1", Minor: fmt.Sprint(m.Minor), Platform: "linux/amd64"},
		lists: map[string]listAnswer{},
	}
}

// serve объявляет, что кластер обслуживает «group/version» с видами kinds.
func (f *fakeReader) serve(gv string, kinds ...string) {
	l := &metav1.APIResourceList{GroupVersion: gv}
	for _, k := range kinds {
		l.APIResources = append(l.APIResources, metav1.APIResource{Name: strings.ToLower(k) + "s", Kind: k})
		l.APIResources = append(l.APIResources, metav1.APIResource{Name: strings.ToLower(k) + "s/status", Kind: k})
	}
	f.resources = append(f.resources, l)
}

func (f *fakeReader) set(group, kind, ns string, a listAnswer) { f.lists[listKey(group, kind, ns)] = a }

// obj строит объект, созданный через apiVersion (след в managedFields) и, при lastApplied, в аннотации.
func obj(apiVersion, kind, ns, name string, lastApplied bool) unstructured.Unstructured {
	u := unstructured.Unstructured{Object: map[string]any{"apiVersion": "x/v1", "kind": kind}}
	u.SetNamespace(ns)
	u.SetName(name)
	u.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "kubectl", Operation: metav1.ManagedFieldsOperationApply, APIVersion: apiVersion}})
	if lastApplied {
		raw, _ := json.Marshal(map[string]any{"apiVersion": apiVersion, "kind": kind})
		u.SetAnnotations(map[string]string{"kubectl.kubernetes.io/last-applied-configuration": string(raw)})
	}
	return u
}

func workload(kind, ns, name, image string, replicas, ready int64) unstructured.Unstructured {
	u := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": kind,
		"spec": map[string]any{
			"replicas": replicas,
			"template": map[string]any{"spec": map[string]any{"containers": []any{
				map[string]any{"name": "c", "image": image, "env": []any{map[string]any{"name": "TOKEN", "value": "hunter2-secret-value"}}},
			}}},
		},
		"status": map[string]any{"readyReplicas": ready},
	}}
	u.SetNamespace(ns)
	u.SetName(name)
	return u
}

func node(name, kubelet string, ready bool) unstructured.Unstructured {
	st := "False"
	if ready {
		st = "True"
	}
	u := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Node",
		"status": map[string]any{
			"nodeInfo":   map[string]any{"kubeletVersion": kubelet},
			"conditions": []any{map[string]any{"type": "Ready", "status": st}},
		},
	}}
	u.SetName(name)
	return u
}

func newService(t *testing.T) (*readiness.Service, readiness.Backend) {
	t.Helper()
	b := readiness.NewFileBackend(t.TempDir())
	n := 0
	s, err := readiness.New(readiness.Options{
		Backend: b,
		Now:     func() time.Time { return readinesstest.Base },
		NewID:   func() string { n++; return fmt.Sprintf("rep-%08d", n) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, b
}

func req(rd readiness.Reader, target string) readiness.Request {
	return readiness.Request{Scope: readinesstest.Scope("c1"), Reader: rd, Initiator: "admin", CorrelationID: "corr-1", Target: target}
}

func find(r *readiness.Report, key string) *readiness.Finding {
	for i := range r.Findings {
		if r.Findings[i].Key == key {
			return &r.Findings[i]
		}
	}
	return nil
}

func keysOf(r *readiness.Report) []string {
	var k []string
	for _, f := range r.Findings {
		k = append(k, f.Key)
	}
	return k
}

// apiFixture — кластер 1.34 с объектами трёх видов, чьи версии API по данным матрицы меняют статус по окну целей.
// Ожидания записаны явно, а не вычисляются из матрицы: тест проверяет и данные, и логику.
type apiFixture struct {
	group, version, kind string
	deprecated, removed  string
	replacement          string
	ns, name             string
}

var apiFixtures = []apiFixture{
	{"storagemigration.k8s.io", "v1alpha1", "StorageVersionMigration", "1.33", "1.36", "", "", "svm-1"},
	{"certificates.k8s.io", "v1alpha1", "ClusterTrustBundle", "1.34", "1.37", "", "", "ctb-1"},
	{"storage.k8s.io", "v1beta1", "VolumeAttributesClass", "1.34", "1.37", "storage.k8s.io/v1, VolumeAttributesClass", "", "vac-1"},
}

func apiCluster(t *testing.T) *fakeReader {
	t.Helper()
	m, err := apimatrix.Default()
	if err != nil {
		t.Fatal(err)
	}
	f := newFake("v1.34.3")
	for _, fx := range apiFixtures {
		e, ok := m.Lookup(apimatrix.GVK{Group: fx.group, Version: fx.version, Kind: fx.kind})
		if !ok || e.Deprecated.String() != fx.deprecated || e.Removed.String() != fx.removed {
			t.Fatalf("the embedded matrix no longer says what the fixture assumes for %s/%s %s: %+v", fx.group, fx.version, fx.kind, e)
		}
		f.serve(fx.group+"/"+fx.version, fx.kind)
		f.set(fx.group, fx.kind, "", listAnswer{items: []unstructured.Unstructured{obj(fx.group+"/"+fx.version, fx.kind, fx.ns, fx.name, fx.kind == "ClusterTrustBundle")}})
	}
	return f
}

// Фикстура на каждую целевую версию окна (ADR-0025 §3).
func TestAPIFindingsForEveryTargetInTheWindow(t *testing.T) {
	t.Parallel()
	want := map[string]map[string]readiness.Status{
		"1.35": {"StorageVersionMigration": readiness.StatusDeprecated, "ClusterTrustBundle": readiness.StatusDeprecated, "VolumeAttributesClass": readiness.StatusDeprecated},
		"1.36": {"StorageVersionMigration": readiness.StatusRemoved, "ClusterTrustBundle": readiness.StatusDeprecated, "VolumeAttributesClass": readiness.StatusDeprecated},
		"1.37": {"StorageVersionMigration": readiness.StatusRemoved, "ClusterTrustBundle": readiness.StatusRemoved, "VolumeAttributesClass": readiness.StatusRemoved},
	}
	svc, _ := newService(t)
	if got := fmt.Sprint(svc.Window()); got != "[1.35 1.36 1.37]" {
		t.Fatalf("window = %s", got)
	}
	for target, kinds := range want {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			svc, _ := newService(t)
			rep, err := svc.Check(t.Context(), req(apiCluster(t), target))
			if err != nil {
				t.Fatal(err)
			}
			if rep.Target.String() != target || rep.Coverage.EvaluatedAgainst.String() != target || !rep.Coverage.TargetSpecified {
				t.Fatalf("target = %s, coverage = %+v", rep.Target, rep.Coverage)
			}
			for _, fx := range apiFixtures {
				f := find(rep, "api/"+fx.group+"/"+fx.version+"/"+fx.kind)
				if f == nil {
					t.Fatalf("no finding for %s in %v", fx.kind, keysOf(rep))
				}
				if f.Status != kinds[fx.kind] {
					t.Errorf("%s: status = %s, want %s", fx.kind, f.Status, kinds[fx.kind])
				}
				wantSev := readiness.SeverityWarning
				if f.Status == readiness.StatusRemoved {
					wantSev = readiness.SeverityBlocker
				}
				if f.Severity != wantSev {
					t.Errorf("%s: severity = %s, want %s", fx.kind, f.Severity, wantSev)
				}
				d := f.API
				if d.APIVersion != fx.group+"/"+fx.version || d.Kind != fx.kind || d.DeprecatedIn != fx.deprecated || d.RemovedIn != fx.removed || d.Replacement != fx.replacement {
					t.Errorf("%s: detail = %+v", fx.kind, d)
				}
				if d.ResourceCount != 1 || len(d.Resources) != 1 || d.Resources[0].Name != fx.name || len(d.Resources[0].Evidence) == 0 {
					t.Errorf("%s: resources = %+v", fx.kind, d.Resources)
				}
				if f.Source != readiness.SourceLiveAPI || f.ObservedAt.IsZero() || f.Recommendation == nil || d.GuideURL == "" {
					t.Errorf("%s: source/time/recommendation/guide missing: %+v", fx.kind, f)
				}
				if fx.replacement != "" && f.Recommendation.Code != readiness.CodeRecMigrate {
					t.Errorf("%s: recommendation = %+v", fx.kind, f.Recommendation)
				}
				if fx.replacement == "" && f.Recommendation.Code != readiness.CodeRecMigrateNoReplace {
					t.Errorf("%s: recommendation = %+v", fx.kind, f.Recommendation)
				}
			}
		})
	}
}

func TestEvidenceAndResourceCap(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	f := newFake("v1.34.3")
	f.serve("certificates.k8s.io/v1alpha1", "ClusterTrustBundle")
	var items []unstructured.Unstructured
	for i := range readiness.MaxResourcesPerFinding + 5 {
		items = append(items, obj("certificates.k8s.io/v1alpha1", "ClusterTrustBundle", "", fmt.Sprintf("b-%03d", i), i == 0))
	}
	// Объект, чья версия в managedFields современная, находкой не считается.
	items = append(items, obj("certificates.k8s.io/v1beta1", "ClusterTrustBundle", "", "modern", false))
	f.set("certificates.k8s.io", "ClusterTrustBundle", "", listAnswer{items: items})
	rep, err := svc.Check(t.Context(), req(f, "1.37"))
	if err != nil {
		t.Fatal(err)
	}
	d := find(rep, "api/certificates.k8s.io/v1alpha1/ClusterTrustBundle").API
	if d.ResourceCount != readiness.MaxResourcesPerFinding+5 || len(d.Resources) != readiness.MaxResourcesPerFinding || !d.ResourcesTruncated {
		t.Fatalf("count=%d listed=%d truncated=%v", d.ResourceCount, len(d.Resources), d.ResourcesTruncated)
	}
	if !slices.Equal(d.Resources[0].Evidence, []string{readiness.EvidenceLastApplied, readiness.EvidenceManagedFields}) {
		t.Errorf("evidence = %v", d.Resources[0].Evidence)
	}
	for _, r := range d.Resources {
		if r.Name == "modern" {
			t.Error("an object managed through the new version was reported")
		}
	}
}

func TestNoTargetReportsOnlyAlreadyDeprecated(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	rep, err := svc.Check(t.Context(), req(apiCluster(t), ""))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Target.IsZero() || rep.Coverage.TargetSpecified || rep.Coverage.EvaluatedAgainst.String() != "1.34" {
		t.Fatalf("coverage = %+v", rep.Coverage)
	}
	if !hasLimit(rep.Coverage.Limitations, readiness.LimitNoTarget) {
		t.Error("coverage must say that no target was chosen")
	}
	if f := find(rep, "api/storagemigration.k8s.io/v1alpha1/StorageVersionMigration"); f == nil || f.Status != readiness.StatusDeprecated {
		t.Errorf("deprecated since 1.33 must be reported: %+v", f)
	}
	if find(rep, "api/certificates.k8s.io/v1alpha1/ClusterTrustBundle") == nil {
		t.Error("deprecated since 1.34 must be reported at 1.34")
	}
	if find(rep, "platform/upgrade-path") != nil {
		t.Error("no upgrade path without a target")
	}
}

func hasLimit(l []readiness.Message, code string) bool {
	return slices.ContainsFunc(l, func(m readiness.Message) bool { return m.Code == code })
}

func TestCoverageStatesWhatWasAndWasNotChecked(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	rep, err := svc.Check(t.Context(), req(apiCluster(t), "1.36"))
	if err != nil {
		t.Fatal(err)
	}
	c := rep.Coverage
	if c.Mode != readiness.ModeGenericKubernetes || !hasLimit(c.Limitations, readiness.LimitProviderNotChecked) {
		t.Errorf("mode/limitations = %s %+v", c.Mode, c.Limitations)
	}
	live, _ := c.Source(readiness.SourceLiveAPI)
	if !live.Checked || live.Partial || len(live.Methods) < 3 || len(live.Limitations) == 0 || live.ObservedAt.IsZero() {
		t.Errorf("live = %+v", live)
	}
	for _, k := range []readiness.SourceKind{readiness.SourceGitOps, readiness.SourceLocalFiles} {
		s, ok := c.Source(k)
		if !ok || s.Checked || s.Reason == nil {
			t.Errorf("%s must be listed as not checked with a reason: %+v", k, s)
		}
	}
	if !slices.Contains(live.Scanned, "storage.k8s.io/VolumeAttributesClass") {
		t.Errorf("scanned = %v", live.Scanned)
	}
	if c.APIMatrix.Version == "" || c.APIMatrix.GeneratedAt == "" || len(c.APIMatrix.Window) != 3 || c.Catalog.Version == "" || c.Catalog.CheckedAt == "" {
		t.Errorf("matrix/catalog info missing: %+v %+v", c.APIMatrix, c.Catalog)
	}
}

func TestUnreadableKindsAreNotCoveredNotSilentlyClean(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	f := apiCluster(t)
	f.set("storage.k8s.io", "VolumeAttributesClass", "", listAnswer{err: readiness.ErrSourceForbidden})
	f.set("certificates.k8s.io", "ClusterTrustBundle", "", listAnswer{err: fmt.Errorf("boom: %w", readiness.ErrSourceUnavailable)})
	rep, err := svc.Check(t.Context(), req(f, "1.37"))
	if err != nil {
		t.Fatal(err)
	}
	live, _ := rep.Coverage.Source(readiness.SourceLiveAPI)
	for _, k := range []string{"storage.k8s.io/VolumeAttributesClass", "certificates.k8s.io/ClusterTrustBundle"} {
		if !slices.Contains(live.NotCovered, k) {
			t.Errorf("%s must be listed as not covered: %v", k, live.NotCovered)
		}
		if slices.Contains(live.Scanned, k) {
			t.Errorf("%s was not read but is listed as scanned", k)
		}
	}
	// Недоступность — предупреждение «не проверено» и частичное покрытие, а не пустой успех.
	u := find(rep, "source/list-unavailable")
	if u == nil || u.Status != readiness.StatusNotChecked || u.Severity != readiness.SeverityWarning || !live.Partial {
		t.Fatalf("unavailable source: finding=%+v partial=%v", u, live.Partial)
	}
	if find(rep, "api/storage.k8s.io/v1beta1/VolumeAttributesClass") != nil {
		t.Error("a kind that was not read produced an API finding")
	}
	// Запрет платформы — известный предел, отдельное информационное сообщение.
	n := find(rep, "source/api-not-covered")
	if n == nil || n.Severity != readiness.SeverityInfo || n.Status != readiness.StatusNotChecked {
		t.Errorf("not-covered finding = %+v", n)
	}
}

func TestTruncatedListIsPartial(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	f := apiCluster(t)
	f.set("storage.k8s.io", "VolumeAttributesClass", "", listAnswer{truncated: true, items: []unstructured.Unstructured{obj("storage.k8s.io/v1beta1", "VolumeAttributesClass", "", "v", false)}})
	rep, err := svc.Check(t.Context(), req(f, "1.37"))
	if err != nil {
		t.Fatal(err)
	}
	tr := find(rep, "source/list-truncated")
	live, _ := rep.Coverage.Source(readiness.SourceLiveAPI)
	if tr == nil || tr.Status != readiness.StatusPartial || !live.Partial {
		t.Fatalf("truncated = %+v partial=%v", tr, live.Partial)
	}
	if find(rep, "api/storage.k8s.io/v1beta1/VolumeAttributesClass") == nil {
		t.Error("what was read must still be reported")
	}
}

func TestServerResourcesFailureIsNotAnEmptySuccess(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	f := apiCluster(t)
	f.resErr = readiness.ErrSourceUnavailable
	rep, err := svc.Check(t.Context(), req(f, "1.37"))
	if err != nil {
		t.Fatal(err)
	}
	live, _ := rep.Coverage.Source(readiness.SourceLiveAPI)
	if slices.Contains(live.Methods, readiness.MethodServerResources) || !live.Partial {
		t.Errorf("live = %+v", live)
	}
	if f := find(rep, "source/server-resources"); f == nil || f.Status != readiness.StatusNotChecked {
		t.Errorf("finding = %+v", f)
	}
	for _, k := range keysOf(rep) {
		if strings.HasPrefix(k, "api/") {
			t.Errorf("API findings without the API list: %s", k)
		}
	}
}

func TestPartialDiscoveryUsesWhatWasRead(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	f := apiCluster(t)
	f.resErr = &readiness.PartialError{Failed: []string{"metrics.k8s.io/v1beta1"}}
	rep, err := svc.Check(t.Context(), req(f, "1.37"))
	if err != nil {
		t.Fatal(err)
	}
	if pf := find(rep, "source/server-resources"); pf == nil || pf.Status != readiness.StatusPartial || pf.Explanation.Params["failed"] != "metrics.k8s.io/v1beta1" {
		t.Errorf("finding = %+v", pf)
	}
	if find(rep, "api/storage.k8s.io/v1beta1/VolumeAttributesClass") == nil {
		t.Error("the readable part must still be analyzed")
	}
}

func TestKindNotServedIsSkipped(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	f := newFake("v1.34.3") // кластер ничего не обслуживает
	rep, err := svc.Check(t.Context(), req(f, "1.37"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "VolumeAttributesClass") {
			t.Errorf("a kind the cluster does not serve was listed: %s", c)
		}
	}
	for _, k := range keysOf(rep) {
		if strings.HasPrefix(k, "api/") {
			t.Errorf("unexpected API finding %s", k)
		}
	}
}

func TestRequestValidation(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	tests := []struct {
		name   string
		reader func() *fakeReader
		target string
		scope  *tenant.Scope
		want   error
	}{
		{"garbage target", func() *fakeReader { return newFake("v1.34.3") }, "latest", nil, readiness.ErrInvalidTarget},
		{"target outside window", func() *fakeReader { return newFake("v1.34.3") }, "1.30", nil, readiness.ErrTargetOutOfWindow},
		{"target beyond window", func() *fakeReader { return newFake("v1.34.3") }, "1.99", nil, readiness.ErrTargetOutOfWindow},
		{"target older than current", func() *fakeReader { return newFake("v1.36.1") }, "1.35", nil, readiness.ErrTargetOlder},
		{"version unreadable", func() *fakeReader { f := newFake("v1.34.3"); f.infoErr = readiness.ErrSourceUnavailable; return f }, "", nil, readiness.ErrCurrentVersion},
		{"version garbage", func() *fakeReader { f := newFake("v1.34.3"); f.info = version.Info{GitVersion: "abc"}; return f }, "", nil, readiness.ErrCurrentVersion},
		{"incomplete scope", func() *fakeReader { return newFake("v1.34.3") }, "", &tenant.Scope{OrgID: "o"}, readiness.ErrInvalidScope},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := req(tt.reader(), tt.target)
			if tt.scope != nil {
				r.Scope = *tt.scope
			}
			_, err := svc.Run(t.Context(), r)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
	if _, err := svc.Check(t.Context(), readiness.Request{Scope: readinesstest.Scope("c1")}); err == nil {
		t.Error("a request without a reader must fail")
	}
}

func TestCanceledContextIsAnErrorButDeadlineGivesPartialReport(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := svc.Check(ctx, req(apiCluster(t), "1.36")); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	dctx, dcancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer dcancel()
	rep, err := svc.Check(dctx, req(apiCluster(t), "1.36"))
	if err != nil {
		t.Fatalf("an expired deadline must give a report with not-checked marks: %v", err)
	}
	live, _ := rep.Coverage.Source(readiness.SourceLiveAPI)
	if !live.Partial || len(live.NotCovered) == 0 {
		t.Errorf("unread kinds must be reported as not covered: %+v", live)
	}
}

func TestUpgradePath(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	tests := []struct {
		gitVersion, target string
		code               string
		status             readiness.Status
	}{
		{"v1.35.1", "1.36", readiness.CodePlatformPathOK, readiness.StatusOK},
		{"v1.35.1", "1.35", readiness.CodePlatformSameVersion, readiness.StatusOK},
		{"v1.34.1", "1.36", readiness.CodePlatformPathSkips, readiness.StatusAttention},
		{"v1.31.0", "1.37", readiness.CodePlatformPathSkips, readiness.StatusAttention},
	}
	for _, tt := range tests {
		t.Run(tt.gitVersion+"→"+tt.target, func(t *testing.T) {
			t.Parallel()
			rep, err := svc.Check(t.Context(), req(newFake(tt.gitVersion), tt.target))
			if err != nil {
				t.Fatal(err)
			}
			f := find(rep, "platform/upgrade-path")
			if f == nil || f.Explanation.Code != tt.code || f.Status != tt.status {
				t.Fatalf("finding = %+v", f)
			}
		})
	}
	rep, _ := svc.Check(t.Context(), req(newFake("v1.31.0"), "1.37"))
	if find(rep, "platform/current-support") == nil {
		t.Error("a cluster older than the upstream window must be flagged")
	}
	rep, _ = svc.Check(t.Context(), req(newFake("v1.50.0"), ""))
	if !hasLimit(rep.Coverage.Limitations, readiness.LimitStaleMatrix) {
		t.Error("a cluster newer than the matrix window must report a stale matrix")
	}
	rep, _ = svc.Check(t.Context(), req(newFake("v1.20.0"), "1.35"))
	if !hasLimit(rep.Coverage.Limitations, readiness.LimitClusterOlderThanMatrix) {
		t.Error("a cluster older than the matrix data must say so")
	}
}

func TestRunStoresAndComparesWithThePreviousReport(t *testing.T) {
	t.Parallel()
	svc, backend := newService(t)
	f := apiCluster(t)
	first, err := svc.Run(t.Context(), req(f, "1.36"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Compare(t.Context(), readinesstest.Scope("c1"), first.ID, ""); !errors.Is(err, readiness.ErrNotFound) {
		t.Fatalf("the first report has nothing to compare with: %v", err)
	}
	// Между проверками объект мигрировали.
	f.set("storagemigration.k8s.io", "StorageVersionMigration", "", listAnswer{})
	svc2, _ := readiness.New(readiness.Options{Backend: backend, Now: func() time.Time { return readinesstest.Base.Add(time.Hour) }, NewID: func() string { return "rep-00000099" }})
	second, err := svc2.Run(t.Context(), req(f, "1.36"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(t.Context(), readinesstest.Scope("c1"), second.ID)
	if err != nil || got.ID != second.ID {
		t.Fatalf("Get = %v, %v", got, err)
	}
	d, err := svc.Compare(t.Context(), readinesstest.Scope("c1"), second.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if d.PreviousID != first.ID || len(d.Resolved) != 1 || d.Resolved[0].Key != "api/storagemigration.k8s.io/v1alpha1/StorageVersionMigration" || len(d.New) != 0 {
		t.Fatalf("diff = %+v", d)
	}
	page, err := svc.History(t.Context(), readinesstest.Scope("c1"), readiness.HistoryQuery{})
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != second.ID || page.Items[0].Blockers != second.Summary().Blockers {
		t.Fatalf("history = %+v, %v", page, err)
	}
	// Чужая область отчёт не видит.
	other := tenant.Scope{OrgID: "x", TeamID: "y", ClusterID: "c1"}
	if _, err := svc.Compare(t.Context(), other, second.ID, ""); !errors.Is(err, readiness.ErrNotFound) {
		t.Errorf("foreign scope: %v", err)
	}
}

func TestPrioritiesAndActions(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	rep, err := svc.Check(t.Context(), req(apiCluster(t), "1.37"))
	if err != nil {
		t.Fatal(err)
	}
	prev := readiness.Severity("")
	for i, f := range rep.Findings {
		if f.Priority != i+1 {
			t.Errorf("finding %s priority = %d, want %d", f.Key, f.Priority, i+1)
		}
		if prev != "" && f.Severity.Rank() < prev.Rank() {
			t.Errorf("findings are not ordered by importance at %s", f.Key)
		}
		prev = f.Severity
	}
	if rep.Findings[0].Severity != readiness.SeverityBlocker {
		t.Errorf("blockers must come first: %+v", rep.Findings[0])
	}
	var before, after int
	for _, a := range rep.Actions {
		switch a.Phase {
		case readiness.PhaseBefore:
			before++
		case readiness.PhaseAfter:
			after++
		}
	}
	if before == 0 || after == 0 {
		t.Fatalf("actions before/after = %d/%d", before, after)
	}
	first := rep.Actions[0]
	if first.Message.Code != readiness.CodeActMigrateRemoved || len(first.Related) != 3 || first.Priority != 1 {
		t.Errorf("first action = %+v", first)
	}
	// Что проверка не видит, остаётся действием человека.
	var manifests bool
	for _, a := range rep.Actions {
		manifests = manifests || a.Message.Code == readiness.CodeActGitOps
	}
	if !manifests {
		t.Error("the action to check Git manifests must always be present")
	}
}

// Все коды, которые порождает сервис, известны экспорту; ни один код не префикс другого (вложенные ключи переводов).
func TestEveryEmittedCodeIsKnown(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	f := componentCluster(t)
	rep, err := svc.Check(t.Context(), req(f, "1.37"))
	if err != nil {
		t.Fatal(err)
	}
	check := func(m readiness.Message) {
		if m.Code != "" && !readiness.KnownCode(m.Code) {
			t.Errorf("code %q has no English text", m.Code)
		}
	}
	for _, fd := range rep.Findings {
		check(fd.Explanation)
		if fd.Recommendation != nil {
			check(*fd.Recommendation)
		}
		if fd.Component != nil && fd.Component.NoteCode != "" && fd.Component.Note == "" {
			check(readiness.Message{Code: fd.Component.NoteCode})
		}
	}
	for _, a := range rep.Actions {
		check(a.Message)
	}
	for _, m := range rep.Coverage.Limitations {
		check(m)
	}
	for _, s := range rep.Coverage.Sources {
		for _, m := range s.Limitations {
			check(m)
		}
		if s.Reason != nil {
			check(*s.Reason)
		}
	}
}

func TestCodesAreNotPrefixes(t *testing.T) {
	t.Parallel()
	codes := readiness.Codes()
	for i, a := range codes {
		for _, b := range codes[i+1:] {
			if strings.HasPrefix(b, a+".") {
				t.Errorf("code %q is a prefix of %q: nested translation keys would collide", a, b)
			}
		}
	}
}
