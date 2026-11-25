// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation_test

import (
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// chainWorld — Ingress → Service → Deployment api (Pod'ы в CrashLoopBackOff). Service без готовых endpoints.
func chainWorld() *world {
	w := &world{}
	w.crashDeployment("prod", "api", "n1", 2, 20)
	w.node("n1", false)
	w.add("apiVersion: v1\nkind: Service\nmetadata: {name: api, namespace: prod}\nspec: {selector: {pod-template-hash: hapi}, ports: [{port: 80}]}")
	w.ingress("prod", "api", "", "api")
	return w
}

func TestSharedRootChainFromEveryLayer(t *testing.T) {
	// Расследование любого слоя цепочки приходит к одному workload-владельцу и одному ключу кандидата — именно по
	// нему автоприсоединение собирает расследования в одну проблему.
	res, _ := run(t, chainWorld(), "prod", "api-hapi-0")
	c := byKind(res, correlation.KindSharedRoot)
	if c == nil {
		t.Fatalf("shared_root нет; кандидаты: %+v", res.Candidates)
	}
	if got, want := c.Key(), "shared_root:apps/Deployment/prod/api"; got != want {
		t.Fatalf("Key=%q, want %q", got, want)
	}
	if c.Total < 2 {
		t.Fatalf("Total=%d: нужно не менее двух слоёв", c.Total)
	}
	if c.Confidence == diagnose.ConfidenceHigh {
		t.Fatalf("у правила нет временной связи, high невозможен: %s", c.Confidence)
	}
	codes := factCodes(res, c)
	if !hasCode(codes, "correlation.shared_root.candidate") || !hasCode(codes, "correlation.shared_root.layers") {
		t.Fatalf("факты: %v", codes)
	}
}

func TestSharedRootSameKeyAcrossTargets(t *testing.T) {
	// Тот же кластер, расследование начато с Pod'а второй реплики: ключ тот же.
	res1, _ := run(t, chainWorld(), "prod", "api-hapi-0")
	res2, _ := run(t, chainWorld(), "prod", "api-hapi-1")
	c1, c2 := byKind(res1, correlation.KindSharedRoot), byKind(res2, correlation.KindSharedRoot)
	if c1 == nil || c2 == nil {
		t.Fatalf("кандидаты: %v %v", c1, c2)
	}
	if c1.Key() != c2.Key() {
		t.Fatalf("ключи разошлись: %q и %q", c1.Key(), c2.Key())
	}
}

func TestSharedRootNeedsTwoLayers(t *testing.T) {
	w := &world{}
	w.crashDeployment("prod", "api", "n1", 2, 20) // над Deployment ничего нет
	w.node("n1", false)
	res, _ := run(t, w, "prod", "api-hapi-0")
	if c := byKind(res, correlation.KindSharedRoot); c != nil {
		t.Fatalf("один слой не даёт кандидата: %+v", c)
	}
}

func TestSharedRootHealthyWorkloadIsNotCandidate(t *testing.T) {
	w := &world{}
	w.deployment("prod", "api", "n1", 2, 0)
	w.node("n1", false)
	w.add("apiVersion: v1\nkind: Service\nmetadata: {name: api, namespace: prod}\nspec: {selector: {pod-template-hash: hapi}}")
	w.ingress("prod", "api", "", "api")
	res, _ := run(t, w, "prod", "api-hapi-0")
	if c := byKind(res, correlation.KindSharedRoot); c != nil {
		t.Fatalf("здоровая нагрузка не может быть первопричиной: %+v", c)
	}
}

func TestSharedRootNoReadsAndDeterministic(t *testing.T) {
	f := chainWorld().reader(t)
	c := diagCtx(t, f, "prod", "api-hapi-0")
	r1, extra := runCtx(t, f, c)
	if byKind(r1, correlation.KindSharedRoot) == nil {
		t.Fatal("кандидата нет")
	}
	_ = extra
	noSecretReads(t, f)
	for _, call := range f.CallLog() {
		if strings.Contains(call, "Secret") {
			t.Fatal(call)
		}
	}
	r2, _ := run(t, chainWorld(), "prod", "api-hapi-0")
	if mustJSON(t, r1) != mustJSON(t, r2) {
		t.Fatal("результат недетерминирован")
	}
	golden(t, "shared_root", r1)
}
