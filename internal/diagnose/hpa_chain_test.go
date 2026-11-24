// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

const hpaTestNamespace = `
apiVersion: v1
kind: Namespace
metadata: {name: app}
`

const hpaTestDeployment = `
apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: app, uid: d1, generation: 1}
spec: {replicas: 2, selector: {matchLabels: {app: web}}}
status: {observedGeneration: 1, updatedReplicas: 2, availableReplicas: 2, readyReplicas: 2, replicas: 2}
`

// hpaTestHPA — HPA app/web: цель — Deployment web; conditions — готовые строки «    - {…}».
func hpaTestHPA(current, max int, conditions ...string) string {
	return fmt.Sprintf(`
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata: {name: web, namespace: app, uid: hpa-1}
spec:
  scaleTargetRef: {apiVersion: apps/v1, kind: Deployment, name: web}
  minReplicas: 1
  maxReplicas: %d
status:
  currentReplicas: %d
  conditions:
%s`, max, current, strings.Join(conditions, "\n"))
}

func hpaTestCond(typ, status, reason, message string) string {
	return fmt.Sprintf("    - {type: %s, status: %q, reason: %s, message: %q}", typ, status, reason, message)
}

var hpaTestTarget = ResourceRef{Kind: "HorizontalPodAutoscaler", Namespace: "app", Name: "web"}

func hpaTestReport(t *testing.T, manifests ...string) Report {
	t.Helper()
	ctx, err := Collect(t.Context(), newChain(t, manifests...), hpaTestTarget, chainOpts())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	rep := Analyze(ctx)
	checkInvariants(t, rep)
	return rep
}

func hpaCodes(rep Report) []string {
	var out []string
	for _, h := range rep.Hypotheses {
		out = append(out, h.Code+":"+string(h.Confidence))
	}
	return out
}

// Таблица «условия → гипотеза»: суть сбоя определяют по Reason условия, текст сообщения — только запасной путь.
func TestHPAConditionsToHypotheses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		current, max int
		conds        []string
		want         []string // «код:уверенность»; пусто — ни одной гипотезы HPA
	}{
		{"healthy", 2, 5, []string{
			hpaTestCond("AbleToScale", "True", "ReadyForNewScale", "ok"),
			hpaTestCond("ScalingActive", "True", "ValidMetricFound", "ok"),
			hpaTestCond("ScalingLimited", "False", "DesiredWithinRange", "ok"),
		}, nil},
		{"no conditions yet", 1, 5, nil, nil},
		{"resource metric failed", 2, 5, []string{hpaTestCond("ScalingActive", "False", "FailedGetResourceMetric", "x")},
			[]string{"hpa.metrics_unavailable:high"}},
		{"container resource metric failed", 2, 5, []string{hpaTestCond("ScalingActive", "False", "FailedGetContainerResourceMetric", "x")},
			[]string{"hpa.metrics_unavailable:high"}},
		{"pods metric failed", 2, 5, []string{hpaTestCond("ScalingActive", "False", "FailedGetPodsMetric", "x")},
			[]string{"hpa.metrics_unavailable:high"}},
		{"object metric failed", 2, 5, []string{hpaTestCond("ScalingActive", "False", "FailedGetObjectMetric", "x")},
			[]string{"hpa.metrics_unavailable:high"}},
		{"external metric failed", 2, 5, []string{hpaTestCond("ScalingActive", "False", "FailedGetExternalMetric", "x")},
			[]string{"hpa.metrics_unavailable:high"}},
		{"invalid metric source type", 2, 5, []string{hpaTestCond("ScalingActive", "False", "InvalidMetricSourceType", "x")},
			[]string{"hpa.metrics_unavailable:high"}},
		{"unknown reason, message about metrics: fallback", 2, 5, []string{hpaTestCond("ScalingActive", "False", "SomethingNew", "no metrics returned")},
			[]string{"hpa.metrics_unavailable:low"}},
		{"unknown reason, unrelated message", 2, 5, []string{hpaTestCond("ScalingActive", "False", "SomethingNew", "something else")}, nil},
		{"scaling disabled is not a failure", 0, 5, []string{hpaTestCond("ScalingActive", "False", "ScalingDisabled", "metrics are not needed")}, nil},
		{"active condition with a failure-like reason", 2, 5, []string{hpaTestCond("ScalingActive", "True", "FailedGetResourceMetric", "x")}, nil},
		{"able to scale false alone", 2, 5, []string{hpaTestCond("AbleToScale", "False", "FailedUpdateScale", "metrics")}, nil},
		{"max replicas reached", 5, 5, []string{
			hpaTestCond("ScalingActive", "True", "ValidMetricFound", "ok"),
			hpaTestCond("ScalingLimited", "True", "TooManyReplicas", "the desired replica count is more than the maximum replica count"),
		}, []string{"hpa.max_replicas_reached:medium"}},
		{"limited but below the maximum", 3, 5, []string{hpaTestCond("ScalingLimited", "True", "TooManyReplicas", "x")}, nil},
		{"at the maximum but not limited", 5, 5, []string{hpaTestCond("ScalingLimited", "False", "DesiredWithinRange", "x")}, nil},
		{"limited from below", 1, 5, []string{hpaTestCond("ScalingLimited", "True", "TooFewReplicas", "x")}, nil},
		{"metrics unavailable and at the maximum", 5, 5, []string{
			hpaTestCond("ScalingActive", "False", "FailedGetResourceMetric", "x"),
			hpaTestCond("ScalingLimited", "True", "TooManyReplicas", "x"),
		}, []string{"hpa.metrics_unavailable:high", "hpa.max_replicas_reached:medium"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rep := hpaTestReport(t, hpaTestNamespace, hpaTestDeployment, hpaTestHPA(tc.current, tc.max, tc.conds...))
			var got []string
			for _, h := range rep.Hypotheses {
				if h.RuleID == ruleHPAChainID {
					got = append(got, h.Code+":"+string(h.Confidence))
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("hypotheses = %v, want %v", got, tc.want)
			}
		})
	}
}

