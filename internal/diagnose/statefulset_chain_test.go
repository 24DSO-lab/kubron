// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// Правило chain.statefulset и цель StatefulSet (ADR-0031 §6, этап 3). Сценарии — testdata/chain/sts_*.

func stsHasHypothesis(rep Report, code string) bool {
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
	if stsHasHypothesis(Analyze(ctx), "sts.stuck_ordered_start") {
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
	if stsHasHypothesis(Analyze(ctx), "sts.update_partitioned") {
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

// stsOrdinalsWorld — StatefulSet db (OrderedReady или Parallel) с ordinals.start и Pod'ами по ordinal'ам: ready[o]
// задаёт наличие Pod'а (ключ есть) и его готовность. PVC шаблона у каждого Pod'а есть.
func stsOrdinalsWorld(start *int, replicas int, policy string, ready map[int]bool) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\nkind: Namespace\nmetadata: {name: app}\n---\napiVersion: v1\nkind: Node\nmetadata: {name: n1}\nstatus: {conditions: [{type: Ready, status: \"True\"}]}\n---\n")
	b.WriteString("apiVersion: apps/v1\nkind: StatefulSet\nmetadata: {name: db, namespace: app, uid: s1}\nspec:\n")
	fmt.Fprintf(&b, "  replicas: %d\n  serviceName: db\n  selector: {matchLabels: {app: db}}\n  volumeClaimTemplates: [{metadata: {name: data}}]\n", replicas)
	if start != nil {
		fmt.Fprintf(&b, "  ordinals: {start: %d}\n", *start)
	}
	if policy != "" {
		fmt.Fprintf(&b, "  podManagementPolicy: %s\n", policy)
	}
	b.WriteString("status: {replicas: 1, readyReplicas: 0}\n")
	for _, o := range slices.Sorted(maps.Keys(ready)) {
		status := `{phase: Pending, conditions: [{type: PodScheduled, status: "True"}, {type: Ready, status: "False"}], containerStatuses: [{name: db, ready: false, restartCount: 0, state: {waiting: {reason: ImagePullBackOff, message: 'Back-off pulling image'}}}]}`
		if ready[o] {
			status = `{phase: Running, conditions: [{type: PodScheduled, status: "True"}, {type: Ready, status: "True"}], containerStatuses: [{name: db, ready: true, restartCount: 0, state: {running: {startedAt: "2026-11-11T09:00:00Z"}}}]}`
		}
		fmt.Fprintf(&b, `---
apiVersion: v1
kind: Pod
metadata:
  name: db-%[1]d
  namespace: app
  uid: p%[1]d
  labels: {app: db}
  ownerReferences: [{apiVersion: apps/v1, kind: StatefulSet, name: db, uid: s1, controller: true}]
spec:
  nodeName: n1
  containers: [{name: db, image: example/db}]
  volumes: [{name: data, persistentVolumeClaim: {claimName: data-db-%[1]d}}]
status: %[2]s
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: data-db-%[1]d, namespace: app}
spec: {volumeName: pv-%[1]d}
status: {phase: Bound}
`, o, status)
	}
	return b.String()
}

func stsOrdinalsReport(t *testing.T, raw string, o *CollectOptions) Report {
	t.Helper()
	opts := chainOpts()
	if o != nil {
		opts = *o
	}
	ctx, err := Collect(t.Context(), newChain(t, raw), ResourceRef{Kind: "StatefulSet", Namespace: "app", Name: "db"}, opts)
	if err != nil {
		t.Fatal(err)
	}
	return Analyze(ctx)
}

func stuckHypothesis(rep Report) (Hypothesis, bool) {
	for _, h := range rep.Hypotheses {
		if h.Code == "sts.stuck_ordered_start" {
			return h, true
		}
	}
	return Hypothesis{}, false
}

func stsFactParams(rep Report, code string) map[string]string {
	for _, e := range rep.Facts {
		if e.Code == code {
			return e.Params
		}
	}
	return nil
}

func intPtr(v int) *int { return &v }

// OrderedReady: диапазон [start, start+replicas). Последний созданный Pod не готов, следующих ещё нет — запуск застрял,
// а число созданных считается от start.
func TestStatefulSetOrderedStartRespectsOrdinalsStart(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		start       *int
		ready       map[int]bool
		wantStuck   bool
		wantPod     string
		wantCreated string
	}{
		// База: без ordinals.start поведение прежнее.
		{"no ordinals, db-0 not ready", nil, map[int]bool{0: false}, true, "db-0", "1"},
		{"start 0, db-1 not ready", intPtr(0), map[int]bool{0: true, 1: false}, true, "db-1", "2"},
		// db-5 не Ready, db-6 и db-7 ещё не созданы.
		{"start 5, db-5 not ready", intPtr(5), map[int]bool{5: false}, true, "db-5", "1"},
		{"start 5, db-6 not ready", intPtr(5), map[int]bool{5: true, 6: false}, true, "db-6", "2"},
		// Все три реплики диапазона созданы: застревать больше нечему.
		{"start 5, all created", intPtr(5), map[int]bool{5: true, 6: true, 7: false}, false, "", ""},
		// Все готовы.
		{"start 5, all ready", intPtr(5), map[int]bool{5: true, 6: true}, false, "", ""},
		// Pod вне диапазона (остаток от прежних реплик) не «последний созданный» и не активная реплика.
		{"start 5, only leftover db-2 not ready", intPtr(5), map[int]bool{2: false}, false, "", ""},
		{"start 5, leftover db-9 above range", intPtr(5), map[int]bool{5: true, 9: false}, false, "", ""},
		{"start 5, leftover db-2 and db-5 not ready", intPtr(5), map[int]bool{2: false, 5: false}, true, "db-5", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rep := stsOrdinalsReport(t, stsOrdinalsWorld(tc.start, 3, "", tc.ready), nil)
			h, stuck := stuckHypothesis(rep)
			if stuck != tc.wantStuck {
				t.Fatalf("stuck_ordered_start = %v, want %v", stuck, tc.wantStuck)
			}
			if !stuck {
				return
			}
			if h.Resource.Name != tc.wantPod {
				t.Errorf("candidate pod = %s, want %s", h.Resource.Name, tc.wantPod)
			}
			p := stsFactParams(rep, "sts.pods_not_created")
			if p["created"] != tc.wantCreated || p["replicas"] != "3" {
				t.Errorf("pods_not_created params = %v, want created=%s replicas=3", p, tc.wantCreated)
			}
		})
	}
}

// Parallel не ждёт готовности предыдущего Pod'а, поэтому stuck_ordered_start не выдаётся и с ordinals.start.
func TestStatefulSetParallelNeverStuckWithOrdinalsStart(t *testing.T) {
	t.Parallel()
	for _, start := range []*int{nil, intPtr(0), intPtr(5)} {
		first := 0
		if start != nil {
			first = *start
		}
		rep := stsOrdinalsReport(t, stsOrdinalsWorld(start, 3, "Parallel", map[int]bool{first: false}), nil)
		if _, stuck := stuckHypothesis(rep); stuck {
			t.Errorf("start=%v: Parallel must not produce stuck_ordered_start", start)
		}
		if slices.Contains(rep.Symptoms, SymptomStatefulSetStuck) {
			t.Errorf("start=%v: symptom %s for Parallel", start, SymptomStatefulSetStuck)
		}
	}
}

// Граф, усечённый лимитами, не даёт утверждать «следующих Pod'ов нет»: правило молчит и при ordinals.start.
func TestStatefulSetTruncatedGraphDoesNotClaimNextPodsMissing(t *testing.T) {
	t.Parallel()
	o := chainOpts()
	o.Graph = &depgraph.Options{Limits: depgraph.Limits{MaxNodes: 3, MaxEdges: 600, MaxDepth: 4, MaxReads: 40}}
	rep := stsOrdinalsReport(t, stsOrdinalsWorld(intPtr(5), 3, "", map[int]bool{5: true, 6: false}), &o)
	if !slices.Contains(rep.Partial, "Graph") {
		t.Fatalf("the graph must be reported partial: %v", rep.Partial)
	}
	if _, stuck := stuckHypothesis(rep); stuck {
		t.Error("a truncated graph must not claim that the next Pods are absent")
	}
	if p := stsFactParams(rep, "sts.pods_not_created"); p != nil {
		t.Errorf("pods_not_created fact from a truncated graph: %v", p)
	}
}

// Цепочка StatefulSet > Pod ведёт к настоящему Pod'у диапазона (db-5), а не к ordinal'у от нуля.
func TestStatefulSetOrdinalsStartChainReachesRealPod(t *testing.T) {
	t.Parallel()
	rep := stsOrdinalsReport(t, stsOrdinalsWorld(intPtr(5), 3, "", map[int]bool{5: false}), nil)
	h, ok := stuckHypothesis(rep)
	if !ok {
		t.Fatal("stuck_ordered_start is missing")
	}
	var path []string
	for _, ch := range rep.Chains {
		if ch.Candidate == h.ID {
			path = append(path, chainPath(ch))
		}
	}
	if !slices.Contains(path, "StatefulSet/db > Pod/db-5") {
		t.Errorf("chains = %q", path)
	}
}
