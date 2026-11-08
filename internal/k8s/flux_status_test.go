// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"errors"
	"maps"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"
)

// fluxRes строит список ресурсов одной группы-версии для fake discovery.
func fluxRes(groupVersion string, plurals ...string) *metav1.APIResourceList {
	l := &metav1.APIResourceList{GroupVersion: groupVersion}
	for _, p := range plurals {
		l.APIResources = append(l.APIResources, metav1.APIResource{Name: p, Namespaced: true})
	}
	return l
}

// fluxFullInstall — полная установка Flux 2.8 (flux install без --components) плюс посторонние группы.
func fluxFullInstall() []*metav1.APIResourceList {
	return []*metav1.APIResourceList{
		fluxRes("v1", "pods", "services"),
		fluxRes("argoproj.io/v1alpha1", "applications"),
		fluxRes("kustomize.toolkit.fluxcd.io/v1", "kustomizations", "kustomizations/status"),
		fluxRes("helm.toolkit.fluxcd.io/v2", "helmreleases", "helmreleases/status"),
		fluxRes("source.toolkit.fluxcd.io/v1", "gitrepositories", "gitrepositories/status", "helmrepositories", "helmcharts", "buckets", "ocirepositories"),
		fluxRes("notification.toolkit.fluxcd.io/v1beta3", "alerts", "providers"),
		fluxRes("notification.toolkit.fluxcd.io/v1", "receivers"),
		fluxRes("image.toolkit.fluxcd.io/v1", "imagerepositories", "imagepolicies", "imageupdateautomations"),
	}
}

// fluxCountingDiscovery — fake discovery, который считает вызовы ServerGroupsAndResources и умеет отдавать ошибку.
type fluxCountingDiscovery struct {
	*fake.FakeDiscovery
	calls atomic.Int32
	// err отдаётся вместе с данными (как ErrGroupDiscoveryFailed) или вместо них, если dropData.
	err      error
	dropData bool
}

func newFluxDiscovery(lists []*metav1.APIResourceList) *fluxCountingDiscovery {
	return &fluxCountingDiscovery{FakeDiscovery: &fake.FakeDiscovery{Fake: &clienttesting.Fake{Resources: lists}}}
}

func (d *fluxCountingDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	d.calls.Add(1)
	groups, lists, err := d.FakeDiscovery.ServerGroupsAndResources()
	if d.err != nil {
		if d.dropData {
			return nil, nil, d.err
		}
		return groups, lists, d.err
	}
	return groups, lists, err
}

// fluxStatusManager — Manager с активным подключением, у которого есть только discovery: любое обращение к другому API
// (Client и DynamicClient равны nil) обрушит тест, это и проверяет «кроме discovery обращений к API нет».
func fluxStatusManager(d discovery.DiscoveryInterface) (*Manager, *ClusterConnection) {
	conn := &ClusterConnection{Name: "c", DiscoveryClient: d}
	m := managerWithBuiltConn("c", conn)
	m.activeKey = "c"
	return m, conn
}

