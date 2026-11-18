// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/depgraph/graphtest"
)

// chainReader — diagnose.Reader поверх graphtest.FakeReader: к Get и List добавлен PodLogs (логов в фикстурах этапа 1
// нет). Типы чтения у diagnose и depgraph — одни и те же, поэтому фейк D2 встраивается как есть.
type chainReader struct{ *graphtest.FakeReader }

func (chainReader) PodLogs(context.Context, LogRequest) ([]byte, error) { return nil, ErrNotFound }

func newChain(t *testing.T, manifests ...string) chainReader {
	t.Helper()
	f := graphtest.NewFakeReader()
	for _, m := range manifests {
		f.PutYAML(t, m)
	}
	return chainReader{f}
}

func chainOpts() CollectOptions {
	return CollectOptions{Now: func() time.Time { return fixedNow }}
}

// pvcWorld — Pod db-0 не запланирован из-за PVC data, у которого нет StorageClass gone; события на PVC и на Pod.
const pvcWorld = `
apiVersion: v1
kind: Pod
metadata: {name: db-0, namespace: app, uid: p1, labels: {app: db}}
spec:
  containers: [{name: db, image: example/db}]
  volumes: [{name: data, persistentVolumeClaim: {claimName: data}}]
status:
  phase: Pending
  conditions:
    - {type: PodScheduled, status: "False", reason: Unschedulable, message: "0/1 nodes are available: pod has unbound immediate PersistentVolumeClaims."}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: data, namespace: app, uid: c1}
spec: {storageClassName: gone, accessModes: [ReadWriteOnce]}
status: {phase: Pending}
---
apiVersion: v1
kind: Event
metadata: {name: e-pvc, namespace: app}
involvedObject: {kind: PersistentVolumeClaim, name: data, namespace: app}
type: Warning
reason: ProvisioningFailed
message: 'storageclass.storage.k8s.io "gone" not found'
count: 3
firstTimestamp: "2026-11-11T09:50:00Z"
lastTimestamp: "2026-11-11T09:58:00Z"
---
apiVersion: v1
kind: Event
metadata: {name: e-pod, namespace: app}
involvedObject: {kind: Pod, name: db-0, namespace: app}
type: Warning
reason: FailedScheduling
message: "0/1 nodes are available: pod has unbound immediate PersistentVolumeClaims."
count: 2
firstTimestamp: "2026-11-11T09:51:00Z"
lastTimestamp: "2026-11-11T09:59:00Z"
---
apiVersion: v1
kind: Node
metadata: {name: n1}
status: {conditions: [{type: Ready, status: "True"}]}
`

var podDB0 = ResourceRef{Kind: "Pod", Namespace: "app", Name: "db-0"}

func graphNodeKinds(t *testing.T, rep Report) map[string]string {
	t.Helper()
	if rep.Graph == nil {
		t.Fatal("report has no graph")
	}
	out := map[string]string{}
	for _, n := range rep.Graph.Nodes {
		out[n.Kind+"/"+n.Name] = n.Health
	}
	return out
}

func TestGraphHookBuildsGraphAndEvents(t *testing.T) {
	t.Parallel()
	r := newChain(t, pvcWorld)
	c, err := Collect(t.Context(), r, podDB0, chainOpts())
	if err != nil {
		t.Fatal(err)
	}
	if c.Graph == nil {
		t.Fatal("graph hook did not build a graph")
	}
	rep := Analyze(c)
	nodes := graphNodeKinds(t, rep)
	for _, want := range []string{"Pod/db-0", "PersistentVolumeClaim/data", "StorageClass/gone"} {
		if _, ok := nodes[want]; !ok {
			t.Errorf("graph lacks %s: %v", want, nodes)
		}
	}
	if nodes["StorageClass/gone"] != string(depgraph.Missing) {
		t.Errorf("StorageClass/gone health = %q, want Missing", nodes["StorageClass/gone"])
	}
	// События PVC базовый сбор не берёт: их добавляет хук графа из пула событий namespace.
	if !slices.ContainsFunc(c.Events, func(e Event) bool {
		return e.Reason == "ProvisioningFailed" && e.Object.Kind == "PersistentVolumeClaim"
	}) {
		t.Errorf("PVC event is missing from the context: %+v", c.Events)
	}
	if len(rep.Missing) != 0 && slices.ContainsFunc(rep.Missing, func(m MissingContext) bool { return m.Code == "graph.unavailable" }) {
		t.Errorf("unexpected graph.unavailable: %+v", rep.Missing)
	}
}

