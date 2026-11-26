// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/depgraph/graphtest"
)

// healthyWorld — рабочий namespace app: Deployment → ReplicaSet → Pod с PVC (→ PV, StorageClass), Node, Service
// и EndpointSlice, плюс чужой Pod на той же Node. Всё здорово.
const healthyWorld = `
apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: app, uid: d1, generation: 2}
spec: {replicas: 1, selector: {matchLabels: {app: web}}}
status: {observedGeneration: 2, updatedReplicas: 1, availableReplicas: 1, readyReplicas: 1, replicas: 1}
---
apiVersion: apps/v1
kind: ReplicaSet
metadata:
  name: web-7d
  namespace: app
  uid: r1
  labels: {app: web, pod-template-hash: 7d}
  ownerReferences: [{apiVersion: apps/v1, kind: Deployment, name: web, uid: d1, controller: true}]
spec: {replicas: 1, selector: {matchLabels: {app: web, pod-template-hash: 7d}}}
status: {replicas: 1, readyReplicas: 1}
---
apiVersion: apps/v1
kind: ReplicaSet
metadata:
  name: web-old
  namespace: app
  labels: {app: web, pod-template-hash: old}
  ownerReferences: [{apiVersion: apps/v1, kind: Deployment, name: web, uid: d1}]
spec: {replicas: 0, selector: {matchLabels: {app: web, pod-template-hash: old}}}
status: {replicas: 0}
---
apiVersion: v1
kind: Pod
metadata:
  name: web-7d-x
  namespace: app
  uid: p1
  labels: {app: web, pod-template-hash: 7d, secret-label: LABEL-VALUE-MUST-NOT-LEAK}
  annotations: {note: ANNOTATION-MUST-NOT-LEAK}
  ownerReferences: [{apiVersion: apps/v1, kind: ReplicaSet, name: web-7d, uid: r1, controller: true}]
spec:
  nodeName: n1
  containers: [{name: app, image: example/app}]
  volumes:
    - {name: data, persistentVolumeClaim: {claimName: data}}
    - {name: cfg, configMap: {name: web-cfg}}
    - {name: tls, secret: {secretName: web-tls}}
status:
  phase: Running
  conditions: [{type: Ready, status: "True"}]
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: data, namespace: app}
spec: {volumeName: pv-1, storageClassName: fast}
status: {phase: Bound}
---
apiVersion: v1
kind: PersistentVolume
metadata: {name: pv-1}
status: {phase: Bound}
---
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata: {name: fast}
provisioner: example.com/csi
---
apiVersion: v1
kind: Node
metadata: {name: n1}
status:
  conditions: [{type: Ready, status: "True"}]
---
apiVersion: v1
kind: Pod
metadata: {name: other-1, namespace: other, labels: {app: other}}
spec: {nodeName: n1, containers: [{name: c, image: x}]}
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
---
apiVersion: v1
kind: Service
metadata: {name: web, namespace: app}
spec: {selector: {app: web}}
---
apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata: {name: web-abc, namespace: app, labels: {kubernetes.io/service-name: web}}
addressType: IPv4
endpoints: [{addresses: ["10.0.0.1"], conditions: {ready: true}}]
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: web-cfg
  namespace: app
  annotations: {kubectl.kubernetes.io/last-applied-configuration: "CONFIG-COPY-MUST-NOT-LEAK"}
data: {password: CONFIGMAP-DATA-MUST-NOT-LEAK}
binaryData: {blob: Q09ORklHTUFQLUJJTkFSWQ==}
`

var (
	refPod    = depgraph.Ref{Kind: "Pod", Namespace: "app", Name: "web-7d-x"}
	refRS     = depgraph.Ref{Group: "apps", Kind: "ReplicaSet", Namespace: "app", Name: "web-7d"}
	refDeploy = depgraph.Ref{Group: "apps", Kind: "Deployment", Namespace: "app", Name: "web"}
	refPVC    = depgraph.Ref{Kind: "PersistentVolumeClaim", Namespace: "app", Name: "data"}
	refPV     = depgraph.Ref{Kind: "PersistentVolume", Name: "pv-1"}
	refSC     = depgraph.Ref{Group: "storage.k8s.io", Kind: "StorageClass", Name: "fast"}
	refNode   = depgraph.Ref{Kind: "Node", Name: "n1"}
	refSvc    = depgraph.Ref{Kind: "Service", Namespace: "app", Name: "web"}
	refSlice  = depgraph.Ref{Group: "discovery.k8s.io", Kind: "EndpointSlice", Namespace: "app", Name: "web-abc"}
	refOther  = depgraph.Ref{Kind: "Pod", Namespace: "other", Name: "other-1"}
)

func world(t testing.TB, extra ...string) *graphtest.FakeReader {
	t.Helper()
	f := graphtest.NewFakeReader()
	f.PutYAML(t, healthyWorld)
	for _, y := range extra {
		f.PutYAML(t, y)
	}
	return f
}

func build(t testing.TB, r depgraph.Reader, seed depgraph.Ref, o depgraph.Options) *depgraph.Graph {
	t.Helper()
	g, err := depgraph.Build(context.Background(), r, seed, o)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

func mustNode(t testing.TB, g *depgraph.Graph, r depgraph.Ref) depgraph.Node {
	t.Helper()
	n, ok := g.Node(r)
	if !ok {
		t.Fatalf("node %s is not in the graph; have %v", r.Key(), keysOf(g))
	}
	return n
}

func keysOf(g *depgraph.Graph) []string {
	var out []string
	for _, n := range g.Nodes() {
		out = append(out, n.Key())
	}
	return out
}

func hasNode(g *depgraph.Graph, r depgraph.Ref) bool { _, ok := g.Node(r); return ok }

func edgeOf(t testing.TB, g *depgraph.Graph, from, to depgraph.Ref, typ depgraph.EdgeType) depgraph.Edge {
	t.Helper()
	for _, e := range g.EdgesFrom(from, typ) {
		if e.To == to {
			return e
		}
	}
	t.Fatalf("no %s edge %s -> %s; edges: %+v", typ, from.Key(), to.Key(), g.Edges())
	return depgraph.Edge{}
}

func refsOf(nodes []depgraph.Node) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.Key())
	}
	return out
}

func contains(list []string, s string) bool { return slices.Contains(list, s) }

func nestedField(obj map[string]any, path ...string) (any, bool, error) {
	cur := any(obj)
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false, nil
		}
		if cur, ok = m[p]; !ok {
			return nil, false, nil
		}
	}
	return cur, true, nil
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func graphtestFrom(t testing.TB, manifests string) *graphtest.FakeReader {
	t.Helper()
	f := graphtest.NewFakeReader()
	f.PutYAML(t, manifests)
	return f
}

// gvkOf берёт вид из первой строки «kind: X» манифеста и подбирает версию и группу для FakeReader.
func gvkOf(manifest string) schema.GroupVersionKind {
	kind := ""
	for _, line := range strings.Split(manifest, "\n") {
		if k, ok := strings.CutPrefix(line, "kind: "); ok {
			kind = strings.TrimSpace(k)
			break
		}
	}
	g := map[string]string{
		"ReplicaSet": "apps", "Deployment": "apps", "StatefulSet": "apps", "DaemonSet": "apps",
		"Job": "batch", "CronJob": "batch", "EndpointSlice": "discovery.k8s.io",
	}[kind]
	return schema.GroupVersionKind{Group: g, Version: "v1", Kind: kind}
}
