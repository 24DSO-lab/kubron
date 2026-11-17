// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// (а) Отчёт набора правил версии 1 читается без потерь: новые поля пусты, версия сохраняется. Документ версии 1
// получается из отчётов сценариев: новые поля убираются, ruleSetVersion = 1 (как у сохранённого инцидента).
func TestReadsLegacyReport(t *testing.T) {
	t.Parallel()
	n := 0
	for _, sc := range loadScenarios(t) {
		raw, ok := legacyReport(t, sc)
		if !ok {
			continue
		}
		n++
		v1 := normalizeJSON(t, raw)
		var rep Report
		if err := json.Unmarshal(v1, &rep); err != nil {
			t.Fatalf("%s: %v", sc.Name, err)
		}
		if rep.RuleSetVersion != 1 {
			t.Errorf("%s: ruleSetVersion = %d, want 1", sc.Name, rep.RuleSetVersion)
		}
		if rep.Graph != nil || len(rep.Chains) != 0 {
			t.Errorf("%s: a v1 report has no graph or chains", sc.Name)
		}
		for _, h := range rep.Hypotheses {
			if h.RuleID != "" || h.RuleEpoch != 0 || len(h.Explains) != 0 {
				t.Errorf("%s: hypothesis %s has v2 fields", sc.Name, h.ID)
			}
		}
		if got := rep.RootCandidates(); len(got) != 0 {
			t.Errorf("%s: root candidates of a v1 report = %v", sc.Name, got)
		}
		// Повторная сериализация возвращает тот же документ: старый отчёт не обрастает новыми ключами.
		out, err := json.Marshal(rep)
		if err != nil {
			t.Fatal(err)
		}
		var a, b any
		if err := json.Unmarshal(v1, &a); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(out, &b); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: a v1 report changes after a round trip\n--- before\n%s\n--- after\n%s", sc.Name, v1, out)
		}
	}
	if n == 0 {
		t.Fatal("no reports")
	}
}

// (б) Каждый выданный RuleID есть в таблице правил, и у него эпоха >= 1; все записи правил помечены.
func TestIssuedRuleIDsAreInTable(t *testing.T) {
	t.Parallel()
	epochs := map[string]int{}
	for _, r := range rules {
		if r.epoch < 1 {
			t.Errorf("rule %s has epoch %d", r.id, r.epoch)
		}
		epochs[r.id] = r.epoch
	}
	seen := 0
	for _, sc := range loadScenarios(t) {
		raw, ok := legacyReport(t, sc)
		if !ok {
			continue
		}
		var rep Report
		if err := json.Unmarshal(raw, &rep); err != nil {
			t.Fatal(err)
		}
		if rep.RuleSetVersion != RuleSetVersion {
			t.Errorf("%s: ruleSetVersion = %d", sc.Name, rep.RuleSetVersion)
		}
		check := func(kind, id, ruleID string, epoch int) {
			seen++
			want, known := epochs[ruleID]
			if ruleID == "" || !known {
				t.Errorf("%s: %s %s has unknown rule id %q", sc.Name, kind, id, ruleID)
			} else if epoch != want || epoch < 1 {
				t.Errorf("%s: %s %s epoch = %d, table says %d", sc.Name, kind, id, epoch, want)
			}
		}
		for _, h := range rep.Hypotheses {
			check("hypothesis", h.ID, h.RuleID, h.RuleEpoch)
		}
		for _, r := range rep.Recommendations {
			check("recommendation", r.ID, r.RuleID, r.RuleEpoch)
		}
	}
	if seen == 0 {
		t.Fatal("no hypotheses or recommendations were checked")
	}
}

