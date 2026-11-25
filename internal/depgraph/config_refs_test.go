// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/depgraph/graphtest"
)

// ADR-0031 §6, этап 2: существование ConfigMap, на который ссылается Pod, проверяется графом без чтения содержимого.

var gvkConfigMap = schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}

func cmRef(name string) depgraph.Ref {
	return depgraph.Ref{Kind: "ConfigMap", Namespace: "app", Name: name}
}

func cfgPodRef(name string) depgraph.Ref {
	return depgraph.Ref{Kind: "Pod", Namespace: "app", Name: name}
}

func configOpts() depgraph.Options { return depgraph.Options{ExpandConfigMapRefs: true} }

// cmManifest — ConfigMap без данных: для проверок существования содержимое не нужно.
func cmManifest(name string) string {
	return fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata: {name: %s, namespace: app}\n", name)
}

const refsPod = `
apiVersion: v1
kind: Pod
metadata: {name: refs, namespace: app, uid: p-refs}
spec:
  initContainers:
    - name: init
      image: example/init
      env: [{name: A, valueFrom: {configMapKeyRef: {name: from-init-env, key: k}}}]
  containers:
    - name: web
      image: example/web
      envFrom: [{configMapRef: {name: from-envfrom}}]
      env: [{name: B, valueFrom: {configMapKeyRef: {name: from-env-key, key: k}}}]
  volumes:
    - {name: v1, configMap: {name: from-volume}}
    - name: v2
      projected:
        sources:
          - configMap: {name: from-projected}
          - secret: {name: some-secret}
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
`

// Все пять источников ссылок дают ребро mounts; существующие ConfigMap — ok, отсутствующие — заглушка и missing.
func TestConfigMapRefSources(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, refsPod)
	for _, n := range []string{"from-init-env", "from-envfrom", "from-volume"} { // два других отсутствуют
		f.PutYAML(t, cmManifest(n))
	}
	g := build(t, f, cfgPodRef("refs"), configOpts())

	for name, want := range map[string]depgraph.EdgeState{
		"from-init-env": depgraph.EdgeOK, "from-envfrom": depgraph.EdgeOK, "from-volume": depgraph.EdgeOK,
		"from-env-key": depgraph.EdgeMissing, "from-projected": depgraph.EdgeMissing,
	} {
		e := edgeOf(t, g, cfgPodRef("refs"), cmRef(name), depgraph.EdgeMounts)
		if e.State != want {
			t.Errorf("edge to %s = %s, want %s", name, e.State, want)
		}
		n := mustNode(t, g, cmRef(name))
		if wantMissing := want == depgraph.EdgeMissing; n.Placeholder != wantMissing || (n.Health == depgraph.Missing) != wantMissing {
			t.Errorf("node %s = %+v, want placeholder=%v", name, n, wantMissing)
		}
	}
	// Secret остаётся вершиной Unknown/policy и не читается.
	sec := mustNode(t, g, depgraph.Ref{Kind: "Secret", Namespace: "app", Name: "some-secret"})
	if sec.Unknown != depgraph.UnknownPolicy {
		t.Errorf("secret = %+v", sec)
	}
}

// Чтение ConfigMap — только Get по имени: List ConfigMap никогда не вызывается, Secret не читается.
func TestConfigMapsAreReadByNameOnly(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, refsPod)
	f.PutYAML(t, cmManifest("from-volume"))
	build(t, f, cfgPodRef("refs"), configOpts())
	var gets int
	for _, c := range f.CallLog() {
		switch {
		case strings.HasPrefix(c, "LIST ConfigMap"):
			t.Errorf("ConfigMap listed: %s", c)
		case strings.HasPrefix(c, "GET ConfigMap"):
			gets++
		case strings.Contains(c, "Secret"):
			t.Errorf("Secret read: %s", c)
		}
	}
	if gets != 5 {
		t.Errorf("ConfigMap Get calls = %d, want 5 (one per name); log %v", gets, f.CallLog())
	}
}

// Каждый ConfigMap читается один раз, сколько бы контейнеров и томов на него ни ссылались.
func TestConfigMapIsReadOnce(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, `
apiVersion: v1
kind: Pod
metadata: {name: dup, namespace: app}
spec:
  containers:
    - {name: a, image: x, envFrom: [{configMapRef: {name: shared}}]}
    - {name: b, image: x, envFrom: [{configMapRef: {name: shared}}]}
  volumes: [{name: v, configMap: {name: shared}}]
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
`)
	f.PutYAML(t, cmManifest("shared"))
	build(t, f, cfgPodRef("dup"), configOpts())
	n := 0
	for _, c := range f.CallLog() {
		if strings.HasPrefix(c, "GET ConfigMap") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("ConfigMap read %d times, want 1", n)
	}
}

