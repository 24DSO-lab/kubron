// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/depgraph/graphtest"
)

// Итоговое состояние графа не зависит от порядка обхода Pod'ов и ссылок: optional-ссылка на отсутствующий ConfigMap —
// ребро ok с причиной optional_absent; обязательная ссылка на тот же ConfigMap оставляет вершину заглушкой Missing.

const (
	cmOrderRoleOptional = "opt"
	cmOrderRoleRequired = "req"
)

var cmOrderRsSeed = depgraph.Ref{Group: "apps", Kind: "ReplicaSet", Namespace: "app", Name: "rs"}

// refYAML — как Pod ссылается на ConfigMap shared: envFrom (optional или обязательно).
func cmOrderEnvFromRef(optional bool) string {
	if optional {
		return "{configMapRef: {name: shared, optional: true}}"
	}
	return "{configMapRef: {name: shared}}"
}

func cmOrderVolumeRef(optional bool) string {
	if optional {
		return "{name: v, configMap: {name: shared, optional: true}}"
	}
	return "{name: v, configMap: {name: shared}}"
}

// cmOrderOrderPod — Pod name; envFrom и volumes задают ссылки на shared (пустая строка — ссылки нет).
func cmOrderOrderPod(name, envFrom, volume string) string {
	spec := "  containers: [{name: c, image: x"
	if envFrom != "" {
		spec += ", envFrom: [" + envFrom + "]"
	}
	spec += "}]\n"
	if volume != "" {
		spec += "  volumes: [" + volume + "]\n"
	}
	return fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: app
  labels: {app: x}
  ownerReferences: [{apiVersion: apps/v1, kind: ReplicaSet, name: rs, uid: rs1, controller: true}]
spec:
%sstatus: {phase: Running, conditions: [{type: Ready, status: "True"}]}
`, name, spec)
}

func cmOrderOrderWorld(t *testing.T, pods ...string) (*graphtest.FakeReader, *depgraph.Graph) {
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
	return f, build(t, f, cmOrderRsSeed, configOpts())
}

// cmOrderSignature — итоговое состояние ConfigMap и рёбер ссылок по ролям Pod'ов (имена Pod'ов в подпись не входят).
func cmOrderSignature(t *testing.T, g *depgraph.Graph, roles map[string]string) string {
	t.Helper()
	n := mustNode(t, g, cmRef("shared"))
	parts := []string{fmt.Sprintf("node:%s/%s/placeholder=%v/object=%v", n.Health, n.Reason, n.Placeholder, n.Object != nil)}
	for pod, role := range roles {
		e := edgeOf(t, g, cfgPodRef(pod), cmRef("shared"), depgraph.EdgeMounts)
		parts = append(parts, fmt.Sprintf("%s:%s/%s/optional=%v", role, e.State, e.Reason, e.Optional))
	}
	slices.Sort(parts)
	return strings.Join(parts, "|")
}

func cmOrderPermutations(roles []string) [][]string {
	if len(roles) <= 1 {
		return [][]string{slices.Clone(roles)}
	}
	var out [][]string
	for i := range roles {
		rest := slices.Concat(roles[:i:i], roles[i+1:])
		for _, p := range cmOrderPermutations(rest) {
			out = append(out, append([]string{roles[i]}, p...))
		}
	}
	return out
}

// Несколько Pod'ов с optional и один с обязательной ссылкой: в любой расстановке по именам (то есть в любом порядке
// обхода) результат один — заглушка Missing, required-ребро missing, optional-рёбра ok/optional_absent.
func TestOptionalAndRequiredOrderDoesNotMatter(t *testing.T) {
	names := []string{"p-a", "p-b", "p-c"}
	var first string
	for _, perm := range cmOrderPermutations([]string{cmOrderRoleOptional, cmOrderRoleOptional, cmOrderRoleRequired}) {
		t.Run(strings.Join(perm, "-"), func(t *testing.T) {
			roles := map[string]string{}
			var pods []string
			for i, role := range perm {
				roles[names[i]] = role
				pods = append(pods, cmOrderOrderPod(names[i], cmOrderEnvFromRef(role == cmOrderRoleOptional), ""))
			}
			_, g := cmOrderOrderWorld(t, pods...)
			sig := cmOrderSignature(t, g, roles)
			want := "node:Missing/not_found/placeholder=true/object=false|opt:ok/optional_absent/optional=true|opt:ok/optional_absent/optional=true|req:missing/not_found/optional=false"
			if sig != want {
				t.Errorf("cmOrderSignature\n got  %s\n want %s", sig, want)
			}
			if first == "" {
				first = sig
			} else if sig != first {
				t.Errorf("order changed the result:\n got  %s\n prev %s", sig, first)
			}
		})
	}
}

// Только optional-ссылки (несколько Pod'ов): вершина не выглядит сломанной, но и не объявляет объект существующим —
// у неё нет объекта, причина optional_absent сохранена на вершине и на каждом ребре.
func TestOnlyOptionalKeepsExplicitReasonWithoutObject(t *testing.T) {
	roles := map[string]string{"p-a": cmOrderRoleOptional, "p-b": cmOrderRoleOptional, "p-c": cmOrderRoleOptional}
	var pods []string
	for _, n := range []string{"p-a", "p-b", "p-c"} {
		pods = append(pods, cmOrderOrderPod(n, cmOrderEnvFromRef(true), ""))
	}
	_, g := cmOrderOrderWorld(t, pods...)
	want := "node:Healthy/optional_absent/placeholder=false/object=false|opt:ok/optional_absent/optional=true|opt:ok/optional_absent/optional=true|opt:ok/optional_absent/optional=true"
	if got := cmOrderSignature(t, g, roles); got != want {
		t.Errorf("cmOrderSignature\n got  %s\n want %s", got, want)
	}
	n := mustNode(t, g, cmRef("shared"))
	if n.Object != nil {
		t.Error("an absent optional ConfigMap must not carry an object")
	}
	// Снимок (его читает интерфейс) несёт причину: отсутствие объекта не скрыто за зелёным состоянием.
	var found bool
	for _, sn := range g.Snapshot().Nodes {
		if sn.Kind == "ConfigMap" && sn.Name == "shared" {
			found = true
			if sn.Reason != depgraph.ReasonOptionalAbsent || sn.Health == string(depgraph.Missing) {
				t.Errorf("snapshot node = %+v", sn)
			}
		}
	}
	if !found {
		t.Error("ConfigMap is not in the snapshot")
	}
	for _, sn := range g.Snapshot().Edges {
		if strings.Contains(sn.To, "ConfigMap") && (sn.State != string(depgraph.EdgeOK) || sn.Reason != depgraph.ReasonOptionalAbsent) {
			t.Errorf("snapshot edge = %+v", sn)
		}
	}
}

// Один Pod ссылается на один ConfigMap несколько раз: обязательная ссылка побеждает независимо от того, какая из
// ссылок встретилась первой (volumes обходятся раньше envFrom).
func TestOnePodSeveralRefsToTheSameConfigMap(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		volumeOptional, envFrom bool
		wantRequired            bool
	}{
		{"optional volume, required envFrom", true, false, true},
		{"required volume, optional envFrom", false, true, true},
		{"both optional", true, true, false},
		{"both required", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, g := cmOrderOrderWorld(t, cmOrderOrderPod("p-a", cmOrderEnvFromRef(tc.envFrom), cmOrderVolumeRef(tc.volumeOptional)))
			n := mustNode(t, g, cmRef("shared"))
			e := edgeOf(t, g, cfgPodRef("p-a"), cmRef("shared"), depgraph.EdgeMounts)
			if tc.wantRequired {
				if !n.Placeholder || n.Health != depgraph.Missing || e.State != depgraph.EdgeMissing || e.Optional {
					t.Errorf("node=%+v edge=%+v, want a Missing node and a missing required edge", n, e)
				}
				return
			}
			if n.Placeholder || n.Health == depgraph.Missing || n.Reason != depgraph.ReasonOptionalAbsent ||
				e.State != depgraph.EdgeOK || !e.Optional || e.Reason != depgraph.ReasonOptionalAbsent {
				t.Errorf("node=%+v edge=%+v, want optional_absent", n, e)
			}
			if got := len(g.EdgesFrom(cfgPodRef("p-a"), depgraph.EdgeMounts)); got != 1 {
				t.Errorf("mounts edges to one ConfigMap = %d, want 1", got)
			}
		})
	}
}

// Отказ «не найден» кэшируется: сколько бы Pod'ов ни ссылалось на отсутствующий ConfigMap, он читается один раз, и
// кэш не меняет итог ни в одном порядке.
func TestCachedNotFoundIsReadOnceAndIsOrderFree(t *testing.T) {
	for _, perm := range cmOrderPermutations([]string{cmOrderRoleOptional, cmOrderRoleRequired, cmOrderRoleOptional, cmOrderRoleRequired}) {
		t.Run(strings.Join(perm, "-"), func(t *testing.T) {
			var pods []string
			for i, role := range perm {
				pods = append(pods, cmOrderOrderPod(fmt.Sprintf("p-%d", i), cmOrderEnvFromRef(role == cmOrderRoleOptional), ""))
			}
			f, g := cmOrderOrderWorld(t, pods...)
			reads := 0
			for _, c := range f.CallLog() {
				if c == "GET ConfigMap app/shared" {
					reads++
				}
			}
			if reads != 1 {
				t.Errorf("ConfigMap read %d times, want 1 (cached ErrNotFound)", reads)
			}
			if n := mustNode(t, g, cmRef("shared")); !n.Placeholder || n.Health != depgraph.Missing {
				t.Errorf("node = %+v, want Missing placeholder", n)
			}
		})
	}
}

// Существующий ConfigMap: optional не меняет ничего, и вершина — настоящий объект в любом порядке.
func TestOptionalReferenceToExistingConfigMapIsNormal(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, "apiVersion: apps/v1\nkind: ReplicaSet\nmetadata: {name: rs, namespace: app, uid: rs1}\nspec: {replicas: 2, selector: {matchLabels: {app: x}}}\nstatus: {replicas: 2}\n")
	f.PutYAML(t, cmManifest("shared"))
	f.PutYAML(t, cmOrderOrderPod("p-a", cmOrderEnvFromRef(true), ""))
	f.PutYAML(t, cmOrderOrderPod("p-b", cmOrderEnvFromRef(false), ""))
	g := build(t, f, cmOrderRsSeed, configOpts())
	n := mustNode(t, g, cmRef("shared"))
	if n.Placeholder || n.Health != depgraph.Healthy || n.Reason == depgraph.ReasonOptionalAbsent || n.Object == nil {
		t.Errorf("node = %+v", n)
	}
	for _, p := range []string{"p-a", "p-b"} {
		if e := edgeOf(t, g, cfgPodRef(p), cmRef("shared"), depgraph.EdgeMounts); e.State != depgraph.EdgeOK || e.Reason == depgraph.ReasonOptionalAbsent {
			t.Errorf("%s edge = %+v", p, e)
		}
	}
}