func TestRuleTableValidation(t *testing.T) {
	old := slices.Clone(rules)
	t.Cleanup(func() { rules = old })
	nop := func(*builder) {}
	for name, call := range map[string]func(){
		"epoch 0":   func() { registerRule("x.zero", 0, nop) },
		"empty id":  func() { registerRule("", 1, nop) },
		"nil apply": func() { registerRule("x.nil", 1, nil) },
		"duplicate": func() { registerRule("pod.pending", 1, nop) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: expected a panic", name)
				}
			}()
			call()
		}()
	}
	registerRule("x.new", 3, func(b *builder) {
		b.hypothesis(b.c.Target, "x.code", ConfidenceLow, nil, []string{b.fact(b.c.Target, "x.fact", Source{Kind: SourceDerived, Resource: b.c.Target}, nil)}, nil)
		b.recommend(b.c.Target, "rec.x", nil)
	})
	rep := Analyze(&Context{Target: ResourceRef{Kind: "Pod", Namespace: "a", Name: "b"}, ObservedAt: fixedNow})
	i := slices.IndexFunc(rep.Hypotheses, func(h Hypothesis) bool { return h.Code == "x.code" })
	if i < 0 || rep.Hypotheses[i].RuleID != "x.new" || rep.Hypotheses[i].RuleEpoch != 3 {
		t.Fatalf("hypotheses = %+v", rep.Hypotheses)
	}
	if len(rep.Recommendations) != 1 || rep.Recommendations[0].RuleID != "x.new" || rep.Recommendations[0].RuleEpoch != 3 {
		t.Errorf("recommendations = %+v", rep.Recommendations)
	}
}

func TestCollectHooksRunInOrderAndAreContained(t *testing.T) {
	old := slices.Clone(collectHooks)
	t.Cleanup(func() { collectHooks = old })
	var order []string
	RegisterCollectHook(300, "c", func(context.Context, Reader, *Context) { order = append(order, "c") })
	RegisterCollectHook(100, "a", func(context.Context, Reader, *Context) { order = append(order, "a") })
	RegisterCollectHook(200, "boom", func(context.Context, Reader, *Context) { panic("boom") })
	RegisterCollectHook(100, "a2", func(_ context.Context, _ Reader, c *Context) {
		order = append(order, "a2")
		c.addPartial("hook")
	})
	sc := scenarioByName(t, "healthy")
	c, err := CollectWith(t.Context(), newFakeReader(sc.Objects, nil), sc.Expect.Target, CollectOptions{Now: func() time.Time { return fixedNow }})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "a,a2,c" {
		t.Errorf("hook order = %s, want a,a2,c (order, then registration)", got)
	}
	if !slices.Contains(c.Partial, "hook") {
		t.Errorf("partial = %v", c.Partial)
	}
	if !slices.ContainsFunc(c.Missing, func(m MissingContext) bool {
		return m.Code == "rule.failed" && m.Params["rule"] == "collect_hook:boom"
	}) {
		t.Errorf("a panicking hook must become missing context: %+v", c.Missing)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("duplicate hook name must panic")
			}
		}()
		RegisterCollectHook(1, "a", func(context.Context, Reader, *Context) {})
	}()
}

func TestCollectHookHonoursCancellation(t *testing.T) {
	old := slices.Clone(collectHooks)
	t.Cleanup(func() { collectHooks = old })
	ctx, cancel := context.WithCancel(t.Context())
	called := false
	RegisterCollectHook(1, "first", func(context.Context, Reader, *Context) { cancel() })
	RegisterCollectHook(2, "second", func(context.Context, Reader, *Context) { called = true })
	sc := scenarioByName(t, "healthy")
	_, err := CollectWith(ctx, newFakeReader(sc.Objects, nil), sc.Expect.Target, CollectOptions{})
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.Canceled) || called {
		t.Errorf("err = %v, second hook called = %v", err, called)
	}
}

