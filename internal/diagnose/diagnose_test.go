// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// allowedReads — всё, что вправе читать диагностика (ADR-0024 §2). Secret здесь нет и быть не может.
var allowedReads = []string{"Pod", "Namespace", "Node", "Event", "ReplicaSet", "Deployment", "pods/log"}

func errFor(kind string) error {
	switch kind {
	case "forbidden":
		return fmt.Errorf("rbac: %w", ErrForbidden)
	case "not_found":
		return ErrNotFound
	}
	return ErrUnavailable
}

func TestScenarios(t *testing.T) {
	t.Parallel()
	for _, sc := range loadScenarios(t) {
		t.Run(sc.Name, func(t *testing.T) {
			t.Parallel()
			r := newFakeReader(sc.Objects, sc.Logs)
			for k, v := range sc.Expect.Reader {
				r.errs[k] = errFor(v)
			}
			c, err := Collect(t.Context(), r, sc.Expect.Target, CollectOptions{Logs: true, Now: func() time.Time { return fixedNow }})
			if sc.Expect.Error != "" {
				if err == nil {
					t.Fatalf("expected error %q", sc.Expect.Error)
				}
				if !errors.Is(err, errFor(sc.Expect.Error)) {
					t.Fatalf("error = %v, want %s", err, sc.Expect.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			rep := Analyze(c)
			checkInvariants(t, rep)
			for k := range r.kinds {
				if !slices.Contains(allowedReads, k) {
					t.Errorf("read of %q is outside the allowlist", k)
				}
			}
			e := sc.Expect
			if !sameSet(rep.Symptoms, e.Symptoms) {
				t.Errorf("symptoms = %v, want %v", rep.Symptoms, e.Symptoms)
			}
			got := hypSet(rep)
			for _, want := range e.Hypotheses {
				if !got[want] {
					t.Errorf("missing hypothesis %s; have %v", want, slices.Sorted(maps.Keys(got)))
				}
			}
			for _, code := range e.Absent {
				for h := range got {
					if strings.HasPrefix(h, code+":") {
						t.Errorf("unexpected hypothesis %s", h)
					}
				}
			}
			for _, code := range e.Missing {
				if !slices.ContainsFunc(rep.Missing, func(m MissingContext) bool { return m.Code == code }) {
					t.Errorf("missing context %q not reported; have %+v", code, rep.Missing)
				}
			}
			for _, code := range e.Recs {
				if !slices.ContainsFunc(rep.Recommendations, func(x Recommendation) bool { return x.Code == code }) {
					t.Errorf("recommendation %q not produced", code)
				}
			}
		})
	}
}

func hypSet(rep Report) map[string]bool {
	out := map[string]bool{}
	for _, h := range rep.Hypotheses {
		out[h.Code+":"+string(h.Confidence)] = true
	}
	return out
}

func sameSet(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

// checkInvariants — свойства любого отчёта: типы различимы, ссылки целы, у факта есть источник и время,
// гипотеза не «установлена», в JSON нет значений Secret.
func checkInvariants(t *testing.T, rep Report) {
	t.Helper()
	facts := map[string]bool{}
	for _, f := range rep.Facts {
		if f.Kind != KindFact || f.ID == "" || f.Code == "" || f.Source.Kind == "" || f.ObservedAt.IsZero() {
			t.Errorf("incomplete fact: %+v", f)
		}
		if facts[f.ID] {
			t.Errorf("duplicate fact id %s", f.ID)
		}
		facts[f.ID] = true
	}
	hyps := map[string]bool{}
	for _, h := range rep.Hypotheses {
		hyps[h.ID] = true
		if h.Kind != KindHypothesis || !h.Confidence.Valid() {
			t.Errorf("bad hypothesis: %+v", h)
		}
		if len(h.Supporting) == 0 {
			t.Errorf("hypothesis %s has no supporting facts", h.ID)
		}
		for _, id := range slices.Concat(h.Supporting, h.Contradicting) {
			if !facts[id] {
				t.Errorf("hypothesis %s references unknown fact %s", h.ID, id)
			}
		}
	}
	for _, r := range rep.Recommendations {
		if r.Kind != KindRecommendation {
			t.Errorf("bad recommendation kind: %+v", r)
		}
		for _, id := range r.Hypotheses {
			if !hyps[id] {
				t.Errorf("recommendation %s references unknown hypothesis %s", r.ID, id)
			}
		}
	}
	for _, s := range rep.Steps {
		if s.Kind != KindStep {
			t.Errorf("bad step kind: %+v", s)
		}
		for _, id := range s.Hypotheses {
			if !hyps[id] {
				t.Errorf("step %s references unknown hypothesis %s", s.ID, id)
			}
		}
	}
	if rep.RuleSetVersion != RuleSetVersion {
		t.Errorf("rule set version = %d", rep.RuleSetVersion)
	}
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(raw)), `"established"`) || strings.Contains(string(raw), `"rootCause"`) {
		t.Error("a hypothesis must never be marked as an established cause")
	}
}

func TestHypothesisHasNoEstablishedState(t *testing.T) {
	t.Parallel()
	for _, c := range []Confidence{ConfidenceLow, ConfidenceMedium, ConfidenceHigh} {
		if !c.Valid() {
			t.Errorf("%q must be valid", c)
		}
	}
	for _, c := range []Confidence{"", "certain", "established", "confirmed"} {
		if c.Valid() {
			t.Errorf("%q must not be a valid confidence", c)
		}
	}
}

func TestLogReferencesOnFindings(t *testing.T) {
	t.Parallel()
	var sc scenario
	for _, s := range loadScenarios(t) {
		if s.Name == "crashloop_exit1" {
			sc = s
		}
	}
	r := newFakeReader(sc.Objects, sc.Logs)
	c, err := Collect(t.Context(), r, sc.Expect.Target, CollectOptions{Logs: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Logs) != 1 || !c.Logs[0].Previous || c.Logs[0].Container != "app" {
		t.Fatalf("logs = %+v", c.Logs)
	}
	if got := r.logReqs[0]; !got.Previous || got.TailLines != logTailLines || got.LimitBytes != logLimitBytes {
		t.Errorf("log request = %+v", got)
	}
	rep := Analyze(c)
	var ref *LogRef
	for _, f := range rep.Facts {
		if f.Log != nil {
			ref = f.Log
		}
	}
	if ref == nil || ref.Container != "app" || !ref.Previous {
		t.Fatalf("no fact refers to the previous log: %+v", rep.Facts)
	}
}

func TestLogsNotReadWhenDisabled(t *testing.T) {
	t.Parallel()
	for _, s := range loadScenarios(t) {
		if s.Name != "crashloop_exit1" {
			continue
		}
		r := newFakeReader(s.Objects, s.Logs)
		c, err := Collect(t.Context(), r, s.Expect.Target, CollectOptions{Logs: false})
		if err != nil {
			t.Fatal(err)
		}
		if r.kinds["pods/log"] != 0 || len(c.Logs) != 0 {
			t.Errorf("logs were read although disabled: %v", r.kinds)
		}
		// Ссылка на лог в факте остаётся: сервис пометит «не сохранено по политике».
		rep := Analyze(c)
		if !slices.ContainsFunc(rep.Facts, func(f Fact) bool { return f.Log != nil }) {
			t.Error("the log reference must stay in the finding")
		}
	}
}

func TestLogUnavailableIsMissingContext(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		err    error
		reason string
	}{
		{"forbidden", ErrForbidden, "forbidden"},
		{"unavailable", ErrUnavailable, "unavailable"},
		{"not found", ErrNotFound, "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, s := range loadScenarios(t) {
				if s.Name != "crashloop_exit1" {
					continue
				}
				r := newFakeReader(s.Objects, nil)
				r.logErr = tc.err
				c, err := Collect(t.Context(), r, s.Expect.Target, CollectOptions{Logs: true})
				if err != nil {
					t.Fatal(err)
				}
				i := slices.IndexFunc(c.Missing, func(m MissingContext) bool { return m.Code == "logs.unavailable" })
				if i < 0 || c.Missing[i].Params["reason"] != tc.reason {
					t.Errorf("missing = %+v", c.Missing)
				}
			}
		})
	}
}

