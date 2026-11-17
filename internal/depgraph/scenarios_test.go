// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

func podWithPVC(name, pvc string) string {
	return `
apiVersion: v1
kind: Pod
metadata: {name: ` + name + `, namespace: app}
spec:
  containers: [{name: c, image: x}]
  volumes: [{name: d, persistentVolumeClaim: {claimName: ` + pvc + `}}]
status:
  phase: Pending
  conditions: [{type: PodScheduled, status: "False", reason: Unschedulable}]
`
}

func TestPendingPVCWithoutDefaultClass(t *testing.T) {
	f := world(t, podWithPVC("stuck", "orphan"), `
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: orphan, namespace: app}
status: {phase: Pending}
`)
	seed := depgraph.Ref{Kind: "Pod", Namespace: "app", Name: "stuck"}
	g := build(t, f, seed, depgraph.Options{})
	pvc := mustNode(t, g, depgraph.Ref{Kind: "PersistentVolumeClaim", Namespace: "app", Name: "orphan"})
	if pvc.Health != depgraph.Degraded || pvc.Reason != depgraph.ReasonNoDefaultClass {
		t.Errorf("pvc = %s/%s, want Degraded/no_default_storageclass", pvc.Health, pvc.Reason)
	}
	if n := mustNode(t, g, seed); n.Reason != depgraph.ReasonPendingUnscheduled {
		t.Errorf("pod reason = %s", n.Reason)
	}
	if e := edgeOf(t, g, seed, pvc.Ref, depgraph.EdgeMounts); e.State != depgraph.EdgeBroken || e.Reason != depgraph.ReasonNoDefaultClass {
		t.Errorf("mounts edge = %+v", e)
	}
}

func TestPendingPVCWithDefaultClass(t *testing.T) {
	f := world(t, podWithPVC("stuck", "claim"), `
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: claim, namespace: app}
status: {phase: Pending}
---
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata: {name: std, annotations: {storageclass.kubernetes.io/is-default-class: "true"}}
provisioner: example.com/csi
`)
	seed := depgraph.Ref{Kind: "Pod", Namespace: "app", Name: "stuck"}
	g := build(t, f, seed, depgraph.Options{})
	pvc := depgraph.Ref{Kind: "PersistentVolumeClaim", Namespace: "app", Name: "claim"}
	sc := depgraph.Ref{Group: "storage.k8s.io", Kind: "StorageClass", Name: "std"}
	if e := edgeOf(t, g, pvc, sc, depgraph.EdgeBinds); e.Reason != "default_class" {
		t.Errorf("binds edge = %+v", e)
	}
	if n := mustNode(t, g, pvc); n.Reason != depgraph.ReasonUnbound {
		t.Errorf("pvc reason = %s, want unbound", n.Reason)
	}
}

func TestPVCWaitForFirstConsumer(t *testing.T) {
	f := world(t, podWithPVC("waiter", "lazy"), `
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: lazy, namespace: app}
spec: {storageClassName: wffc}
status: {phase: Pending}
---
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata: {name: wffc}
provisioner: example.com/csi
volumeBindingMode: WaitForFirstConsumer
`)
	g := build(t, f, depgraph.Ref{Kind: "Pod", Namespace: "app", Name: "waiter"}, depgraph.Options{})
	pvc := mustNode(t, g, depgraph.Ref{Kind: "PersistentVolumeClaim", Namespace: "app", Name: "lazy"})
	if pvc.Health != depgraph.Progressing || pvc.Reason != depgraph.ReasonWaitingFirstConsumer {
		t.Errorf("pvc = %s/%s", pvc.Health, pvc.Reason)
	}
}

func TestPVCReferencesMissingClassAndVolume(t *testing.T) {
	f := world(t, podWithPVC("p", "broken"), `
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: broken, namespace: app}
spec: {storageClassName: nope, volumeName: gone-pv}
status: {phase: Lost}
`)
	g := build(t, f, depgraph.Ref{Kind: "Pod", Namespace: "app", Name: "p"}, depgraph.Options{})
	pvc := depgraph.Ref{Kind: "PersistentVolumeClaim", Namespace: "app", Name: "broken"}
	if e := edgeOf(t, g, pvc, depgraph.Ref{Group: "storage.k8s.io", Kind: "StorageClass", Name: "nope"}, depgraph.EdgeBinds); e.State != depgraph.EdgeMissing {
		t.Errorf("class edge = %+v", e)
	}
	if e := edgeOf(t, g, pvc, depgraph.Ref{Kind: "PersistentVolume", Name: "gone-pv"}, depgraph.EdgeBinds); e.State != depgraph.EdgeMissing {
		t.Errorf("volume edge = %+v", e)
	}
	if n := mustNode(t, g, pvc); n.Reason != depgraph.ReasonLost {
		t.Errorf("pvc reason = %s", n.Reason)
	}
}

