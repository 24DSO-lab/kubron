// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/depgraph/graphtest"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// Тесты этапа C строят граф вручную (depgraph.New/AddNode/AddEdge), а живые чтения отдаёт фейк: так правила
// проверяются без реализаций видов D1–D4 и без кластера.

// cmDeployment добавляет Deployment из n Pod'ов в Pending, которые ссылаются на ConfigMap cm. agoMin — когда
// созданы Pod'ы (от него считается время сбоя), optional — ссылка с optional: true.
func (w *world) cmDeployment(ns, name, cm string, n, agoMin int, optional bool) {
	hash := "h" + name
	w.add("apiVersion: apps/v1\nkind: ReplicaSet\nmetadata:\n name: %s-%s\n namespace: %s\n ownerReferences: [{apiVersion: apps/v1, kind: Deployment, name: %s, controller: true}]", name, hash, ns, name)
	for i := 0; i < n; i++ {
		w.add("apiVersion: v1\nkind: Pod\nmetadata:\n name: %s-%s-%d\n namespace: %s\n creationTimestamp: \"%s\"\n labels: {pod-template-hash: %s}\n"+
			" ownerReferences: [{apiVersion: apps/v1, kind: ReplicaSet, name: %s-%s, controller: true}]\n"+
			"spec:\n containers:\n - name: app\n   image: img\n   envFrom: [{configMapRef: {name: %s, optional: %v}}]\nstatus: {phase: Pending}",
			name, hash, i, ns, ts(agoMin), hash, name, hash, cm, optional)
	}
}

// crashDeployment добавляет Deployment из n Pod'ов в CrashLoopBackOff на узле node.
func (w *world) crashDeployment(ns, name, node string, n, agoMin int) {
	hash := "h" + name
	w.add("apiVersion: apps/v1\nkind: ReplicaSet\nmetadata:\n name: %s-%s\n namespace: %s\n ownerReferences: [{apiVersion: apps/v1, kind: Deployment, name: %s, controller: true}]", name, hash, ns, name)
	for i := 0; i < n; i++ {
		w.add("apiVersion: v1\nkind: Pod\nmetadata:\n name: %s-%s-%d\n namespace: %s\n labels: {pod-template-hash: %s}\n"+
			" ownerReferences: [{apiVersion: apps/v1, kind: ReplicaSet, name: %s-%s, controller: true}]\n"+
			"spec: {nodeName: %s, containers: [{name: app, image: img}]}\n"+
			"status:\n phase: Running\n conditions:\n - {type: Ready, status: \"False\", lastTransitionTime: \"%s\"}\n"+
			" containerStatuses:\n - name: app\n   ready: false\n   restartCount: 7\n   state: {waiting: {reason: CrashLoopBackOff}}\n"+
			"   lastState: {terminated: {reason: Error, exitCode: 1, finishedAt: \"%s\"}}",
			name, hash, i, ns, hash, name, hash, node, ts(agoMin), ts(agoMin))
	}
}

// ingress добавляет Ingress с одним backend; class пуст — класс не указан.
func (w *world) ingress(ns, name, class, service string) {
	cls := ""
	if class != "" {
		cls = "\n ingressClassName: " + class
	}
	w.add("apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata: {name: %s, namespace: %s}\nspec:%s\n rules:\n - http:\n    paths:\n    - {path: /, pathType: Prefix, backend: {service: {name: %s, port: {number: 80}}}}",
		name, ns, cls, service)
}

// manual — вручную построенный граф и контекст диагностики поверх фейка кластера.
type manual struct {
	t *testing.T
	f *graphtest.FakeReader
	g *depgraph.Graph
}

func newManual(t *testing.T, f *graphtest.FakeReader, seed depgraph.Ref) *manual {
	t.Helper()
	return &manual{t: t, f: f, g: depgraph.New(seed)}
}

