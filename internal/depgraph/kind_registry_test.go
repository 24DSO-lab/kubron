// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"errors"
	"testing"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/depgraph/graphtest"
)

const widgetYAML = `
apiVersion: example.io/v1
kind: Widget
metadata: {name: w1, namespace: app, uid: w-1, labels: {broken: "true"}}
`

var refWidget = depgraph.Ref{Group: "example.io", Kind: "Widget", Namespace: "app", Name: "w1"}

// Без регистрации вид неизвестен графу: целью быть не может.
func TestUnregisteredKindIsNotSeedable(t *testing.T) {
	f := graphtest.NewFakeReader()
	if _, err := depgraph.Build(t.Context(), f, refWidget, depgraph.Options{}); !errors.Is(err, depgraph.ErrUnsupportedSeed) {
		t.Fatalf("got %v, want ErrUnsupportedSeed", err)
	}
}

// Зарегистрированный вид читается, может быть целью и получает состояние из своей функции.
func TestRegisteredKindIsSeedableAndHasItsOwnHealth(t *testing.T) {
	depgraph.InstallFakeKind(t)
	f := world(t, widgetYAML)
	g := build(t, f, refWidget, depgraph.Options{})
	n, ok := g.Node(refWidget)
	if !ok || n.Health != depgraph.Degraded || n.Reason != "widget_broken" {
		t.Fatalf("widget node = %+v, found %v", n, ok)
	}
}

// Расширение вершин известного вида (Pod) достраивает граф и не меняет встроенную часть.
func TestExpanderRunsAfterBuiltInExpansion(t *testing.T) {
	depgraph.InstallFakeKind(t)
	f := world(t, widgetYAML, `
apiVersion: v1
kind: Pod
metadata: {name: widget-pod, namespace: app, uid: p-wp, annotations: {test/widget: w1}}
spec: {containers: [{name: web, image: example/web}]}
status: {phase: Running, conditions: [{type: Ready, status: "True"}]}
`)
	g := build(t, f, depgraph.Ref{Kind: "Pod", Namespace: "app", Name: "widget-pod"}, depgraph.Options{})
	if _, ok := g.Node(refWidget); !ok {
		t.Fatal("the expander did not add the widget")
	}
	var found bool
	for _, e := range g.Edges() {
		found = found || (e.From == refWidget && e.Type == depgraph.EdgeScales)
	}
	if !found {
		t.Errorf("edge widget → pod is missing: %+v", g.Edges())
	}
}
