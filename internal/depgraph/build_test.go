// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/depgraph/graphtest"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestBuildFromPodHealthyChain(t *testing.T) {
	f := world(t)
	g := build(t, f, refPod, depgraph.Options{})

	for _, r := range []depgraph.Ref{refPod, refRS, refDeploy, refPVC, refPV, refSC, refNode, refSvc, refSlice, refOther} {
		if n := mustNode(t, g, r); n.Health != depgraph.Healthy {
			t.Errorf("%s: health %s/%s, want Healthy", r.Key(), n.Health, n.Reason)
		}
	}
	if hasNode(g, depgraph.Ref{Group: "apps", Kind: "ReplicaSet", Namespace: "app", Name: "web-old"}) {
		t.Error("the scaled-down old ReplicaSet must not be pulled in from a Pod seed")
	}
	edgeOf(t, g, refRS, refPod, depgraph.EdgeOwns)
	edgeOf(t, g, refDeploy, refRS, depgraph.EdgeOwns)
	edgeOf(t, g, refPod, refPVC, depgraph.EdgeMounts)
	edgeOf(t, g, refPVC, refPV, depgraph.EdgeBinds)
	edgeOf(t, g, refPVC, refSC, depgraph.EdgeBinds)
	edgeOf(t, g, refPod, refNode, depgraph.EdgeRunsOn)
	edgeOf(t, g, refSvc, refPod, depgraph.EdgeSelects)
	edgeOf(t, g, refSvc, refSlice, depgraph.EdgeBackedBy)
	edgeOf(t, g, refOther, refNode, depgraph.EdgeRunsOn)
	for _, e := range g.Edges() {
		if e.Reason == "policy" { // Secret не читается: связь неизвестна по политике
			continue
		}
		if e.State != depgraph.EdgeOK {
			t.Errorf("edge %s %s->%s is %s/%s, want ok", e.Type, e.From.Key(), e.To.Key(), e.State, e.Reason)
		}
	}
	if g.Truncated() || len(g.Warnings()) != 0 {
		t.Errorf("unexpected truncation: %v %v", g.Truncated(), g.Warnings())
	}
	if f.Calls() > 12 {
		t.Errorf("%d reads for a simple chain; log: %v", f.Calls(), f.CallLog())
	}

	if got := refsOf(g.Owners(refPod)); strings.Join(got, ",") != refRS.Key()+","+refDeploy.Key() {
		t.Errorf("Owners(pod) = %v", got)
	}
	deps := refsOf(g.Dependents(refNode))
	for _, want := range []string{refPod.Key(), refOther.Key(), refSvc.Key()} {
		if !contains(deps, want) {
			t.Errorf("Dependents(node) = %v, missing %s", deps, want)
		}
	}
	if got := refsOf(g.Dependents(refDeploy)); !contains(got, refRS.Key()) || !contains(got, refPod.Key()) {
		t.Errorf("Dependents(deployment) = %v", got)
	}
}

