// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"slices"
	"strconv"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// Цепочка для цели HorizontalPodAutoscaler (ADR-0031 §6, этап 2): «не масштабируется» объясняется по
// spec.scaleTargetRef и conditions HPA. Три гипотезы контракта: цели нет (hpa.target_missing), метрики недоступны
// (hpa.metrics_unavailable) и достигнут maxReplicas (hpa.max_replicas_reached). Четвёртая, hpa.target_unhealthy,
// — только указатель на нездоровую цель: её собственные гипотезы в отчёт HPA не копируются, их даёт расследование
// самой цели (шаг hpa.investigate_target и цепочка HPA → цель).

// Коды симптомов.
const (
	SymptomHPANotScaling      = "hpa.not_scaling"      // автомасштабирование не работает: цели нет или метрики недоступны
	SymptomHPAAtMax           = "hpa.at_max_replicas"  // достигнут верхний предел реплик
	SymptomHPATargetUnhealthy = "hpa.target_unhealthy" // цель HPA нездорова
)

const ruleHPAChainID = "chain.hpa"

func init() {
	registerChainRule(ruleHPAChainID, 1, ruleHPAChain)
	// После хука связей (100): добавляет цепочки HPA → цель, которых хук связей не строит (HPA — не Pod и не Service).
	registerPostHook(101, "hpa_chains", hpaChains)
}

// Причины условий HPA (поле Reason, ADR-0031 §6): по ним, а не по тексту сообщения, определяется суть сбоя.
const (
	hpaReasonScalingDisabled = "ScalingDisabled"
	hpaReasonTooManyReplicas = "TooManyReplicas"
)

// hpaMetricReasons — причины ScalingActive=False, означающие, что метрики не получены. Значение — вид источника,
// как он показывается в отчёте (не значение Kubernetes).
var hpaMetricReasons = map[string]string{
	"FailedGetResourceMetric":          "resource",
	"FailedGetContainerResourceMetric": "container-resource",
	"FailedGetPodsMetric":              "pods",
	"FailedGetObjectMetric":            "object",
	"FailedGetExternalMetric":          "external",
	"InvalidMetricSourceType":          "unsupported-type",
	"FailedComputeMetricsReplicas":     "compute-failed",
}

// hpaMetricEventReasons — причины событий HPA о недоступных метриках: подтверждают условие свежим событием.
var hpaMetricEventReasons = []string{
	"FailedGetResourceMetric", "FailedGetContainerResourceMetric", "FailedGetPodsMetric",
	"FailedGetObjectMetric", "FailedGetExternalMetric", "FailedComputeMetricsReplicas",
}

// hpaMetricSourceUnknown — источник метрик не назван: причина условия не из списка, срабатывает запасной путь по тексту.
const hpaMetricSourceUnknown = "unknown"

func ruleHPAChain(b *builder) {
	g, t := b.c.Graph, b.c.Target
	if g == nil || t.Kind != "HorizontalPodAutoscaler" {
		return
	}
	root, ok := hpaNode(g, t)
	if !ok || root.Placeholder || root.Object == nil || root.Health == depgraph.Unknown {
		return // HPA не прочитан графом: пробел уже записан в Missing
	}
	hpa, err := decode[autoscalingv2.HorizontalPodAutoscaler](root.Object)
	if err != nil {
		return
	}
	ref := t
	ref.Group = hpaGroup
	tgt, hasTarget := hpaScaleTarget(g, root.Ref)
	params := hpaParams(hpa)

	if hasTarget {
		hpaTargetState(b, ref, tgt, params)
	}
	hpaMetricsUnavailable(b, ref, hpa, params)
	if hasTarget && hpaTargetIsDegraded(tgt) {
		hpaTargetUnhealthy(b, ref, tgt, params)
	}
	hpaMaxReplicas(b, ref, hpa, params)
}

// hpaNode ищет вершину HPA-цели в графе: корень графа несёт группу API, а ссылка цели её могла не нести.
func hpaNode(g *depgraph.Graph, t ResourceRef) (depgraph.Node, bool) {
	return g.Node(depgraph.Ref{Group: hpaGroup, Kind: t.Kind, Namespace: t.Namespace, Name: t.Name})
}

