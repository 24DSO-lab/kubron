// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fluxHR строит минимальный HelmRelease так, как его отдаёт GetFluxHelmReleases (значения по умолчанию уже
// подставлены: TargetNamespace — namespace самого объекта, ReleaseName — имя релиза Helm).
func fluxHR(ns, name, releaseName, targetNS string) FluxHelmReleaseInfo {
	return FluxHelmReleaseInfo{
		FluxObjectInfo:  FluxObjectInfo{Kind: "HelmRelease", Name: name, Namespace: ns},
		ReleaseName:     releaseName,
		TargetNamespace: targetNS,
	}
}

func ref(ns, name string) *FluxRef {
	return &FluxRef{Kind: "HelmRelease", Namespace: ns, Name: name}
}

func TestMarkFluxManaged(t *testing.T) {
	tests := []struct {
		name     string
		releases []HelmReleaseInfo
		hrs      []FluxHelmReleaseInfo
		want     []*FluxRef // по порядку releases; nil — не под управлением Flux
	}{
		{
			name:     "совпадение по имени релиза и namespace",
			releases: []HelmReleaseInfo{{Name: "podinfo", Namespace: "apps"}},
			hrs:      []FluxHelmReleaseInfo{fluxHR("apps", "podinfo", "podinfo", "apps")},
			want:     []*FluxRef{ref("apps", "podinfo")},
		},
		{
			name:     "релиз с тем же именем в другом namespace не помечается",
			releases: []HelmReleaseInfo{{Name: "podinfo", Namespace: "other"}},
			hrs:      []FluxHelmReleaseInfo{fluxHR("apps", "podinfo", "podinfo", "apps")},
			want:     []*FluxRef{nil},
		},
		{
			name:     "релиз с другим именем не помечается",
			releases: []HelmReleaseInfo{{Name: "redis", Namespace: "apps"}},
			hrs:      []FluxHelmReleaseInfo{fluxHR("apps", "podinfo", "podinfo", "apps")},
			want:     []*FluxRef{nil},
		},
		{
			// HelmRelease лежит в flux-system, а релиз Helm создан в targetNamespace с именем из spec.releaseName.
			name:     "targetNamespace и spec.releaseName",
			releases: []HelmReleaseInfo{{Name: "ingress", Namespace: "ingress-nginx"}},
			hrs:      []FluxHelmReleaseInfo{fluxHR("flux-system", "nginx", "ingress", "ingress-nginx")},
			want:     []*FluxRef{ref("flux-system", "nginx")},
		},
		{
			name:     "пустые ReleaseName и TargetNamespace заменяются именем и namespace объекта",
			releases: []HelmReleaseInfo{{Name: "podinfo", Namespace: "apps"}},
			hrs:      []FluxHelmReleaseInfo{fluxHR("apps", "podinfo", "", "")},
			want:     []*FluxRef{ref("apps", "podinfo")},
		},
		{
			name:     "HelmRelease с kubeConfig: релиз живёт в другом кластере и локально не совпадает",
			releases: []HelmReleaseInfo{{Name: "podinfo", Namespace: "apps"}},
			hrs: []FluxHelmReleaseInfo{func() FluxHelmReleaseInfo {
				h := fluxHR("apps", "podinfo", "podinfo", "apps")
				h.Remote = true
				return h
			}()},
			want: []*FluxRef{nil},
		},
		{
			name:     "приостановленный HelmRelease остаётся управляющим",
			releases: []HelmReleaseInfo{{Name: "podinfo", Namespace: "apps"}},
			hrs: []FluxHelmReleaseInfo{func() FluxHelmReleaseInfo {
				h := fluxHR("apps", "podinfo", "podinfo", "apps")
				h.Suspended = true
				return h
			}()},
			want: []*FluxRef{ref("apps", "podinfo")},
		},
		{
			name: "несколько релизов: помечаются только управляемые",
			releases: []HelmReleaseInfo{
				{Name: "a", Namespace: "x"},
				{Name: "b", Namespace: "x"},
				{Name: "c", Namespace: "y"},
			},
			hrs: []FluxHelmReleaseInfo{
				fluxHR("x", "a", "a", "x"),
				fluxHR("y", "c", "c", "y"),
			},
			want: []*FluxRef{ref("x", "a"), nil, ref("y", "c")},
		},
		{
			// Два HelmRelease на один релиз — ошибка конфигурации, но результат должен быть детерминированным.
			name:     "дубликаты разрешаются по namespace и имени объекта",
			releases: []HelmReleaseInfo{{Name: "app", Namespace: "t"}},
			hrs: []FluxHelmReleaseInfo{
				fluxHR("z-ns", "app", "app", "t"),
				fluxHR("a-ns", "app", "app", "t"),
			},
			want: []*FluxRef{ref("a-ns", "app")},
		},
		{
			name:     "пустой список HelmRelease",
			releases: []HelmReleaseInfo{{Name: "podinfo", Namespace: "apps"}},
			hrs:      nil,
			want:     []*FluxRef{nil},
		},
		{
			name:     "пустое имя HelmRelease игнорируется",
			releases: []HelmReleaseInfo{{Name: "", Namespace: ""}},
			hrs:      []FluxHelmReleaseInfo{fluxHR("", "", "", "")},
			want:     []*FluxRef{nil},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			markFluxManaged(tc.releases, tc.hrs)
			for i, want := range tc.want {
				got := tc.releases[i].FluxManagedBy
				switch {
				case want == nil && got != nil:
					t.Errorf("release %d: помечен %+v, ожидали nil", i, *got)
				case want != nil && got == nil:
					t.Errorf("release %d: не помечен, ожидали %+v", i, *want)
				case want != nil && *got != *want:
					t.Errorf("release %d: got %+v, want %+v", i, *got, *want)
				}
			}
		})
	}
}