// ReplicaSet с двумя Pod'ами, которые ссылаются на один отсутствующий ConfigMap: optional у одного и обязательная
// ссылка у другого. Заглушка нужна обязательной ссылке в любом порядке обхода, висячих рёбер быть не должно.
func TestOptionalAndRequiredShareAMissingConfigMap(t *testing.T) {
	for _, tc := range []struct{ optionalPod, requiredPod string }{{"a-opt", "b-req"}, {"b-opt", "a-req"}} {
		t.Run(tc.optionalPod, func(t *testing.T) {
			f := graphtest.NewFakeReader()
			f.PutYAML(t, `
apiVersion: apps/v1
kind: ReplicaSet
metadata: {name: rs, namespace: app, uid: rs1}
spec: {replicas: 2, selector: {matchLabels: {app: x}}}
status: {replicas: 2}
`)
			pod := func(name, optional string) string {
				return fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: app
  labels: {app: x}
  ownerReferences: [{apiVersion: apps/v1, kind: ReplicaSet, name: rs, uid: rs1, controller: true}]
spec:
  containers: [{name: c, image: x, envFrom: [{configMapRef: {name: shared%s}}]}]
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
`, name, optional)
			}
			f.PutYAML(t, pod(tc.optionalPod, ", optional: true"))
			f.PutYAML(t, pod(tc.requiredPod, ""))
			g := build(t, f, depgraph.Ref{Group: "apps", Kind: "ReplicaSet", Namespace: "app", Name: "rs"}, configOpts())

			shared := mustNode(t, g, cmRef("shared"))
			if !shared.Placeholder {
				t.Fatalf("shared = %+v, want a placeholder for the required reference", shared)
			}
			e := edgeOf(t, g, cfgPodRef(tc.requiredPod), cmRef("shared"), depgraph.EdgeMounts)
			if e.State != depgraph.EdgeMissing {
				t.Errorf("required edge = %+v", e)
			}
			if es := g.EdgesFrom(cfgPodRef(tc.optionalPod), depgraph.EdgeMounts); len(es) != 0 {
				t.Errorf("optional reference must not become an edge: %+v", es)
			}
			for _, e := range g.Edges() { // ни одно ребро не ведёт в никуда
				if !hasNode(g, e.From) || !hasNode(g, e.To) {
					t.Errorf("dangling edge %+v", e)
				}
			}
		})
	}
}

// optional: true для существующего ConfigMap — обычное ребро ok.
func TestOptionalPresentConfigMapIsAnOKEdge(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, `
apiVersion: v1
kind: Pod
metadata: {name: opt, namespace: app}
spec:
  containers: [{name: c, image: x, envFrom: [{configMapRef: {name: here, optional: true}}]}]
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
`)
	f.PutYAML(t, cmManifest("here"))
	g := build(t, f, cfgPodRef("opt"), configOpts())
	if e := edgeOf(t, g, cfgPodRef("opt"), cmRef("here"), depgraph.EdgeMounts); e.State != depgraph.EdgeOK {
		t.Errorf("edge = %+v", e)
	}
}

// Запрет RBAC на ConfigMap — «не знаем», а не «нет»: вершина Unknown/forbidden, ребро unknown, предупреждение.
func TestConfigMapForbiddenIsUnknownNotMissing(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, refsPod)
	f.Forbid(gvkConfigMap)
	g := build(t, f, cfgPodRef("refs"), configOpts())
	n := mustNode(t, g, cmRef("from-volume"))
	if n.Health != depgraph.Unknown || n.Unknown != depgraph.UnknownForbidden || n.Placeholder {
		t.Errorf("node = %+v", n)
	}
	if e := edgeOf(t, g, cfgPodRef("refs"), cmRef("from-volume"), depgraph.EdgeMounts); e.State != depgraph.EdgeUnknown || e.Reason != "forbidden" {
		t.Errorf("edge = %+v", e)
	}
	if !slices.Contains(g.Warnings(), "forbidden:ConfigMap") {
		t.Errorf("warnings = %v", g.Warnings())
	}
}

// Недоступность одного ConfigMap не превращается в «нет»: Unknown/unavailable.
func TestConfigMapUnavailableIsUnknown(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, refsPod)
	f.FailObject(gvkConfigMap, "app", "from-volume", depgraph.ErrUnavailable)
	g := build(t, f, cfgPodRef("refs"), configOpts())
	if n := mustNode(t, g, cmRef("from-volume")); n.Unknown != depgraph.UnknownUnavailable || n.Placeholder {
		t.Errorf("node = %+v", n)
	}
}

// Бюджет чтения (ADR-0031 §3): когда MaxReads исчерпан, оставшиеся ConfigMap — Unknown/limit, а не Missing; граф
// усечён; чтений не больше предела.
func TestConfigMapReadBudget(t *testing.T) {
	var refs, names []string
	for i := range 45 {
		n := fmt.Sprintf("cm-%02d", i)
		names = append(names, n)
		refs = append(refs, fmt.Sprintf("{configMapRef: {name: %s}}", n))
	}
	f := graphtest.NewFakeReader()
	f.PutYAML(t, fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata: {name: many, namespace: app}
spec: {containers: [{name: c, image: x, envFrom: [%s]}]}
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
`, strings.Join(refs, ", ")))
	for _, n := range names { // все 45 существуют: «нет» здесь быть не может
		f.PutYAML(t, cmManifest(n))
	}
	g := build(t, f, cfgPodRef("many"), configOpts())

	if f.Calls() > depgraph.DefaultLimits.MaxReads {
		t.Errorf("reads = %d, limit %d", f.Calls(), depgraph.DefaultLimits.MaxReads)
	}
	if !g.Truncated() || !slices.Contains(g.Warnings(), "limit.reads") {
		t.Errorf("truncated=%v warnings=%v", g.Truncated(), g.Warnings())
	}
	var read, unknown int
	for _, name := range names {
		n := mustNode(t, g, cmRef(name))
		switch {
		case n.Health == depgraph.Healthy:
			read++
		case n.Health == depgraph.Unknown && n.Unknown == depgraph.UnknownLimit && !n.Placeholder:
			unknown++
			if e := edgeOf(t, g, cfgPodRef("many"), n.Ref, depgraph.EdgeMounts); e.State != depgraph.EdgeUnknown || e.Reason != "limit" {
				t.Errorf("edge to unread %s = %+v", name, e)
			}
		default:
			t.Errorf("%s = %+v: out of budget must be Unknown/limit, never Missing", name, n)
		}
	}
	if read == 0 || unknown == 0 {
		t.Errorf("read=%d unknown=%d: both must be non-zero", read, unknown)
	}
	// Порядок чтения детерминирован: читаются первые по имени.
	if n := mustNode(t, g, cmRef("cm-00")); n.Health != depgraph.Healthy {
		t.Errorf("cm-00 = %+v", n)
	}
}

// Содержимое ConfigMap не покидает depgraph: ни в вершине, ни в снимке, ни в тексте предупреждений.
const leakyConfigMap = `
apiVersion: v1
kind: ConfigMap
metadata:
  name: web-cfg
  namespace: app
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: '{"data":{"token":"LEAK-LAST-APPLIED"}}'
  managedFields:
    - {manager: kubectl, operation: Update, apiVersion: v1, fieldsV1: {"f:data": {"f:token": {}}}}
data:
  token: LEAK-DATA-TOKEN
  config.yaml: "password: LEAK-DATA-PASSWORD"
binaryData:
  blob: TEVBSy1CSU5BUlk=
`

var leakMarkers = []string{"LEAK-DATA-TOKEN", "LEAK-DATA-PASSWORD", "LEAK-LAST-APPLIED", "TEVBSy1CSU5BUlk", "f:token"}

func TestConfigMapContentNeverLeavesTheGraph(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, `
apiVersion: v1
kind: Pod
metadata: {name: leaky, namespace: app}
spec: {containers: [{name: c, image: x, envFrom: [{configMapRef: {name: web-cfg}}]}]}
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
`)
	f.PutYAML(t, leakyConfigMap)
	g := build(t, f, cfgPodRef("leaky"), configOpts())

	n := mustNode(t, g, cmRef("web-cfg"))
	if n.Health != depgraph.Healthy || n.Object == nil {
		t.Fatalf("node = %+v", n)
	}
	for _, field := range []string{"data", "binaryData"} {
		if _, found, _ := nestedField(n.Object.Object, field); found {
			t.Errorf("%s survived in the node object", field)
		}
	}
	if _, found, _ := nestedField(n.Object.Object, "metadata", "managedFields"); found {
		t.Error("managedFields survived in the node object")
	}
	dump := jsonOf(g.Snapshot())
	for _, node := range g.Nodes() {
		if node.Object != nil {
			dump += jsonOf(node.Object.Object)
		}
	}
	dump += strings.Join(g.Warnings(), " ") + fmt.Sprintf("%+v", g.Nodes()[0])
	for _, m := range leakMarkers {
		if strings.Contains(dump, m) {
			t.Errorf("%q leaked out of depgraph", m)
		}
	}
	// Читатель отдаёт копию: чужой кэш санитайзер не портит (иначе соседняя расшифровка потеряла бы данные).
	again, err := f.Get(t.Context(), gvkConfigMap, "app", "web-cfg")
	if err != nil {
		t.Fatal(err)
	}
	if _, found, _ := nestedField(again.Object, "data"); !found {
		t.Error("the sanitizer modified the reader's own copy")
	}
}
