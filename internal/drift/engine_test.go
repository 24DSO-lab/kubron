// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package drift

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/baseline"
)

// fakeCluster — двойник кластера: хранит объекты и записывает, что читалось.
type fakeCluster struct {
	mu      sync.Mutex
	objs    map[string]*unstructured.Unstructured // ключ: Kind/ns/name
	hpas    []unstructured.Unstructured
	hpaErr  error
	getErr  map[string]error
	listErr map[string]error
	reads   []string
}

func newCluster() *fakeCluster {
	return &fakeCluster{objs: map[string]*unstructured.Unstructured{}, getErr: map[string]error{}, listErr: map[string]error{}}
}

func ck(kind, ns, name string) string { return kind + "/" + ns + "/" + name }

func (f *fakeCluster) put(u *unstructured.Unstructured) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objs[ck(u.GetKind(), u.GetNamespace(), u.GetName())] = u
}

func (f *fakeCluster) Get(_ context.Context, g schema.GroupVersionKind, ns, name string) (*unstructured.Unstructured, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, "get:"+g.Kind)
	if err := f.getErr[ck(g.Kind, ns, name)]; err != nil {
		return nil, err
	}
	u, ok := f.objs[ck(g.Kind, ns, name)]
	if !ok {
		return nil, ErrNotFound
	}
	return u.DeepCopy(), nil
}

func (f *fakeCluster) List(_ context.Context, g schema.GroupVersionKind, ns, _ string, _ int64) (ListResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, "list:"+g.Kind)
	if g.Kind == "HorizontalPodAutoscaler" {
		if f.hpaErr != nil {
			return ListResult{}, f.hpaErr
		}
		return ListResult{Items: f.hpas}, nil
	}
	if err := f.listErr[g.Kind]; err != nil {
		return ListResult{}, err
	}
	var out []unstructured.Unstructured
	for _, u := range f.objs {
		if u.GetKind() == g.Kind && u.GetNamespace() == ns {
			out = append(out, *u.DeepCopy())
		}
	}
	return ListResult{Items: out}, nil
}

type fakeAudit struct {
	at    time.Time
	found bool
}

func (a fakeAudit) LastApply(context.Context, baseline.Ref, time.Time) (time.Time, bool, error) {
	return a.at, a.found, nil
}

const canaryPW = "canary-engine-secret-5d41402a"

func deployment(name, image, envVal string, replicas int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": name, "namespace": "prod", "resourceVersion": "1", "uid": "x"},
		"spec": map[string]any{
			"replicas": replicas,
			"selector": map[string]any{"matchLabels": map[string]any{"app": name}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": name}},
				"spec": map[string]any{"containers": []any{map[string]any{
					"name": "app", "image": image,
					"env": []any{map[string]any{"name": "DB_PASSWORD", "value": envVal}},
				}}},
			},
		},
	}}
}

func configMap(name string, data map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": name, "namespace": "prod"},
		"data":     data,
	}}
}

