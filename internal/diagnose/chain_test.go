// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// Фикстуры цепочечных правил (testdata/chain/<имя>/): manifests.yaml — мир для graphtest.FakeReader, case.json — цель,
// настройки чтения и ожидания, report.json — золотой отчёт (для случаев с "golden": true). Пересоздать отчёты:
//
//	KUBRON_UPDATE_CHAIN=1 go test ./internal/diagnose -run TestChainFixtures
type chainCase struct {
	Target ResourceRef `json:"target"`
	Forbid []string    `json:"forbid,omitempty"`
	Limits *struct {
		MaxNodes int `json:"maxNodes"`
		MaxEdges int `json:"maxEdges"`
		MaxDepth int `json:"maxDepth"`
		MaxReads int `json:"maxReads"`
	} `json:"limits,omitempty"`
	Golden bool `json:"golden,omitempty"`
	Expect struct {
		Symptoms    []string    `json:"symptoms,omitempty"`
		Hypotheses  []string    `json:"hypotheses,omitempty"`   // "код:уверенность"
		Explains    [][2]string `json:"explains,omitempty"`     // [код причины, код следствия]
		Chains      []string    `json:"chains,omitempty"`       // "код кандидата: Kind/имя > Kind/имя ..."
		Missing     []string    `json:"missing,omitempty"`      // коды MissingContext
		Partial     []string    `json:"partial,omitempty"`      // источники Partial
		Absent      []string    `json:"absent,omitempty"`       // коды гипотез, которых быть не должно
		AbsentSteps []string    `json:"absent_steps,omitempty"` // коды шагов, которых быть не должно
	} `json:"expect"`
}

var chainKindGVK = map[string]schema.GroupVersionKind{
	"PersistentVolumeClaim": gvkPVC, "Service": gvkService, "Node": gvkNode, "Event": gvkEvent, "Pod": gvkPod,
	"StorageClass": {Group: "storage.k8s.io", Version: "v1", Kind: "StorageClass"},
}

func loadChainCases(t *testing.T) map[string]chainCase {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join("testdata", "chain", "*", "case.json"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no chain fixtures: %v", err)
	}
	out := map[string]chainCase{}
	for _, f := range dirs {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var c chainCase
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out[filepath.Base(filepath.Dir(f))] = c
	}
	return out
}

// chainContext собирает контекст случая: мир из manifests.yaml, запреты и пределы из case.json.
func chainContext(t *testing.T, name string, c chainCase) *Context {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "chain", name, "manifests.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	r := newChain(t, string(raw))
	for _, k := range c.Forbid {
		gvk, ok := chainKindGVK[k]
		if !ok {
			t.Fatalf("%s: unknown kind %q in forbid", name, k)
		}
		r.Forbid(gvk)
	}
	o := chainOpts()
	if c.Limits != nil {
		o.Graph = &depgraph.Options{Limits: depgraph.Limits{
			MaxNodes: c.Limits.MaxNodes, MaxEdges: c.Limits.MaxEdges, MaxDepth: c.Limits.MaxDepth, MaxReads: c.Limits.MaxReads}}
	}
	ctx, err := Collect(t.Context(), r, c.Target, o)
	if err != nil {
		t.Fatalf("%s: collect: %v", name, err)
	}
	return ctx
}

func chainPath(ch Chain) string {
	var parts []string
	for _, s := range ch.Path {
		parts = append(parts, s.Ref.Kind+"/"+s.Ref.Name)
	}
	return strings.Join(parts, " > ")
}