// hpaScaleTarget — вершина, на которую указывает ребро scales (HPA масштабирует одну цель).
func hpaScaleTarget(g *depgraph.Graph, root depgraph.Ref) (depgraph.Node, bool) {
	edges := g.EdgesFrom(root, depgraph.EdgeScales)
	if len(edges) == 0 {
		return depgraph.Node{}, false
	}
	return g.Node(edges[0].To)
}

// hpaParams — параметры, общие для записей отчёта: имя HPA и цель из spec.scaleTargetRef.
func hpaParams(hpa *autoscalingv2.HorizontalPodAutoscaler) map[string]string {
	return map[string]string{
		"hpa": hpa.Name, "kind": hpa.Spec.ScaleTargetRef.Kind, "target": hpa.Spec.ScaleTargetRef.Name,
	}
}

func hpaWith(params map[string]string, kv ...string) map[string]string {
	out := make(map[string]string, len(params)+len(kv)/2)
	for k, v := range params {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func hpaCondition(hpa *autoscalingv2.HorizontalPodAutoscaler, typ autoscalingv2.HorizontalPodAutoscalerConditionType) (autoscalingv2.HorizontalPodAutoscalerCondition, bool) {
	for _, c := range hpa.Status.Conditions {
		if c.Type == typ {
			return c, true
		}
	}
	return autoscalingv2.HorizontalPodAutoscalerCondition{}, false
}

// hpaTargetState разбирает состояние ссылки на цель: цели нет (Missing) или её вид граф не читает (policy).
// Отказ чтения (forbidden, unavailable, ...) здесь выводов не даёт: пробел уже записан хуком графа.
func hpaTargetState(b *builder, ref ResourceRef, tgt depgraph.Node, params map[string]string) {
	switch {
	case tgt.Placeholder || tgt.Health == depgraph.Missing:
		b.symptom(SymptomHPANotScaling)
		src := b.fact(ref, "hpa.target_ref", Source{Kind: SourceResource, Resource: ref, Field: "spec.scaleTargetRef"}, params)
		nf := b.fact(ref, "hpa.target_not_found", Source{Kind: SourceDerived, Resource: ref, Field: "spec.scaleTargetRef"}, params)
		h := b.hypothesis(ref, "hpa.target_missing", ConfidenceHigh, params, []string{src, nf}, nil)
		b.recommend(ref, "hpa.fix_scale_target", params, h)
	case tgt.Health == depgraph.Unknown && tgt.Unknown == depgraph.UnknownPolicy:
		b.missing("hpa.target_unsupported", fromGraphRef(tgt.Ref), params)
	}
}

// hpaMetricSource определяет источник метрик по причине условия; byReason=false — причина неизвестна, а решение
// принимается по тексту сообщения (запасной путь с пониженной уверенностью).
func hpaMetricSource(c autoscalingv2.HorizontalPodAutoscalerCondition) (source string, byReason, ok bool) {
	if s, found := hpaMetricReasons[c.Reason]; found {
		return s, true, true
	}
	if contains(c.Message, "metric") {
		return hpaMetricSourceUnknown, false, true
	}
	return "", false, false
}

// hpaMetricsUnavailable: ScalingActive=False по причине недоступных метрик. Сообщение условия в отчёт не попадает:
// оно служит только запасным признаком, когда причина не из известного списка.
func hpaMetricsUnavailable(b *builder, ref ResourceRef, hpa *autoscalingv2.HorizontalPodAutoscaler, params map[string]string) {
	c, ok := hpaCondition(hpa, autoscalingv2.ScalingActive)
	if !ok || c.Status != corev1.ConditionFalse || c.Reason == hpaReasonScalingDisabled {
		return
	}
	source, byReason, ok := hpaMetricSource(c)
	if !ok {
		return
	}
	conf := ConfidenceHigh
	if !byReason {
		conf = ConfidenceLow
	}
	b.symptom(SymptomHPANotScaling)
	p := hpaWith(params, "metric", source)
	supporting := []string{b.fact(ref, "hpa.metrics_condition", Source{Kind: SourceResource, Resource: ref, Field: "status.conditions[ScalingActive]"}, p)}
	if ev, found := lastEvent(hpaEvents(b.c, ref.Name, hpaMetricEventReasons...)); found {
		supporting = append(supporting, b.factFromEvent(ref, ev))
	}
	h := b.hypothesis(ref, "hpa.metrics_unavailable", conf, p, supporting, nil)
	b.recommend(ref, "hpa.check_metrics_source", p, h)
	b.step(ref, "hpa.check_metrics_api", p, h)
}

// hpaEvents — события самого HPA (из событий namespace, отобранных сбором).
func hpaEvents(c *Context, name string, reasons ...string) []Event {
	var out []Event
	for _, e := range c.Events {
		if e.Object.Kind == "HorizontalPodAutoscaler" && e.Object.Name == name && (len(reasons) == 0 || slices.Contains(reasons, e.Reason)) {
			out = append(out, e)
		}
	}
	return out
}

// hpaTargetIsDegraded — цель прочитана и нездорова. Идёт выкатка (Progressing) или чтение не удалось (Unknown) —
// не нездорова: об этом ничего не доказано.
func hpaTargetIsDegraded(tgt depgraph.Node) bool {
	return !tgt.Placeholder && tgt.Object != nil && tgt.Health == depgraph.Degraded
}

// hpaTargetUnhealthy — указатель на нездоровую цель. Гипотезы самой цели не копируются: человек открывает
// расследование цели (шаг), а граф показывает цепочку HPA → цель с состоянием и причиной.
func hpaTargetUnhealthy(b *builder, ref ResourceRef, tgt depgraph.Node, params map[string]string) {
	tref := fromGraphRef(tgt.Ref)
	b.symptom(SymptomHPATargetUnhealthy)
	f := b.fact(tref, "hpa.target_degraded", Source{Kind: SourceDerived, Resource: tref, Field: "status"}, params)
	h := b.hypothesis(ref, "hpa.target_unhealthy", ConfidenceLow, params, []string{f}, nil)
	b.step(tref, "hpa.investigate_target", params, h)
}

// hpaMaxReplicas: ScalingLimited=True/TooManyReplicas и текущее число реплик равно maxReplicas. Оба признака берутся
// из status одного объекта, поэтому уверенность средняя: подтверждение независимым источником — сигнал метрик (ADR-0032).
func hpaMaxReplicas(b *builder, ref ResourceRef, hpa *autoscalingv2.HorizontalPodAutoscaler, params map[string]string) {
	c, ok := hpaCondition(hpa, autoscalingv2.ScalingLimited)
	if !ok || c.Status != corev1.ConditionTrue || c.Reason != hpaReasonTooManyReplicas {
		return
	}
	cur, limit := hpa.Status.CurrentReplicas, hpa.Spec.MaxReplicas
	if limit <= 0 || cur != limit {
		return
	}
	b.symptom(SymptomHPAAtMax)
	p := hpaWith(params, "current", strconv.Itoa(int(cur)), "max", strconv.Itoa(int(limit)))
	f := b.fact(ref, "hpa.scaling_limited", Source{Kind: SourceResource, Resource: ref, Field: "status.conditions[ScalingLimited]"}, p)
	h := b.hypothesis(ref, "hpa.max_replicas_reached", ConfidenceMedium, p, []string{f}, nil)
	b.recommend(ref, "hpa.raise_max_replicas", p, h)
}

// hpaChains добавляет цепочки HPA → цель для гипотез «цели нет» и «цель нездорова». Хук связей (chains.go) строит
// цепочки только от нездоровых Pod и Service к кандидату, а здесь затронутый ресурс — сам HPA.
func hpaChains(b *builder) {
	g, t := b.c.Graph, b.c.Target
	if g == nil || t.Kind != "HorizontalPodAutoscaler" {
		return
	}
	root, ok := hpaNode(g, t)
	if !ok {
		return
	}
	tgt, hasTarget := hpaScaleTarget(g, root.Ref)
	if !hasTarget {
		return
	}
	for _, h := range b.rep.Hypotheses {
		if h.RuleID != ruleHPAChainID || (h.Code != "hpa.target_missing" && h.Code != "hpa.target_unhealthy") {
			continue
		}
		if len(b.rep.Chains) >= maxChains {
			return
		}
		b.rep.Chains = append(b.rep.Chains, Chain{Path: chainSteps([]depgraph.Node{root, tgt}), Candidate: h.ID})
	}
}