var t0 = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func testVersion(t *testing.T, e *Engine, spec Spec) baseline.Version {
	t.Helper()
	snap, err := e.Snapshot(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	return baseline.Version{ID: "v1", BaselineID: "b1", Seq: 1, RulesVersion: snap.RulesVersion, Salt: snap.Salt, Objects: snap.Objects}
}

func nsSpec() Spec { return Spec{Namespaces: []string{"prod"}} }

func findItem(t *testing.T, r baseline.CheckResult, kind, name string) baseline.ItemResult {
	t.Helper()
	for _, it := range r.Items {
		if it.Ref.Kind == kind && it.Ref.Name == name {
			return it
		}
	}
	t.Fatalf("no item %s/%s in %+v", kind, name, r.Items)
	return baseline.ItemResult{}
}

func TestCheckMatchesRightAfterSnapshot(t *testing.T) {
	t.Parallel()
	c := newCluster()
	c.put(deployment("web", "nginx:1.27", canaryPW, 3))
	c.put(configMap("cfg", map[string]any{"k": "v"}))
	e := NewEngine(c, Options{})
	v := testVersion(t, e, nsSpec())
	res, err := e.Check(t.Context(), CheckInput{Version: v, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Total != 2 || res.Summary.Matched != 2 || res.Summary.Checked != 2 {
		t.Fatalf("summary = %+v", res.Summary)
	}
}

func TestCheckChangedMissingAndUnchecked(t *testing.T) {
	t.Parallel()
	c := newCluster()
	c.put(deployment("web", "nginx:1.27", canaryPW, 3))
	c.put(deployment("api", "api:1", "x", 1))
	c.put(deployment("gone", "g:1", "x", 1))
	c.put(deployment("locked", "l:1", "x", 1))
	e := NewEngine(c, Options{})
	v := testVersion(t, e, nsSpec())

	c.put(deployment("web", "nginx:1.28", canaryPW+"-rotated", 3))
	delete(c.objs, ck("Deployment", "prod", "gone"))
	c.getErr[ck("Deployment", "prod", "locked")] = fmt.Errorf("denied: %w", ErrForbidden)
	c.getErr[ck("Deployment", "prod", "api")] = errors.New("boom")

	res, err := e.Check(t.Context(), CheckInput{Version: v, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	web := findItem(t, res, "Deployment", "web")
	if web.Status != baseline.StatusChanged || web.Priority != baseline.PriorityHigh {
		t.Fatalf("web = %+v", web)
	}
	var sawSensitive bool
	for _, ch := range web.Changes {
		if ch.Sensitive {
			sawSensitive = true
			if ch.Old != "" || ch.New != "" || ch.Key != "DB_PASSWORD" {
				t.Errorf("sensitive change leaks or lacks key: %+v", ch)
			}
		}
	}
	if !sawSensitive {
		t.Error("rotated secret value must be reported as a sensitive change")
	}
	if got := findItem(t, res, "Deployment", "gone").Status; got != baseline.StatusMissing {
		t.Errorf("gone = %s", got)
	}
	if it := findItem(t, res, "Deployment", "locked"); it.Status != baseline.StatusUnchecked || it.Reason != baseline.ReasonForbidden {
		t.Errorf("locked = %+v", it)
	}
	if it := findItem(t, res, "Deployment", "api"); it.Status != baseline.StatusUnchecked || it.Reason != baseline.ReasonUnavailable {
		t.Errorf("api = %+v", it)
	}
	s := res.Summary
	if s.Changed != 1 || s.Missing != 1 || s.Unchecked != 2 || s.Matched != 0 || s.Checked != 2 {
		t.Errorf("summary = %+v", s)
	}
	if s.UncheckedByReason[baseline.ReasonForbidden] != 1 {
		t.Errorf("by reason = %v", s.UncheckedByReason)
	}
	if res.Items[0].Status != baseline.StatusChanged {
		t.Error("changed items must come first")
	}
}

func TestSecretIsNeverReadOrReported(t *testing.T) {
	t.Parallel()
	c := newCluster()
	c.put(configMap("cfg", map[string]any{"k": "v"}))
	e := NewEngine(c, Options{})

	snap, err := e.Snapshot(t.Context(), Spec{Namespaces: []string{"prod"}, Kinds: []string{"ConfigMap", "Secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Skipped) != 1 || snap.Skipped[0].Reason != baseline.ReasonSecretUnsupported {
		t.Fatalf("skipped = %+v", snap.Skipped)
	}
	_, err = e.Snapshot(t.Context(), Spec{Refs: []baseline.Ref{{Version: "v1", Kind: "Secret", Namespace: "prod", Name: "s"}}})
	if err != nil {
		t.Fatal(err)
	}
	v := testVersion(t, e, nsSpec())
	v.Objects = append(v.Objects, baseline.Object{Ref: baseline.Ref{Version: "v1", Kind: "Secret", Namespace: "prod", Name: "s"}})
	res, err := e.Check(t.Context(), CheckInput{Version: v, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	if it := findItem(t, res, "Secret", "s"); it.Status != baseline.StatusUnchecked || it.Reason != baseline.ReasonSecretUnsupported {
		t.Errorf("secret item = %+v", it)
	}
	for _, r := range c.reads {
		if strings.Contains(r, "Secret") {
			t.Errorf("secret was read: %s", r)
		}
	}
}

func TestRulesVersionMismatchDoesNotCompare(t *testing.T) {
	t.Parallel()
	c := newCluster()
	c.put(configMap("cfg", map[string]any{"k": "v"}))
	e := NewEngine(c, Options{})
	v := testVersion(t, e, nsSpec())
	v.RulesVersion = RulesVersion + 1
	c.reads = nil
	res, err := e.Check(t.Context(), CheckInput{Version: v, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Unchecked != 1 || res.Summary.UncheckedByReason[baseline.ReasonRulesMismatch] != 1 || res.Summary.Matched != 0 {
		t.Fatalf("summary = %+v", res.Summary)
	}
	if len(c.reads) != 0 {
		t.Errorf("cluster must not be read on rules mismatch: %v", c.reads)
	}
	if res.VersionRulesVersion != RulesVersion+1 || res.RulesVersion != RulesVersion {
		t.Errorf("rules versions = %d/%d", res.RulesVersion, res.VersionRulesVersion)
	}
}

func TestExceptions(t *testing.T) {
	t.Parallel()
	c := newCluster()
	c.put(configMap("cfg", map[string]any{"a": "1", "b": "2"}))
	e := NewEngine(c, Options{})
	v := testVersion(t, e, nsSpec())
	c.put(configMap("cfg", map[string]any{"a": "1x", "b": "2x"}))

	past := t0.Add(-time.Hour)
	ex := []baseline.Exception{
		{ID: "e1", Scope: baseline.ExceptionResource, Ref: baseline.Ref{Kind: "ConfigMap", Namespace: "prod", Name: "cfg"}, Path: "data.a", Comment: "ok"},
		{ID: "e2", Scope: baseline.ExceptionKind, Ref: baseline.Ref{Kind: "ConfigMap"}, Path: "data.b", Comment: "old", ExpiresAt: &past},
	}
	res, err := e.Check(t.Context(), CheckInput{Version: v, Exceptions: ex, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	it := findItem(t, res, "ConfigMap", "cfg")
	if it.Status != baseline.StatusChanged || len(it.Changes) != 1 || !strings.HasPrefix(it.Changes[0].Path, "data.b") {
		t.Fatalf("item = %+v", it)
	}
	if len(it.Suppressed) != 1 || it.Suppressed[0] != "e1" {
		t.Errorf("suppressed = %v", it.Suppressed)
	}
	if len(res.Exceptions.Applied) != 1 || res.Exceptions.Applied[0].Hits != 1 || len(res.Exceptions.Expired) != 1 || res.Exceptions.Expired[0] != "e2" {
		t.Errorf("exceptions = %+v", res.Exceptions)
	}
	if res.Summary.SuppressedChanges != 1 {
		t.Errorf("suppressed changes = %d", res.Summary.SuppressedChanges)
	}

	// Исключение скрывает всё изменение: ресурс соответствует, но исключение видно.
	ex[1].ExpiresAt = nil
	res, err = e.Check(t.Context(), CheckInput{Version: v, Exceptions: ex, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	if it := findItem(t, res, "ConfigMap", "cfg"); it.Status != baseline.StatusMatch || len(it.Suppressed) != 2 {
		t.Errorf("item = %+v", it)
	}
}

func TestHPAExcludesReplicas(t *testing.T) {
	t.Parallel()
	c := newCluster()
	c.put(deployment("web", "nginx:1", "x", 2))
	e := NewEngine(c, Options{})
	hpa := unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"scaleTargetRef": map[string]any{"kind": "Deployment", "name": "web"}}}}
	hpa.SetNamespace("prod")
	c.hpas = []unstructured.Unstructured{hpa}
	v := testVersion(t, e, nsSpec())
	c.put(deployment("web", "nginx:1", "x", 9))

	res, err := e.Check(t.Context(), CheckInput{Version: v, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	if findItem(t, res, "Deployment", "web").Status != baseline.StatusMatch {
		t.Errorf("replicas under HPA must be ignored: %+v", res.Items)
	}

	c.hpaErr = fmt.Errorf("hpa: %w", ErrForbidden)
	res, err = e.Check(t.Context(), CheckInput{Version: v, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Caveats) != 1 || res.Caveats[0] != baseline.CaveatHPAUnreadable {
		t.Errorf("caveats = %v", res.Caveats)
	}
}

func managedDeployment(name, image string, mgr string, at time.Time) *unstructured.Unstructured {
	u := deployment(name, image, "x", 1)
	u.SetManagedFields([]metav1.ManagedFieldsEntry{{
		Manager: mgr, Operation: metav1.ManagedFieldsOperationUpdate, Time: &metav1.Time{Time: at},
		FieldsV1: metav1.NewFieldsV1(`{"f:spec":{"f:template":{"f:spec":{"f:containers":{"k:{\"name\":\"app\"}":{"f:image":{}}}}}}}`),
	}})
	return u
}

func TestChangeSourceNeedsEvidence(t *testing.T) {
	t.Parallel()
	c := newCluster()
	c.put(deployment("web", "nginx:1", "x", 1))
	at := t0.Add(-time.Minute)

	e := NewEngine(c, Options{})
	v := testVersion(t, e, nsSpec())

	// Нет managedFields — источника нет.
	c.put(deployment("web", "nginx:2", "x", 1))
	res, _ := e.Check(t.Context(), CheckInput{Version: v, Now: t0})
	for _, ch := range findItem(t, res, "Deployment", "web").Changes {
		if ch.Source != nil {
			t.Errorf("source without evidence: %+v", ch)
		}
	}

	// managedFields: источник — запись менеджера, имя человека не приписывается.
	c.put(managedDeployment("web", "nginx:2", "kubectl-edit", at))
	res, _ = e.Check(t.Context(), CheckInput{Version: v, Now: t0})
	var got *baseline.ChangeSource
	for _, ch := range findItem(t, res, "Deployment", "web").Changes {
		if strings.HasSuffix(ch.Path, "image") {
			got = ch.Source
		}
	}
	if got == nil || got.Kind != baseline.SourceManagedFields || got.Manager != "kubectl-edit" || !got.At.Equal(at) {
		t.Fatalf("source = %+v", got)
	}

	// Аудит Kubron подтверждает по времени.
	e2 := NewEngine(c, Options{Audit: fakeAudit{at: at.Add(2 * time.Second), found: true}})
	res, _ = e2.Check(t.Context(), CheckInput{Version: v, Now: t0})
	for _, ch := range findItem(t, res, "Deployment", "web").Changes {
		if strings.HasSuffix(ch.Path, "image") && (ch.Source == nil || ch.Source.Kind != baseline.SourceKubronAudit || ch.Source.Manager != "") {
			t.Errorf("audit source = %+v", ch.Source)
		}
	}

	// Запись аудита далеко по времени — не подтверждает.
	e3 := NewEngine(c, Options{Audit: fakeAudit{at: at.Add(time.Hour), found: true}})
	res, _ = e3.Check(t.Context(), CheckInput{Version: v, Now: t0})
	for _, ch := range findItem(t, res, "Deployment", "web").Changes {
		if strings.HasSuffix(ch.Path, "image") && ch.Source.Kind != baseline.SourceManagedFields {
			t.Errorf("far audit must not corroborate: %+v", ch.Source)
		}
	}
}

func TestPreviewToken(t *testing.T) {
	t.Parallel()
	c := newCluster()
	c.put(deployment("web", "nginx:1", canaryPW, 1))
	e := NewEngine(c, Options{})
	pv, err := e.Preview(t.Context(), nsSpec(), nil, "b1", t0)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Objects != 1 || pv.Hashed == 0 || pv.HasPrevious {
		t.Fatalf("preview = %+v", pv)
	}
	if _, err := e.SnapshotConfirmed(t.Context(), nsSpec(), pv.Token, "b1", t0.Add(time.Minute)); err != nil {
		t.Fatalf("unchanged cluster must confirm: %v", err)
	}
	if _, err := e.SnapshotConfirmed(t.Context(), nsSpec(), pv.Token, "other", t0); !errors.Is(err, ErrStalePreview) {
		t.Errorf("other baseline: %v", err)
	}
	if _, err := e.SnapshotConfirmed(t.Context(), nsSpec(), pv.Token, "b1", t0.Add(PreviewTTL+time.Second)); !errors.Is(err, ErrPreviewExpired) {
		t.Errorf("expired: %v", err)
	}
	if _, err := e.SnapshotConfirmed(t.Context(), nsSpec(), "garbage", "b1", t0); !errors.Is(err, ErrPreviewExpired) {
		t.Errorf("garbage: %v", err)
	}
	c.put(deployment("web", "nginx:2", canaryPW, 1))
	if _, err := e.SnapshotConfirmed(t.Context(), nsSpec(), pv.Token, "b1", t0); !errors.Is(err, ErrStalePreview) {
		t.Errorf("changed cluster: %v", err)
	}
	// Другой движок (другой ключ) токен не принимает.
	if _, err := NewEngine(c, Options{}).SnapshotConfirmed(t.Context(), nsSpec(), pv.Token, "b1", t0); err == nil {
		t.Error("token of another engine accepted")
	}
}

func TestPreviewAgainstPrevious(t *testing.T) {
	t.Parallel()
	c := newCluster()
	c.put(deployment("web", "nginx:1", "x", 1))
	c.put(deployment("old", "o:1", "x", 1))
	e := NewEngine(c, Options{})
	prev := testVersion(t, e, nsSpec())

	c.put(deployment("web", "nginx:2", "x", 1))
	delete(c.objs, ck("Deployment", "prod", "old"))
	c.put(deployment("fresh", "f:1", "x", 1))
	pv, err := e.Preview(t.Context(), nsSpec(), &prev, "b1", t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Added) != 1 || pv.Added[0].Name != "fresh" || len(pv.Removed) != 1 || pv.Removed[0].Name != "old" || len(pv.Changed) != 1 || pv.Changed[0].Ref.Name != "web" {
		t.Fatalf("preview = %+v", pv)
	}
	prev.RulesVersion = RulesVersion + 1
	pv, _ = e.Preview(t.Context(), nsSpec(), &prev, "b1", t0)
	if !pv.RulesChanged || len(pv.Changed) != 0 {
		t.Errorf("rules change must disable comparison: %+v", pv)
	}
}

func TestSnapshotSkipsAndLimits(t *testing.T) {
	t.Parallel()
	c := newCluster()
	c.put(configMap("cfg", map[string]any{"k": "v"}))
	c.listErr["Deployment"] = ErrForbidden
	c.listErr["Ingress"] = ErrNotFound
	e := NewEngine(c, Options{})
	snap, err := e.Snapshot(t.Context(), nsSpec())
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]baseline.Reason{}
	for _, s := range snap.Skipped {
		reasons[s.Ref.Kind] = s.Reason
	}
	if reasons["Deployment"] != baseline.ReasonForbidden || reasons["Ingress"] != baseline.ReasonKindUnavailable {
		t.Errorf("skipped = %+v", snap.Skipped)
	}
	if len(snap.Objects) != 1 {
		t.Errorf("objects = %d", len(snap.Objects))
	}
	if _, err := e.Snapshot(t.Context(), Spec{}); !errors.Is(err, baseline.ErrInvalid) {
		t.Errorf("empty spec: %v", err)
	}
}

func TestCheckHonoursCancellation(t *testing.T) {
	t.Parallel()
	c := newCluster()
	c.put(configMap("cfg", map[string]any{"k": "v"}))
	e := NewEngine(c, Options{})
	v := testVersion(t, e, nsSpec())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.Check(ctx, CheckInput{Version: v, Now: t0}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

// Значение секрета не встречается ни в предпросмотре, ни в снимке, ни в результате проверки, ни в журнале, ни в ошибках.
func TestEngineNeverLeaksSecretValues(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := newCluster()
	const rotated = "canary-engine-rotated-9e107d9d"
	c.put(deployment("web", "nginx:1", canaryPW, 1))
	c.put(configMap("cfg", map[string]any{"password": canaryPW}))
	c.getErr[ck("Deployment", "prod", "web")] = errors.New("api error")
	e := NewEngine(c, Options{Log: log})

	dump := func(v ...any) string {
		var b strings.Builder
		for _, x := range v {
			j, _ := json.Marshal(x)
			fmt.Fprintf(&b, "%s %v %+v %#v\n", j, x, x, x)
		}
		return b.String()
	}
	delete(c.getErr, ck("Deployment", "prod", "web"))
	pv, err := e.Preview(t.Context(), nsSpec(), nil, "b1", t0)
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := e.Snapshot(t.Context(), nsSpec())
	v := baseline.Version{ID: "v1", BaselineID: "b1", RulesVersion: snap.RulesVersion, Salt: snap.Salt, Objects: snap.Objects}
	c.put(deployment("web", "nginx:1", rotated, 1))
	c.put(configMap("cfg", map[string]any{"password": rotated}))
	res, err := e.Check(t.Context(), CheckInput{Version: v, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	pv2, _ := e.Preview(t.Context(), nsSpec(), &v, "b1", t0)
	c.getErr[ck("Deployment", "prod", "web")] = errors.New("api error")
	res2, _ := e.Check(t.Context(), CheckInput{Version: v, Now: t0})

	out := dump(pv, pv2, snap, v, res, res2) + logs.String()
	for _, secret := range []string{canaryPW, rotated} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret value %q leaked", secret)
		}
	}
	if res.Summary.Changed != 2 {
		t.Errorf("rotation must be detected for env value and ConfigMap data: %+v", res.Summary)
	}
}