func TestChainFixtures(t *testing.T) {
	t.Parallel()
	update := os.Getenv("KUBRON_UPDATE_CHAIN") != ""
	for name, c := range loadChainCases(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := chainContext(t, name, c)
			rep := Analyze(ctx)
			checkInvariants(t, rep)
			byID := map[string]Hypothesis{}
			var have []string
			for _, h := range rep.Hypotheses {
				byID[h.ID] = h
				have = append(have, h.Code+":"+string(h.Confidence))
			}
			e := c.Expect
			if len(e.Symptoms) > 0 && !sameSet(rep.Symptoms, e.Symptoms) {
				t.Errorf("symptoms = %v, want %v", rep.Symptoms, e.Symptoms)
			}
			for _, w := range e.Hypotheses {
				if !slices.Contains(have, w) {
					t.Errorf("missing hypothesis %s; have %v", w, have)
				}
			}
			for _, code := range e.Absent {
				if slices.ContainsFunc(rep.Hypotheses, func(h Hypothesis) bool { return h.Code == code }) {
					t.Errorf("unexpected hypothesis %s", code)
				}
			}
			for _, code := range e.AbsentSteps {
				if slices.ContainsFunc(rep.Steps, func(s Step) bool { return s.Code == code }) {
					t.Errorf("unexpected step %s", code)
				}
			}
			for _, pair := range e.Explains {
				ok := false
				for _, h := range rep.Hypotheses {
					if h.Code != pair[0] {
						continue
					}
					for _, id := range h.Explains {
						ok = ok || byID[id].Code == pair[1]
					}
				}
				if !ok {
					t.Errorf("%s does not explain %s", pair[0], pair[1])
				}
			}
			for _, w := range e.Chains {
				cand, path, _ := strings.Cut(w, ": ")
				if !slices.ContainsFunc(rep.Chains, func(ch Chain) bool { return byID[ch.Candidate].Code == cand && chainPath(ch) == path }) {
					var got []string
					for _, ch := range rep.Chains {
						got = append(got, byID[ch.Candidate].Code+": "+chainPath(ch))
					}
					t.Errorf("chain %q not found; have %q", w, got)
				}
			}
			for _, code := range e.Missing {
				if !slices.ContainsFunc(rep.Missing, func(m MissingContext) bool { return m.Code == code }) {
					t.Errorf("missing context %q not reported: %+v", code, rep.Missing)
				}
			}
			for _, p := range e.Partial {
				if !slices.Contains(rep.Partial, p) {
					t.Errorf("partial %q not reported: %v", p, rep.Partial)
				}
			}
			// Кандидатом первопричины может стать только гипотеза, которая что-то объясняет и сама не объясняется.
			for _, id := range rep.RootCandidates() {
				if len(byID[id].Explains) == 0 {
					t.Errorf("root candidate %s explains nothing", id)
				}
			}
			// Детерминизм: один и тот же Context дважды даёт побайтово одинаковый отчёт.
			a, _ := json.Marshal(rep)
			b, _ := json.Marshal(Analyze(ctx))
			if !bytes.Equal(a, b) {
				t.Error("report is not deterministic")
			}
			if !c.Golden {
				return
			}
			got, err := json.MarshalIndent(rep, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			path := filepath.Join("testdata", "chain", name, "report.json")
			if update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("no golden report (KUBRON_UPDATE_CHAIN=1 creates it): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("report differs from %s\n--- got\n%s", path, got)
			}
		})
	}
}

