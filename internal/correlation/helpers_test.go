// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/depgraph/graphtest"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// now — фиксированное «сейчас» всех тестов; все метки времени в манифестах отсчитываются от него.
var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

// reader добавляет к фейку PodLogs, чтобы он удовлетворял diagnose.Reader.
type reader struct{ *graphtest.FakeReader }

func (reader) PodLogs(context.Context, diagnose.LogRequest) ([]byte, error) { return nil, nil }

func ts(minutesAgo int) string {
	return now.Add(-time.Duration(minutesAgo) * time.Minute).Format(time.RFC3339)
}

// world собирает манифесты тестового кластера.
type world struct{ y strings.Builder }

func (w *world) add(format string, a ...any) { fmt.Fprintf(&w.y, format+"\n---\n", a...) }

// node добавляет узел. conds — строки вида «MemoryPressure=True@10» (тип=статус@минут назад).
func (w *world) node(name string, unschedulable bool, conds ...string) {
	var cs strings.Builder
	ready := true
	for _, c := range conds {
		typ, rest, _ := strings.Cut(c, "=")
		status, ago, _ := strings.Cut(rest, "@")
		m, _ := strconv.Atoi(ago)
		if typ == "Ready" {
			ready = false
		}
		fmt.Fprintf(&cs, "  - {type: %s, status: \"%s\", lastTransitionTime: \"%s\"}\n", typ, status, ts(m))
	}
	if ready {
		cs.WriteString("  - {type: Ready, status: \"True\"}\n")
	}
	w.add("apiVersion: v1\nkind: Node\nmetadata: {name: %s, uid: uid-%s}\nspec: {unschedulable: %v}\nstatus:\n conditions:\n%s",
		name, name, unschedulable, cs.String())
}

// deployment добавляет ReplicaSet и n Pod'ов Deployment'а на узле; failedMinAgo — когда Pod'ы перестали быть Ready
// (0 — здоровые).
func (w *world) deployment(ns, name, node string, n, failedMinAgo int) {
	hash := "h" + name
	w.add("apiVersion: apps/v1\nkind: ReplicaSet\nmetadata:\n name: %s-%s\n namespace: %s\n ownerReferences: [{apiVersion: apps/v1, kind: Deployment, name: %s, controller: true}]", name, hash, ns, name)
	for i := 0; i < n; i++ {
		ready := "\"True\""
		trans := ts(60)
		if failedMinAgo > 0 {
			ready, trans = "\"False\"", ts(failedMinAgo)
		}
		w.add("apiVersion: v1\nkind: Pod\nmetadata:\n name: %s-%s-%d\n namespace: %s\n labels: {pod-template-hash: %s}\n"+
			" ownerReferences: [{apiVersion: apps/v1, kind: ReplicaSet, name: %s-%s, controller: true}]\n"+
			"spec: {nodeName: %s}\nstatus:\n phase: Running\n conditions:\n - {type: Ready, status: %s, lastTransitionTime: \"%s\"}",
			name, hash, i, ns, hash, name, hash, node, ready, trans)
	}
}

// barePod — Pod без владельца (сам является нагрузкой).
func (w *world) barePod(ns, name, node string, failedMinAgo int) {
	w.add("apiVersion: v1\nkind: Pod\nmetadata: {name: %s, namespace: %s}\nspec: {nodeName: %s}\nstatus:\n phase: Running\n conditions:\n - {type: Ready, status: \"False\", lastTransitionTime: \"%s\"}",
		name, ns, node, ts(failedMinAgo))
}

func (w *world) reader(t *testing.T) *graphtest.FakeReader {
	t.Helper()
	f := graphtest.NewFakeReader()
	f.PutYAML(t, w.y.String())
	return f
}

// diagCtx собирает контекст диагностики: цель — Pod, Pod'ы и узлы читаются из фейка, граф — настоящий depgraph.
func diagCtx(t *testing.T, f *graphtest.FakeReader, ns, pod string) *diagnose.Context {
	t.Helper()
	c := &diagnose.Context{Target: diagnose.ResourceRef{Kind: "Pod", Namespace: ns, Name: pod}, ObservedAt: now,
		Nodes: map[string]*corev1.Node{}}
	ctx := context.Background()
	res, err := f.List(ctx, schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, diagnose.ListOptions{Namespace: ns})
	if err != nil {
		t.Fatal(err)
	}
	for i := range res.Items {
		var p corev1.Pod
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(res.Items[i].Object, &p); err != nil {
			t.Fatal(err)
		}
		if p.Name == pod {
			c.Pods = append(c.Pods, &p)
		}
	}
	nres, _ := f.List(ctx, schema.GroupVersionKind{Version: "v1", Kind: "Node"}, diagnose.ListOptions{})
	for i := range nres.Items {
		var n corev1.Node
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(nres.Items[i].Object, &n); err == nil {
			c.Nodes[n.Name] = &n
		}
	}
	g, err := depgraph.Build(ctx, f, depgraph.Ref{Kind: "Pod", Namespace: ns, Name: pod}, depgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	c.Graph = g
	return c
}
