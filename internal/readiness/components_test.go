// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/24DSO-lab/kubron/internal/readiness"
	"github.com/24DSO-lab/kubron/internal/readiness/catalog"
)

func put(f *fakeReader, kind, ns string, items ...unstructured.Unstructured) {
	a := f.lists[listKey("apps", kind, ns)]
	a.items = append(a.items, items...)
	f.lists[listKey("apps", kind, ns)] = a
}

// componentCluster — кластер 1.34 с компонентами каждого статуса.
func componentCluster(t *testing.T) *fakeReader {
	t.Helper()
	f := newFake("v1.34.3")
	f.set("", "Node", "", listAnswer{items: []unstructured.Unstructured{
		node("n1", "v1.34.3", true), node("n2", "v1.33.1", true), node("n3", "v1.34.3", false),
	}})
	put(f, "Deployment", "kube-system",
		workload("Deployment", "kube-system", "metrics-server", "registry.k8s.io/metrics-server/metrics-server:v0.8.0", 2, 2), // подтверждено правилом
		workload("Deployment", "kube-system", "coredns", "registry.k8s.io/coredns/coredns:v1.12.0", 2, 2),                     // правил нет
		workload("Deployment", "kube-system", "my-addon", "registry.example.com/team/addon:1.0", 1, 1),                        // вне каталога
	)
	put(f, "DaemonSet", "kube-system",
		workload("DaemonSet", "kube-system", "kube-proxy", "registry.k8s.io/kube-proxy:v1.34.3", 3, 3),
	)
	put(f, "Deployment", "ingress-nginx",
		workload("Deployment", "ingress-nginx", "ingress-nginx-controller", "registry.k8s.io/ingress-nginx/controller:v1.12.1@sha256:abcdef", 2, 1), // нет правила для 1.37, не готов
	)
	// Нагрузка в пользовательском пространстве имён не рассматривается.
	put(f, "Deployment", "shop", workload("Deployment", "shop", "frontend", "registry.example.com/shop/front:1", 1, 1))
	return f
}

