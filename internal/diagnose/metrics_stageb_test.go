// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/signals"
)

// Метрики, этап B (ADR-0032 §4): сбор по графу, правила, усиление чужих гипотез, недоступность. Граф и чужие
// гипотезы собираются вручную: тесты не импортируют реализацию D1–D4.

var (
	sbPVC     = depgraph.Ref{Kind: "PersistentVolumeClaim", Namespace: "ns", Name: "data-db-0"}
	sbHPA     = depgraph.Ref{Group: groupAutoscaling, Kind: "HorizontalPodAutoscaler", Namespace: "ns", Name: "api"}
	sbIngress = depgraph.Ref{Group: groupNetworking, Kind: "Ingress", Namespace: "ns", Name: "web"}
	sbDeploy  = depgraph.Ref{Group: groupApps, Kind: "Deployment", Namespace: "ns", Name: "api"}
	sbService = depgraph.Ref{Kind: "Service", Namespace: "ns", Name: "svc"}

	sbPVCSub     = signals.Subject{Namespace: "ns", PVC: "data-db-0"}
	sbHPASub     = signals.Subject{Namespace: "ns", HPA: "api"}
	sbIngressSub = signals.Subject{Namespace: "ns", Ingress: "web"}
	sbDeploySub  = signals.Subject{Namespace: "ns", Deployment: "api"}
)

func sbNode(r depgraph.Ref, h depgraph.Health) depgraph.Node { return depgraph.Node{Ref: r, Health: h} }

// sbGraph — граф из вершин; seed — первая.
func sbGraph(nodes ...depgraph.Node) *depgraph.Graph {
	g := depgraph.New(nodes[0].Ref)
	for _, n := range nodes {
		g.AddNode(n)
	}
	return g
}

// sbCtx — контекст с целью и графом; unready добавляет не готовый Pod (расследование касается неисправности).
func sbCtx(target ResourceRef, g *depgraph.Graph, unready bool) *Context {
	c := &Context{Target: target, ObservedAt: t0, Graph: g, Nodes: map[string]*corev1.Node{}}
	if unready {
		c.Pods = []*corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "api-0"}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}}
	}
	return c
}

func sbVal(sig signals.Signal, s signals.Subject, v float64, unit string) signals.Value {
	return signals.Value{Signal: sig, Subject: s, Value: v, Unit: unit, Agg: "max", Window: 15 * 60e9, ObservedAt: t0}
}

// sbReport собирает отчёт: чужие гипотезы pre уже в отчёте (как после правил D1–D4), затем пост-хук метрик.
func sbReport(c *Context, set *signals.Set, pre ...Hypothesis) Report {
	b := newBuilder(c)
	b.touch(c.Target)
	for i := range pre {
		pre[i].Kind, pre[i].ID = KindHypothesis, "h"+string(rune('1'+i))
		if pre[i].Supporting == nil {
			pre[i].Supporting = []string{}
		}
		if pre[i].Contradicting == nil {
			pre[i].Contradicting = []string{}
		}
		b.rep.Hypotheses = append(b.rep.Hypotheses, pre[i])
	}
	b.ruleID, b.ruleEpoch = metricsRuleID, metricsRuleEpoch
	applyMetrics(b, &signalOutcome{Wants: collectWants(c), Set: set})
	return *b.rep
}

func wantSignals(ws []signals.Want) []signals.Signal {
	var out []signals.Signal
	for _, w := range ws {
		out = append(out, w.Signal)
	}
	return out
}