func TestExplainablePairs(t *testing.T) {
	t.Parallel()
	pod := ResourceRef{Kind: "Pod", Namespace: "app", Name: "p"}
	pvc := ResourceRef{Kind: "PersistentVolumeClaim", Namespace: "app", Name: "c"}
	node := ResourceRef{Kind: "Node", Name: "n"}
	svc := ResourceRef{Kind: "Service", Namespace: "app", Name: "s"}
	h := func(code string, res ResourceRef) Hypothesis { return Hypothesis{Code: code, Resource: res} }
	for _, tc := range []struct {
		name          string
		cause, effect Hypothesis
		want          bool
	}{
		{"claim problem explains an unbound claim", h("storage.class_not_found", pvc), h("pending.unbound_pvc", pod), true},
		{"missing claim explains a failed mount", h("pvc.missing", pvc), h("startup.volume_mount_failed", pod), true},
		{"claim problem does not explain a crash loop", h("storage.class_not_found", pvc), h("crash.application_error", pod), false},
		{"claim problem does not explain anything on a Service", h("storage.class_not_found", pvc), h("service.no_ready_endpoints", svc), false},
		{"node pressure explains an OOM kill", h("node.pressure_memory", node), h("oom.node_memory_pressure", pod), true},
		{"node pressure does not explain an unbound claim", h("node.pressure_memory", node), h("pending.unbound_pvc", pod), false},
		{"a sick Pod explains empty endpoints", h("crash.application_error", pod), h("service.no_ready_endpoints", svc), true},
		{"a sick Pod does not explain a Pod", h("crash.application_error", pod), h("pending.unbound_pvc", pod), false},
		{"a Service explains nothing", h("service.no_ready_endpoints", svc), h("crash.application_error", pod), false},
	} {
		if got := explainable(tc.cause, tc.effect); got != tc.want {
			t.Errorf("%s: explainable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRootCandidateIsTheCauseNotTheSymptom(t *testing.T) {
	t.Parallel()
	c := loadChainCases(t)["storage_class_missing"]
	rep := Analyze(chainContext(t, "storage_class_missing", c))
	roots := rep.RootCandidates()
	if len(roots) != 1 {
		t.Fatalf("root candidates = %v, want exactly one", roots)
	}
	for _, h := range rep.Hypotheses {
		if h.ID == roots[0] && h.Code != "storage.class_not_found" {
			t.Errorf("root candidate is %s, want storage.class_not_found", h.Code)
		}
	}
}

func TestDependencyPath(t *testing.T) {
	t.Parallel()
	c := loadChainCases(t)["storage_class_missing"]
	ctx := chainContext(t, "storage_class_missing", c)
	g := ctx.Graph
	pod := depgraph.Ref{Kind: "Pod", Namespace: "app", Name: "db-0"}
	pvc := depgraph.Ref{Kind: "PersistentVolumeClaim", Namespace: "app", Name: "data"}
	path := dependencyPath(g, pod, pvc)
	if len(path) != 2 || path[0].Name != "db-0" || path[1].Name != "data" {
		t.Errorf("path = %+v", path)
	}
	// Зависимость не симметрична: от PVC к Pod пути «по зависимостям» нет, а от узла к себе — тем более.
	if p := dependencyPath(g, pvc, pod); p != nil {
		t.Errorf("a claim does not depend on its Pod: %+v", p)
	}
	if p := dependencyPath(g, pod, pod); p != nil {
		t.Errorf("a path from a resource to itself must be empty: %+v", p)
	}
	if p := dependencyPath(g, pod, depgraph.Ref{Kind: "Service", Namespace: "app", Name: "nope"}); p != nil {
		t.Errorf("a path to an unknown node must be empty: %+v", p)
	}
}

// Без графа цепочечные правила молчат, а связи не строятся: отчёт равен прежнему.
func TestChainRulesAreSilentWithoutGraph(t *testing.T) {
	t.Parallel()
	c := loadChainCases(t)["storage_class_missing"]
	ctx := chainContext(t, "storage_class_missing", c)
	ctx.Graph = nil
	rep := Analyze(ctx)
	for _, h := range rep.Hypotheses {
		if isChainRule(h.RuleID) || len(h.Explains) != 0 {
			t.Errorf("hypothesis %s (%s) was produced without a graph", h.ID, h.Code)
		}
	}
	if rep.Graph != nil || len(rep.Chains) != 0 {
		t.Errorf("graph=%v chains=%v", rep.Graph, rep.Chains)
	}
	// Текстовое правило остаётся при своей уверенности: цепочки нет, понижать нечем.
	for _, h := range rep.Hypotheses {
		if h.Code == "pending.unbound_pvc" && h.Confidence != ConfidenceHigh {
			t.Errorf("text path confidence = %s without a chain, want high", h.Confidence)
		}
	}
}

func TestChainsAreCapped(t *testing.T) {
	t.Parallel()
	if maxChains < 1 || maxChains > 10 {
		t.Fatalf("maxChains = %d: chains explain «why», they do not list the graph", maxChains)
	}
	c := loadChainCases(t)["storage_class_missing"]
	if n := len(Analyze(chainContext(t, "storage_class_missing", c)).Chains); n > maxChains {
		t.Errorf("chains = %d, want at most %d", n, maxChains)
	}
}