// Условие описано кодами, а не текстом: сообщение условия в отчёт не попадает ни в каком виде.
func TestHPAConditionMessageNeverReachesTheReport(t *testing.T) {
	t.Parallel()
	const leak = "CONDITION-MESSAGE-MUST-NOT-LEAK"
	for _, reason := range []string{"FailedGetResourceMetric", "SomethingNew"} {
		rep := hpaTestReport(t, hpaTestNamespace, hpaTestDeployment,
			hpaTestHPA(5, 5,
				hpaTestCond("ScalingActive", "False", reason, "metrics "+leak),
				hpaTestCond("ScalingLimited", "True", "TooManyReplicas", leak)))
		raw, err := json.Marshal(rep)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), leak) {
			t.Errorf("reason %s: the report contains a condition message", reason)
		}
	}
}

// Метрики недоступны: параметры гипотезы — источник из таблицы кодов, а не сырой Reason Kubernetes.
func TestHPAMetricSourceIsNormalized(t *testing.T) {
	t.Parallel()
	for reason, want := range hpaMetricReasons {
		rep := hpaTestReport(t, hpaTestNamespace, hpaTestDeployment,
			hpaTestHPA(2, 5, hpaTestCond("ScalingActive", "False", reason, "x")))
		var got string
		for _, f := range rep.Facts {
			if f.Code == "hpa.metrics_condition" {
				got = f.Params["metric"]
			}
		}
		if got != want {
			t.Errorf("reason %s: metric = %q, want %q", reason, got, want)
		}
		for _, f := range rep.Facts {
			for _, v := range f.Params {
				if v == reason {
					t.Errorf("reason %s: the raw Kubernetes reason is in the params of %s", reason, f.Code)
				}
			}
		}
	}
}