func TestSnapshotGoldenAndDeterminism(t *testing.T) {
	g1 := build(t, world(t), refPod, depgraph.Options{})
	g2 := build(t, world(t), refPod, depgraph.Options{})
	a, _ := json.MarshalIndent(g1.Snapshot(), "", "  ")
	b, _ := json.MarshalIndent(g2.Snapshot(), "", "  ")
	if string(a) != string(b) {
		t.Fatal("two builds of the same cluster state produced different snapshots")
	}
	const path = "testdata/pod_chain.golden.json"
	if *update {
		if err := os.WriteFile(path, append(a, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if strings.TrimSpace(string(want)) != string(a) {
		t.Errorf("snapshot differs from %s (run with -update if intended):\n%s", path, a)
	}
}

func TestSnapshotLeaksNothing(t *testing.T) {
	g := build(t, world(t), refPod, depgraph.Options{ExpandConfigMapRefs: true})
	raw, _ := json.Marshal(g.Snapshot())
	for _, bad := range []string{"MUST-NOT-LEAK", "password", "example/app", "10.0.0.1"} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("snapshot contains %q: %s", bad, raw)
		}
	}
}

func TestBuildFromOtherSeeds(t *testing.T) {
	t.Run("Deployment", func(t *testing.T) {
		g := build(t, world(t), refDeploy, depgraph.Options{})
		for _, r := range []depgraph.Ref{refDeploy, refRS, refPod, refNode, refPVC} {
			mustNode(t, g, r)
		}
		if hasNode(g, depgraph.Ref{Group: "apps", Kind: "ReplicaSet", Namespace: "app", Name: "web-old"}) {
			t.Error("inactive ReplicaSet must be skipped while an active one exists")
		}
	})
	t.Run("ReplicaSet", func(t *testing.T) {
		g := build(t, world(t), refRS, depgraph.Options{})
		edgeOf(t, g, refDeploy, refRS, depgraph.EdgeOwns)
		edgeOf(t, g, refRS, refPod, depgraph.EdgeOwns)
	})
	t.Run("Service", func(t *testing.T) {
		g := build(t, world(t), refSvc, depgraph.Options{})
		edgeOf(t, g, refSvc, refPod, depgraph.EdgeSelects)
		edgeOf(t, g, refSvc, refSlice, depgraph.EdgeBackedBy)
		mustNode(t, g, refNode)
	})
	t.Run("PVC", func(t *testing.T) {
		g := build(t, world(t), refPVC, depgraph.Options{})
		edgeOf(t, g, refPod, refPVC, depgraph.EdgeMounts)
		edgeOf(t, g, refPVC, refPV, depgraph.EdgeBinds)
		mustNode(t, g, refRS)
	})
	t.Run("Node", func(t *testing.T) {
		g := build(t, world(t), refNode, depgraph.Options{})
		edgeOf(t, g, refPod, refNode, depgraph.EdgeRunsOn)
		edgeOf(t, g, refOther, refNode, depgraph.EdgeRunsOn)
	})
}

func TestUnsupportedSeed(t *testing.T) {
	for _, seed := range []depgraph.Ref{
		{Kind: "Secret", Namespace: "app", Name: "x"},
		{Kind: "ConfigMap", Namespace: "app", Name: "x"},
		{Kind: "Pod", Namespace: "app"},
		{Kind: "Pod", Name: "no-namespace"},
	} {
		if _, err := depgraph.Build(context.Background(), world(t), seed, depgraph.Options{}); !errors.Is(err, depgraph.ErrUnsupportedSeed) {
			t.Errorf("Build(%+v) error = %v, want ErrUnsupportedSeed", seed, err)
		}
	}
	if _, err := depgraph.Build(context.Background(), nil, refPod, depgraph.Options{}); !errors.Is(err, depgraph.ErrUnsupportedSeed) {
		t.Errorf("nil reader: %v", err)
	}
}

func TestSeedGroupMayBeOmitted(t *testing.T) {
	g := build(t, world(t), depgraph.Ref{Kind: "Deployment", Namespace: "app", Name: "web"}, depgraph.Options{})
	if g.Seed() != refDeploy {
		t.Errorf("seed = %+v, want %+v", g.Seed(), refDeploy)
	}
}

func TestMissingReferenceBecomesPlaceholder(t *testing.T) {
	f2 := world(t, `
apiVersion: v1
kind: Pod
metadata: {name: lonely, namespace: app}
spec:
  nodeName: gone-node
  containers: [{name: c, image: x}]
  volumes: [{name: d, persistentVolumeClaim: {claimName: ghost}}]
status: {phase: Pending}
`)
	seed := depgraph.Ref{Kind: "Pod", Namespace: "app", Name: "lonely"}
	g := build(t, f2, seed, depgraph.Options{})

	ghost := depgraph.Ref{Kind: "PersistentVolumeClaim", Namespace: "app", Name: "ghost"}
	n := mustNode(t, g, ghost)
	if !n.Placeholder || n.Health != depgraph.Missing {
		t.Errorf("ghost PVC = %+v, want a Missing placeholder", n)
	}
	if e := edgeOf(t, g, seed, ghost, depgraph.EdgeMounts); e.State != depgraph.EdgeMissing {
		t.Errorf("mounts edge state = %s, want missing", e.State)
	}
	gone := depgraph.Ref{Kind: "Node", Name: "gone-node"}
	if e := edgeOf(t, g, seed, gone, depgraph.EdgeRunsOn); e.State != depgraph.EdgeMissing {
		t.Errorf("runs_on edge state = %s, want missing", e.State)
	}
}

func TestForbiddenAndUnavailableBecomeUnknown(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want depgraph.UnknownReason
		warn string
	}{
		{"forbidden", depgraph.ErrForbidden, depgraph.UnknownForbidden, "forbidden:Node"},
		{"unavailable", depgraph.ErrUnavailable, depgraph.UnknownUnavailable, "unavailable:Node"},
		{"too large", depgraph.ErrTooLarge, depgraph.UnknownTooLarge, "too_large:Node"},
		{"other error", errors.New("boom"), depgraph.UnknownUnavailable, "unavailable:Node"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := world(t)
			f.Fail(schema.GroupVersionKind{Version: "v1", Kind: "Node"}, c.err)
			g := build(t, f, refPod, depgraph.Options{})
			n := mustNode(t, g, refNode)
			if n.Health != depgraph.Unknown || n.Unknown != c.want {
				t.Errorf("node = %s/%s, want Unknown/%s", n.Health, n.Unknown, c.want)
			}
			if e := edgeOf(t, g, refPod, refNode, depgraph.EdgeRunsOn); e.State != depgraph.EdgeUnknown {
				t.Errorf("edge state = %s, want unknown", e.State)
			}
			if !contains(g.Warnings(), c.warn) {
				t.Errorf("warnings = %v, want %s", g.Warnings(), c.warn)
			}
			if hasNode(g, refPod) && mustNode(t, g, refPod).Health != depgraph.Healthy {
				t.Error("a failure on the Node must not change the Pod's own health")
			}
		})
	}
}