func TestStageBWantsComeFromTheGraph(t *testing.T) {
	ingTarget := ResourceRef{Group: groupNetworking, Kind: "Ingress", Namespace: "ns", Name: "web"}
	svcMissing := depgraph.Node{Ref: sbService, Health: depgraph.Missing, Placeholder: true}
	for _, tc := range []struct {
		name string
		c    *Context
		want []signals.Want
	}{
		{"ingress target with a missing backend",
			sbCtx(ingTarget, sbGraph(sbNode(sbIngress, depgraph.Degraded), svcMissing), false),
			[]signals.Want{{Signal: signals.Ingress5xxRatio, Subject: sbIngressSub}, {Signal: signals.IngressLatencyP95, Subject: sbIngressSub}}},
		{"healthy ingress target is still asked",
			sbCtx(ingTarget, sbGraph(sbNode(sbIngress, depgraph.Healthy)), false),
			[]signals.Want{{Signal: signals.Ingress5xxRatio, Subject: sbIngressSub}, {Signal: signals.IngressLatencyP95, Subject: sbIngressSub}}},
		{"a missing ingress is never asked",
			sbCtx(ingTarget, sbGraph(depgraph.Node{Ref: sbIngress, Health: depgraph.Missing, Placeholder: true}), true), nil},
		{"unreadable ingress is never asked",
			sbCtx(ingTarget, sbGraph(depgraph.Node{Ref: sbIngress, Health: depgraph.Unknown, Unknown: depgraph.UnknownForbidden}), true), nil},
		{"pod target: bound pvc and hpa of an unhealthy chain",
			sbCtx(ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-0"},
				sbGraph(sbNode(sbPVC, depgraph.Healthy), sbNode(sbHPA, depgraph.Healthy), sbNode(sbDeploy, depgraph.Degraded)), true),
			[]signals.Want{
				{Signal: signals.PVCUsageRatio, Subject: sbPVCSub}, {Signal: signals.HPAAtMaxReplicas, Subject: sbHPASub},
				{Signal: signals.DeploymentUnavailableReplicas, Subject: sbDeploySub},
			}},
		{"pending pvc has no volume statistics",
			sbCtx(ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-0"}, sbGraph(sbNode(sbPVC, depgraph.Degraded)), true), nil},
		{"a healthy investigation asks nothing",
			sbCtx(ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-0"},
				sbGraph(sbNode(sbPVC, depgraph.Healthy), sbNode(sbHPA, depgraph.Healthy), sbNode(sbDeploy, depgraph.Healthy)), false), nil},
		{"no graph, no subjects", sbCtx(ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-0"}, nil, true), nil},
		{"hpa target is asked even when everything looks healthy",
			sbCtx(ResourceRef{Group: groupAutoscaling, Kind: "HorizontalPodAutoscaler", Namespace: "ns", Name: "api"}, sbGraph(sbNode(sbHPA, depgraph.Healthy)), false),
			[]signals.Want{{Signal: signals.HPAAtMaxReplicas, Subject: sbHPASub}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := collectWants(tc.c); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("wants:\n got  %+v\n want %+v", got, tc.want)
			}
		})
	}
}

// Сигналы этапа B идут после сигналов Pod и не вытесняют их из бюджета запросов.
func TestStageBWantsRespectTheBudget(t *testing.T) {
	c := metricsCtx(metricsPod("oom", 4, true))
	base := collectWants(c) // сигналы Pod без графа
	c.Graph = sbGraph(sbNode(sbPVC, depgraph.Healthy), sbNode(sbHPA, depgraph.Healthy), sbNode(sbIngress, depgraph.Degraded), sbNode(sbDeploy, depgraph.Degraded))
	ws := collectWants(c)
	if len(ws) > signals.MaxSignalQueries {
		t.Fatalf("%d wants exceed the budget", len(ws))
	}
	if len(ws) < len(base) || !reflect.DeepEqual(ws[:len(base)], base) {
		t.Errorf("pod signals lost their priority:\n got  %v\n base %v", wantSignals(ws), wantSignals(base))
	}
}

// Таблица «сигнал → факт и гипотеза» при отсутствии чужих гипотез.
func TestStageBSignalToHypothesis(t *testing.T) {
	ingTarget := ResourceRef{Group: groupNetworking, Kind: "Ingress", Namespace: "ns", Name: "web"}
	podTarget := ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-0"}
	graph := func() *depgraph.Graph {
		return sbGraph(sbNode(sbPVC, depgraph.Healthy), sbNode(sbHPA, depgraph.Healthy), sbNode(sbDeploy, depgraph.Degraded), sbNode(sbIngress, depgraph.Degraded))
	}
	for _, tc := range []struct {
		name     string
		target   ResourceRef
		vals     []signals.Value
		fact     string
		code     string // пусто — гипотезы нет
		conf     Confidence
		rec      string
		ruleID   string
		deployed int32 // недоступные реплики по статусу Deployment
	}{
		{"pvc 95% is low", podTarget, []signals.Value{sbVal(signals.PVCUsageRatio, sbPVCSub, 0.95, "ratio")},
			"metric.pvc_usage_ratio", hypPVCNearlyFull, ConfidenceLow, "rec.expand_pvc", ruleMetricsPVCFull, 0},
		{"pvc 98% is medium", podTarget, []signals.Value{sbVal(signals.PVCUsageRatio, sbPVCSub, 0.98, "ratio")},
			"metric.pvc_usage_ratio", hypPVCNearlyFull, ConfidenceMedium, "rec.expand_pvc", ruleMetricsPVCFull, 0},
		{"pvc 50% is only a fact", podTarget, []signals.Value{sbVal(signals.PVCUsageRatio, sbPVCSub, 0.5, "ratio")},
			"metric.pvc_usage_ratio", "", "", "", "", 0},
		{"hpa at max", podTarget, []signals.Value{sbVal(signals.HPAAtMaxReplicas, sbHPASub, 1, "flag")},
			"metric.hpa_at_max_replicas", hypHPASaturated, ConfidenceLow, "rec.review_hpa_max_replicas", ruleMetricsHPASaturated, 0},
		{"hpa below max is only a fact", podTarget, []signals.Value{sbVal(signals.HPAAtMaxReplicas, sbHPASub, 0, "flag")},
			"metric.hpa_at_max_replicas", "", "", "", "", 0},
		{"unavailable replicas, kubernetes agrees", podTarget, []signals.Value{sbVal(signals.DeploymentUnavailableReplicas, sbDeploySub, 2, "count")},
			"metric.deployment_unavailable_replicas", hypDeploymentUnavailable, ConfidenceMedium, "", ruleMetricsUnavailableReplicas, 2},
		{"unavailable replicas only in the window", podTarget, []signals.Value{sbVal(signals.DeploymentUnavailableReplicas, sbDeploySub, 1, "count")},
			"metric.deployment_unavailable_replicas", hypDeploymentUnavailable, ConfidenceLow, "", ruleMetricsUnavailableReplicas, 0},
		{"no unavailable replicas", podTarget, []signals.Value{sbVal(signals.DeploymentUnavailableReplicas, sbDeploySub, 0, "count")},
			"metric.deployment_unavailable_replicas", "", "", "", "", 0},
		{"5xx 6%", ingTarget, []signals.Value{sbVal(signals.Ingress5xxRatio, sbIngressSub, 0.06, "ratio")},
			"metric.ingress_5xx_ratio", hypIngressDegraded, ConfidenceLow, "rec.review_ingress_backend", ruleMetricsIngressErrors, 0},
		{"5xx 4% is below the threshold", ingTarget, []signals.Value{sbVal(signals.Ingress5xxRatio, sbIngressSub, 0.04, "ratio")},
			"metric.ingress_5xx_ratio", "", "", "", "", 0},
		{"latency 2.5s", ingTarget, []signals.Value{sbVal(signals.IngressLatencyP95, sbIngressSub, 2.5, "seconds")},
			"metric.ingress_latency_p95", hypIngressDegraded, ConfidenceLow, "rec.review_ingress_backend", ruleMetricsIngressErrors, 0},
		{"latency 1.9s is below the threshold", ingTarget, []signals.Value{sbVal(signals.IngressLatencyP95, sbIngressSub, 1.9, "seconds")},
			"metric.ingress_latency_p95", "", "", "", "", 0},
		{"errors and latency together are medium", ingTarget, []signals.Value{
			sbVal(signals.Ingress5xxRatio, sbIngressSub, 0.2, "ratio"), sbVal(signals.IngressLatencyP95, sbIngressSub, 3, "seconds")},
			"metric.ingress_5xx_ratio", hypIngressDegraded, ConfidenceMedium, "rec.review_ingress_backend", ruleMetricsIngressErrors, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := sbCtx(tc.target, graph(), true)
			if tc.deployed > 0 {
				c.Deployment = &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "api"}, Status: appsv1.DeploymentStatus{UnavailableReplicas: tc.deployed}}
			}
			rep := sbReport(c, signals.NewSet("", tc.vals, nil))

			var fact *Fact
			for i := range rep.Facts {
				if rep.Facts[i].Code == tc.fact {
					fact = &rep.Facts[i]
				}
			}
			if fact == nil || fact.Source.Kind != SourceMetric || fact.Source.Signal == "" || fact.ObservedAt != t0 {
				t.Fatalf("no metric fact %s with its source: %+v", tc.fact, rep.Facts)
			}
			if tc.code == "" {
				if len(rep.Hypotheses) != 0 || len(rep.Recommendations) != 0 {
					t.Errorf("unexpected hypotheses %+v", rep.Hypotheses)
				}
				return
			}
			h := hyp(rep, tc.code)
			if h == nil || h.Confidence != tc.conf || h.RuleID != tc.ruleID || h.RuleEpoch != metricsStageBEpoch || len(h.Supporting) == 0 {
				t.Fatalf("hypothesis = %+v, want %s %s by %s", h, tc.code, tc.conf, tc.ruleID)
			}
			if tc.rec != "" && (len(rep.Recommendations) != 1 || rep.Recommendations[0].Code != tc.rec || rep.Recommendations[0].RuleID != tc.ruleID) {
				t.Errorf("recommendations = %+v, want %s", rep.Recommendations, tc.rec)
			}
		})
	}
}