func TestGraphGapsBecomeMissingAndPartial(t *testing.T) {
	t.Parallel()
	small := &depgraph.Options{Limits: depgraph.Limits{MaxNodes: 2, MaxEdges: 600, MaxDepth: 4, MaxReads: 40}}
	for _, tc := range []struct {
		name        string
		setup       func(chainReader)
		opts        func(*CollectOptions)
		wantMissing func(MissingContext) bool
		wantPartial string
	}{
		{
			name:  "PVC read is forbidden",
			setup: func(r chainReader) { r.Forbid(gvkPVC) },
			wantMissing: func(m MissingContext) bool {
				return m.Code == "source.unavailable" && m.Resource.Kind == "PersistentVolumeClaim" && m.Params["reason"] == "forbidden"
			},
		},
		{
			name:  "Services are unavailable",
			setup: func(r chainReader) { r.Fail(gvkService, depgraph.ErrUnavailable) },
			wantMissing: func(m MissingContext) bool {
				return m.Code == "source.unavailable" && m.Resource.Kind == "Service" && m.Params["reason"] == "unavailable"
			},
		},
		{
			name:        "graph limits are reached",
			setup:       func(chainReader) {},
			opts:        func(o *CollectOptions) { o.Graph = small },
			wantPartial: "Graph",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newChain(t, pvcWorld)
			tc.setup(r)
			o := chainOpts()
			if tc.opts != nil {
				tc.opts(&o)
			}
			c, err := Collect(t.Context(), r, podDB0, o)
			if err != nil {
				t.Fatalf("a graph failure must not fail the investigation: %v", err)
			}
			rep := Analyze(c)
			if tc.wantMissing != nil && !slices.ContainsFunc(rep.Missing, tc.wantMissing) {
				t.Errorf("missing = %+v", rep.Missing)
			}
			if tc.wantPartial != "" && !slices.Contains(rep.Partial, tc.wantPartial) {
				t.Errorf("partial = %v, want %q", rep.Partial, tc.wantPartial)
			}
		})
	}
}

// Цель, которую граф не умеет строить, не роняет расследование: «graph.unavailable» и отчёт без графа.
func TestGraphUnsupportedSeedIsMissingContext(t *testing.T) {
	old := maps.Clone(targets)
	t.Cleanup(func() { targets = old })
	RegisterTarget("Ingress", func(_ context.Context, _ Reader, target ResourceRef, o CollectOptions) (*Context, error) {
		return &Context{Target: target, ObservedAt: o.now()}, nil
	})
	c, err := Collect(t.Context(), newChain(t), ResourceRef{Kind: "Ingress", Namespace: "app", Name: "web"}, chainOpts())
	if err != nil {
		t.Fatal(err)
	}
	if c.Graph != nil {
		t.Error("an unsupported target must not get a graph")
	}
	rep := Analyze(c)
	if rep.Graph != nil || !slices.ContainsFunc(rep.Missing, func(m MissingContext) bool {
		return m.Code == "graph.unavailable" && m.Params["reason"] == "unsupported"
	}) {
		t.Errorf("graph=%v missing=%+v", rep.Graph, rep.Missing)
	}
}

