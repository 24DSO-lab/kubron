// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation_test

import (
	"strconv"
	"testing"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/depgraph/graphtest"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// cmDep — Deployment из двух Pod'ов, ссылающихся на общий ConfigMap.
type cmDep struct {
	ns, name string
	agoMin   int
	optional bool
}

func (d cmDep) namespace() string {
	if d.ns == "" {
		return "prod"
	}
	return d.ns
}

func (d cmDep) pod() string { return d.name + "-h" + d.name + "-0" }

// cmScene строит мир и контекст расследования Pod'а первого Deployment: граф знает только его и отсутствующий
// ConfigMap, остальных потребителей находят живые чтения.
func cmScene(t *testing.T, cm string, deps []cmDep) (*graphtest.FakeReader, *diagnose.Context) {
	t.Helper()
	w := &world{}
	for _, d := range deps {
		w.cmDeployment(d.namespace(), d.name, cm, 2, d.agoMin, d.optional)
	}
	f := w.reader(t)
	first := deps[0]
	ns := first.namespace()
	m := newManual(t, f, depgraph.Ref{Kind: "Pod", Namespace: ns, Name: first.pod()})
	p := m.pod(ns, first.pod())
	cmRef := depgraph.Ref{Kind: "ConfigMap", Namespace: ns, Name: cm}
	m.missing(cmRef)
	m.edge(p.Ref, cmRef, depgraph.EdgeMounts, depgraph.EdgeMissing)
	return f, m.ctx(diagnose.ResourceRef{Kind: "Pod", Namespace: ns, Name: first.pod()}, first.pod())
}

func TestSharedDependencyMissingConfigMap(t *testing.T) {
	tests := []struct {
		name      string
		deps      []cmDep
		want      diagnose.Confidence // "" — кандидата нет
		wantTotal int
		wantFacts []string
		noFacts   []string
	}{
		{"high: три нагрузки, сбои в окне", []cmDep{{name: "a", agoMin: 10}, {name: "b", agoMin: 12}, {name: "c", agoMin: 15}},
			diagnose.ConfidenceHigh, 3,
			[]string{"correlation.shared_dependency.consumers", "correlation.shared_dependency.missing",
				"correlation.shared_dependency.temporal", "correlation.shared_dependency.multi_workload"}, nil},
		{"medium: две нагрузки, сбои в окне", []cmDep{{name: "a", agoMin: 10}, {name: "b", agoMin: 12}},
			diagnose.ConfidenceMedium, 2,
			[]string{"correlation.shared_dependency.consumers", "correlation.shared_dependency.missing", "correlation.shared_dependency.temporal"},
			[]string{"correlation.shared_dependency.multi_workload"}},
		{"medium: три нагрузки, сбои разнесены дольше окна", []cmDep{{name: "a", agoMin: 10}, {name: "b", agoMin: 200}, {name: "c", agoMin: 300}},
			diagnose.ConfidenceMedium, 3,
			[]string{"correlation.shared_dependency.missing", "correlation.shared_dependency.multi_workload"},
			[]string{"correlation.shared_dependency.temporal"}},
		{"нет кандидата: одна нагрузка", []cmDep{{name: "a", agoMin: 10}}, "", 0, nil, nil},
		{"optional-ссылка потребителем не считается", []cmDep{{name: "a", agoMin: 10}, {name: "b", agoMin: 12}, {name: "c", agoMin: 14, optional: true}},
			diagnose.ConfidenceMedium, 2, nil, nil},
		{"нагрузка другого namespace не считается", []cmDep{{name: "a", agoMin: 10}, {ns: "stage", name: "b", agoMin: 12}}, "", 0, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, c := cmScene(t, "app-cfg", tt.deps)
			res, reads := runCtx(t, f, c)
			if reads > correlation.MaxExtraReads {
				t.Fatalf("чтений %d, предел %d", reads, correlation.MaxExtraReads)
			}
			noSecretReads(t, f)
			got := byKind(res, correlation.KindSharedDependency)
			if tt.want == "" {
				if got != nil {
					t.Fatalf("ожидался пустой результат, получен %+v", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("кандидата нет; candidates=%+v missing=%+v", res.Candidates, res.Missing)
			}
			if got.Confidence != tt.want || got.Total != tt.wantTotal {
				t.Fatalf("confidence=%s total=%d, want %s/%d", got.Confidence, got.Total, tt.want, tt.wantTotal)
			}
			if k := got.Key(); k != "shared_dependency:core/ConfigMap/prod/app-cfg" {
				t.Fatalf("Key = %q", k)
			}
			if len(got.Affected) != tt.wantTotal || got.Truncated {
				t.Fatalf("affected=%d truncated=%v", len(got.Affected), got.Truncated)
			}
			codes := factCodes(res, got)
			for _, want := range tt.wantFacts {
				if !hasCode(codes, want) {
					t.Fatalf("нет факта %s: %v", want, codes)
				}
			}
			for _, bad := range tt.noFacts {
				if hasCode(codes, bad) {
					t.Fatalf("лишний факт %s: %v", bad, codes)
				}
			}
			if got.Since.IsZero() {
				t.Fatal("Since не заполнен")
			}
		})
	}
}

func TestSharedDependencyConsumersCounts(t *testing.T) {
	f, c := cmScene(t, "app-cfg", []cmDep{{name: "a", agoMin: 10}, {name: "b", agoMin: 12}, {name: "c", agoMin: 15}})
	res, _ := runCtx(t, f, c)
	got := byKind(res, correlation.KindSharedDependency)
	if got == nil {
		t.Fatal("кандидата нет")
	}
	if w, p := factParam(t, res, got, "correlation.shared_dependency.consumers", "workloads"),
		factParam(t, res, got, "correlation.shared_dependency.consumers", "pods"); w != "3" || p != "6" {
		t.Fatalf("workloads=%s pods=%s, want 3/6", w, p)
	}
	for _, a := range got.Affected {
		if a.Kind != "Deployment" || a.ProblemPods != 2 {
			t.Fatalf("затронутая нагрузка: %+v", a)
		}
	}
}

// Здоровый Pod, который тоже ссылается на ConfigMap, — потенциально затронутая нагрузка, а не затронутая.
func TestSharedDependencyPotential(t *testing.T) {
	w := &world{}
	w.cmDeployment("prod", "a", "app-cfg", 1, 10, false)
	w.cmDeployment("prod", "b", "app-cfg", 1, 12, false)
	w.add("apiVersion: v1\nkind: Pod\nmetadata: {name: old, namespace: prod}\nspec:\n containers:\n - {name: app, image: img, envFrom: [{configMapRef: {name: app-cfg}}]}\n"+
		"status:\n phase: Running\n conditions:\n - {type: Ready, status: \"True\", lastTransitionTime: \"%s\"}", ts(600))
	f := w.reader(t)
	m := newManual(t, f, depgraph.Ref{Kind: "Pod", Namespace: "prod", Name: "a-ha-0"})
	p := m.pod("prod", "a-ha-0")
	cm := depgraph.Ref{Kind: "ConfigMap", Namespace: "prod", Name: "app-cfg"}
	m.missing(cm)
	m.edge(p.Ref, cm, depgraph.EdgeMounts, depgraph.EdgeMissing)
	res, _ := runCtx(t, f, m.ctx(diagnose.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "a-ha-0"}, "a-ha-0"))
	got := byKind(res, correlation.KindSharedDependency)
	if got == nil || got.Total != 2 {
		t.Fatalf("кандидат: %+v", got)
	}
	if len(got.Potential) != 1 || got.Potential[0].Kind != "Pod" || got.Potential[0].Name != "old" {
		t.Fatalf("potential: %+v", got.Potential)
	}
}

// ingressScene — Ingress'ы одного namespace, граф знает только первый из них и источник.
func ingressScene(t *testing.T, w *world, src depgraph.Node, build func(m *manual, first depgraph.Node), target diagnose.ResourceRef) (*graphtest.FakeReader, *diagnose.Context) {
	t.Helper()
	f := w.reader(t)
	m := newManual(t, f, depgraph.Ref{Group: "networking.k8s.io", Kind: "Ingress", Namespace: "prod", Name: "i1"})
	first := m.node(depgraph.Ref{Group: "networking.k8s.io", Kind: "Ingress", Namespace: "prod", Name: "i1"}, depgraph.Healthy, "")
	build(m, first)
	_ = src
	return f, m.ctx(target)
}

func TestSharedDependencyServiceWithoutEndpoints(t *testing.T) {
	tests := []struct {
		name      string
		ingresses []string // backend каждого Ingress: i1, i2, …
		want      diagnose.Confidence
		wantTotal int
		wantMulti bool
	}{
		{"medium: два Ingress на один Service", []string{"api", "api"}, diagnose.ConfidenceMedium, 2, false},
		{"medium: три Ingress, временной связи нет", []string{"api", "api", "api"}, diagnose.ConfidenceMedium, 3, true},
		{"нет кандидата: Ingress на Service один", []string{"api", "other"}, "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &world{}
			for i, be := range tt.ingresses {
				w.ingress("prod", "i"+itoa(i+1), "nginx", be)
			}
			svc := depgraph.Ref{Kind: "Service", Namespace: "prod", Name: "api"}
			f, c := ingressScene(t, w, depgraph.Node{}, func(m *manual, first depgraph.Node) {
				m.node(svc, depgraph.Degraded, depgraph.ReasonNoReadyEndpoints)
				m.edge(first.Ref, svc, depgraph.EdgeRoutes, depgraph.EdgeOK)
			}, diagnose.ResourceRef{Group: "networking.k8s.io", Kind: "Ingress", Namespace: "prod", Name: "i1"})
			res, reads := runCtx(t, f, c)
			if reads > correlation.MaxExtraReads {
				t.Fatalf("чтений %d, предел %d", reads, correlation.MaxExtraReads)
			}
			got := byKind(res, correlation.KindSharedDependency)
			if tt.want == "" {
				if got != nil {
					t.Fatalf("ожидался пустой результат, получен %+v", *got)
				}
				return
			}
			if got == nil || got.Confidence != tt.want || got.Total != tt.wantTotal {
				t.Fatalf("кандидат: %+v", got)
			}
			if k := got.Key(); k != "shared_dependency:core/Service/prod/api" {
				t.Fatalf("Key = %q", k)
			}
			for _, a := range got.Affected {
				if a.Kind != "Ingress" || a.Namespace != "prod" {
					t.Fatalf("затронутый потребитель: %+v", a)
				}
			}
			codes := factCodes(res, got)
			if !hasCode(codes, "correlation.shared_dependency.no_endpoints") || hasCode(codes, "correlation.shared_dependency.missing") {
				t.Fatalf("факты состояния источника: %v", codes)
			}
			if p := factParam(t, res, got, "correlation.shared_dependency.referrers", "count"); p != itoa(tt.wantTotal) {
				t.Fatalf("referrers count = %s", p)
			}
			if hasCode(codes, "correlation.shared_dependency.multi_workload") != tt.wantMulti {
				t.Fatalf("multi_workload: %v", codes)
			}
		})
	}
}