func TestComponentStatuses(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	rep, err := svc.Check(t.Context(), req(componentCluster(t), "1.36"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		key      string
		status   readiness.Status
		severity readiness.Severity
		version  string
	}{
		{"component/metrics-server/kube-system/deployment/metrics-server", readiness.StatusConfirmed, readiness.SeverityInfo, "0.8.0"},
		{"component/coredns/kube-system/deployment/coredns", readiness.StatusInsufficient, readiness.SeverityWarning, "1.12.0"},
		{"component/unrecognized/kube-system/deployment/my-addon", readiness.StatusNotRecognized, readiness.SeverityInfo, ""},
		{"component/kube-proxy/kube-system/daemonset/kube-proxy", readiness.StatusConfirmed, readiness.SeverityInfo, "1.34.3"},
		// ingress-nginx 1.12 с Kubernetes 1.36: в документе проекта такой комбинации нет — это не «совместимо».
		{"component/ingress-nginx/ingress-nginx/deployment/ingress-nginx-controller", readiness.StatusInsufficient, readiness.SeverityWarning, "1.12.1"},
	}
	for _, tt := range tests {
		f := find(rep, tt.key)
		if f == nil {
			t.Errorf("no finding %s; have %v", tt.key, keysOf(rep))
			continue
		}
		if f.Status != tt.status || f.Severity != tt.severity {
			t.Errorf("%s: %s/%s, want %s/%s", tt.key, f.Status, f.Severity, tt.status, tt.severity)
		}
		d := f.Component
		if d.Version != tt.version || d.TargetVersion != "1.36" {
			t.Errorf("%s: version=%q target=%q", tt.key, d.Version, d.TargetVersion)
		}
		if f.Status != readiness.StatusNotRecognized && (d.DocsURL == "" || d.CheckedAt == "") {
			t.Errorf("%s: the compatibility source and date must be visible", tt.key)
		}
	}
	if find(rep, "component/unrecognized/shop/deployment/frontend") != nil {
		t.Error("user namespaces must not be inspected")
	}
	// Версия образа с digest не тянет digest в отчёт.
	ing := find(rep, "component/ingress-nginx/ingress-nginx/deployment/ingress-nginx-controller")
	if strings.Contains(ing.Component.Image, "sha256") || ing.Component.Image != "registry.k8s.io/ingress-nginx/controller:v1.12.1" {
		t.Errorf("image = %q", ing.Component.Image)
	}
	// Неготовый компонент не выглядит безобидным, даже если версия подходит.
	if ing.Component.State != readiness.StateDegraded || ing.Component.Ready != 1 || ing.Component.Desired != 2 {
		t.Errorf("state = %+v", ing.Component)
	}
	// Замечание каталога о закрытии проекта показывается один раз.
	adv := find(rep, "component/ingress-nginx/advisory/ingress-nginx.retired")
	if adv == nil || adv.Severity != readiness.SeverityWarning || adv.Component.NoteURL == "" {
		t.Errorf("advisory = %+v", adv)
	}
}

// Неизвестная комбинация версий никогда не становится «совместимо» (ADR-0025 §4).
func TestUnknownCombinationIsNeverConfirmed(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	f := newFake("v1.34.3")
	put(f, "Deployment", "kube-system",
		workload("Deployment", "kube-system", "metrics-server", "registry.k8s.io/metrics-server/metrics-server:latest", 1, 1),
		workload("Deployment", "kube-system", "coredns", "registry.k8s.io/coredns/coredns@sha256:abc", 1, 1),
	)
	for _, target := range []string{"1.35", "1.36", "1.37"} {
		rep, err := svc.Check(t.Context(), req(f, target))
		if err != nil {
			t.Fatal(err)
		}
		for _, fd := range rep.Findings {
			if fd.Type == readiness.TypeComponent && fd.Status == readiness.StatusConfirmed {
				t.Errorf("target %s: %s was confirmed without a known version", target, fd.Key)
			}
		}
		ms := find(rep, "component/metrics-server/kube-system/deployment/metrics-server")
		if ms.Status != readiness.StatusInsufficient || ms.Component.NoteCode != catalog.CodeVersionUnknown || ms.Component.Version != "latest" {
			t.Errorf("metrics-server: %+v", ms.Component)
		}
	}
}

func TestKubeletSkew(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	tests := []struct {
		name   string
		nodes  []unstructured.Unstructured
		target string
		status readiness.Status
		sev    readiness.Severity
		ver    string
	}{
		{"within policy", []unstructured.Unstructured{node("a", "v1.34.1", true), node("b", "v1.33.0", true)}, "1.35", readiness.StatusConfirmed, readiness.SeverityInfo, "1.33–1.34"},
		{"three behind is allowed", []unstructured.Unstructured{node("a", "v1.33.1", true)}, "1.36", readiness.StatusConfirmed, readiness.SeverityInfo, "1.33"},
		{"four behind blocks", []unstructured.Unstructured{node("a", "v1.32.1", true)}, "1.36", readiness.StatusKnownLimitation, readiness.SeverityBlocker, "1.32"},
		{"equal to target", []unstructured.Unstructured{node("a", "v1.35.1", true)}, "1.35", readiness.StatusConfirmed, readiness.SeverityInfo, "1.35"},
		{"one unparseable node", []unstructured.Unstructured{node("a", "v1.34.1", true), node("b", "weird", true)}, "1.35", readiness.StatusInsufficient, readiness.SeverityWarning, "1.34"},
		{"all unparseable", []unstructured.Unstructured{node("a", "weird", true)}, "1.35", readiness.StatusInsufficient, readiness.SeverityWarning, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFake("v1.34.3")
			f.set("", "Node", "", listAnswer{items: tt.nodes})
			rep, err := svc.Check(t.Context(), req(f, tt.target))
			if err != nil {
				t.Fatal(err)
			}
			k := find(rep, "component/kubelet")
			if k == nil {
				t.Fatalf("no kubelet finding: %v", keysOf(rep))
			}
			if k.Status != tt.status || k.Severity != tt.sev || k.Component.Version != tt.ver || k.Component.NodeCount != len(tt.nodes) {
				t.Errorf("finding = %s/%s %+v", k.Status, k.Severity, k.Component)
			}
		})
	}
	// Узел новее целевой версии control plane — нарушение version skew.
	f := newFake("v1.36.1")
	f.set("", "Node", "", listAnswer{items: []unstructured.Unstructured{node("a", "v1.37.0", true)}})
	rep, _ := svc.Check(t.Context(), req(f, "1.36"))
	if k := find(rep, "component/kubelet"); k.Status != readiness.StatusKnownLimitation || k.Severity != readiness.SeverityBlocker || k.Component.NoteCode != catalog.CodeSkewNewer {
		t.Errorf("kubelet newer than the target: %+v", k)
	}
}

