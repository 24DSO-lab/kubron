// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// Правило chain.config_refs (ADR-0031 §6, этап 2). Сценарии и золотые отчёты — testdata/chain/config_*.

var configLeakMarkers = []string{"LEAK-DATA-TOKEN", "LEAK-DATA-PASSWORD", "LEAK-LAST-APPLIED", "TEVBSy1CSU5BUlk"}

// Содержимое ConfigMap не попадает ни в граф контекста, ни в события, ни в JSON отчёта.
func TestConfigContentNeverReachesContextOrReport(t *testing.T) {
	t.Parallel()
	c := loadChainCases(t)["config_no_leak"]
	ctx := chainContext(t, "config_no_leak", c)

	cm := depgraph.Ref{Kind: "ConfigMap", Namespace: "app", Name: "app-env"}
	n, ok := ctx.Graph.Node(cm)
	if !ok || n.Health != depgraph.Healthy || n.Object == nil {
		t.Fatalf("the ConfigMap must be in the graph as an existing node: %+v", n)
	}
	var dump strings.Builder
	for _, node := range ctx.Graph.Nodes() {
		if node.Object != nil {
			raw, _ := json.Marshal(node.Object.Object)
			dump.Write(raw)
		}
	}
	rep := Analyze(ctx)
	raw, _ := json.Marshal(rep)
	dump.Write(raw)
	fmt.Fprintf(&dump, "%+v %+v %+v %+v", ctx.Events, ctx.Missing, ctx.Partial, ctx.Target)
	for _, m := range configLeakMarkers {
		if strings.Contains(dump.String(), m) {
			t.Errorf("%q leaked into the context or the report", m)
		}
	}
}

func fixtureReport(t *testing.T, name string) Report {
	t.Helper()
	return Analyze(chainContext(t, name, loadChainCases(t)[name]))
}

// ConfigMap есть, а kubelet жалуется на ключ: уверенность текстового правила не растёт, граф гипотез не добавляет,
// у человека — шаг проверить ключ.
func TestConfigPresentKeyMissingDoesNotRaiseConfidence(t *testing.T) {
	t.Parallel()
	rep := fixtureReport(t, "config_present_key_missing")
	var cfg []Hypothesis
	for _, h := range rep.Hypotheses {
		if h.Code == "config.missing_reference" {
			cfg = append(cfg, h)
		}
	}
	if len(cfg) != 1 || cfg[0].Resource.Kind != "Pod" || cfg[0].Confidence != ConfidenceHigh || len(cfg[0].Explains) != 0 {
		t.Fatalf("hypotheses = %+v: want only the text one, unchanged", cfg)
	}
	if !slices.ContainsFunc(rep.Steps, func(s Step) bool {
		return s.Code == "step.check_configmap_key" && s.Params["configmap"] == "app-env" && slices.Contains(s.Hypotheses, cfg[0].ID)
	}) {
		t.Errorf("no step to check the key: %+v", rep.Steps)
	}
	if len(rep.Chains) != 0 {
		t.Errorf("no chain expected, got %v", rep.Chains)
	}
}

// Отказ RBAC и предел чтения — «не знаем»: текстовая гипотеза не меняется, отчёт говорит, что проверить не удалось.
func TestConfigUnverifiedIsNeverReportedAsMissing(t *testing.T) {
	t.Parallel()
	for name, reason := range map[string]string{"config_forbidden": "forbidden", "config_truncated": "limit"} {
		rep := fixtureReport(t, name)
		for _, h := range rep.Hypotheses {
			if h.Resource.Kind == "ConfigMap" {
				t.Errorf("%s: hypothesis on a ConfigMap that could not be read: %+v", name, h)
			}
			if h.Code == "config.missing_reference" && h.Confidence != ConfidenceHigh {
				t.Errorf("%s: text confidence changed to %s without graph evidence", name, h.Confidence)
			}
		}
		if !slices.ContainsFunc(rep.Missing, func(m MissingContext) bool { return m.Code == "config.unverified" && m.Params["reason"] == reason }) {
			t.Errorf("%s: no config.unverified with reason %s: %+v", name, reason, rep.Missing)
		}
	}
}

