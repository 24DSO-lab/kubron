// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

var (
	gvkPod = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	gvkRS  = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"}
)

func run(t *testing.T, w *world, ns, pod string) (*correlation.Result, int) {
	t.Helper()
	f := w.reader(t)
	c := diagCtx(t, f, ns, pod)
	before := f.Calls()
	res, err := correlation.Run(context.Background(), reader{f}, c, now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res, f.Calls() - before
}

func fourDeployments(node string, failedAgo int) *world {
	w := &world{}
	for _, d := range []string{"api", "web", "worker", "cache"} {
		w.deployment("prod", d, node, 3, failedAgo)
	}
	return w
}

func TestSharedNodeConfidence(t *testing.T) {
	tests := []struct {
		name    string
		build   func() *world
		want    diagnose.Confidence // "" — кандидата нет
		wantTot int
	}{
		{"high: давление памяти до сбоев и четыре нагрузки", func() *world {
			w := fourDeployments("n1", 20)
			w.node("n1", false, "MemoryPressure=True@30")
			return w
		}, diagnose.ConfidenceHigh, 4},
		{"low: четыре нагрузки без признаков узла", func() *world {
			w := fourDeployments("n1", 20)
			w.node("n1", false)
			return w
		}, diagnose.ConfidenceLow, 4},
		{"medium: две нагрузки на NotReady узле", func() *world {
			w := &world{}
			w.deployment("prod", "api", "n1", 2, 20)
			w.deployment("prod", "web", "n1", 2, 20)
			w.node("n1", false, "Ready=False@25")
			return w
		}, diagnose.ConfidenceMedium, 2},
		{"medium: состояние узла изменилось после сбоев — только признак (б)", func() *world {
			w := fourDeployments("n1", 40)
			w.node("n1", false, "DiskPressure=True@5")
			return w
		}, diagnose.ConfidenceMedium, 4},
		{"medium: узел изменился давно — вне окна, признак (б)", func() *world {
			w := fourDeployments("n1", 20)
			w.node("n1", false, "MemoryPressure=True@300")
			return w
		}, diagnose.ConfidenceMedium, 4},
		{"нет кандидата: две нагрузки, узел в порядке", func() *world {
			w := &world{}
			w.deployment("prod", "api", "n1", 2, 20)
			w.deployment("prod", "web", "n1", 2, 20)
			w.node("n1", false)
			return w
		}, "", 0},
		{"нет кандидата: все Pod'ы здоровы", func() *world {
			w := &world{}
			w.deployment("prod", "api", "n1", 2, 0)
			w.node("n1", true, "MemoryPressure=True@30")
			return w
		}, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, _ := run(t, tt.build(), "prod", "api-hapi-0")
			best := res.Best()
			if tt.want == "" {
				if best != nil {
					t.Fatalf("ожидался пустой результат, получен %+v", *best)
				}
				return
			}
			if best == nil {
				t.Fatal("кандидата нет")
			}
			if best.Kind != correlation.KindSharedNode || best.Confidence != tt.want || best.Total != tt.wantTot {
				t.Fatalf("kind=%s confidence=%s total=%d, want shared_node/%s/%d", best.Kind, best.Confidence, best.Total, tt.want, tt.wantTot)
			}
			if got := best.Key(); got != "shared_node:core/Node//n1" {
				t.Fatalf("Key = %q", got)
			}
			if len(best.Supporting) == 0 {
				t.Fatal("у кандидата нет подтверждающих фактов")
			}
			ids := map[string]bool{}
			for _, f := range res.Facts {
				ids[f.ID] = true
			}
			for _, id := range best.Supporting {
				if !ids[id] {
					t.Fatalf("Supporting ссылается на отсутствующий факт %q", id)
				}
			}
		})
	}
}

func TestSharedNodeAffectedDetails(t *testing.T) {
	w := fourDeployments("n1", 20)
	w.node("n1", false, "MemoryPressure=True@30")
	res, _ := run(t, w, "prod", "api-hapi-0")
	best := res.Best()
	if best == nil || len(best.Affected) != 4 {
		t.Fatalf("Affected = %+v", best)
	}
	for i, name := range []string{"api", "cache", "web", "worker"} { // сортировка по имени
		a := best.Affected[i]
		if a.Kind != "Deployment" || a.Name != name || a.Namespace != "prod" || a.ProblemPods != 3 ||
			len(a.Symptoms) != 1 || a.Symptoms[0] != "pod.not_ready" {
			t.Fatalf("Affected[%d] = %+v", i, a)
		}
		if want := now.Add(-20 * time.Minute); !a.Since.Equal(want) {
			t.Fatalf("Since = %s, want %s", a.Since, want)
		}
	}
	if best.Truncated || res.Partial != nil {
		t.Fatalf("полный результат помечен усечённым: truncated=%v partial=%v", best.Truncated, res.Partial)
	}
}