func TestPostHooksRunInOrderAfterRulesAndAreContained(t *testing.T) {
	old := slices.Clone(postHooks)
	t.Cleanup(func() { postHooks = old })
	var order []string
	var sawHypotheses int
	registerPostHook(200, "second", func(b *builder) { order = append(order, "second") })
	registerPostHook(100, "first", func(b *builder) {
		order = append(order, "first")
		sawHypotheses = len(b.rep.Hypotheses)
		// Вне правила RuleID не проставляется.
		id := b.hypothesis(b.c.Target, "hook.h", ConfidenceLow, nil, []string{b.fact(b.c.Target, "hook.f", Source{Kind: SourceDerived, Resource: b.c.Target}, nil)}, nil)
		if h := b.rep.Hypotheses[len(b.rep.Hypotheses)-1]; h.ID != id || h.RuleID != "" || h.RuleEpoch != 0 {
			t.Errorf("hook hypothesis = %+v", h)
		}
	})
	registerPostHook(300, "boom", func(*builder) { panic("boom") })
	sc := scenarioByName(t, "oom_limit")
	c, err := Collect(t.Context(), newFakeReader(sc.Objects, sc.Logs), sc.Expect.Target, CollectOptions{Logs: true, Now: func() time.Time { return fixedNow }})
	if err != nil {
		t.Fatal(err)
	}
	rep := Analyze(c)
	if got := strings.Join(order, ","); got != "first,second" {
		t.Errorf("post hook order = %s", got)
	}
	if sawHypotheses == 0 {
		t.Error("post hooks must run after the rules")
	}
	if !slices.ContainsFunc(rep.Missing, func(m MissingContext) bool {
		return m.Code == "rule.failed" && m.Params["rule"] == "post_hook:boom"
	}) {
		t.Errorf("a panicking post hook must become missing context: %+v", rep.Missing)
	}
}

func TestTargetRegistry(t *testing.T) {
	oldTargets := map[string]TargetCollector{}
	for k, v := range targets {
		oldTargets[k] = v
	}
	t.Cleanup(func() { targets = oldTargets })

	if got, want := targetKinds(), []string{"Deployment", "Pod", "ReplicaSet"}; !slices.Equal(got, want) {
		t.Fatalf("built-in targets = %v, want %v", got, want)
	}
	node := ResourceRef{Kind: "Node", Name: "n1"}
	if _, err := CollectWith(t.Context(), newFakeReader(nil, nil), node, CollectOptions{}); !errors.Is(err, ErrUnsupportedTarget) {
		t.Fatalf("unregistered kind: err = %v", err)
	}
	var got ResourceRef
	RegisterTarget("Node", func(_ context.Context, _ Reader, target ResourceRef, o CollectOptions) (*Context, error) {
		got = target
		return &Context{Target: target, ObservedAt: o.now()}, nil
	})
	c, err := CollectWith(t.Context(), newFakeReader(nil, nil), node, CollectOptions{Now: func() time.Time { return fixedNow }})
	if err != nil || got != node || c.ObservedAt != fixedNow {
		t.Fatalf("custom target: err=%v got=%+v ctx=%+v", err, got, c)
	}
	for name, call := range map[string]func(){
		"duplicate": func() {
			RegisterTarget("Node", func(context.Context, Reader, ResourceRef, CollectOptions) (*Context, error) { return nil, nil })
		},
		"empty": func() {
			RegisterTarget("", func(context.Context, Reader, ResourceRef, CollectOptions) (*Context, error) { return nil, nil })
		},
		"nil": func() { RegisterTarget("X", nil) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: expected a panic", name)
				}
			}()
			call()
		}()
	}
}

// Collect и CollectWith — одно и то же: сигнатуры и результаты совпадают для всех сценариев.
func TestCollectDelegatesToCollectWith(t *testing.T) {
	t.Parallel()
	for _, sc := range loadScenarios(t) {
		if sc.Expect.Error != "" {
			continue
		}
		o := CollectOptions{Logs: true, Now: func() time.Time { return fixedNow }}
		mk := func() Reader {
			r := newFakeReader(sc.Objects, sc.Logs)
			for k, v := range sc.Expect.Reader {
				r.errs[k] = errFor(v)
			}
			return r
		}
		a, errA := Collect(t.Context(), mk(), sc.Expect.Target, o)
		b, errB := CollectWith(t.Context(), mk(), sc.Expect.Target, o)
		if (errA == nil) != (errB == nil) {
			t.Fatalf("%s: errors differ: %v / %v", sc.Name, errA, errB)
		}
		ja, _ := json.Marshal(Analyze(a))
		jb, _ := json.Marshal(Analyze(b))
		if string(ja) != string(jb) {
			t.Errorf("%s: Collect and CollectWith differ", sc.Name)
		}
		if a.Graph != nil || a.Signals != nil {
			t.Errorf("%s: without hooks the context has no graph or signals", sc.Name)
		}
	}
}