// Событие HPA о недоступных метриках подтверждает условие; событие чужого HPA — нет.
func TestHPAEventsConfirmOnlyTheirOwnAutoscaler(t *testing.T) {
	t.Parallel()
	event := func(hpa string) string {
		return fmt.Sprintf(`
apiVersion: v1
kind: Event
metadata: {name: e-%s, namespace: app}
involvedObject: {kind: HorizontalPodAutoscaler, name: %s, namespace: app}
type: Warning
reason: FailedGetResourceMetric
message: "no metrics"
count: 2
firstTimestamp: "2026-11-11T09:50:00Z"
lastTimestamp: "2026-11-11T09:58:00Z"
`, hpa, hpa)
	}
	base := []string{hpaTestNamespace, hpaTestDeployment,
		hpaTestHPA(2, 5, hpaTestCond("ScalingActive", "False", "FailedGetResourceMetric", "x"))}
	eventFacts := func(rep Report) int {
		n := 0
		for _, f := range rep.Facts {
			if f.Code == "event" {
				n++
			}
		}
		return n
	}
	if n := eventFacts(hpaTestReport(t, append(slices.Clone(base), event("web"))...)); n != 1 {
		t.Errorf("own event facts = %d, want 1", n)
	}
	if n := eventFacts(hpaTestReport(t, append(slices.Clone(base), event("other"))...)); n != 0 {
		t.Errorf("a foreign HPA event became evidence: %d facts", n)
	}
}

// Нездоровая цель — указатель: никаких копий гипотез цели и Pod'ов в отчёте HPA, но есть цепочка HPA → цель.
func TestHPAUnhealthyTargetIsAPointerNotACopy(t *testing.T) {
	t.Parallel()
	c := loadChainCases(t)["hpa_target_unhealthy"]
	rep := Analyze(chainContext(t, "hpa_target_unhealthy", c))
	for _, h := range rep.Hypotheses {
		if h.RuleID != ruleHPAChainID {
			t.Errorf("hypothesis %s (%s) of rule %q was copied into the HPA report", h.ID, h.Code, h.RuleID)
		}
		if h.Resource.Kind == "Pod" {
			t.Errorf("hypothesis %s is about a Pod", h.ID)
		}
	}
	if !slices.ContainsFunc(rep.Steps, func(s Step) bool { return s.Code == "hpa.investigate_target" }) {
		t.Errorf("no step that sends a human to the target: %+v", rep.Steps)
	}
	if len(rep.Chains) != 1 || chainPath(rep.Chains[0]) != "HorizontalPodAutoscaler/web > Deployment/web" {
		t.Errorf("chains = %+v", rep.Chains)
	}
}

// Сбор не читает Pod'ы и целевой workload: они приходят из графа, а правила Pod'ов к HPA не применяются.
func TestHPACollectKeepsPodsOutOfContext(t *testing.T) {
	t.Parallel()
	c := loadChainCases(t)["hpa_target_unhealthy"]
	ctx := chainContext(t, "hpa_target_unhealthy", c)
	if len(ctx.Pods) != 0 || ctx.Deployment != nil || len(ctx.ReplicaSets) != 0 {
		t.Errorf("pods=%d deployment=%v replicasets=%d, want none", len(ctx.Pods), ctx.Deployment != nil, len(ctx.ReplicaSets))
	}
	if ctx.Graph == nil {
		t.Fatal("no graph")
	}
	if _, ok := ctx.Graph.Node(depgraph.Ref{Group: "apps", Kind: "Deployment", Namespace: "app", Name: "web"}); !ok {
		t.Error("the target is not in the graph")
	}
}

// Ссылка цели несёт группу API: по ней цепочки находят вершину графа.
func TestHPATargetCarriesTheAPIGroup(t *testing.T) {
	t.Parallel()
	ctx, err := Collect(t.Context(), newChain(t, hpaTestNamespace, hpaTestDeployment, hpaTestHPA(2, 5)), hpaTestTarget, chainOpts())
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Target.Group != "autoscaling" || ctx.Target.UID != "hpa-1" {
		t.Errorf("target = %+v", ctx.Target)
	}
}

// Цель без namespace и несуществующий HPA — ошибки сбора, а не пустой отчёт.
func TestHPACollectErrors(t *testing.T) {
	t.Parallel()
	_, err := Collect(t.Context(), newChain(t, hpaTestNamespace), ResourceRef{Kind: "HorizontalPodAutoscaler", Name: "web"}, chainOpts())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("no namespace: err = %v, want ErrNotFound", err)
	}
	_, err = Collect(t.Context(), newChain(t, hpaTestNamespace), hpaTestTarget, chainOpts())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("missing HPA: err = %v, want ErrNotFound", err)
	}
}