// Первая итерация: без ConfigMap-ссылок в графе (флаг выключен) или без графа отчёт тот же, что был: одна текстовая
// гипотеза с прежней уверенностью, без цепочек и без записей правила.
func TestConfigFlagOffKeepsFirstIterationReport(t *testing.T) {
	t.Parallel()
	c := loadChainCases(t)["config_missing_envfrom"]
	raw, err := os.ReadFile(filepath.Join("testdata", "chain", "config_missing_envfrom", "manifests.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	off := chainOpts()
	off.Graph = &depgraph.Options{} // ExpandConfigMapRefs выключен явно
	ctx, err := Collect(t.Context(), newChain(t, string(raw)), c.Target, off)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range ctx.Graph.Nodes() {
		if n.Kind == "ConfigMap" {
			t.Fatalf("ConfigMap in the graph with the flag off: %+v", n)
		}
	}
	noGraph := *ctx
	noGraph.Graph = nil
	for name, rep := range map[string]Report{"flag off": Analyze(ctx), "no graph": Analyze(&noGraph)} {
		if len(rep.Hypotheses) != 1 || rep.Hypotheses[0].Code != "config.missing_reference" ||
			rep.Hypotheses[0].Confidence != ConfidenceHigh || rep.Hypotheses[0].Resource.Kind != "Pod" {
			t.Errorf("%s: hypotheses = %+v", name, rep.Hypotheses)
		}
		if len(rep.Chains) != 0 || slices.ContainsFunc(rep.Hypotheses, func(h Hypothesis) bool { return h.RuleID == ruleConfigRefsID }) {
			t.Errorf("%s: the rule must stay silent: chains=%v", name, rep.Chains)
		}
		if !slices.ContainsFunc(rep.Steps, func(s Step) bool { return s.Code == "step.check_referenced_object" }) {
			t.Errorf("%s: the human step of the text rule disappeared", name)
		}
	}
}

// Правило детерминировано и работает по тем же данным, что и текстовое: расследование Deployment с неготовым Pod'ом
// находит ту же причину (цель — не Pod).
func TestConfigRuleWorksForWorkloadTargets(t *testing.T) {
	t.Parallel()
	manifests := `
apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: app, uid: d1}
spec: {replicas: 1, selector: {matchLabels: {app: web}}}
---
apiVersion: apps/v1
kind: ReplicaSet
metadata:
  name: web-1
  namespace: app
  uid: r1
  labels: {app: web}
  ownerReferences: [{apiVersion: apps/v1, kind: Deployment, name: web, uid: d1, controller: true}]
spec: {replicas: 1, selector: {matchLabels: {app: web}}}
---
apiVersion: v1
kind: Pod
metadata:
  name: web-1-x
  namespace: app
  uid: p1
  labels: {app: web}
  ownerReferences: [{apiVersion: apps/v1, kind: ReplicaSet, name: web-1, uid: r1, controller: true}]
spec:
  containers: [{name: web, image: example/web, envFrom: [{configMapRef: {name: gone}}]}]
status:
  phase: Pending
  conditions: [{type: Ready, status: "False"}]
  containerStatuses: [{name: web, ready: false, state: {waiting: {reason: CreateContainerConfigError, message: "configmap \"gone\" not found"}}}]
`
	ctx, err := Collect(t.Context(), newChain(t, manifests), ResourceRef{Kind: "Deployment", Namespace: "app", Name: "web"}, chainOpts())
	if err != nil {
		t.Fatal(err)
	}
	rep := Analyze(ctx)
	if !slices.ContainsFunc(rep.Hypotheses, func(h Hypothesis) bool {
		return h.Code == "config.missing_reference" && h.Resource.Kind == "ConfigMap" && h.Resource.Name == "gone" && h.Confidence == ConfidenceHigh
	}) {
		t.Errorf("hypotheses = %+v", rep.Hypotheses)
	}
}