// Детерминизм: один и тот же Context дважды даёт побайтово одинаковый JSON.
func TestAnalyzeIsDeterministic(t *testing.T) {
	t.Parallel()
	for _, sc := range loadScenarios(t) {
		raw1, ok := legacyReport(t, sc)
		if !ok {
			continue
		}
		raw2, _ := legacyReport(t, sc)
		if string(raw1) != string(raw2) {
			t.Errorf("%s: report is not deterministic", sc.Name)
		}
	}
}

func TestResourceRefGroupIsOptional(t *testing.T) {
	t.Parallel()
	raw, _ := json.Marshal(ResourceRef{Kind: "Pod", Name: "x"})
	if strings.Contains(string(raw), "group") {
		t.Errorf("empty group must be omitted: %s", raw)
	}
	raw, _ = json.Marshal(ResourceRef{Group: "storage.k8s.io", Kind: "StorageClass", Name: "fast"})
	if !strings.Contains(string(raw), `"group":"storage.k8s.io"`) {
		t.Errorf("group is lost: %s", raw)
	}
}

func TestReaderTypesAreDepgraphAliases(t *testing.T) {
	t.Parallel()
	var _ depgraph.ListOptions = ListOptions{}
	var _ *depgraph.ListResult = (*ListResult)(nil)
	for _, p := range [][2]error{
		{ErrNotFound, depgraph.ErrNotFound}, {ErrForbidden, depgraph.ErrForbidden},
		{ErrUnavailable, depgraph.ErrUnavailable}, {ErrTooLarge, depgraph.ErrTooLarge},
	} {
		if p[0] != p[1] {
			t.Errorf("%v is not the depgraph error value", p[0])
		}
	}
}

func hypExplains(id string, explains ...string) Hypothesis {
	return Hypothesis{ID: id, Explains: explains}
}

func TestRootCandidates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		hyps []Hypothesis
		want []string
	}{
		{"none", nil, []string{}},
		{"unrelated hypotheses are not candidates", []Hypothesis{hypExplains("h1"), hypExplains("h2")}, []string{}},
		{"chain of three", []Hypothesis{hypExplains("h3"), hypExplains("h2", "h3"), hypExplains("h1", "h2")}, []string{"h1"}},
		{"two independent chains", []Hypothesis{hypExplains("h1", "h2"), hypExplains("h2"), hypExplains("h3", "h4"), hypExplains("h4")}, []string{"h1", "h3"}},
		{"one cause explains two", []Hypothesis{hypExplains("h1", "h2", "h3"), hypExplains("h2"), hypExplains("h3")}, []string{"h1"}},
		{"cycle has no candidate and does not hang", []Hypothesis{hypExplains("h1", "h2"), hypExplains("h2", "h1")}, []string{}},
		{"self reference is ignored", []Hypothesis{hypExplains("h1", "h1")}, []string{}},
		{"cycle with a head", []Hypothesis{hypExplains("h0", "h1"), hypExplains("h1", "h2"), hypExplains("h2", "h1")}, []string{"h0"}},
		{"unknown target still makes a candidate", []Hypothesis{hypExplains("h1", "h9")}, []string{"h1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rep := Report{Hypotheses: tc.hyps}
			if got := rep.RootCandidates(); !slices.Equal(got, tc.want) {
				t.Errorf("RootCandidates = %v, want %v", got, tc.want)
			}
		})
	}
	var nilRep *Report
	if got := nilRep.RootCandidates(); got == nil || len(got) != 0 {
		t.Errorf("nil report: %v", got)
	}
}

func scenarioByName(t *testing.T, name string) scenario {
	t.Helper()
	for _, s := range loadScenarios(t) {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no scenario %q", name)
	return scenario{}
}