func TestSeedReadFailures(t *testing.T) {
	g := build(t, graphtestEmpty(t), refPod, depgraph.Options{})
	if n := mustNode(t, g, refPod); n.Health != depgraph.Missing || !n.Placeholder {
		t.Errorf("absent seed = %+v, want Missing placeholder", n)
	}
	f := world(t)
	f.Forbid(schema.GroupVersionKind{Version: "v1", Kind: "Pod"})
	g = build(t, f, refPod, depgraph.Options{})
	if n := mustNode(t, g, refPod); n.Health != depgraph.Unknown || n.Unknown != depgraph.UnknownForbidden {
		t.Errorf("forbidden seed = %+v", n)
	}
	if len(g.Nodes()) != 1 {
		t.Errorf("an unreadable seed must yield a one-node graph, got %v", keysOf(g))
	}
}

func TestNodePodListForbiddenMarksGraphIncomplete(t *testing.T) {
	f := world(t)
	f.Fail(schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, depgraph.ErrForbidden)
	g := build(t, f, refNode, depgraph.Options{})
	if !g.Truncated() || !contains(g.Warnings(), "forbidden:Pod") {
		t.Errorf("truncated=%v warnings=%v", g.Truncated(), g.Warnings())
	}
}

func TestLimits(t *testing.T) {
	t.Run("nodes", func(t *testing.T) {
		g := build(t, world(t), refPod, depgraph.Options{Limits: depgraph.Limits{MaxNodes: 3, MaxEdges: 600, MaxDepth: 4, MaxReads: 40}})
		if len(g.Nodes()) > 3 || !g.Truncated() || !contains(g.Warnings(), "limit.nodes") {
			t.Errorf("nodes=%d truncated=%v warnings=%v", len(g.Nodes()), g.Truncated(), g.Warnings())
		}
		for _, e := range g.Edges() {
			if !hasNode(g, e.From) || !hasNode(g, e.To) {
				t.Errorf("edge %+v points outside the graph", e)
			}
		}
	})
	t.Run("edges", func(t *testing.T) {
		g := build(t, world(t), refPod, depgraph.Options{Limits: depgraph.Limits{MaxNodes: 200, MaxEdges: 2, MaxDepth: 4, MaxReads: 40}})
		if len(g.Edges()) > 2 || !g.Truncated() || !contains(g.Warnings(), "limit.edges") {
			t.Errorf("edges=%d truncated=%v warnings=%v", len(g.Edges()), g.Truncated(), g.Warnings())
		}
	})
	t.Run("depth", func(t *testing.T) {
		g := build(t, world(t), refPod, depgraph.Options{Limits: depgraph.Limits{MaxNodes: 200, MaxEdges: 600, MaxDepth: 1, MaxReads: 40}})
		if hasNode(g, refDeploy) || hasNode(g, refPV) {
			t.Errorf("nodes beyond depth 1: %v", keysOf(g))
		}
		mustNode(t, g, refRS)
		if !g.Truncated() || !contains(g.Warnings(), "limit.depth") {
			t.Errorf("truncated=%v warnings=%v", g.Truncated(), g.Warnings())
		}
	})
	t.Run("reads", func(t *testing.T) {
		f := world(t)
		g := build(t, f, refPod, depgraph.Options{Limits: depgraph.Limits{MaxNodes: 200, MaxEdges: 600, MaxDepth: 4, MaxReads: 3}})
		if f.Calls() > 3 {
			t.Errorf("%d reads with MaxReads=3", f.Calls())
		}
		if !g.Truncated() || !contains(g.Warnings(), "limit.reads") {
			t.Errorf("truncated=%v warnings=%v", g.Truncated(), g.Warnings())
		}
		limited := 0
		for _, n := range g.Nodes() {
			if n.Health == depgraph.Unknown && n.Unknown == depgraph.UnknownLimit {
				limited++
			}
		}
		if limited == 0 {
			t.Errorf("references that could not be read must be Unknown/limit: %+v", g.Nodes())
		}
	})
	t.Run("default reads", func(t *testing.T) {
		f := world(t)
		build(t, f, refNode, depgraph.Options{})
		if f.Calls() > depgraph.DefaultLimits.MaxReads {
			t.Errorf("%d reads", f.Calls())
		}
	})
}