func TestSeveralClaimsUseOneList(t *testing.T) {
	f := world(t, `
apiVersion: v1
kind: Pod
metadata: {name: multi, namespace: app}
spec:
  containers: [{name: c, image: x}]
  volumes:
    - {name: a, persistentVolumeClaim: {claimName: c1}}
    - {name: b, persistentVolumeClaim: {claimName: c2}}
    - {name: e, ephemeral: {volumeClaimTemplate: {spec: {accessModes: [ReadWriteOnce]}}}}
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: c1, namespace: app}
spec: {storageClassName: ""}
status: {phase: Bound}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: multi-e, namespace: app}
spec: {storageClassName: ""}
status: {phase: Bound}
`)
	g := build(t, f, depgraph.Ref{Kind: "Pod", Namespace: "app", Name: "multi"}, depgraph.Options{})
	gets := 0
	for _, c := range f.CallLog() {
		if c == "GET PersistentVolumeClaim app/c1" || c == "GET PersistentVolumeClaim app/c2" || c == "GET PersistentVolumeClaim app/multi-e" {
			gets++
		}
	}
	if gets != 0 {
		t.Errorf("%d single PVC reads, want one List: %v", gets, f.CallLog())
	}
	if n := mustNode(t, g, depgraph.Ref{Kind: "PersistentVolumeClaim", Namespace: "app", Name: "c2"}); !n.Placeholder {
		t.Errorf("c2 must be Missing: %+v", n)
	}
	mustNode(t, g, depgraph.Ref{Kind: "PersistentVolumeClaim", Namespace: "app", Name: "multi-e"}) // ephemeral
}

func TestNodeNotReadyBreaksRunsOnEdge(t *testing.T) {
	f := world(t, `
apiVersion: v1
kind: Node
metadata: {name: n1}
status:
  conditions: [{type: Ready, status: "False"}, {type: MemoryPressure, status: "True"}]
`)
	g := build(t, f, refPod, depgraph.Options{})
	if n := mustNode(t, g, refNode); n.Health != depgraph.Degraded || n.Reason != depgraph.ReasonNodeNotReady {
		t.Errorf("node = %s/%s", n.Health, n.Reason)
	}
	if e := edgeOf(t, g, refPod, refNode, depgraph.EdgeRunsOn); e.State != depgraph.EdgeBroken || e.Reason != depgraph.ReasonNodeNotReady {
		t.Errorf("runs_on = %+v", e)
	}
}

func TestServiceHealthRefinements(t *testing.T) {
	svc := func(name, extra string) string {
		return `
apiVersion: v1
kind: Service
metadata: {name: ` + name + `, namespace: app}
spec: {selector: {app: ` + name + `}` + extra + `}
`
	}
	t.Run("selector matches nothing", func(t *testing.T) {
		g := build(t, world(t, svc("ghost", "")), depgraph.Ref{Kind: "Service", Namespace: "app", Name: "ghost"}, depgraph.Options{})
		if n := mustNode(t, g, depgraph.Ref{Kind: "Service", Namespace: "app", Name: "ghost"}); n.Health != depgraph.Degraded || n.Reason != depgraph.ReasonSelectorMatchesNothing {
			t.Errorf("service = %s/%s", n.Health, n.Reason)
		}
	})
	t.Run("no endpoint slices", func(t *testing.T) {
		f := world(t, svc("noeps", ""), `
apiVersion: v1
kind: Pod
metadata: {name: noeps-1, namespace: app, labels: {app: noeps}}
spec: {containers: [{name: c, image: x}]}
status: {phase: Running, conditions: [{type: Ready, status: "False"}]}
`)
		ref := depgraph.Ref{Kind: "Service", Namespace: "app", Name: "noeps"}
		g := build(t, f, ref, depgraph.Options{})
		if n := mustNode(t, g, ref); n.Health != depgraph.Degraded || n.Reason != depgraph.ReasonNoEndpoints {
			t.Errorf("service = %s/%s", n.Health, n.Reason)
		}
		pod := depgraph.Ref{Kind: "Pod", Namespace: "app", Name: "noeps-1"}
		if e := edgeOf(t, g, ref, pod, depgraph.EdgeSelects); e.State != depgraph.EdgeBroken || e.Reason != depgraph.ReasonNotReady {
			t.Errorf("selects edge = %+v", e)
		}
	})
	t.Run("slice without ready endpoints", func(t *testing.T) {
		f := world(t, svc("notready", ""), `
apiVersion: v1
kind: Pod
metadata: {name: notready-1, namespace: app, labels: {app: notready}}
spec: {containers: [{name: c, image: x}]}
status: {phase: Running, conditions: [{type: Ready, status: "False"}]}
---
apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata: {name: notready-s, namespace: app, labels: {kubernetes.io/service-name: notready}}
addressType: IPv4
endpoints: [{addresses: ["10.0.0.9"], conditions: {ready: false}}]
`)
		ref := depgraph.Ref{Kind: "Service", Namespace: "app", Name: "notready"}
		g := build(t, f, ref, depgraph.Options{})
		if n := mustNode(t, g, ref); n.Reason != depgraph.ReasonNoReadyEndpoints {
			t.Errorf("service reason = %s", n.Reason)
		}
	})
	t.Run("selectorless and ExternalName are not inspected", func(t *testing.T) {
		f := world(t, `
apiVersion: v1
kind: Service
metadata: {name: manual, namespace: app}
spec: {}
---
apiVersion: v1
kind: Service
metadata: {name: ext, namespace: app}
spec: {type: ExternalName, externalName: example.com, selector: {app: ext}}
`)
		for _, n := range []string{"manual", "ext"} {
			ref := depgraph.Ref{Kind: "Service", Namespace: "app", Name: n}
			g := build(t, f, ref, depgraph.Options{})
			if node := mustNode(t, g, ref); node.Health != depgraph.Healthy || len(g.Nodes()) != 1 {
				t.Errorf("%s: %+v, nodes %v", n, node, keysOf(g))
			}
		}
	})
}