func TestLogTooLargeRetriesWithSmallerTail(t *testing.T) {
	t.Parallel()
	for _, s := range loadScenarios(t) {
		if s.Name != "crashloop_exit1" {
			continue
		}
		r := &tooLargeOnce{fakeReader: newFakeReader(s.Objects, s.Logs)}
		c, err := Collect(t.Context(), r, s.Expect.Target, CollectOptions{Logs: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Logs) != 1 || len(r.tails) != 2 || r.tails[1] != logTailRetry {
			t.Errorf("logs=%d tails=%v", len(c.Logs), r.tails)
		}
	}
}

type tooLargeOnce struct {
	*fakeReader
	tails []int64
}

func (r *tooLargeOnce) PodLogs(ctx context.Context, req LogRequest) ([]byte, error) {
	r.tails = append(r.tails, req.TailLines)
	if len(r.tails) == 1 {
		return nil, ErrTooLarge
	}
	return r.fakeReader.PodLogs(ctx, req)
}

func TestCollectErrors(t *testing.T) {
	t.Parallel()
	pod := ResourceRef{Kind: "Pod", Namespace: "shop", Name: "web-1"}
	for _, tc := range []struct {
		name   string
		target ResourceRef
		want   error
	}{
		{"secret kind is not supported", ResourceRef{Kind: "Secret", Namespace: "shop", Name: "x"}, ErrUnsupportedTarget},
		{"statefulset is not in the MVP", ResourceRef{Kind: "StatefulSet", Namespace: "shop", Name: "x"}, ErrUnsupportedTarget},
		{"empty name", ResourceRef{Kind: "Pod", Namespace: "shop"}, ErrNotFound},
		{"empty namespace", ResourceRef{Kind: "Pod", Name: "x"}, ErrNotFound},
		{"missing pod", pod, ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newFakeReader(nil, nil)
			if _, err := Collect(t.Context(), r, tc.target, CollectOptions{}); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if len(r.kinds) != 0 && tc.want == ErrUnsupportedTarget {
				t.Errorf("unsupported target must not touch the cluster: %v", r.kinds)
			}
		})
	}
}