// pod кладёт в граф Pod, прочитанный из фейка, с состоянием по его полям.
func (m *manual) pod(ns, name string) depgraph.Node {
	m.t.Helper()
	u, err := m.f.Get(context.Background(), gvkPod, ns, name)
	if err != nil {
		m.t.Fatalf("pod %s/%s: %v", ns, name, err)
	}
	h, reason := depgraph.HealthOf(u)
	n := depgraph.Node{Ref: depgraph.Ref{Kind: "Pod", Namespace: ns, Name: name}, Health: h, Reason: reason, Object: u}
	m.g.AddNode(n)
	return n
}

// missing кладёт заглушку Missing.
func (m *manual) missing(ref depgraph.Ref) depgraph.Node {
	n := depgraph.Node{Ref: ref, Health: depgraph.Missing, Reason: "not_found", Placeholder: true}
	m.g.AddNode(n)
	return n
}

// node кладёт вершину без объекта с заданным состоянием.
func (m *manual) node(ref depgraph.Ref, h depgraph.Health, reason string) depgraph.Node {
	n := depgraph.Node{Ref: ref, Health: h, Reason: reason}
	m.g.AddNode(n)
	return n
}

func (m *manual) edge(from, to depgraph.Ref, typ depgraph.EdgeType, state depgraph.EdgeState) {
	m.g.AddEdge(depgraph.Edge{From: from, To: to, Type: typ, State: state})
}

// ctx собирает контекст диагностики цели; pods — Pod'ы цели.
func (m *manual) ctx(target diagnose.ResourceRef, pods ...string) *diagnose.Context {
	m.t.Helper()
	c := &diagnose.Context{Target: target, ObservedAt: now, Nodes: map[string]*corev1.Node{}, Graph: m.g}
	for _, name := range pods {
		u, err := m.f.Get(context.Background(), gvkPod, target.Namespace, name)
		if err != nil {
			m.t.Fatal(err)
		}
		var p corev1.Pod
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &p); err != nil {
			m.t.Fatal(err)
		}
		c.Pods = append(c.Pods, &p)
	}
	return c
}

// runCtx запускает корреляцию на готовом контексте и возвращает результат и число дополнительных чтений.
func runCtx(t *testing.T, f *graphtest.FakeReader, c *diagnose.Context) (*correlation.Result, int) {
	t.Helper()
	before := f.Calls()
	res, err := correlation.Run(context.Background(), reader{f}, c, now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res, f.Calls() - before
}

// byKind возвращает первого кандидата вида kind или nil.
func byKind(res *correlation.Result, kind correlation.Kind) *correlation.Candidate {
	for i := range res.Candidates {
		if res.Candidates[i].Kind == kind {
			return &res.Candidates[i]
		}
	}
	return nil
}

// factCodes возвращает коды подтверждающих фактов кандидата.
func factCodes(res *correlation.Result, c *correlation.Candidate) []string {
	byID := map[string]string{}
	for _, f := range res.Facts {
		byID[f.ID] = f.Code
	}
	var out []string
	for _, id := range c.Supporting {
		out = append(out, byID[id])
	}
	return out
}

func hasCode(codes []string, want string) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

// factParam возвращает параметр первого факта кандидата с кодом code.
func factParam(t *testing.T, res *correlation.Result, c *correlation.Candidate, code, key string) string {
	t.Helper()
	for _, id := range c.Supporting {
		for _, f := range res.Facts {
			if f.ID == id && f.Code == code {
				return f.Params[key]
			}
		}
	}
	t.Fatalf("факта %s нет у кандидата", code)
	return ""
}

// noSecretReads проверяет, что журнал чтений фейка не содержит обращений к Secret.
func noSecretReads(t *testing.T, f *graphtest.FakeReader) {
	t.Helper()
	for _, call := range f.CallLog() {
		if strings.Contains(strings.ToLower(call), "secret") {
			t.Fatalf("обращение к Secret: %s", call)
		}
	}
}

// mustJSON — детерминированный снимок результата для сравнения двух запусков.
func mustJSON(t *testing.T, res *correlation.Result) string {
	t.Helper()
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