// Обязательный инвариант этапа 1: если граф недоступен, отчёт совпадает с построенным без графа, кроме
// Graph, Missing и Partial.
func TestReportWithoutGraphEqualsReportWithUnavailableGraph(t *testing.T) {
	t.Parallel()
	report := func(o CollectOptions, hooks bool) map[string]any {
		r := newChain(t, pvcWorld)
		var c *Context
		var err error
		if hooks {
			c, err = Collect(t.Context(), r, podDB0, o)
		} else {
			c, err = targets["Pod"](t.Context(), r, podDB0, o) // базовый сбор без хуков
		}
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(Analyze(c))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"graph", "missing", "partial", "resources"} {
			delete(m, k)
		}
		return m
	}
	// Граф с одним чтением — только цель: остальное недоступно (limit.reads).
	starved := chainOpts()
	starved.Graph = &depgraph.Options{Limits: depgraph.Limits{MaxNodes: 200, MaxEdges: 600, MaxDepth: 4, MaxReads: 1}}
	with, without := report(starved, true), report(chainOpts(), false)
	a, _ := json.Marshal(with)
	b, _ := json.Marshal(without)
	if string(a) != string(b) {
		t.Errorf("report changed without a usable graph\n--- with\n%s\n--- without\n%s", a, b)
	}
}

const svcWorld = `
apiVersion: v1
kind: Service
metadata: {name: web, namespace: app, uid: s1}
spec: {selector: {app: web}, ports: [{port: 80, targetPort: 8080}]}
---
apiVersion: v1
kind: Service
metadata: {name: external, namespace: app}
spec: {type: ExternalName, externalName: example.com}
---
apiVersion: v1
kind: Pod
metadata: {name: web-1, namespace: app, labels: {app: web}}
spec: {nodeName: n1, containers: [{name: app, image: example/app}]}
status: {phase: Running, conditions: [{type: Ready, status: "False"}]}
---
apiVersion: v1
kind: Pod
metadata: {name: other, namespace: app, labels: {app: other}}
spec: {nodeName: n1, containers: [{name: app, image: example/app}]}
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
`

func TestServiceTarget(t *testing.T) {
	t.Parallel()
	svc := ResourceRef{Kind: "Service", Namespace: "app", Name: "web"}
	c, err := Collect(t.Context(), newChain(t, svcWorld), svc, chainOpts())
	if err != nil {
		t.Fatal(err)
	}
	if c.Target.UID != "s1" {
		t.Errorf("uid = %q", c.Target.UID)
	}
	if len(c.Pods) != 1 || c.Pods[0].Name != "web-1" {
		t.Errorf("pods selected by the service selector = %v", c.Pods)
	}
	if c.Graph == nil {
		t.Error("a Service target must get a graph")
	}

	// Service без селектора не выбирает поды: не ошибка, но «объяснять нечем».
	ext := ResourceRef{Kind: "Service", Namespace: "app", Name: "external"}
	c, err = Collect(t.Context(), newChain(t, svcWorld), ext, chainOpts())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Pods) != 0 || !slices.ContainsFunc(c.Missing, func(m MissingContext) bool { return m.Code == "service.selector_missing" }) {
		t.Errorf("pods=%v missing=%+v", c.Pods, c.Missing)
	}

	for name, target := range map[string]ResourceRef{
		"missing service": {Kind: "Service", Namespace: "app", Name: "nope"},
		"empty namespace": {Kind: "Service", Name: "web"},
		"empty name":      {Kind: "Service", Namespace: "app"},
	} {
		if _, err := Collect(t.Context(), newChain(t, svcWorld), target, chainOpts()); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
}

const pvcTargetWorld = pvcWorld + `
---
apiVersion: v1
kind: Pod
metadata: {name: bystander, namespace: app}
spec: {containers: [{name: x, image: example/x}], volumes: [{name: tmp, emptyDir: {}}]}
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
`