func TestFluxDiscover(t *testing.T) {
	allCtrl := FluxControllers{Kustomize: true, Helm: true, Source: true, Notification: true, Image: true}

	// partial оставляет от полной установки только перечисленные группы (как --components).
	partial := func(groups ...string) []*metav1.APIResourceList {
		var out []*metav1.APIResourceList
		for _, l := range fluxFullInstall() {
			gv, _ := schema.ParseGroupVersion(l.GroupVersion)
			for _, g := range groups {
				if gv.Group == g {
					out = append(out, l)
				}
			}
		}
		return out
	}

	cases := []struct {
		name         string
		lists        []*metav1.APIResourceList
		wantInstall  bool
		wantState    string
		wantCtrl     FluxControllers
		wantVersions map[string]string
	}{
		{
			name:        "full install 2.8",
			lists:       fluxFullInstall(),
			wantInstall: true, wantState: FluxStatusOK, wantCtrl: allCtrl,
			wantVersions: map[string]string{
				FluxKindKustomization: "v1", FluxKindHelmRelease: "v2",
				FluxKindGitRepository: "v1", FluxKindOCIRepository: "v1", FluxKindHelmRepository: "v1",
				FluxKindHelmChart: "v1", FluxKindBucket: "v1",
				FluxKindAlert: "v1beta3", FluxKindProvider: "v1beta3", FluxKindReceiver: "v1",
				FluxKindImageRepository: "v1", FluxKindImagePolicy: "v1", FluxKindImageUpdateAutomation: "v1",
			},
		},
		{
			name:        "partial install: kustomize and source only",
			lists:       partial(fluxGroupKustomize, fluxGroupSource),
			wantInstall: true, wantState: FluxStatusOK,
			wantCtrl: FluxControllers{Kustomize: true, Source: true},
			wantVersions: map[string]string{
				FluxKindKustomization: "v1",
				FluxKindGitRepository: "v1", FluxKindOCIRepository: "v1", FluxKindHelmRepository: "v1",
				FluxKindHelmChart: "v1", FluxKindBucket: "v1",
			},
		},
		{
			name:        "partial install: helm and source only",
			lists:       partial(fluxGroupHelm, fluxGroupSource),
			wantInstall: true, wantState: FluxStatusOK,
			wantCtrl: FluxControllers{Helm: true, Source: true},
			wantVersions: map[string]string{
				FluxKindHelmRelease:   "v2",
				FluxKindGitRepository: "v1", FluxKindOCIRepository: "v1", FluxKindHelmRepository: "v1",
				FluxKindHelmChart: "v1", FluxKindBucket: "v1",
			},
		},
		{
			name:        "no kustomize and no helm: section stays hidden",
			lists:       partial(fluxGroupSource, fluxGroupNotification, fluxGroupImage),
			wantInstall: false, wantState: "",
			wantCtrl: FluxControllers{Source: true, Notification: true, Image: true},
			wantVersions: map[string]string{
				FluxKindGitRepository: "v1", FluxKindOCIRepository: "v1", FluxKindHelmRepository: "v1",
				FluxKindHelmChart: "v1", FluxKindBucket: "v1",
				FluxKindAlert: "v1beta3", FluxKindProvider: "v1beta3", FluxKindReceiver: "v1",
				FluxKindImageRepository: "v1", FluxKindImagePolicy: "v1", FluxKindImageUpdateAutomation: "v1",
			},
		},
		{
			name: "only legacy versions",
			lists: []*metav1.APIResourceList{
				fluxRes("kustomize.toolkit.fluxcd.io/v1beta2", "kustomizations"),
				fluxRes("helm.toolkit.fluxcd.io/v2beta2", "helmreleases"),
				fluxRes("source.toolkit.fluxcd.io/v1beta2", "gitrepositories", "helmrepositories", "helmcharts"),
			},
			wantInstall: true, wantState: FluxStatusUnsupportedVersion,
			wantCtrl:     FluxControllers{Kustomize: true, Helm: true, Source: true},
			wantVersions: map[string]string{},
		},
		{
			name: "legacy and current versions are served together: the current one wins",
			lists: []*metav1.APIResourceList{
				fluxRes("kustomize.toolkit.fluxcd.io/v1beta2", "kustomizations"),
				fluxRes("kustomize.toolkit.fluxcd.io/v1", "kustomizations"),
			},
			wantInstall: true, wantState: FluxStatusOK,
			wantCtrl:     FluxControllers{Kustomize: true},
			wantVersions: map[string]string{FluxKindKustomization: "v1"},
		},
		{
			name:         "no CRDs at all",
			lists:        []*metav1.APIResourceList{fluxRes("v1", "pods"), fluxRes("argoproj.io/v1alpha1", "applications")},
			wantInstall:  false,
			wantVersions: map[string]string{},
		},
		{
			name:         "empty discovery",
			lists:        nil,
			wantVersions: map[string]string{},
		},
		{
			name: "subresource alone is not a CRD",
			lists: []*metav1.APIResourceList{
				fluxRes("kustomize.toolkit.fluxcd.io/v1", "kustomizations/status"),
			},
			wantVersions: map[string]string{},
		},
		{
			name: "Alert and Provider only in v1beta3",
			lists: []*metav1.APIResourceList{
				fluxRes("kustomize.toolkit.fluxcd.io/v1", "kustomizations"),
				fluxRes("notification.toolkit.fluxcd.io/v1beta3", "alerts", "providers"),
			},
			wantInstall: true, wantState: FluxStatusOK,
			wantCtrl: FluxControllers{Kustomize: true, Notification: true},
			wantVersions: map[string]string{
				FluxKindKustomization: "v1", FluxKindAlert: "v1beta3", FluxKindProvider: "v1beta3",
			},
		},
		{
			name: "Alert only in an older beta is unsupported",
			lists: []*metav1.APIResourceList{
				fluxRes("kustomize.toolkit.fluxcd.io/v1", "kustomizations"),
				fluxRes("notification.toolkit.fluxcd.io/v1beta2", "alerts", "providers"),
			},
			wantInstall: true, wantState: FluxStatusUnsupportedVersion,
			wantCtrl:     FluxControllers{Kustomize: true, Notification: true},
			wantVersions: map[string]string{FluxKindKustomization: "v1"},
		},
		{
			name: "image kinds in v1beta2",
			lists: []*metav1.APIResourceList{
				fluxRes("helm.toolkit.fluxcd.io/v2", "helmreleases"),
				fluxRes("image.toolkit.fluxcd.io/v1beta2", "imagerepositories", "imagepolicies", "imageupdateautomations"),
			},
			wantInstall: true, wantState: FluxStatusOK,
			wantCtrl: FluxControllers{Helm: true, Image: true},
			wantVersions: map[string]string{
				FluxKindHelmRelease:     "v2",
				FluxKindImageRepository: "v1beta2", FluxKindImagePolicy: "v1beta2", FluxKindImageUpdateAutomation: "v1beta2",
			},
		},
		{
			name: "Flux 2.3: Bucket and OCIRepository in v1beta2, Receiver in v1",
			lists: []*metav1.APIResourceList{
				fluxRes("kustomize.toolkit.fluxcd.io/v1", "kustomizations"),
				fluxRes("source.toolkit.fluxcd.io/v1", "gitrepositories", "helmrepositories", "helmcharts"),
				fluxRes("source.toolkit.fluxcd.io/v1beta2", "buckets", "ocirepositories"),
				fluxRes("notification.toolkit.fluxcd.io/v1", "receivers"),
			},
			wantInstall: true, wantState: FluxStatusOK,
			wantCtrl: FluxControllers{Kustomize: true, Source: true, Notification: true},
			wantVersions: map[string]string{
				FluxKindKustomization: "v1",
				FluxKindGitRepository: "v1", FluxKindHelmRepository: "v1", FluxKindHelmChart: "v1",
				FluxKindBucket: "v1beta2", FluxKindOCIRepository: "v1beta2",
				FluxKindReceiver: "v1",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fluxDiscover(newFluxDiscovery(tc.lists))
			if got.Installed != tc.wantInstall {
				t.Errorf("Installed = %v, want %v", got.Installed, tc.wantInstall)
			}
			if got.State != tc.wantState {
				t.Errorf("State = %q, want %q", got.State, tc.wantState)
			}
			if got.Controllers != tc.wantCtrl {
				t.Errorf("Controllers = %+v, want %+v", got.Controllers, tc.wantCtrl)
			}
			if got.Versions == nil {
				t.Fatal("Versions must never be nil: the frontend reads it as an object")
			}
			if !maps.Equal(got.Versions, tc.wantVersions) {
				t.Errorf("Versions = %v, want %v", got.Versions, tc.wantVersions)
			}
		})
	}
}