// Метка не должна ссылаться на элемент входного среза: изменение HelmRelease после разметки не трогает релиз.
func TestMarkFluxManagedCopiesRef(t *testing.T) {
	releases := []HelmReleaseInfo{{Name: "a", Namespace: "x"}, {Name: "a", Namespace: "y"}}
	hrs := []FluxHelmReleaseInfo{fluxHR("x", "a", "a", "x"), fluxHR("y", "a", "a", "y")}
	markFluxManaged(releases, hrs)
	if releases[0].FluxManagedBy == releases[1].FluxManagedBy {
		t.Fatal("релизы делят один указатель на FluxRef")
	}
	hrs[0].Name = "changed"
	if releases[0].FluxManagedBy.Name != "a" {
		t.Errorf("метка изменилась вслед за входными данными: %+v", *releases[0].FluxManagedBy)
	}
}

// fluxHelmManager — Manager с discovery, у которого есть только discovery (Client и DynamicClient равны nil):
// любое чтение объектов Flux мимо переданного lister'а обрушит тест.
func fluxHelmManager(t *testing.T, lists []*metav1.APIResourceList) (*Manager, *fluxCountingDiscovery) {
	t.Helper()
	d := newFluxDiscovery(lists)
	m, _ := fluxStatusManager(d)
	return m, d
}

// countingLister — подмена GetFluxHelmReleases: считает вызовы и отдаёт заданный результат.
type countingLister struct {
	calls int
	hrs   []FluxHelmReleaseInfo
	err   error
}

func (l *countingLister) list(namespaces []string) ([]FluxHelmReleaseInfo, error) {
	l.calls++
	return l.hrs, l.err
}