// Чужая гипотеза есть: метрика усиливает её не более чем на один уровень и не выше medium, собственной не выдаётся.
func TestStageBStrengthensForeignHypotheses(t *testing.T) {
	podTarget := ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-0"}
	g := sbGraph(sbNode(sbPVC, depgraph.Healthy), sbNode(sbHPA, depgraph.Healthy), sbNode(sbIngress, depgraph.Degraded))
	pvcRes := ResourceRef{Group: "apps", Kind: "StatefulSet", Namespace: "ns", Name: "db"}
	hpaRes := ResourceRef{Group: groupAutoscaling, Kind: "HorizontalPodAutoscaler", Namespace: "ns", Name: "api"}
	ingRes := ResourceRef{Group: groupNetworking, Kind: "Ingress", Namespace: "ns", Name: "web"}

	for _, tc := range []struct {
		name string
		pre  Hypothesis
		vals []signals.Value
		own  string // собственная гипотеза метрик, которой быть не должно
		from Confidence
		want Confidence
	}{
		{"pvc low to medium", Hypothesis{Code: hypStsPVCPending, Resource: pvcRes, Params: map[string]string{"pvc": "data-db-0"}, Confidence: ConfidenceLow},
			[]signals.Value{sbVal(signals.PVCUsageRatio, sbPVCSub, 0.99, "ratio")}, hypPVCNearlyFull, ConfidenceLow, ConfidenceMedium},
		{"pvc high stays high", Hypothesis{Code: hypStsPVCPending, Resource: pvcRes, Params: map[string]string{"pvc": "data-db-0"}, Confidence: ConfidenceHigh},
			[]signals.Value{sbVal(signals.PVCUsageRatio, sbPVCSub, 0.99, "ratio")}, hypPVCNearlyFull, ConfidenceHigh, ConfidenceHigh},
		{"hpa low to medium", Hypothesis{Code: hypHPAMaxReached, Resource: hpaRes, Params: map[string]string{"hpa": "api"}, Confidence: ConfidenceLow},
			[]signals.Value{sbVal(signals.HPAAtMaxReplicas, sbHPASub, 1, "flag")}, hypHPASaturated, ConfidenceLow, ConfidenceMedium},
		{"hpa medium never reaches high", Hypothesis{Code: hypHPAMaxReached, Resource: hpaRes, Params: map[string]string{"hpa": "api"}, Confidence: ConfidenceMedium},
			[]signals.Value{sbVal(signals.HPAAtMaxReplicas, sbHPASub, 1, "flag")}, hypHPASaturated, ConfidenceMedium, ConfidenceMedium},
		{"ingress low to medium", Hypothesis{Code: hypIngressNoEndpoints, Resource: ingRes, Params: map[string]string{"ingress": "web", "service": "svc"}, Confidence: ConfidenceLow},
			[]signals.Value{sbVal(signals.Ingress5xxRatio, sbIngressSub, 0.4, "ratio"), sbVal(signals.IngressLatencyP95, sbIngressSub, 4, "seconds")},
			hypIngressDegraded, ConfidenceLow, ConfidenceMedium},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := sbCtx(podTarget, g, true)
			rep := sbReport(c, signals.NewSet("", tc.vals, nil), tc.pre)
			h := hyp(rep, tc.pre.Code)
			if h.Confidence != tc.want {
				t.Errorf("confidence %s -> %s, want %s", tc.from, h.Confidence, tc.want)
			}
			if len(h.Supporting) < len(tc.vals) {
				t.Errorf("supporting facts were not added: %v", h.Supporting)
			}
			if own := hyp(rep, tc.own); own != nil {
				t.Errorf("a duplicate hypothesis %s next to the foreign one", own.Code)
			}
			if len(rep.Recommendations) != 0 {
				t.Errorf("metrics must not add recommendations to a foreign hypothesis: %+v", rep.Recommendations)
			}
			if h.RuleID != "" {
				t.Errorf("the foreign hypothesis kept its rule? got %q", h.RuleID) // чужая запись правилом метрик не переписывается
			}
		})
	}
}