func TestSharedDependencyMissingService(t *testing.T) {
	w := &world{}
	for _, n := range []string{"i1", "i2", "i3"} {
		w.ingress("prod", n, "nginx", "gone")
	}
	svc := depgraph.Ref{Kind: "Service", Namespace: "prod", Name: "gone"}
	f, c := ingressScene(t, w, depgraph.Node{}, func(m *manual, first depgraph.Node) {
		m.missing(svc)
		m.edge(first.Ref, svc, depgraph.EdgeRoutes, depgraph.EdgeMissing)
	}, diagnose.ResourceRef{Group: "networking.k8s.io", Kind: "Ingress", Namespace: "prod", Name: "i1"})
	res, _ := runCtx(t, f, c)
	got := byKind(res, correlation.KindSharedDependency)
	if got == nil || got.Total != 3 || got.Confidence != diagnose.ConfidenceMedium {
		t.Fatalf("кандидат: %+v", got)
	}
	if k := got.Key(); k != "shared_dependency:core/Service/prod/gone" {
		t.Fatalf("Key = %q", k)
	}
	if !hasCode(factCodes(res, got), "correlation.shared_dependency.missing") {
		t.Fatalf("нет факта об отсутствии объекта: %v", factCodes(res, got))
	}
}

// IngressClass принадлежит кластеру: потребители ищутся во всех namespace.
func TestSharedDependencyIngressClassAcrossNamespaces(t *testing.T) {
	w := &world{}
	w.ingress("prod", "i1", "nginx", "api")
	w.ingress("stage", "i2", "nginx", "api")
	w.ingress("stage", "i3", "other", "api")
	cls := depgraph.Ref{Group: "networking.k8s.io", Kind: "IngressClass", Name: "nginx"}
	f, c := ingressScene(t, w, depgraph.Node{}, func(m *manual, first depgraph.Node) {
		m.missing(cls)
		m.edge(first.Ref, cls, depgraph.EdgeBinds, depgraph.EdgeMissing)
	}, diagnose.ResourceRef{Group: "networking.k8s.io", Kind: "Ingress", Namespace: "prod", Name: "i1"})
	res, _ := runCtx(t, f, c)
	got := byKind(res, correlation.KindSharedDependency)
	if got == nil || got.Total != 2 {
		t.Fatalf("кандидат: %+v", got)
	}
	if k := got.Key(); k != "shared_dependency:networking.k8s.io/IngressClass//nginx" {
		t.Fatalf("Key = %q", k)
	}
	seen := map[string]bool{}
	for _, a := range got.Affected {
		seen[a.Namespace+"/"+a.Name] = true
	}
	if !seen["prod/i1"] || !seen["stage/i2"] || seen["stage/i3"] {
		t.Fatalf("потребители: %+v", got.Affected)
	}
}