func TestFluxDiscoverPartialGroupFailureCountsAsSuccess(t *testing.T) {
	d := newFluxDiscovery(fluxFullInstall())
	// Недоступный aggregated API (например, metrics-server) не должен прятать Flux.
	d.err = &discovery.ErrGroupDiscoveryFailed{Groups: map[schema.GroupVersion]error{
		{Group: "metrics.k8s.io", Version: "v1beta1"}: errors.New("the server is currently unable to handle the request"),
	}}
	got := fluxDiscover(d)
	if !got.Installed || got.State != FluxStatusOK {
		t.Fatalf("partial group failure must keep Flux visible: %+v", got)
	}
	if got.Versions[FluxKindKustomization] != "v1" {
		t.Errorf("versions lost on partial failure: %v", got.Versions)
	}
}

func TestFluxDiscoverFailureMeansNotInstalledWithoutError(t *testing.T) {
	for name, err := range map[string]error{
		"cluster unreachable": errors.New("dial tcp 10.0.0.1:6443: connect: connection refused"),
		"forbidden":           errors.New("forbidden: User cannot list resource \"apiservices\""),
	} {
		t.Run(name, func(t *testing.T) {
			d := newFluxDiscovery(fluxFullInstall())
			d.err, d.dropData = err, true
			m, _ := fluxStatusManager(d)
			st, gotErr := m.GetFluxStatus()
			if gotErr != nil {
				t.Fatalf("GetFluxStatus returned an error: %v", gotErr)
			}
			if st.Installed || st.State != "" || st.Controllers != (FluxControllers{}) || st.Versions == nil || len(st.Versions) != 0 {
				t.Fatalf("expected a clean «not installed» status, got %+v", st)
			}
		})
	}
}

