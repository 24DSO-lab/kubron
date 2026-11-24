// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Правило chain.statefulset и цель StatefulSet (ADR-0031 §6, этап 3). Сценарии — testdata/chain/sts_*.

func hasHypothesis(rep Report, code string) bool {
	return slices.ContainsFunc(rep.Hypotheses, func(h Hypothesis) bool { return h.Code == code })
}

// Кандидат первопричины — причина Pod'а (образ не найден), а не остановка запуска, которую она объясняет.
func TestStatefulSetRootCandidateIsThePodCause(t *testing.T) {
	t.Parallel()
	rep := fixtureReport(t, "sts_stuck_ordered")
	roots := rep.RootCandidates()
	if len(roots) != 1 {
		t.Fatalf("root candidates = %v", roots)
	}
	for _, h := range rep.Hypotheses {
		if h.ID == roots[0] && h.Code != "image.not_found" {
			t.Errorf("root candidate = %s, want image.not_found", h.Code)
		}
	}
}

// Без графа правило молчит, а цель всё равно собирается: отчёт строится по Pod'ам и событиям.
func TestStatefulSetRuleIsSilentWithoutGraph(t *testing.T) {
	t.Parallel()
	ctx := chainContext(t, "sts_stuck_ordered", loadChainCases(t)["sts_stuck_ordered"])
	if len(ctx.Pods) != 2 {
		t.Fatalf("pods in context = %d, want 2", len(ctx.Pods))
	}
	ctx.Graph = nil
	rep := Analyze(ctx)
	for _, h := range rep.Hypotheses {
		if strings.HasPrefix(h.Code, "sts.") || len(h.Explains) != 0 {
			t.Errorf("hypothesis %s without a graph", h.Code)
		}
	}
	if len(rep.Chains) != 0 {
		t.Errorf("chains = %v", rep.Chains)
	}
}

// Запуск «застрял» только пока есть что создавать: если все реплики созданы, неготовый Pod — обычная проблема Pod'а.
func TestStatefulSetNotStuckWhenAllReplicasExist(t *testing.T) {
	t.Parallel()
	c := loadChainCases(t)["sts_stuck_ordered"]
	raw := chainManifests(t, "sts_stuck_ordered")
	raw = strings.Replace(raw, "replicas: 3", "replicas: 2", 1)
	ctx, err := Collect(t.Context(), newChain(t, raw), c.Target, chainOpts())
	if err != nil {
		t.Fatal(err)
	}
	if hasHypothesis(Analyze(ctx), "sts.stuck_ordered_start") {
		t.Error("all replicas exist: nothing is waiting to be created")
	}
}

// Параллельная политика не блокирует запуск (случай sts_stuck_parallel), а partition без отставших реплик — не причина.
func TestStatefulSetPartitionWithAllUpdatedIsQuiet(t *testing.T) {
	t.Parallel()
	c := loadChainCases(t)["sts_partitioned"]
	raw := strings.Replace(chainManifests(t, "sts_partitioned"), "updatedReplicas: 1", "updatedReplicas: 3", 1)
	ctx, err := Collect(t.Context(), newChain(t, raw), c.Target, chainOpts())
	if err != nil {
		t.Fatal(err)
	}
	if hasHypothesis(Analyze(ctx), "sts.update_partitioned") {
		t.Error("every replica is updated: partition holds nothing back")
	}
}

// Цель без namespace и StatefulSet без селектора — явные пробелы, а не паника.
func TestStatefulSetTargetEdgeCases(t *testing.T) {
	t.Parallel()
	if _, err := Collect(t.Context(), newChain(t), ResourceRef{Kind: "StatefulSet", Name: "db"}, chainOpts()); err == nil {
		t.Error("a target without namespace must fail")
	}
	raw := strings.Replace(chainManifests(t, "sts_healthy"), "selector: {matchLabels: {app: db}}", "selector: {}", 1)
	ctx, err := Collect(t.Context(), newChain(t, raw), loadChainCases(t)["sts_healthy"].Target, chainOpts())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(Analyze(ctx).Missing, func(m MissingContext) bool { return m.Code == "statefulset.selector_missing" }) {
		t.Error("an empty selector must be reported")
	}
}

func chainManifests(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "chain", name, "manifests.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