// Node и StorageClass разбирают shared_node и shared_storage; Unknown («не прочитали») источником не бывает.
func TestSharedDependencyIgnoresOtherSources(t *testing.T) {
	w := &world{}
	w.cmDeployment("prod", "a", "app-cfg", 1, 10, false)
	w.cmDeployment("prod", "b", "app-cfg", 1, 12, false)
	f := w.reader(t)
	m := newManual(t, f, depgraph.Ref{Kind: "Pod", Namespace: "prod", Name: "a-ha-0"})
	p := m.pod("prod", "a-ha-0")
	for _, ref := range []depgraph.Ref{
		{Kind: "Node", Name: "gone-node"},
		{Group: "storage.k8s.io", Kind: "StorageClass", Name: "gone-class"},
	} {
		m.missing(ref)
	}
	cm := depgraph.Ref{Kind: "ConfigMap", Namespace: "prod", Name: "app-cfg"}
	m.g.AddNode(depgraph.Node{Ref: cm, Health: depgraph.Unknown, Unknown: depgraph.UnknownForbidden})
	m.edge(p.Ref, cm, depgraph.EdgeMounts, depgraph.EdgeUnknown)
	m.edge(p.Ref, depgraph.Ref{Kind: "Node", Name: "gone-node"}, depgraph.EdgeRunsOn, depgraph.EdgeMissing)
	res, reads := runCtx(t, f, m.ctx(diagnose.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "a-ha-0"}, "a-ha-0"))
	if got := byKind(res, correlation.KindSharedDependency); got != nil {
		t.Fatalf("неожиданный кандидат: %+v", *got)
	}
	for _, call := range f.CallLog() {
		if call == "LIST Pod prod limit=500" {
			t.Fatalf("лишнее чтение потребителей: %v (reads=%d)", f.CallLog(), reads)
		}
	}
}