func TestPotentialFromGraph(t *testing.T) {
	w := &world{}
	w.deployment("prod", "api", "n1", 1, 20) // в сбое
	w.deployment("prod", "web", "n1", 2, 20) // в сбое
	w.deployment("prod", "cache", "n1", 2, 20)
	w.deployment("prod", "batch", "n1", 2, 0) // здоровая нагрузка на том же узле — «потенциальная»
	w.node("n1", false, "Ready=False@30")
	f := w.reader(t)
	// Цель — сам узел: граф от Node содержит все Pod'ы на нём.
	c := diagCtx(t, f, "prod", "api-hapi-0")
	g, err := depgraph.Build(context.Background(), f, depgraph.Ref{Kind: "Node", Name: "n1"}, depgraph.Options{})
	if err != nil {
		t.Fatal(err)
	}
	c.Graph = g
	res, err := correlation.Run(context.Background(), reader{f}, c, now)
	if err != nil {
		t.Fatal(err)
	}
	b := res.Best()
	if b == nil || b.Total != 3 {
		t.Fatalf("результат: %+v", b)
	}
	if len(b.Potential) != 1 || b.Potential[0].Kind != "Deployment" || b.Potential[0].Name != "batch" ||
		b.Potential[0].ProblemPods != 0 || len(b.Potential[0].Symptoms) != 0 {
		t.Fatalf("Potential = %+v, want одна здоровая нагрузка batch", b.Potential)
	}
}

func TestSharedStorage(t *testing.T) {
	storageWorld := func(scYAML string, pvcPhase string) *world {
		w := &world{}
		w.add("%s", scYAML)
		for i, d := range []string{"db", "queue", "search"} {
			w.add("apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata: {name: data-%s, namespace: prod}\nspec: {storageClassName: fast}\nstatus: {phase: %s}", d, pvcPhase)
			w.add("apiVersion: v1\nkind: Pod\nmetadata:\n name: %s-0\n namespace: prod\nspec:\n volumes: [{name: v, persistentVolumeClaim: {claimName: data-%s}}]\nstatus:\n phase: Pending\n conditions:\n - {type: PodScheduled, status: \"False\", lastTransitionTime: \"%s\"}", d, d, ts(20+i))
			w.add("apiVersion: v1\nkind: Event\nmetadata: {name: ev-%s, namespace: prod}\nreason: ProvisioningFailed\ninvolvedObject: {kind: PersistentVolumeClaim, namespace: prod, name: data-%s}\nlastTimestamp: \"%s\"", d, d, ts(19+i))
		}
		return w
	}
	t.Run("high: класс без provisioner, события и три нагрузки", func(t *testing.T) {
		w := storageWorld("apiVersion: storage.k8s.io/v1\nkind: StorageClass\nmetadata: {name: fast}\nprovisioner: kubernetes.io/no-provisioner", "Pending")
		res, _ := run(t, w, "prod", "db-0")
		b := res.Best()
		if b == nil || b.Kind != correlation.KindSharedStorage || b.Confidence != diagnose.ConfidenceHigh || b.Total != 3 {
			t.Fatalf("кандидат: %+v", b)
		}
		if got := b.Key(); got != "shared_storage:storage.k8s.io/StorageClass//fast" {
			t.Fatalf("Key = %q", got)
		}
		if b.Affected[0].Symptoms[0] != "pod.pending" {
			t.Fatalf("симптомы: %+v", b.Affected[0])
		}
	})
	t.Run("PVC Bound — кандидата нет", func(t *testing.T) {
		w := storageWorld("apiVersion: storage.k8s.io/v1\nkind: StorageClass\nmetadata: {name: fast}\nprovisioner: csi.example/x", "Bound")
		res, _ := run(t, w, "prod", "db-0")
		if res.Best() != nil {
			t.Fatalf("неожиданный кандидат: %+v", *res.Best())
		}
	})
	t.Run("WaitForFirstConsumer — ожидание Pod'а не поломка", func(t *testing.T) {
		w := storageWorld("apiVersion: storage.k8s.io/v1\nkind: StorageClass\nmetadata: {name: fast}\nprovisioner: csi.example/x\nvolumeBindingMode: WaitForFirstConsumer", "Pending")
		res, _ := run(t, w, "prod", "db-0")
		if res.Best() != nil {
			t.Fatalf("неожиданный кандидат: %+v", *res.Best())
		}
	})
}

