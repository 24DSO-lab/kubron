// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"encoding/json"
	"testing"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

func TestRefKey(t *testing.T) {
	cases := []struct {
		ref  depgraph.Ref
		want string
	}{
		{depgraph.Ref{Kind: "Pod", Namespace: "ns", Name: "p"}, "core/Pod/ns/p"},
		{depgraph.Ref{Group: "apps", Kind: "Deployment", Namespace: "ns", Name: "d"}, "apps/Deployment/ns/d"},
		{depgraph.Ref{Kind: "Node", Name: "n1"}, "core/Node//n1"},
	}
	for _, c := range cases {
		if got := c.ref.Key(); got != c.want {
			t.Errorf("Key(%+v) = %q, want %q", c.ref, got, c.want)
		}
	}
}

func TestSnapshotIsSortedAndNeverNil(t *testing.T) {
	pod := depgraph.Ref{Kind: "Pod", Namespace: "ns", Name: "p"}
	pvc := depgraph.Ref{Kind: "PersistentVolumeClaim", Namespace: "ns", Name: "data"}
	g := depgraph.New(pod)
	g.AddNode(depgraph.Node{Ref: pvc, Health: depgraph.Missing, Placeholder: true})
	g.AddNode(depgraph.Node{Ref: pod, Health: depgraph.Degraded, Reason: "pending_unscheduled"})
	g.AddEdge(depgraph.Edge{From: pod, To: pvc, Type: depgraph.EdgeMounts, State: depgraph.EdgeMissing})

	raw, err := json.Marshal(g.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"nodes":[{"kind":"PersistentVolumeClaim","namespace":"ns","name":"data","health":"Missing","placeholder":true},` +
		`{"kind":"Pod","namespace":"ns","name":"p","health":"Degraded","reason":"pending_unscheduled"}],` +
		`"edges":[{"from":"core/Pod/ns/p","to":"core/PersistentVolumeClaim/ns/data","type":"mounts","state":"missing"}]}`
	if string(raw) != want {
		t.Errorf("snapshot JSON:\n got %s\nwant %s", raw, want)
	}

	empty, _ := json.Marshal(depgraph.New(pod).Snapshot())
	if string(empty) != `{"nodes":[],"edges":[]}` {
		t.Errorf("empty snapshot = %s", empty)
	}
}

func TestPlaceholderDoesNotOverwriteRealNode(t *testing.T) {
	ref := depgraph.Ref{Kind: "Pod", Namespace: "ns", Name: "p"}
	g := depgraph.New(ref)
	g.AddNode(depgraph.Node{Ref: ref, Health: depgraph.Healthy})
	g.AddNode(depgraph.Node{Ref: ref, Health: depgraph.Missing, Placeholder: true})
	if n, _ := g.Node(ref); n.Health != depgraph.Healthy {
		t.Errorf("placeholder replaced a real node: %+v", n)
	}
}