func TestUnhealthyDeploymentChain(t *testing.T) {
	f := world(t, `
apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: app, uid: d1, generation: 3}
spec: {replicas: 2, selector: {matchLabels: {app: web}}}
status:
  observedGeneration: 3
  updatedReplicas: 2
  availableReplicas: 0
  conditions: [{type: Progressing, status: "False", reason: ProgressDeadlineExceeded}]
`)
	g := build(t, f, refDeploy, depgraph.Options{})
	if n := mustNode(t, g, refDeploy); n.Health != depgraph.Degraded || n.Reason != depgraph.ReasonProgressDeadline {
		t.Errorf("deployment = %s/%s", n.Health, n.Reason)
	}
}

func TestDeploymentWithOnlyInactiveReplicaSetsKeepsNewest(t *testing.T) {
	f := graphtestFrom(t, `
apiVersion: apps/v1
kind: Deployment
metadata: {name: zero, namespace: app}
spec: {replicas: 0, selector: {matchLabels: {app: zero}}}
---
apiVersion: apps/v1
kind: ReplicaSet
metadata: {name: zero-a, namespace: app, creationTimestamp: "2026-01-01T00:00:00Z", labels: {app: zero}, ownerReferences: [{apiVersion: apps/v1, kind: Deployment, name: zero, uid: u}]}
spec: {replicas: 0, selector: {matchLabels: {app: zero}}}
---
apiVersion: apps/v1
kind: ReplicaSet
metadata: {name: zero-b, namespace: app, creationTimestamp: "2026-02-01T00:00:00Z", labels: {app: zero}, ownerReferences: [{apiVersion: apps/v1, kind: Deployment, name: zero, uid: u}]}
spec: {replicas: 0, selector: {matchLabels: {app: zero}}}
`)
	seed := depgraph.Ref{Group: "apps", Kind: "Deployment", Namespace: "app", Name: "zero"}
	g := build(t, f, seed, depgraph.Options{})
	if n := mustNode(t, g, seed); n.Health != depgraph.Suspended || n.Reason != depgraph.ReasonScaledToZero {
		t.Errorf("deployment = %s/%s", n.Health, n.Reason)
	}
	mustNode(t, g, depgraph.Ref{Group: "apps", Kind: "ReplicaSet", Namespace: "app", Name: "zero-b"})
	if hasNode(g, depgraph.Ref{Group: "apps", Kind: "ReplicaSet", Namespace: "app", Name: "zero-a"}) {
		t.Error("only the newest inactive ReplicaSet is kept")
	}
}

func TestJobOwnedByCronJob(t *testing.T) {
	f := graphtestFrom(t, `
apiVersion: batch/v1
kind: CronJob
metadata: {name: nightly, namespace: app}
spec: {suspend: true}
---
apiVersion: batch/v1
kind: Job
metadata: {name: nightly-1, namespace: app, ownerReferences: [{apiVersion: batch/v1, kind: CronJob, name: nightly, uid: u}]}
status: {conditions: [{type: Failed, status: "True"}]}
---
apiVersion: v1
kind: Pod
metadata: {name: nightly-1-x, namespace: app, ownerReferences: [{apiVersion: batch/v1, kind: Job, name: nightly-1, uid: u}]}
spec: {containers: [{name: c, image: x}]}
status: {phase: Failed}
`)
	g := build(t, f, depgraph.Ref{Kind: "Pod", Namespace: "app", Name: "nightly-1-x"}, depgraph.Options{})
	job := depgraph.Ref{Group: "batch", Kind: "Job", Namespace: "app", Name: "nightly-1"}
	cron := depgraph.Ref{Group: "batch", Kind: "CronJob", Namespace: "app", Name: "nightly"}
	if n := mustNode(t, g, job); n.Reason != depgraph.ReasonJobFailed {
		t.Errorf("job = %s/%s", n.Health, n.Reason)
	}
	if n := mustNode(t, g, cron); n.Health != depgraph.Suspended {
		t.Errorf("cronjob = %s", n.Health)
	}
}