// Метрика о другом объекте чужую гипотезу не усиливает.
func TestStageBDoesNotTouchHypothesesOfOtherObjects(t *testing.T) {
	g := sbGraph(sbNode(sbHPA, depgraph.Healthy))
	c := sbCtx(ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-0"}, g, true)
	other := Hypothesis{Code: hypHPAMaxReached, Params: map[string]string{"hpa": "another"}, Confidence: ConfidenceLow,
		Resource: ResourceRef{Group: groupAutoscaling, Kind: "HorizontalPodAutoscaler", Namespace: "ns", Name: "another"}}
	rep := sbReport(c, signals.NewSet("", []signals.Value{sbVal(signals.HPAAtMaxReplicas, sbHPASub, 1, "flag")}, nil), other)
	if h := hyp(rep, hypHPAMaxReached); h.Confidence != ConfidenceLow || len(h.Supporting) != 0 {
		t.Errorf("a hypothesis of another HPA was changed: %+v", h)
	}
	if hyp(rep, hypHPASaturated) == nil {
		t.Error("the saturated HPA must get its own hypothesis")
	}
}

// Нет стека: отчёт полон, фактов и гипотез метрик нет, подсказка одна и спокойная.
func TestStageBWithoutStack(t *testing.T) {
	g := sbGraph(sbNode(sbIngress, depgraph.Degraded), sbNode(sbPVC, depgraph.Healthy))
	c := sbCtx(ResourceRef{Group: groupNetworking, Kind: "Ingress", Namespace: "ns", Name: "web"}, g, true)
	base := sbReport(c, nil)
	for _, reason := range []signals.Reason{signals.ReasonNotInstalled, signals.ReasonForbidden, signals.ReasonUnavailable} {
		rep := sbReport(c, signals.NewSet(reason, nil, nil))
		if len(rep.Facts) != 0 || len(rep.Hypotheses) != 0 {
			t.Errorf("%s: no stack must give no evidence: %+v", reason, rep)
		}
		if len(rep.Missing) != 1 || rep.Missing[0].Code != missingMetricsUnavailable || rep.Missing[0].Params["reason"] != string(reason) {
			t.Errorf("%s: missing = %+v", reason, rep.Missing)
		}
		rep.Missing = base.Missing
		if !reflect.DeepEqual(rep, base) {
			t.Errorf("%s: the report differs from the one without metrics", reason)
		}
	}
}

// Семейства ingress нет: подсказка только про сигналы Ingress, остальные значения работают.
func TestStageBFamilyMissingHintsOnlyForAskedSignals(t *testing.T) {
	g := sbGraph(sbNode(sbIngress, depgraph.Degraded), sbNode(sbPVC, depgraph.Healthy))
	c := sbCtx(ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-0"}, g, true)
	set := signals.NewSet("", []signals.Value{sbVal(signals.PVCUsageRatio, sbPVCSub, 0.95, "ratio")}, []signals.Gap{
		{Signal: signals.Ingress5xxRatio, Subject: sbIngressSub, Reason: signals.ReasonFamilyMissing},
		{Signal: signals.IngressLatencyP95, Subject: sbIngressSub, Reason: signals.ReasonFamilyMissing},
	})
	rep := sbReport(c, set)
	if hyp(rep, hypPVCNearlyFull) == nil {
		t.Error("the available signal must still work")
	}
	var got []string
	for _, m := range rep.Missing {
		if m.Code != missingMetricsSignal || m.Params["reason"] != "family_missing" || m.Resource.Kind != "Ingress" {
			t.Errorf("unexpected hint %+v", m)
		}
		got = append(got, m.Params["signal"])
	}
	if !slices.Equal(got, []string{"ingress_5xx_ratio", "ingress_latency_p95"}) {
		t.Errorf("hints for signals %v", got)
	}
	// Операция не частичная: пробел семейства — не отказ стека.
	if set.PartialSource() != "" {
		t.Error("a missing family must not make the report partial")
	}
}

// Метрики Ingress без ingress-nginx не запрашиваются для здорового объекта чужой цепочки, а факты не содержат
// ничего, кроме кодов, ссылок и чисел.
func TestStageBFactsCarryOnlyCodesAndNumbers(t *testing.T) {
	c := sbCtx(ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-0"}, sbGraph(sbNode(sbIngress, depgraph.Degraded)), true)
	rep := sbReport(c, signals.NewSet("", []signals.Value{
		sbVal(signals.Ingress5xxRatio, sbIngressSub, 0.2, "ratio"), sbVal(signals.IngressLatencyP95, sbIngressSub, 3.25, "seconds"),
	}, nil))
	for _, f := range rep.Facts {
		for k := range f.Params {
			if !slices.Contains([]string{"signal", "value", "unit", "agg", "percent", "window", "ingress"}, k) {
				t.Errorf("fact %s has an unexpected param %q", f.Code, k)
			}
		}
	}
	if f := rep.Facts[0]; f.Params["percent"] != "20" || f.Params["ingress"] != "web" || f.Resource.Group != groupNetworking {
		t.Errorf("ingress fact = %+v", f)
	}
	if f := rep.Facts[1]; f.Params["value"] != "3.250" || f.Params["unit"] != "seconds" {
		t.Errorf("latency fact = %+v", f)
	}
}

// Один и тот же снимок — один и тот же отчёт.
func TestStageBIsDeterministic(t *testing.T) {
	g := sbGraph(sbNode(sbPVC, depgraph.Healthy), sbNode(sbHPA, depgraph.Healthy), sbNode(sbIngress, depgraph.Degraded), sbNode(sbDeploy, depgraph.Degraded))
	vals := []signals.Value{
		sbVal(signals.IngressLatencyP95, sbIngressSub, 3, "seconds"), sbVal(signals.PVCUsageRatio, sbPVCSub, 0.99, "ratio"),
		sbVal(signals.HPAAtMaxReplicas, sbHPASub, 1, "flag"), sbVal(signals.DeploymentUnavailableReplicas, sbDeploySub, 1, "count"),
		sbVal(signals.Ingress5xxRatio, sbIngressSub, 0.3, "ratio"),
	}
	run := func() Report {
		return sbReport(sbCtx(ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-0"}, g, true), signals.NewSet("", slices.Clone(vals), nil))
	}
	first := run()
	for range 5 {
		if !reflect.DeepEqual(first, run()) {
			t.Fatal("the report is not deterministic")
		}
	}
	if len(first.Hypotheses) != 4 {
		t.Errorf("hypotheses = %d, want 4: %+v", len(first.Hypotheses), first.Hypotheses)
	}
}

// Правило этапа B, упавшее с паникой, не ломает остальные: MissingContext rule.failed, отчёт строится дальше.
func TestStageBRulePanicIsIsolated(t *testing.T) {
	c := sbCtx(ResourceRef{Kind: "Pod", Namespace: "ns", Name: "api-0"}, sbGraph(sbNode(sbPVC, depgraph.Healthy)), true)
	b := newBuilder(c)
	e := &evidence{b: b, o: &signalOutcome{}, fact: map[signals.Want]string{}, val: map[signals.Want]signals.Value{}}
	e.asRule("metrics.test_panic", func() { panic("boom") })
	if len(b.rep.Missing) != 1 || b.rep.Missing[0].Code != "rule.failed" || b.rep.Missing[0].Params["rule"] != "metrics.test_panic" {
		t.Errorf("missing = %+v", b.rep.Missing)
	}
	if b.ruleID != "" {
		t.Errorf("the rule id leaked: %q", b.ruleID)
	}
}

type sbProvider struct {
	set *signals.Set
	req signals.Request
}

func (p *sbProvider) Fetch(_ context.Context, r signals.Request) (*signals.Set, error) {
	p.req = r
	return p.set, nil
}

// Сквозной сбор: запрос к провайдеру собирается по графу, а пост-хук читает ответ.
func TestStageBCollectSignalsEndToEnd(t *testing.T) {
	g := sbGraph(sbNode(sbIngress, depgraph.Degraded), sbNode(sbService, depgraph.Degraded))
	c := sbCtx(ResourceRef{Group: groupNetworking, Kind: "Ingress", Namespace: "ns", Name: "web"}, g, false)
	p := &sbProvider{set: signals.NewSet("", []signals.Value{sbVal(signals.Ingress5xxRatio, sbIngressSub, 0.5, "ratio")}, []signals.Gap{
		{Signal: signals.IngressLatencyP95, Subject: sbIngressSub, Reason: signals.ReasonNoSeries},
	})}
	o := collectSignals(t.Context(), c, p)
	if o == nil || o.Set == nil || len(p.req.Want) != 2 || !p.req.At.Equal(t0) {
		t.Fatalf("outcome %+v, request %+v", o, p.req)
	}
	b := newBuilder(c)
	b.touch(c.Target)
	applyMetrics(b, o)
	if hyp(*b.rep, hypIngressDegraded) == nil {
		t.Errorf("no hypothesis from the fetched signals: %+v", b.rep.Hypotheses)
	}
	if len(b.rep.Missing) != 1 || b.rep.Missing[0].Params["reason"] != "no_series" {
		t.Errorf("missing = %+v", b.rep.Missing)
	}
}

// Каждый код этапа B переведён на en, ru и cn, а подстановки в переводах — только параметры, которые код выдаёт, и
// одни и те же во всех языках.
func TestStageBCodesAreTranslated(t *testing.T) {
	type section struct {
		name   string
		codes  []string
		params []string
	}
	factCodes := []string{}
	for _, s := range []signals.Signal{signals.PVCUsageRatio, signals.HPAAtMaxReplicas, signals.DeploymentUnavailableReplicas, signals.Ingress5xxRatio, signals.IngressLatencyP95} {
		factCodes = append(factCodes, "metric."+string(s))
	}
	sections := []section{
		{"facts", factCodes, []string{"signal", "value", "unit", "agg", "percent", "window", "pvc", "hpa", "deployment", "ingress"}},
		{"hypotheses", []string{hypPVCNearlyFull, hypHPASaturated, hypDeploymentUnavailable, hypIngressDegraded}, []string{"pvc", "percent", "hpa", "deployment", "replicas", "window", "ingress"}},
		{"recommendations", []string{"rec.expand_pvc", "rec.review_hpa_max_replicas", "rec.review_ingress_backend"}, []string{"pvc", "hpa", "ingress"}},
	}
	placeholder := regexp.MustCompile(`\{([a-z_]+)\}`)
	found := map[string]map[string][]string{} // раздел.код → язык → подстановки
	for _, lang := range []string{"en", "ru", "cn"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "i18n", "locales", lang, "incidentsMetrics.json"))
		if err != nil {
			t.Fatal(err)
		}
		var msgs map[string]map[string]string
		if err := json.Unmarshal(raw, &msgs); err != nil {
			t.Fatal(err)
		}
		for _, sec := range sections {
			for _, code := range sec.codes {
				text := msgs[sec.name][strings.ReplaceAll(code, ".", "_")]
				if text == "" {
					t.Errorf("%s: no translation incidents.%s.%s", lang, sec.name, strings.ReplaceAll(code, ".", "_"))
					continue
				}
				var ps []string
				for _, m := range placeholder.FindAllStringSubmatch(text, -1) {
					if !slices.Contains(sec.params, m[1]) {
						t.Errorf("%s: %s uses {%s}, which the code never sets", lang, code, m[1])
					}
					ps = append(ps, m[1])
				}
				slices.Sort(ps)
				key := sec.name + "." + code
				if found[key] == nil {
					found[key] = map[string][]string{}
				}
				found[key][lang] = ps
			}
		}
	}
	for key, byLang := range found {
		if !reflect.DeepEqual(byLang["en"], byLang["ru"]) || !reflect.DeepEqual(byLang["en"], byLang["cn"]) {
			t.Errorf("%s: placeholders differ between languages: %v", key, byLang)
		}
	}
}