func TestGetFluxStatusUsesOnlyDiscovery(t *testing.T) {
	d := newFluxDiscovery(fluxFullInstall())
	m, _ := fluxStatusManager(d)
	st, err := m.GetFluxStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Installed || st.State != FluxStatusOK || st.Versions[FluxKindHelmRelease] != "v2" {
		t.Fatalf("unexpected status: %+v", st)
	}
	if n := d.calls.Load(); n != 1 {
		t.Errorf("discovery calls = %d, want 1", n)
	}
}

func TestGetFluxStatusNoActiveConnection(t *testing.T) {
	if _, err := NewManager().GetFluxStatus(); err == nil {
		t.Fatal("expected an error without an active connection")
	}
}

func TestFluxServedVersionsFromDiscovery(t *testing.T) {
	d := newFluxDiscovery(fluxFullInstall())
	_, conn := fluxStatusManager(d)
	got, err := fluxServedVersions(conn)
	if err != nil {
		t.Fatal(err)
	}
	if got[FluxKindKustomization] != "v1" || got[FluxKindAlert] != "v1beta3" || got[FluxKindReceiver] != "v1" {
		t.Errorf("versions = %v", got)
	}
	// Тот же кэш, что у GetFluxStatus: второй вызов discovery не делает.
	if _, err := fluxServedVersions(conn); err != nil {
		t.Fatal(err)
	}
	if n := d.calls.Load(); n != 1 {
		t.Errorf("discovery calls = %d, want 1", n)
	}
}

func TestFluxServedVersionsOmitsAbsentAndUnsupportedKinds(t *testing.T) {
	d := newFluxDiscovery([]*metav1.APIResourceList{
		fluxRes("kustomize.toolkit.fluxcd.io/v1", "kustomizations"),
		fluxRes("helm.toolkit.fluxcd.io/v2beta2", "helmreleases"),
	})
	_, conn := fluxStatusManager(d)
	got, err := fluxServedVersions(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(got, map[string]string{FluxKindKustomization: "v1"}) {
		t.Fatalf("versions = %v, want only Kustomization", got)
	}
	// Вид без обслуживаемой версии из allowlist: fluxList отдаёт errFluxVersionUnsupported.
	kind, _ := fluxKindByName(FluxKindHelmRelease)
	if kind.supports(got[FluxKindHelmRelease]) {
		t.Error("HelmRelease must not be considered supported")
	}
}

func TestFluxServedVersionsNilConnection(t *testing.T) {
	if _, err := fluxServedVersions(nil); !errors.Is(err, errFluxNotInstalled) {
		t.Fatalf("err = %v, want errFluxNotInstalled", err)
	}
}

func TestFluxStatusCacheTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newFluxDiscovery(fluxFullInstall())
		m, _ := fluxStatusManager(d)
		read := func() *FluxStatus {
			t.Helper()
			st, err := m.GetFluxStatus()
			if err != nil {
				t.Fatal(err)
			}
			return st
		}

		read()
		read()
		if n := d.calls.Load(); n != 1 {
			t.Fatalf("within the TTL: discovery calls = %d, want 1", n)
		}

		time.Sleep(fluxDiscoveryTTL - time.Second)
		read()
		if n := d.calls.Load(); n != 1 {
			t.Fatalf("just before the TTL: discovery calls = %d, want 1", n)
		}

		// После TTL discovery запрашивается заново и видит изменения кластера (Flux удалили).
		d.Resources = nil
		time.Sleep(2 * time.Second)
		st := read()
		if n := d.calls.Load(); n != 2 {
			t.Fatalf("after the TTL: discovery calls = %d, want 2", n)
		}
		if st.Installed {
			t.Errorf("status must reflect the new discovery result: %+v", st)
		}
	})
}