func TestMarkFluxManagedHelmWithFlux(t *testing.T) {
	m, _ := fluxHelmManager(t, fluxFullInstall())
	l := &countingLister{hrs: []FluxHelmReleaseInfo{fluxHR("apps", "podinfo", "podinfo", "apps")}}
	releases := []HelmReleaseInfo{{Name: "podinfo", Namespace: "apps"}, {Name: "redis", Namespace: "apps"}}

	m.markFluxManagedHelmWith(releases, l.list)

	if l.calls != 1 {
		t.Fatalf("HelmRelease читались %d раз, ожидали 1", l.calls)
	}
	if got := releases[0].FluxManagedBy; got == nil || *got != *ref("apps", "podinfo") {
		t.Errorf("podinfo: got %+v", got)
	}
	if releases[1].FluxManagedBy != nil {
		t.Errorf("redis помечен без HelmRelease: %+v", *releases[1].FluxManagedBy)
	}
}

// Flux не установлен: никаких запросов объектов Flux, а повторные вызовы не обходят discovery заново (TTL).
func TestMarkFluxManagedHelmNoFluxNoRequests(t *testing.T) {
	noFlux := []*metav1.APIResourceList{
		fluxRes("v1", "pods", "services"),
		fluxRes("argoproj.io/v1alpha1", "applications"),
	}
	m, d := fluxHelmManager(t, noFlux)
	l := &countingLister{err: errors.New("lister не должен вызываться")}
	releases := []HelmReleaseInfo{{Name: "podinfo", Namespace: "apps"}}

	for range 5 {
		m.markFluxManagedHelmWith(releases, l.list)
	}

	if l.calls != 0 {
		t.Fatalf("без Flux прочитано HelmRelease: %d вызовов", l.calls)
	}
	if releases[0].FluxManagedBy != nil {
		t.Errorf("релиз помечен при отсутствии Flux: %+v", *releases[0].FluxManagedBy)
	}
	if got := d.calls.Load(); got != 1 {
		t.Errorf("discovery вызван %d раз за 5 обращений, ожидали 1 (кэш по TTL)", got)
	}
}

// Частичная установка без helm-controller: HelmRelease быть не может, читать нечего.
func TestMarkFluxManagedHelmWithoutHelmController(t *testing.T) {
	m, _ := fluxHelmManager(t, []*metav1.APIResourceList{
		fluxRes("kustomize.toolkit.fluxcd.io/v1", "kustomizations"),
		fluxRes("source.toolkit.fluxcd.io/v1", "gitrepositories"),
	})
	l := &countingLister{err: errors.New("lister не должен вызываться")}

	m.markFluxManagedHelmWith([]HelmReleaseInfo{{Name: "a", Namespace: "b"}}, l.list)

	if l.calls != 0 {
		t.Fatalf("HelmRelease читались без helm-controller: %d вызовов", l.calls)
	}
}

// CRD есть, но обслуживаются только устаревшие версии: объекты не разбираются (ADR-0023, раздел 3).
func TestMarkFluxManagedHelmUnsupportedVersion(t *testing.T) {
	m, _ := fluxHelmManager(t, []*metav1.APIResourceList{
		fluxRes("helm.toolkit.fluxcd.io/v2beta1", "helmreleases"),
	})
	l := &countingLister{err: errors.New("lister не должен вызываться")}

	m.markFluxManagedHelmWith([]HelmReleaseInfo{{Name: "a", Namespace: "b"}}, l.list)

	if l.calls != 0 {
		t.Fatalf("HelmRelease читались при неподдерживаемой версии API: %d вызовов", l.calls)
	}
}

// Нет релизов Helm — нечего размечать, даже discovery не нужен.
func TestMarkFluxManagedHelmNoReleases(t *testing.T) {
	m, d := fluxHelmManager(t, fluxFullInstall())
	l := &countingLister{}

	m.markFluxManagedHelmWith(nil, l.list)

	if l.calls != 0 || d.calls.Load() != 0 {
		t.Fatalf("без релизов выполнены запросы: lister=%d discovery=%d", l.calls, d.calls.Load())
	}
}

