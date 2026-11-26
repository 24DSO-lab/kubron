// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package graphtest_test

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/depgraph/graphtest"
)

var gvkPod = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}

const manifests = `
apiVersion: v1
kind: Pod
metadata: {name: a, namespace: ns, labels: {app: web}}
spec: {nodeName: n1}
---
apiVersion: v1
kind: Pod
metadata: {name: b, namespace: ns, labels: {app: db}}
spec: {nodeName: n2}
---
apiVersion: v1
kind: Pod
metadata: {name: c, namespace: other, labels: {app: web}}
spec: {nodeName: n1}
`

func TestFakeReaderGetAndList(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, manifests)
	ctx := context.Background()

	if _, err := f.Get(ctx, gvkPod, "ns", "missing"); !errors.Is(err, depgraph.ErrNotFound) {
		t.Errorf("Get missing: %v, want ErrNotFound", err)
	}
	if o, err := f.Get(ctx, gvkPod, "ns", "a"); err != nil || o.GetName() != "a" {
		t.Errorf("Get a: %v %v", o, err)
	}
	res, err := f.List(ctx, gvkPod, depgraph.ListOptions{FieldSelector: "spec.nodeName=n1"})
	if err != nil || len(res.Items) != 2 || res.Items[0].GetName() != "a" || res.Items[1].GetName() != "c" {
		t.Errorf("List by node: %+v %v", res, err)
	}
	res, _ = f.List(ctx, gvkPod, depgraph.ListOptions{Namespace: "ns", LabelSelector: "app=web"})
	if len(res.Items) != 1 {
		t.Errorf("List by label: %d items", len(res.Items))
	}
	res, _ = f.List(ctx, gvkPod, depgraph.ListOptions{Limit: 2})
	if len(res.Items) != 2 || !res.Truncated {
		t.Errorf("Limit: %d items, truncated=%v", len(res.Items), res.Truncated)
	}
	if f.Calls() != 5 || len(f.CallLog()) != 5 {
		t.Errorf("Calls = %d, log = %v", f.Calls(), f.CallLog())
	}
}

func TestFakeReaderFailures(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, manifests)
	f.Forbid(gvkPod)
	if _, err := f.Get(context.Background(), gvkPod, "ns", "a"); !errors.Is(err, depgraph.ErrForbidden) {
		t.Errorf("Forbid: %v", err)
	}
	if _, err := f.List(context.Background(), gvkPod, depgraph.ListOptions{}); !errors.Is(err, depgraph.ErrForbidden) {
		t.Errorf("Forbid list: %v", err)
	}
}