func TestCollectHonoursCancellation(t *testing.T) {
	t.Parallel()
	for _, s := range loadScenarios(t) {
		if s.Name != "healthy" {
			continue
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := Collect(ctx, newFakeReader(s.Objects, nil), s.Expect.Target, CollectOptions{})
		if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	}
}

func TestUnreadablePrimaryObject(t *testing.T) {
	t.Parallel()
	obj := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "web-1", "namespace": "shop"},
		"status":   map[string]any{"containerStatuses": "not-a-list"},
	}}
	_, err := Collect(t.Context(), newFakeReader([]unstructured.Unstructured{obj}, nil),
		ResourceRef{Kind: "Pod", Namespace: "shop", Name: "web-1"}, CollectOptions{})
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestEventsTruncatedIsPartial(t *testing.T) {
	t.Parallel()
	for _, s := range loadScenarios(t) {
		if s.Name != "healthy" {
			continue
		}
		r := &truncatedEvents{fakeReader: newFakeReader(s.Objects, nil)}
		c, err := Collect(t.Context(), r, s.Expect.Target, CollectOptions{})
		if err != nil {
			t.Fatal(err)
		}
		rep := Analyze(c)
		if !slices.Contains(rep.Partial, "Event") ||
			!slices.ContainsFunc(rep.Missing, func(m MissingContext) bool { return m.Code == "events.truncated" }) {
			t.Errorf("truncation must be reported: partial=%v missing=%v", rep.Partial, rep.Missing)
		}
	}
}

type truncatedEvents struct{ *fakeReader }

func (r *truncatedEvents) List(ctx context.Context, gvk schema.GroupVersionKind, o ListOptions) (*ListResult, error) {
	res, err := r.fakeReader.List(ctx, gvk, o)
	if err == nil && gvk.Kind == "Event" {
		res.Truncated = true
	}
	return res, err
}

func TestAnalyzeDefensive(t *testing.T) {
	t.Parallel()
	if rep := Analyze(nil); len(rep.Facts) != 0 {
		t.Errorf("nil context: %+v", rep)
	}
	// Пустой контекст без подов и объектов: не паника, а «контекста недостаточно».
	rep := Analyze(&Context{Target: ResourceRef{Kind: "Deployment", Namespace: "a", Name: "b"}, ObservedAt: fixedNow})
	checkInvariants(t, rep)
	if !slices.Contains(rep.Symptoms, SymptomNone) {
		t.Errorf("symptoms = %v", rep.Symptoms)
	}
}

func TestRuleFailureIsContained(t *testing.T) {
	// Не параллельный: подменяет глобальный набор правил.
	old := rules
	t.Cleanup(func() { rules = old })
	rules = append([]rule{{"boom", func(*builder) { panic("boom") }}}, old...)
	rep := Analyze(&Context{Target: ResourceRef{Kind: "Pod", Namespace: "a", Name: "b"}, ObservedAt: fixedNow})
	if !slices.ContainsFunc(rep.Missing, func(m MissingContext) bool { return m.Code == "rule.failed" && m.Params["rule"] == "boom" }) {
		t.Errorf("missing = %+v", rep.Missing)
	}
}

func TestAllowedKindsExcludeSecret(t *testing.T) {
	t.Parallel()
	for _, gk := range AllowedKinds() {
		if strings.EqualFold(gk.Kind, "secret") {
			t.Errorf("Secret must not be in the allowlist: %v", gk)
		}
	}
	if len(AllowedKinds()) != 6 {
		t.Errorf("ADR-0024 §2 allowlist changed: %v", AllowedKinds())
	}
}

func TestRuleIDsAreUnique(t *testing.T) {
	t.Parallel()
	ids := RuleIDs()
	if len(slices.Compact(slices.Sorted(slices.Values(ids)))) != len(ids) {
		t.Errorf("duplicate rule ids: %v", ids)
	}
}