// Запрет RBAC на сам HPA — ошибка чтения цели (ErrForbidden), а не «объекта нет».
func TestHPACollectForbidden(t *testing.T) {
	t.Parallel()
	r := newChain(t, hpaTestNamespace, hpaTestDeployment, hpaTestHPA(2, 5))
	r.Forbid(gvkHPA)
	if _, err := Collect(t.Context(), r, hpaTestTarget, chainOpts()); !errors.Is(err, ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

// Без графа правило молчит, пост-хук цепочек тоже: отчёт не падает и не выдаёт выводов.
func TestHPARuleIsSilentWithoutGraph(t *testing.T) {
	t.Parallel()
	ctx, err := Collect(t.Context(), newChain(t, hpaTestNamespace, hpaTestDeployment,
		hpaTestHPA(5, 5, hpaTestCond("ScalingActive", "False", "FailedGetResourceMetric", "x"))), hpaTestTarget, chainOpts())
	if err != nil {
		t.Fatal(err)
	}
	ctx.Graph = nil
	rep := Analyze(ctx)
	for _, h := range rep.Hypotheses {
		t.Errorf("hypothesis %s without a graph", h.Code)
	}
	if len(rep.Chains) != 0 || rep.Graph != nil {
		t.Errorf("chains=%v graph=%v", rep.Chains, rep.Graph)
	}
}

// Правило подключено через реестры: идентификатор, эпоха 1, цепочечное, цель зарегистрирована.
func TestHPARegistration(t *testing.T) {
	t.Parallel()
	if !slices.Contains(RuleIDs(), ruleHPAChainID) || !isChainRule(ruleHPAChainID) {
		t.Errorf("rule %s is not registered as a chain rule", ruleHPAChainID)
	}
	for _, r := range rules {
		if r.id == ruleHPAChainID && r.epoch != 1 {
			t.Errorf("epoch = %d, want 1", r.epoch)
		}
	}
	if !slices.Contains(targetKinds(), "HorizontalPodAutoscaler") {
		t.Errorf("the HorizontalPodAutoscaler target is not registered: %v", targetKinds())
	}
}

// Пост-хук цепочек соблюдает общий предел maxChains.
func TestHPAChainsRespectTheCap(t *testing.T) {
	t.Parallel()
	c := loadChainCases(t)["hpa_target_missing"]
	ctx := chainContext(t, "hpa_target_missing", c)
	b := newBuilder(ctx)
	for _, r := range rules {
		runRule(b, r)
	}
	b.rep.Chains = make([]Chain, maxChains)
	hpaChains(b)
	if len(b.rep.Chains) != maxChains {
		t.Errorf("chains = %d, want %d (the cap)", len(b.rep.Chains), maxChains)
	}
}

// Все гипотезы, записи и пробелы правила помечены правилом chain.hpa эпохи 1 (ADR-0034 §3).
func TestHPARecordsCarryTheRule(t *testing.T) {
	t.Parallel()
	rep := hpaTestReport(t, hpaTestNamespace, hpaTestDeployment, hpaTestHPA(5, 5,
		hpaTestCond("ScalingActive", "False", "FailedGetResourceMetric", "x"),
		hpaTestCond("ScalingLimited", "True", "TooManyReplicas", "x")))
	if len(rep.Hypotheses) != 2 {
		t.Fatalf("hypotheses = %v", hpaCodes(rep))
	}
	for _, h := range rep.Hypotheses {
		if h.RuleID != ruleHPAChainID || h.RuleEpoch != 1 {
			t.Errorf("hypothesis %s: rule %q/%d", h.Code, h.RuleID, h.RuleEpoch)
		}
	}
	for _, r := range rep.Recommendations {
		if r.RuleID != ruleHPAChainID || r.RuleEpoch != 1 {
			t.Errorf("recommendation %s: rule %q/%d", r.Code, r.RuleID, r.RuleEpoch)
		}
	}
	for _, h := range rep.Hypotheses {
		if !strings.HasPrefix(h.Code, "hpa.") {
			t.Errorf("hypothesis code %q has no hpa. prefix", h.Code)
		}
	}
}
