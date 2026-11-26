// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/depgraph/graphtest"
)

// Итог для отсутствующего ConfigMap не зависит от порядка обхода Pod'ов и ссылок (ADR-0031 §6): заглушка Missing, если
// хоть одна ссылка обязательная; иначе не поломка — Healthy с причиной optional_absent. Объект при этом нигде не
// «появляется»: Object пуст, а причина прямо говорит, что его нет.

var rsRef = depgraph.Ref{Group: "apps", Kind: "ReplicaSet", Namespace: "app", Name: "rs"}

// cmRef kinds of reference in one container: envFrom with or without optional.
func envFromRef(name string, optional bool) string {
	if optional {
		return fmt.Sprintf("{configMapRef: {name: %s, optional: true}}", name)
	}
	return fmt.Sprintf("{configMapRef: {name: %s}}", name)
}

// rsPod — Pod ReplicaSet rs с заданными фрагментами envFrom и volumes (YAML-списки в строку).
func rsPod(name, envFrom, volumes string) string {
	return fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: app
  labels: {app: x}
  ownerReferences: [{apiVersion: apps/v1, kind: ReplicaSet, name: rs, uid: rs1, controller: true}]
spec:
  containers: [{name: c, image: x, envFrom: [%s]}]
  volumes: [%s]
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
`, name, envFrom, volumes)
}

func rsWorld(t *testing.T, pods ...string) *graphtest.FakeReader {
	t.Helper()
	f := graphtest.NewFakeReader()
	f.PutYAML(t, fmt.Sprintf(`
apiVersion: apps/v1
kind: ReplicaSet
metadata: {name: rs, namespace: app, uid: rs1}
spec: {replicas: %d, selector: {matchLabels: {app: x}}}
status: {replicas: %d}
`, len(pods), len(pods)))
	for _, p := range pods {
		f.PutYAML(t, p)
	}
	return f
}

func snapshotJSON(t *testing.T, g *depgraph.Graph) string {
	t.Helper()
	raw, err := json.Marshal(g.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func countGets(f *graphtest.FakeReader, kind string) (n int) {
	for _, c := range f.CallLog() {
		if strings.HasPrefix(c, "GET "+kind) {
			n++
		}
	}
	return n
}

func assertOptionalOnly(t *testing.T, g *depgraph.Graph, name string, pods ...string) {
	t.Helper()
	n := mustNode(t, g, cmRef(name))
	if n.Placeholder || n.Health != depgraph.Healthy || n.Reason != depgraph.ReasonOptionalAbsent || n.Object != nil {
		t.Errorf("optional-only absent ConfigMap = %+v, want Healthy/optional_absent without Object and placeholder", n)
	}
	for _, p := range pods {
		if e := edgeOf(t, g, cfgPodRef(p), cmRef(name), depgraph.EdgeMounts); e.State != depgraph.EdgeOK || e.Reason != depgraph.ReasonOptionalAbsent || !e.Optional {
			t.Errorf("edge %s → %s = %+v, want ok/optional_absent", p, name, e)
		}
	}
}

func assertRequiredMissing(t *testing.T, g *depgraph.Graph, name string, optionalPods, requiredPods []string) {
	t.Helper()
	n := mustNode(t, g, cmRef(name))
	if !n.Placeholder || n.Health != depgraph.Missing || n.Object != nil {
		t.Errorf("ConfigMap with a required reference = %+v, want Missing placeholder", n)
	}
	for _, p := range requiredPods {
		if e := edgeOf(t, g, cfgPodRef(p), cmRef(name), depgraph.EdgeMounts); e.State != depgraph.EdgeMissing {
			t.Errorf("required edge from %s = %+v, want missing", p, e)
		}
	}
	for _, p := range optionalPods {
		if e := edgeOf(t, g, cfgPodRef(p), cmRef(name), depgraph.EdgeMounts); e.State != depgraph.EdgeOK || e.Reason != depgraph.ReasonOptionalAbsent {
			t.Errorf("optional edge from %s = %+v, want ok/optional_absent", p, e)
		}
	}
}

// Порядок Pod'ов (по имени: список отсортирован): optional → required, required → optional, optional между required.
// Итоговый граф один и тот же при любой перестановке имён, а отрицательный ответ читается из кластера один раз.
func TestAbsentConfigMapIsIndependentOfPodOrder(t *testing.T) {
	opt, req := envFromRef("shared", true), envFromRef("shared", false)
	for _, tc := range []struct {
		name         string
		optional     []string
		required     []string
		pods         [][2]string // имя Pod'а → ссылка
		wantRequired bool
	}{
		{"optional then required", []string{"a"}, []string{"b"}, [][2]string{{"a", opt}, {"b", req}}, true},
		{"required then optional", []string{"b"}, []string{"a"}, [][2]string{{"a", req}, {"b", opt}}, true},
		{"optional, required, optional", []string{"a", "c"}, []string{"b"}, [][2]string{{"a", opt}, {"b", req}, {"c", opt}}, true},
		{"required, optional, required", []string{"b"}, []string{"a", "c"}, [][2]string{{"a", req}, {"b", opt}, {"c", req}}, true},
		{"several optional only", []string{"a", "b", "c"}, nil, [][2]string{{"a", opt}, {"b", opt}, {"c", opt}}, false},
		{"several required only", nil, []string{"a", "b"}, [][2]string{{"a", req}, {"b", req}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pods []string
			var names []string
			for _, p := range tc.pods {
				pods = append(pods, rsPod(p[0], p[1], ""))
				names = append(names, p[0])
			}
			f := rsWorld(t, pods...)
			g := build(t, f, rsRef, configOpts())

			if tc.wantRequired {
				assertRequiredMissing(t, g, "shared", tc.optional, tc.required)
			} else {
				assertOptionalOnly(t, g, "shared", names...)
			}
			if n := countGets(f, "ConfigMap"); n != 1 {
				t.Errorf("ConfigMap read %d times, want 1: ErrNotFound must be cached", n)
			}
			for _, e := range g.Edges() {
				if !hasNode(g, e.From) || !hasNode(g, e.To) {
					t.Errorf("dangling edge %+v", e)
				}
			}
		})
	}
}

// Один и тот же набор ссылок в любом порядке имён Pod'ов даёт побайтно одинаковый снимок графа.
func TestAbsentConfigMapSnapshotIsOrderIndependent(t *testing.T) {
	opt, req := envFromRef("shared", true), envFromRef("shared", false)
	// Роли «optional» и «required» меняются местами за счёт имён: порядок обхода — по имени Pod'а.
	layouts := [][3][2]string{
		{{"pod-1", opt}, {"pod-2", opt}, {"pod-3", req}},
		{{"pod-1", req}, {"pod-2", opt}, {"pod-3", opt}},
		{{"pod-1", opt}, {"pod-2", req}, {"pod-3", opt}},
	}
	var snaps []string
	for _, l := range layouts {
		// имена Pod'ов в снимке разные по ролям, поэтому сравниваем только вершину ConfigMap и состояния его рёбер
		var pods []string
		for _, p := range l {
			pods = append(pods, rsPod(p[0], p[1], ""))
		}
		g := build(t, rsWorld(t, pods...), rsRef, configOpts())
		n := mustNode(t, g, cmRef("shared"))
		var states []string
		for _, e := range g.EdgesTo(cmRef("shared"), depgraph.EdgeMounts) {
			states = append(states, fmt.Sprintf("%s:%s", e.State, e.Reason))
		}
		slices.Sort(states)
		snaps = append(snaps, fmt.Sprintf("%v/%v/%s|%v", n.Placeholder, n.Health, n.Reason, states))
	}
	for i := 1; i < len(snaps); i++ {
		if snaps[i] != snaps[0] {
			t.Errorf("layout %d: %s, layout 0: %s", i, snaps[i], snaps[0])
		}
	}
	// Тот же мир собирается дважды — один и тот же снимок.
	w := func() string {
		return snapshotJSON(t, build(t, rsWorld(t, rsPod("pod-1", opt, ""), rsPod("pod-2", req, "")), rsRef, configOpts()))
	}
	if a, b := w(), w(); a != b {
		t.Error("the same world produced different snapshots")
	}
}

// Один Pod ссылается на один ConfigMap несколько раз: ссылка необязательна, только если необязательны все.
func TestOnePodSeveralReferencesToOneConfigMap(t *testing.T) {
	opt, req := envFromRef("shared", true), envFromRef("shared", false)
	volOpt := "{name: v, configMap: {name: shared, optional: true}}"
	volReq := "{name: v, configMap: {name: shared}}"
	for _, tc := range []struct {
		name         string
		env, volumes string
		wantRequired bool
	}{
		{"optional envFrom + optional volume", opt, volOpt, false},
		{"optional envFrom + required volume", opt, volReq, true},
		{"required envFrom + optional volume", req, volOpt, true},
		{"required envFrom + required volume", req, volReq, true},
		{"two optional envFrom", opt + ", " + opt, "", false},
		{"optional and required envFrom", opt + ", " + req, "", true},
		{"required and optional envFrom", req + ", " + opt, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := rsWorld(t, rsPod("only", tc.env, tc.volumes))
			g := build(t, f, rsRef, configOpts())
			if tc.wantRequired {
				assertRequiredMissing(t, g, "shared", nil, []string{"only"})
			} else {
				assertOptionalOnly(t, g, "shared", "only")
			}
			if n := countGets(f, "ConfigMap"); n != 1 {
				t.Errorf("ConfigMap read %d times, want 1", n)
			}
			if es := g.EdgesTo(cmRef("shared"), depgraph.EdgeMounts); len(es) != 1 {
				t.Errorf("edges to shared = %d, want 1 (one per Pod)", len(es))
			}
		})
	}
}

// Существующий ConfigMap с optional: true — обычная вершина: причины optional_absent у неё нет.
func TestOptionalReferenceToExistingConfigMapHasNoAbsentReason(t *testing.T) {
	f := rsWorld(t, rsPod("a", envFromRef("here", true), ""), rsPod("b", envFromRef("here", false), ""))
	f.PutYAML(t, cmManifest("here"))
	g := build(t, f, rsRef, configOpts())
	if n := mustNode(t, g, cmRef("here")); n.Placeholder || n.Health != depgraph.Healthy || n.Reason != "" || n.Object == nil {
		t.Errorf("existing ConfigMap = %+v", n)
	}
	for _, p := range []string{"a", "b"} {
		if e := edgeOf(t, g, cfgPodRef(p), cmRef("here"), depgraph.EdgeMounts); e.State != depgraph.EdgeOK || e.Reason != "" {
			t.Errorf("edge from %s = %+v", p, e)
		}
	}
}

// «Не знаем» не превращается ни в «нет», ни в optional_absent: запрет чтения даёт Unknown и для optional-ссылки.
func TestOptionalReferenceToUnreadableConfigMapStaysUnknown(t *testing.T) {
	f := rsWorld(t, rsPod("a", envFromRef("shared", true), ""))
	f.Forbid(gvkConfigMap)
	g := build(t, f, rsRef, configOpts())
	n := mustNode(t, g, cmRef("shared"))
	if n.Health != depgraph.Unknown || n.Reason == depgraph.ReasonOptionalAbsent || n.Placeholder {
		t.Errorf("node = %+v", n)
	}
	if e := edgeOf(t, g, cfgPodRef("a"), cmRef("shared"), depgraph.EdgeMounts); e.State != depgraph.EdgeUnknown {
		t.Errorf("edge = %+v", e)
	}
}

// Если ребро обязательной ссылки не поместилось в предел графа, обязательная ссылка всё равно была: вершина остаётся
// заглушкой Missing, а не превращается в «не поломку»; граф помечен усечённым. Проверка в обоих порядках: рёбра,
// отброшенные пределом, не должны менять итог.
func TestDroppedRequiredEdgeDoesNotHideMissingConfigMap(t *testing.T) {
	opt, req := envFromRef("shared", true), envFromRef("shared", false)
	for _, tc := range []struct {
		name     string
		a, b     string
		maxEdges int
	}{
		// RS → a, RS → b, a → shared: ребро b → shared отбрасывается.
		{"optional first, required edge dropped", opt, req, 3},
		// RS → a, RS → b: оба ребра к shared отбрасываются, в графе нет ни одного ребра к нему.
		{"required first, every edge dropped", req, opt, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := rsWorld(t, rsPod("a", tc.a, ""), rsPod("b", tc.b, ""))
			o := configOpts()
			o.Limits = depgraph.DefaultLimits
			o.Limits.MaxEdges = tc.maxEdges
			g := build(t, f, rsRef, o)
			if !g.Truncated() {
				t.Fatal("the graph must be truncated by the edge limit")
			}
			if n := mustNode(t, g, cmRef("shared")); !n.Placeholder || n.Health != depgraph.Missing {
				t.Errorf("shared = %+v: a dropped required edge must not turn the node into a non-failure", n)
			}
		})
	}
}