// Отказ RBAC на чтение Pod'ов: полнота не заявляется, причина — в Missing, потребители берутся из графа.
func TestSharedDependencyForbiddenPods(t *testing.T) {
	build := func(t *testing.T, graphPods int) (*graphtest.FakeReader, *diagnose.Context) {
		w := &world{}
		w.cmDeployment("prod", "a", "app-cfg", 1, 10, false)
		w.cmDeployment("prod", "b", "app-cfg", 1, 12, false)
		f := w.reader(t)
		m := newManual(t, f, depgraph.Ref{Kind: "Pod", Namespace: "prod", Name: "a-ha-0"})
		cm := depgraph.Ref{Kind: "ConfigMap", Namespace: "prod", Name: "app-cfg"}
		m.missing(cm)
		for _, name := range []string{"a-ha-0", "b-hb-0"}[:graphPods] {
			p := m.pod("prod", name)
			m.edge(p.Ref, cm, depgraph.EdgeMounts, depgraph.EdgeMissing)
		}
		c := m.ctx(diagnose.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "a-ha-0"}, "a-ha-0")
		f.Forbid(gvkPod)
		return f, c
	}
	t.Run("два потребителя в графе — кандидат помечен усечённым", func(t *testing.T) {
		f, c := build(t, 2)
		res, _ := runCtx(t, f, c)
		got := byKind(res, correlation.KindSharedDependency)
		if got == nil || got.Total != 2 || !got.Truncated {
			t.Fatalf("кандидат: %+v", got)
		}
		if len(res.Partial) == 0 || !hasMissingReason(res, "forbidden") {
			t.Fatalf("отказ не отражён: partial=%v missing=%+v", res.Partial, res.Missing)
		}
	})
	t.Run("один потребитель — кандидата нет, но пробел виден", func(t *testing.T) {
		f, c := build(t, 1)
		res, _ := runCtx(t, f, c)
		if got := byKind(res, correlation.KindSharedDependency); got != nil {
			t.Fatalf("неожиданный кандидат: %+v", *got)
		}
		if !hasMissingReason(res, "forbidden") {
			t.Fatalf("отказ не отражён: %+v", res.Missing)
		}
	})
}