func TestListTruncationIsReported(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 120; i++ {
		sb.WriteString("apiVersion: v1\nkind: Pod\nmetadata: {name: bulk-" + itoa(i) + ", namespace: bulk}\nspec: {nodeName: big, containers: [{name: c, image: x}]}\nstatus: {phase: Running, conditions: [{type: Ready, status: \"True\"}]}\n---\n")
	}
	sb.WriteString("apiVersion: v1\nkind: Node\nmetadata: {name: big}\nstatus: {conditions: [{type: Ready, status: \"True\"}]}\n")
	f := world(t, sb.String())
	g := build(t, f, depgraph.Ref{Kind: "Node", Name: "big"}, depgraph.Options{})
	if !g.Truncated() || !contains(g.Warnings(), "list_truncated:Pod") {
		t.Errorf("truncated=%v warnings=%v", g.Truncated(), g.Warnings())
	}
	if len(g.Nodes()) != 101 {
		t.Errorf("%d nodes, want the node plus a 100-pod page", len(g.Nodes()))
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func TestCancelledContextGivesPartialGraph(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g, err := depgraph.Build(ctx, world(t), refPod, depgraph.Options{})
	if err != nil {
		t.Fatalf("a cancelled context is not a programming error: %v", err)
	}
	if !g.Truncated() || !contains(g.Warnings(), "canceled") {
		t.Errorf("truncated=%v warnings=%v", g.Truncated(), g.Warnings())
	}
	if n := mustNode(t, g, refPod); n.Health != depgraph.Unknown {
		t.Errorf("seed = %+v", n)
	}
}

func TestSecretsAreNeverRead(t *testing.T) {
	f := world(t)
	g := build(t, f, refPod, depgraph.Options{ExpandConfigMapRefs: true})
	for _, c := range f.CallLog() {
		if strings.Contains(c, "Secret") {
			t.Errorf("a Secret was read: %s", c)
		}
	}
	sec := mustNode(t, g, depgraph.Ref{Kind: "Secret", Namespace: "app", Name: "web-tls"})
	if sec.Health != depgraph.Unknown || sec.Unknown != depgraph.UnknownPolicy || sec.Object != nil {
		t.Errorf("secret node = %+v", sec)
	}
	if e := edgeOf(t, g, refPod, sec.Ref, depgraph.EdgeMounts); e.State != depgraph.EdgeUnknown || e.Reason != "policy" {
		t.Errorf("secret edge = %+v", e)
	}
}

func TestConfigMapRefsBehindFlag(t *testing.T) {
	cm := depgraph.Ref{Kind: "ConfigMap", Namespace: "app", Name: "web-cfg"}

	f := world(t)
	g := build(t, f, refPod, depgraph.Options{})
	if hasNode(g, cm) {
		t.Error("ConfigMap must not appear with the flag off")
	}
	for _, c := range f.CallLog() {
		if strings.Contains(c, "ConfigMap") {
			t.Errorf("ConfigMap read with the flag off: %s", c)
		}
	}

	g = build(t, world(t), refPod, depgraph.Options{ExpandConfigMapRefs: true})
	n := mustNode(t, g, cm)
	if n.Health != depgraph.Healthy || n.Object == nil {
		t.Fatalf("configmap node = %+v", n)
	}
	if _, found, _ := nestedField(n.Object.Object, "data"); found {
		t.Error("data must be stripped from the in-memory object")
	}
	if _, found, _ := nestedField(n.Object.Object, "binaryData"); found {
		t.Error("binaryData must be stripped from the in-memory object")
	}
	if strings.Contains(strings.ToLower(jsonOf(n.Object.Object)), "must-not-leak") {
		t.Errorf("sensitive content survived: %s", jsonOf(n.Object.Object))
	}
}

func TestOptionalMissingConfigMapIsNotAnError(t *testing.T) {
	f := world(t, `
apiVersion: v1
kind: Pod
metadata: {name: opt, namespace: app}
spec:
  containers:
    - name: c
      image: x
      envFrom: [{configMapRef: {name: maybe, optional: true}}, {configMapRef: {name: required}}]
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
`)
	seed := depgraph.Ref{Kind: "Pod", Namespace: "app", Name: "opt"}
	g := build(t, f, seed, depgraph.Options{ExpandConfigMapRefs: true})
	if hasNode(g, depgraph.Ref{Kind: "ConfigMap", Namespace: "app", Name: "maybe"}) {
		t.Error("an optional missing ConfigMap must not be reported")
	}
	req := mustNode(t, g, depgraph.Ref{Kind: "ConfigMap", Namespace: "app", Name: "required"})
	if !req.Placeholder {
		t.Errorf("a required missing ConfigMap must be a placeholder: %+v", req)
	}
}

func TestExtensionFlagsAreScaffoldOnly(t *testing.T) {
	f := world(t)
	g := build(t, f, refPod, depgraph.Options{ExpandIngress: true, ExpandHPA: true})
	for _, c := range f.CallLog() {
		if strings.Contains(c, "Ingress") || strings.Contains(c, "HorizontalPodAutoscaler") {
			t.Errorf("stage-2 kind read: %s", c)
		}
	}
	if !contains(g.Warnings(), "not_implemented:ingress") || !contains(g.Warnings(), "not_implemented:hpa") {
		t.Errorf("warnings = %v", g.Warnings())
	}
}

func TestOwnerOfUnknownKindIsPolicyNode(t *testing.T) {
	f := world(t, `
apiVersion: v1
kind: Pod
metadata:
  name: rollout-pod
  namespace: app
  ownerReferences: [{apiVersion: argoproj.io/v1alpha1, kind: Rollout, name: r1, uid: x}]
spec: {containers: [{name: c, image: x}]}
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
`)
	seed := depgraph.Ref{Kind: "Pod", Namespace: "app", Name: "rollout-pod"}
	g := build(t, f, seed, depgraph.Options{})
	owner := depgraph.Ref{Group: "argoproj.io", Kind: "Rollout", Namespace: "app", Name: "r1"}
	if n := mustNode(t, g, owner); n.Unknown != depgraph.UnknownPolicy {
		t.Errorf("owner = %+v", n)
	}
}

func graphtestEmpty(testing.TB) depgraph.Reader { return graphtest.NewFakeReader() }