func TestPVCTarget(t *testing.T) {
	t.Parallel()
	pvc := ResourceRef{Kind: "PersistentVolumeClaim", Namespace: "app", Name: "data"}
	c, err := Collect(t.Context(), newChain(t, pvcTargetWorld), pvc, chainOpts())
	if err != nil {
		t.Fatal(err)
	}
	if c.Target.UID != "c1" {
		t.Errorf("uid = %q", c.Target.UID)
	}
	if len(c.Pods) != 1 || c.Pods[0].Name != "db-0" {
		t.Errorf("consumers = %v, want only db-0", c.Pods)
	}
	if !slices.ContainsFunc(c.Events, func(e Event) bool { return e.Reason == "ProvisioningFailed" }) {
		t.Errorf("events of the claim itself are missing: %+v", c.Events)
	}
	if _, err := Collect(t.Context(), newChain(t, pvcTargetWorld), ResourceRef{Kind: "PersistentVolumeClaim", Namespace: "app", Name: "nope"}, chainOpts()); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing PVC: err = %v", err)
	}
}

const nodeWorld = `
apiVersion: v1
kind: Node
metadata: {name: n1, uid: n-uid}
status: {conditions: [{type: Ready, status: "False", reason: KubeletNotReady}, {type: MemoryPressure, status: "True"}]}
---
apiVersion: v1
kind: Pod
metadata: {name: a, namespace: ns1}
spec: {nodeName: n1, containers: [{name: x, image: example/x}]}
status: {phase: Running, conditions: [{type: Ready, status: "False"}]}
---
apiVersion: v1
kind: Pod
metadata: {name: b, namespace: ns2}
spec: {nodeName: n1, containers: [{name: x, image: example/x}]}
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
---
apiVersion: v1
kind: Pod
metadata: {name: elsewhere, namespace: ns1}
spec: {nodeName: n2, containers: [{name: x, image: example/x}]}
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
---
apiVersion: v1
kind: Event
metadata: {name: ev-node, namespace: default}
involvedObject: {kind: Node, name: n1}
type: Warning
reason: NodeNotReady
message: Node n1 status is now NodeNotReady
lastTimestamp: "2026-11-11T09:57:00Z"
firstTimestamp: "2026-11-11T09:57:00Z"
---
apiVersion: v1
kind: Event
metadata: {name: ev-other, namespace: default}
involvedObject: {kind: Node, name: n2}
type: Normal
reason: Starting
message: other node
lastTimestamp: "2026-11-11T09:00:00Z"
firstTimestamp: "2026-11-11T09:00:00Z"
`

func TestNodeTarget(t *testing.T) {
	t.Parallel()
	// Namespace у Node не имеет смысла: заданный игнорируется.
	node := ResourceRef{Kind: "Node", Namespace: "ignored", Name: "n1"}
	c, err := Collect(t.Context(), newChain(t, nodeWorld), node, chainOpts())
	if err != nil {
		t.Fatal(err)
	}
	if c.Target.Namespace != "" || c.Target.UID != "n-uid" {
		t.Errorf("target = %+v", c.Target)
	}
	if c.Nodes["n1"] == nil {
		t.Error("the target node must be in Context.Nodes")
	}
	var names []string
	for _, p := range c.Pods {
		names = append(names, p.Namespace+"/"+p.Name)
	}
	if !slices.Equal(names, []string{"ns1/a", "ns2/b"}) {
		t.Errorf("pods on the node (all namespaces) = %v", names)
	}
	if len(c.Events) != 1 || c.Events[0].Reason != "NodeNotReady" {
		t.Errorf("events of the node = %+v", c.Events)
	}
	if c.Graph == nil {
		t.Error("a Node target must get a graph")
	}
	if _, err := Collect(t.Context(), newChain(t, nodeWorld), ResourceRef{Kind: "Node", Name: "nope"}, chainOpts()); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing node: err = %v", err)
	}
}

// Запрет на чтение событий узла — MissingContext, а не отказ расследования.
func TestNodeEventsForbiddenIsMissingContext(t *testing.T) {
	t.Parallel()
	r := newChain(t, nodeWorld)
	r.Forbid(gvkEvent)
	c, err := Collect(t.Context(), r, ResourceRef{Kind: "Node", Name: "n1"}, chainOpts())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(c.Missing, func(m MissingContext) bool {
		return m.Code == "events.unavailable" && m.Params["reason"] == "forbidden"
	}) {
		t.Errorf("missing = %+v", c.Missing)
	}
}