// Метка — дополнение к списку Helm: ошибка чтения Flux (нет прав list на helmreleases, таймаут) не ломает список.
func TestMarkFluxManagedHelmListError(t *testing.T) {
	m, _ := fluxHelmManager(t, fluxFullInstall())
	l := &countingLister{err: errors.New("forbidden")}
	releases := []HelmReleaseInfo{{Name: "podinfo", Namespace: "apps"}}

	m.markFluxManagedHelmWith(releases, l.list)

	if l.calls != 1 {
		t.Fatalf("ожидали одну попытку чтения, получили %d", l.calls)
	}
	if releases[0].FluxManagedBy != nil {
		t.Errorf("релиз помечен при ошибке чтения: %+v", *releases[0].FluxManagedBy)
	}
}

// Нет активного подключения: метка пропускается без паники.
func TestMarkFluxManagedHelmNoConnection(t *testing.T) {
	m := NewManager()
	l := &countingLister{}
	m.markFluxManagedHelmWith([]HelmReleaseInfo{{Name: "a", Namespace: "b"}}, l.list)
	if l.calls != 0 {
		t.Fatalf("lister вызван без подключения: %d", l.calls)
	}
}

// Формат для фронтенда: поле fluxManagedBy есть всегда (null без Flux), в нём только вид, namespace и имя.
func TestHelmReleaseInfoFluxJSON(t *testing.T) {
	plain, err := json.Marshal(HelmReleaseInfo{Name: "a", Namespace: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plain), `"fluxManagedBy":null`) {
		t.Errorf("релиз без Flux должен отдавать fluxManagedBy:null, получили %s", plain)
	}

	managed, err := json.Marshal(HelmReleaseInfo{Name: "a", Namespace: "b", FluxManagedBy: ref("flux-system", "a")})
	if err != nil {
		t.Fatal(err)
	}
	want := `"fluxManagedBy":{"kind":"HelmRelease","namespace":"flux-system","name":"a"}`
	if !strings.Contains(string(managed), want) {
		t.Errorf("fluxManagedBy: ожидали %s в %s", want, managed)
	}
}

// Контрольные строки в полях HelmRelease, которые метке не нужны (сообщения контроллера, чарт, условия, секреты
// источника), не должны попасть в ответ Helm: из объекта Flux берутся только вид, namespace и имя.
func TestHelmReleaseInfoFluxNoSecretLeak(t *testing.T) {
	m, _ := fluxHelmManager(t, fluxFullInstall())

	hr := fluxHR("apps", "podinfo", "podinfo", "apps")
	hr.Message = "S3CRET-message"
	hr.Chart = "S3CRET-chart"
	hr.ChartVersion = "S3CRET-chart-version"
	hr.Revision = "S3CRET-revision"
	hr.LastAttemptedRevision = "S3CRET-attempted"
	hr.SourceRef = &FluxRef{Kind: "HelmRepository", Namespace: "apps", Name: "S3CRET-source"}
	hr.ChartRef = &FluxRef{Kind: "OCIRepository", Namespace: "apps", Name: "S3CRET-chartref"}
	hr.StorageNamespace = "S3CRET-storage"
	hr.Conditions = []ConditionInfo{{Type: "Ready", Status: "False", Message: "S3CRET-condition"}}
	hr.History = []FluxHelmHistoryEntry{{ChartName: "S3CRET-history"}}
	l := &countingLister{hrs: []FluxHelmReleaseInfo{hr}}

	releases := []HelmReleaseInfo{{Name: "podinfo", Namespace: "apps"}}
	m.markFluxManagedHelmWith(releases, l.list)

	if releases[0].FluxManagedBy == nil {
		t.Fatal("релиз не помечен: тест не проверяет утечку")
	}
	out, err := json.Marshal(releases)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "S3CRET") {
		t.Fatalf("в ответе Helm есть данные HelmRelease: %s", out)
	}
}