func TestOwnersAndDependentsSurviveCycles(t *testing.T) {
	a := depgraph.Ref{Kind: "Pod", Namespace: "n", Name: "a"}
	b := depgraph.Ref{Kind: "Pod", Namespace: "n", Name: "b"}
	g := depgraph.New(a)
	g.AddNode(depgraph.Node{Ref: a, Health: depgraph.Healthy})
	g.AddNode(depgraph.Node{Ref: b, Health: depgraph.Healthy})
	g.AddEdge(depgraph.Edge{From: a, To: b, Type: depgraph.EdgeOwns})
	g.AddEdge(depgraph.Edge{From: b, To: a, Type: depgraph.EdgeOwns})
	if got := refsOf(g.Owners(a)); len(got) != 1 || got[0] != b.Key() {
		t.Errorf("Owners = %v", got)
	}
	if got := refsOf(g.Dependents(a)); len(got) != 1 || got[0] != b.Key() {
		t.Errorf("Dependents = %v", got)
	}
	if len(g.EdgesTo(a, "")) != 1 || len(g.EdgesFrom(a, depgraph.EdgeMounts)) != 0 {
		t.Error("EdgesTo/EdgesFrom filters")
	}
}

func TestOnlyFirstPodsFromAListAreExpanded(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 8; i++ {
		id := string(rune('a' + i))
		sb.WriteString("apiVersion: v1\nkind: Pod\nmetadata: {name: many-" + id + ", namespace: app, labels: {app: many}}\n" +
			"spec: {nodeName: node-" + id + ", containers: [{name: c, image: x}]}\n" +
			"status: {phase: Running, conditions: [{type: Ready, status: \"True\"}]}\n---\n")
		sb.WriteString("apiVersion: v1\nkind: Node\nmetadata: {name: node-" + id + "}\nstatus: {conditions: [{type: Ready, status: \"True\"}]}\n---\n")
	}
	sb.WriteString("apiVersion: v1\nkind: Service\nmetadata: {name: many, namespace: app}\nspec: {selector: {app: many}}\n")
	g := build(t, graphtestFrom(t, sb.String()), depgraph.Ref{Kind: "Service", Namespace: "app", Name: "many"}, depgraph.Options{})
	pods, nodes := 0, 0
	for _, n := range g.Nodes() {
		switch n.Kind {
		case "Pod":
			pods++
		case "Node":
			nodes++
		}
	}
	if pods != 8 {
		t.Errorf("%d pods, want all 8 matched pods in the graph", pods)
	}
	if nodes != 5 {
		t.Errorf("%d nodes, want 5: only the first five pods are expanded", nodes)
	}
}

func TestPodReferencesToSecretsAndConfigMaps(t *testing.T) {
	f := graphtestFrom(t, `
apiVersion: v1
kind: Pod
metadata: {name: refs, namespace: app}
spec:
  volumes:
    - name: proj
      projected:
        sources:
          - {secret: {name: s-proj}}
          - {configMap: {name: c-proj}}
  initContainers:
    - name: init
      image: x
      envFrom: [{secretRef: {name: s-envfrom}}]
  containers:
    - name: c
      image: x
      envFrom: [{configMapRef: {name: c-envfrom}}]
      env:
        - {name: A, valueFrom: {secretKeyRef: {name: s-key, key: k}}}
        - {name: B, valueFrom: {configMapKeyRef: {name: c-key, key: k}}}
        - {name: C, value: plain}
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
`)
	seed := depgraph.Ref{Kind: "Pod", Namespace: "app", Name: "refs"}
	g := build(t, f, seed, depgraph.Options{ExpandConfigMapRefs: true})
	for _, s := range []string{"s-proj", "s-envfrom", "s-key"} {
		if n := mustNode(t, g, depgraph.Ref{Kind: "Secret", Namespace: "app", Name: s}); n.Unknown != depgraph.UnknownPolicy {
			t.Errorf("secret %s = %+v", s, n)
		}
	}
	for _, c := range []string{"c-proj", "c-envfrom", "c-key"} {
		mustNode(t, g, depgraph.Ref{Kind: "ConfigMap", Namespace: "app", Name: c})
	}
	for _, c := range f.CallLog() {
		if strings.Contains(c, "Secret") {
			t.Errorf("Secret read: %s", c)
		}
	}
}
