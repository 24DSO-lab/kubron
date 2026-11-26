// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// addAllPods добавляет в контекст все Pod'ы кластера (как будто цель — их набор).
func addAllPods(t *testing.T, c *diagnose.Context, f reader) {
	t.Helper()
	res, err := f.List(context.Background(), gvkPod, diagnose.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c.Pods = nil
	for i := range res.Items {
		var p corev1.Pod
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(res.Items[i].Object, &p); err != nil {
			t.Fatal(err)
		}
		c.Pods = append(c.Pods, &p)
	}
}

func TestWarningEventsMakeNodeAbnormal(t *testing.T) {
	w := &world{}
	w.deployment("prod", "api", "n1", 1, 20)
	w.deployment("prod", "web", "n1", 1, 20)
	w.node("n1", false)
	w.add("apiVersion: v1\nkind: Event\nmetadata: {name: e1, namespace: default}\ntype: Warning\nreason: SystemOOM\ninvolvedObject: {kind: Node, name: n1}")
	w.add("apiVersion: v1\nkind: Event\nmetadata: {name: e2, namespace: default}\ntype: Normal\nreason: Starting\ninvolvedObject: {kind: Node, name: n1}")
	res, _ := run(t, w, "prod", "api-hapi-0")
	b := res.Best()
	if b == nil || b.Confidence != diagnose.ConfidenceMedium {
		t.Fatalf("кандидат: %+v", b)
	}
	var found bool
	for _, f := range res.Facts {
		if f.Code == "correlation.shared_node.warning_events" && f.Params["count"] == "1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("нет факта о предупреждениях узла (Normal не должен считаться): %+v", res.Facts)
	}
}

func TestPodSymptoms(t *testing.T) {
	w := &world{}
	w.node("n1", false, "Ready=False@30")
	w.add("apiVersion: v1\nkind: Pod\nmetadata: {name: crash, namespace: prod}\nspec: {nodeName: n1}\nstatus:\n phase: Running\n containerStatuses:\n - {name: c, state: {waiting: {reason: CrashLoopBackOff}}, lastState: {terminated: {reason: Error, finishedAt: \"%s\"}}}", ts(10))
	w.add("apiVersion: v1\nkind: Pod\nmetadata: {name: oom, namespace: prod}\nspec: {nodeName: n1}\nstatus:\n phase: Running\n containerStatuses:\n - {name: c, state: {running: {}}, lastState: {terminated: {reason: OOMKilled, finishedAt: \"%s\"}}}", ts(12))
	w.add("apiVersion: v1\nkind: Pod\nmetadata: {name: evicted, namespace: prod}\nspec: {nodeName: n1}\nstatus: {phase: Failed, reason: Evicted}")
	w.add("apiVersion: v1\nkind: Pod\nmetadata: {name: done, namespace: prod}\nspec: {nodeName: n1}\nstatus: {phase: Succeeded}")
	res, _ := run(t, w, "prod", "crash")
	b := res.Best()
	if b == nil || b.Total != 3 {
		t.Fatalf("кандидат: %+v", b)
	}
	want := map[string]string{"crash": "pod.crash_loop", "oom": "pod.oom_killed", "evicted": "pod.evicted"}
	for _, a := range b.Affected {
		if len(a.Symptoms) != 1 || a.Symptoms[0] != want[a.Name] {
			t.Errorf("%s: симптомы %v, want %s", a.Name, a.Symptoms, want[a.Name])
		}
		delete(want, a.Name)
	}
	if len(want) != 0 {
		t.Fatalf("не найдены нагрузки: %v (Succeeded-Pod не должен попадать)", want)
	}
}

func TestReadFailureReasons(t *testing.T) {
	for _, tt := range []struct {
		err    error
		reason string
	}{
		{depgraph.ErrForbidden, "forbidden"},
		{depgraph.ErrUnavailable, "unavailable"},
		{depgraph.ErrTooLarge, "too_large"},
		{errors.New("boom"), "unavailable"},
		{context.DeadlineExceeded, "deadline"},
	} {
		t.Run(tt.reason, func(t *testing.T) {
			w := &world{}
			w.deployment("prod", "api", "n1", 1, 20)
			w.node("n1", false, "Ready=False@30")
			f := w.reader(t)
			c := diagCtx(t, f, "prod", "api-hapi-0")
			f.Fail(gvkPod, tt.err)
			res, err := correlation.Run(context.Background(), reader{f}, c, now)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range res.Missing {
				if m.Resource.Kind == "Pod" && m.Params["reason"] == tt.reason {
					return
				}
			}
			t.Fatalf("нет Missing с причиной %q: %+v", tt.reason, res.Missing)
		})
	}
}

func TestReadBudgetExhausted(t *testing.T) {
	w := &world{}
	for i := 0; i < 8; i++ {
		n := fmt.Sprintf("n%d", i)
		w.node(n, false, "Ready=False@30")
		w.barePod("prod", "p-"+n, n, 20)
	}
	f := w.reader(t)
	c := diagCtx(t, f, "prod", "p-n0")
	addAllPods(t, c, reader{f})
	before := f.Calls()
	res, err := correlation.Run(context.Background(), reader{f}, c, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Calls() - before; got > correlation.MaxExtraReads {
		t.Fatalf("чтений %d, предел %d", got, correlation.MaxExtraReads)
	}
	var budget bool
	for _, m := range res.Missing {
		budget = budget || m.Params["reason"] == "budget"
	}
	if !budget || len(res.Partial) == 0 {
		t.Fatalf("исчерпание бюджета не отражено: missing=%+v partial=%v", res.Missing, res.Partial)
	}
}

func TestCandidateOrderAcrossKinds(t *testing.T) {
	w := &world{}
	w.add("apiVersion: storage.k8s.io/v1\nkind: StorageClass\nmetadata: {name: fast}\nprovisioner: kubernetes.io/no-provisioner")
	w.node("n1", false)
	for _, d := range []string{"db", "queue", "search"} {
		w.add("apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata: {name: data-%s, namespace: prod}\nspec: {storageClassName: fast}\nstatus: {phase: Pending}", d)
		w.add("apiVersion: v1\nkind: Pod\nmetadata: {name: %s-0, namespace: prod}\nspec:\n volumes: [{name: v, persistentVolumeClaim: {claimName: data-%s}}]\nstatus: {phase: Pending}", d, d)
	}
	for _, d := range []string{"a", "b", "c", "d"} { // узел с четырьмя нагрузками без признаков: low
		w.deployment("prod", d, "n1", 1, 20)
	}
	f := w.reader(t)
	c := diagCtx(t, f, "prod", "db-0")
	addAllPods(t, c, reader{f})
	res, err := correlation.Run(context.Background(), reader{f}, c, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("кандидатов %d, want 2: %+v", len(res.Candidates), res.Candidates)
	}
	if res.Candidates[0].Kind != correlation.KindSharedStorage || res.Candidates[1].Kind != correlation.KindSharedNode {
		t.Fatalf("порядок: %s, %s (сначала более уверенный)", res.Candidates[0].Kind, res.Candidates[1].Kind)
	}
	if res.Candidates[0].Confidence != diagnose.ConfidenceMedium || res.Candidates[1].Confidence != diagnose.ConfidenceLow {
		t.Fatalf("уверенность: %s, %s", res.Candidates[0].Confidence, res.Candidates[1].Confidence)
	}
	for _, c := range res.Candidates { // ID стабилен и не пуст
		if c.ID == "" {
			t.Fatal("пустой ID кандидата")
		}
	}
}

func TestFactsRenumbered(t *testing.T) {
	w := fourDeployments("n1", 20)
	w.node("n1", false, "MemoryPressure=True@30")
	res, _ := run(t, w, "prod", "api-hapi-0")
	for i, f := range res.Facts {
		if want := fmt.Sprintf("cf%d", i+1); f.ID != want {
			t.Fatalf("факт %d имеет ID %q, want %q", i, f.ID, want)
		}
		if f.Kind != diagnose.KindFact || f.Source.Kind != diagnose.SourceDerived || f.Resource.Kind != "Node" {
			t.Fatalf("факт: %+v", f)
		}
	}
}