func hasMissingReason(res *correlation.Result, reason string) bool {
	for _, m := range res.Missing {
		if m.Params["reason"] == reason {
			return true
		}
	}
	return false
}

func TestSharedDependencyDeterministic(t *testing.T) {
	deps := []cmDep{{name: "c", agoMin: 15}, {name: "a", agoMin: 10}, {name: "b", agoMin: 12}, {name: "d", agoMin: 11}}
	f1, c1 := cmScene(t, "app-cfg", deps)
	f2, c2 := cmScene(t, "app-cfg", deps)
	r1, _ := runCtx(t, f1, c1)
	r2, _ := runCtx(t, f2, c2)
	if a, b := mustJSON(t, r1), mustJSON(t, r2); a != b {
		t.Fatalf("результаты различаются:\n%s\n%s", a, b)
	}
	got := byKind(r1, correlation.KindSharedDependency)
	for i := 1; i < len(got.Affected); i++ {
		if got.Affected[i-1].Name > got.Affected[i].Name {
			t.Fatalf("Affected не отсортирован: %+v", got.Affected)
		}
	}
}

// Сотни потребителей: список ограничен MaxAffected, Total точен.
func TestSharedDependencyAffectedLimit(t *testing.T) {
	w := &world{}
	for i := 0; i < correlation.MaxAffected+20; i++ {
		w.cmDeployment("prod", "d"+pad3(i), "app-cfg", 1, 10, false)
	}
	f := w.reader(t)
	first := "d000-hd000-0"
	m := newManual(t, f, depgraph.Ref{Kind: "Pod", Namespace: "prod", Name: first})
	p := m.pod("prod", first)
	cm := depgraph.Ref{Kind: "ConfigMap", Namespace: "prod", Name: "app-cfg"}
	m.missing(cm)
	m.edge(p.Ref, cm, depgraph.EdgeMounts, depgraph.EdgeMissing)
	res, _ := runCtx(t, f, m.ctx(diagnose.ResourceRef{Kind: "Pod", Namespace: "prod", Name: first}, first))
	got := byKind(res, correlation.KindSharedDependency)
	if got == nil || got.Total != correlation.MaxAffected+20 || len(got.Affected) != correlation.MaxAffected {
		t.Fatalf("кандидат: total=%d affected=%d", got.Total, len(got.Affected))
	}
}

func pad3(i int) string {
	s := itoa(i)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestGoldenSharedDependencyHigh(t *testing.T) {
	f, c := cmScene(t, "app-cfg", []cmDep{{name: "a", agoMin: 10}, {name: "b", agoMin: 12}, {name: "c", agoMin: 15}})
	res, _ := runCtx(t, f, c)
	if b := byKind(res, correlation.KindSharedDependency); b == nil || b.Confidence != diagnose.ConfidenceHigh {
		t.Fatalf("ожидался кандидат high: %+v", b)
	}
	golden(t, "shared_dependency_high", res)
}

func TestGoldenSharedDependencyMedium(t *testing.T) {
	f, c := cmScene(t, "app-cfg", []cmDep{{name: "a", agoMin: 10}, {name: "b", agoMin: 12}})
	res, _ := runCtx(t, f, c)
	golden(t, "shared_dependency_medium", res)
}