func TestNodeStateFromReadyCondition(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	for _, tt := range []struct {
		ready []bool
		state string
	}{
		{[]bool{true, true}, readiness.StateReady},
		{[]bool{true, false}, readiness.StateDegraded},
		{[]bool{false, false}, readiness.StateUnavailable},
	} {
		f := newFake("v1.34.3")
		var nodes []unstructured.Unstructured
		for i, r := range tt.ready {
			nodes = append(nodes, node(string(rune('a'+i)), "v1.34.1", r))
		}
		f.set("", "Node", "", listAnswer{items: nodes})
		rep, _ := svc.Check(t.Context(), req(f, "1.35"))
		k := find(rep, "component/kubelet")
		if k.Component.State != tt.state {
			t.Errorf("ready=%v: state = %s, want %s", tt.ready, k.Component.State, tt.state)
		}
		if tt.state != readiness.StateReady && k.Severity.Rank() > readiness.SeverityWarning.Rank() {
			t.Errorf("a node problem must be at least a warning: %s", k.Severity)
		}
	}
}

func TestUnavailableComponentSourcesAreNotChecked(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	f := newFake("v1.34.3")
	f.set("", "Node", "", listAnswer{err: readiness.ErrSourceUnavailable})
	f.set("apps", "Deployment", "kube-system", listAnswer{err: readiness.ErrSourceForbidden})
	f.set("apps", "DaemonSet", "kube-system", listAnswer{truncated: true})
	rep, err := svc.Check(t.Context(), req(f, "1.36"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"source/nodes-unavailable", "source/workloads-unavailable"} {
		if fd := find(rep, key); fd == nil || fd.Status != readiness.StatusNotChecked || fd.Severity != readiness.SeverityWarning {
			t.Errorf("%s = %+v", key, fd)
		}
	}
	if fd := find(rep, "source/workloads-truncated"); fd == nil || fd.Status != readiness.StatusPartial {
		t.Errorf("truncated = %+v", fd)
	}
	if fd := find(rep, "component/kubelet"); fd != nil {
		t.Errorf("kubelet must not be reported when nodes were not read: %+v", fd)
	}
	live, _ := rep.Coverage.Source(readiness.SourceLiveAPI)
	if !live.Partial || slices.Contains(live.Methods, readiness.MethodNodeInfo) {
		t.Errorf("live = %+v", live)
	}
}

func TestImageIsScrubbed(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	f := newFake("v1.34.3")
	put(f, "Deployment", "kube-system",
		workload("Deployment", "kube-system", "odd", "https://deploy:s3cretPassw0rd@registry.example.com/team/odd:2", 1, 1),
	)
	rep, err := svc.Check(t.Context(), req(f, "1.36"))
	if err != nil {
		t.Fatal(err)
	}
	fd := find(rep, "component/unrecognized/kube-system/deployment/odd")
	if fd == nil || strings.Contains(fd.Component.Image, "s3cret") || strings.Contains(fd.Component.Image, "deploy") || fd.Component.Image != "registry.example.com/team/odd:2" {
		t.Fatalf("image = %+v", fd)
	}
}

// В отчёт не попадают значения из объектов, кроме перечисленных полей: env, аннотации, лейблы остаются в кластере.
// Secret не читается ни при каких условиях.
func TestNoSensitiveDataAndNoSecretReads(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	f := componentCluster(t)
	rep, err := svc.Run(t.Context(), req(f, "1.37"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "Secret") {
			t.Errorf("a Secret was read: %s", c)
		}
	}
	raw, _ := json.Marshal(rep)
	for _, secret := range []string{"hunter2-secret-value", "TOKEN"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("report contains %q", secret)
		}
	}
	md, err := readiness.ExportMarkdown(rep)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(md), "hunter2-secret-value") {
		t.Error("export contains a secret value")
	}
}