func TestDegradation(t *testing.T) {
	t.Run("список Pod'ов запрещён: берём Pod'ы контекста, список помечен усечённым", func(t *testing.T) {
		w := &world{}
		w.deployment("prod", "api", "n1", 1, 20)
		w.node("n1", false, "Ready=False@30")
		f := w.reader(t)
		c := diagCtx(t, f, "prod", "api-hapi-0")
		f.Forbid(gvkPod)
		res, err := correlation.Run(context.Background(), reader{f}, c, now)
		if err != nil {
			t.Fatal(err)
		}
		b := res.Best()
		if b == nil || !b.Truncated || b.Total != 1 {
			t.Fatalf("кандидат: %+v", b)
		}
		if len(res.Partial) != 1 || res.Partial[0] != "correlation" {
			t.Fatalf("Partial = %v", res.Partial)
		}
		found := false
		for _, m := range res.Missing {
			if m.Code == "correlation.unavailable" && m.Resource.Kind == "Pod" && m.Params["reason"] == "forbidden" {
				found = true
			}
		}
		if !found {
			t.Fatalf("в Missing нет причины forbidden: %+v", res.Missing)
		}
	})
	t.Run("ReplicaSet недоступен: Deployment выводится по hash", func(t *testing.T) {
		w := fourDeployments("n1", 20)
		w.node("n1", false, "MemoryPressure=True@30")
		f := w.reader(t)
		c := diagCtx(t, f, "prod", "api-hapi-0")
		f.Forbid(gvkRS)
		res, err := correlation.Run(context.Background(), reader{f}, c, now)
		if err != nil {
			t.Fatal(err)
		}
		if b := res.Best(); b == nil || b.Total != 4 || b.Affected[0].Kind != "Deployment" {
			t.Fatalf("кандидат: %+v", b)
		}
		if len(res.Partial) == 0 {
			t.Fatal("недоступность ReplicaSet не отражена в Partial")
		}
	})
	t.Run("общая ошибка чтения не роняет Run", func(t *testing.T) {
		w := &world{}
		w.deployment("prod", "api", "n1", 1, 20)
		w.node("n1", false)
		f := w.reader(t)
		c := diagCtx(t, f, "prod", "api-hapi-0")
		f.Fail(gvkPod, errors.New("boom"))
		if _, err := correlation.Run(context.Background(), reader{f}, c, now); err != nil {
			t.Fatalf("Run вернул ошибку: %v", err)
		}
	})
	t.Run("остаток до deadline меньше резерва — корреляция не запускается", func(t *testing.T) {
		w := fourDeployments("n1", 20)
		w.node("n1", false, "MemoryPressure=True@30")
		f := w.reader(t)
		c := diagCtx(t, f, "prod", "api-hapi-0")
		ctx, cancel := context.WithDeadline(context.Background(), now.Add(correlation.MinDeadlineReserve-time.Second))
		defer cancel()
		before := f.Calls()
		res, err := correlation.Run(ctx, reader{f}, c, now)
		if err != nil {
			t.Fatal(err)
		}
		if res.Best() != nil || len(res.Partial) != 1 || f.Calls() != before {
			t.Fatalf("candidates=%v partial=%v reads=%d", res.Candidates, res.Partial, f.Calls()-before)
		}
	})
	t.Run("nil-вход", func(t *testing.T) {
		res, err := correlation.Run(context.Background(), nil, nil, now)
		if err != nil || res == nil || res.Best() != nil {
			t.Fatalf("res=%v err=%v", res, err)
		}
	})
}

func TestLimits(t *testing.T) {
	t.Run("MaxAffected: список усечён, Total точный", func(t *testing.T) {
		w := &world{}
		n := correlation.MaxAffected + 10
		for i := 0; i < n; i++ {
			w.barePod("prod", fmt.Sprintf("p%03d", i), "n1", 20)
		}
		w.node("n1", false, "Ready=False@30")
		res, _ := run(t, w, "prod", "p000")
		b := res.Best()
		if b == nil || b.Total != n || len(b.Affected) != correlation.MaxAffected {
			t.Fatalf("total=%d affected=%d", b.Total, len(b.Affected))
		}
		if b.Affected[0].Name != "p000" {
			t.Fatalf("порядок: первым %q", b.Affected[0].Name)
		}
	})
	t.Run("бюджет чтений не превышен", func(t *testing.T) {
		w := fourDeployments("n1", 20)
		w.node("n1", false, "MemoryPressure=True@30")
		_, reads := run(t, w, "prod", "api-hapi-0")
		if reads > correlation.MaxExtraReads {
			t.Fatalf("чтений %d, предел %d", reads, correlation.MaxExtraReads)
		}
	})
}

func TestDeterministic(t *testing.T) {
	w := fourDeployments("n1", 20)
	w.node("n1", false, "MemoryPressure=True@30")
	f := w.reader(t)
	c := diagCtx(t, f, "prod", "api-hapi-0")
	var first []byte
	for i := 0; i < 5; i++ {
		res, err := correlation.Run(context.Background(), reader{f}, c, now)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(res)
		if first == nil {
			first = b
		} else if string(first) != string(b) {
			t.Fatalf("результат недетерминирован:\n%s\n%s", first, b)
		}
	}
}

func TestNoSecretsOrValuesInSnapshot(t *testing.T) {
	w := fourDeployments("n1", 20)
	w.node("n1", false, "MemoryPressure=True@30")
	res, _ := run(t, w, "prod", "api-hapi-0")
	b, _ := json.Marshal(res)
	for _, bad := range []string{"pod-template-hash", "labels", "annotations"} { // UID узла в ссылке источника допустим
		if strings.Contains(string(b), bad) {
			t.Fatalf("в снимке есть %q: %s", bad, b)
		}
	}
}