func TestFluxStatusCacheIsPerConnection(t *testing.T) {
	a := newFluxDiscovery(fluxFullInstall())
	b := newFluxDiscovery(nil)
	_, connA := fluxStatusManager(a)
	_, connB := fluxStatusManager(b)

	if st := fluxDiscoveryStatus(connA); !st.Installed {
		t.Errorf("connection A: %+v", st)
	}
	if st := fluxDiscoveryStatus(connB); st.Installed {
		t.Errorf("connection B must not see the cache of A: %+v", st)
	}
	if a.calls.Load() != 1 || b.calls.Load() != 1 {
		t.Errorf("calls: a=%d b=%d, want 1 each", a.calls.Load(), b.calls.Load())
	}
}

func TestFluxStatusCacheIsConcurrencySafe(t *testing.T) {
	d := newFluxDiscovery(fluxFullInstall())
	_, conn := fluxStatusManager(d)

	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			st := fluxDiscoveryStatus(conn)
			if !st.Installed {
				t.Errorf("unexpected status: %+v", st)
			}
			// Мутация копии не должна портить кэш.
			st.Versions["junk"] = "x"
			v, _ := fluxServedVersions(conn)
			v["junk2"] = "y"
		})
	}
	wg.Wait()

	if n := d.calls.Load(); n != 1 {
		t.Errorf("concurrent callers must share one discovery call, got %d", n)
	}
	if st := fluxDiscoveryStatus(conn); len(st.Versions) != 13 {
		t.Errorf("the cache was corrupted by callers, versions = %v", st.Versions)
	}
}

func TestFluxStatusWithoutDiscoveryClient(t *testing.T) {
	// Подключение без discovery (так устроены тесты соседних задач): не паника, «не установлен».
	m := managerWithBuiltConn("c", &ClusterConnection{Name: "c"})
	m.activeKey = "c"
	st, err := m.GetFluxStatus()
	if err != nil || st.Installed || st.Versions == nil {
		t.Fatalf("err=%v status=%+v", err, st)
	}
	// А fluxServedVersions отдаёт предпочтительные версии, чтобы fluxList работал без discovery (см. flux_list_test.go).
	v, err := fluxServedVersions(&ClusterConnection{})
	if err != nil || len(v) != len(fluxKinds) {
		t.Fatalf("err=%v versions=%v", err, v)
	}
}

// fluxRawDiscovery отдаёт списки ресурсов как есть, минуя fake: тот сам отклоняет некорректный GroupVersion.
type fluxRawDiscovery struct {
	*fake.FakeDiscovery
	lists []*metav1.APIResourceList
}

func (d fluxRawDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return nil, d.lists, nil
}

func TestFluxDiscoverToleratesGarbageInLists(t *testing.T) {
	got := fluxDiscover(fluxRawDiscovery{lists: []*metav1.APIResourceList{
		nil,
		fluxRes("a/b/c", "kustomizations"),
		fluxRes("", "kustomizations"),
		fluxRes("kustomize.toolkit.fluxcd.io/v1", "kustomizations"),
	}})
	if !got.Installed || got.State != FluxStatusOK || got.Versions[FluxKindKustomization] != "v1" {
		t.Fatalf("garbage entries must be skipped without hiding the valid ones: %+v", got)
	}
}
