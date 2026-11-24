// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"context"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// HorizontalPodAutoscaler в графе (ADR-0031 §6, этап 2): вид, который можно задать целью, и ребро scales
// HPA → workload по spec.scaleTargetRef. Для HPA читаются только он сам и его цель; дальше цель раскрывается
// стандартными расширениями (Deployment → ReplicaSet → Pod).

// Коды причин состояния HPA (Node.Reason). Короткие стабильные строки без значений из кластера.
const (
	// ReasonScalingInactive — условие ScalingActive=False по причине, отличной от ScalingDisabled: автомасштабирование
	// не работает (метрики недоступны, цель не читается и т. п.).
	ReasonScalingInactive = "scaling_inactive"
	// ReasonScalingDisabled — ScalingActive=False/ScalingDisabled: у цели ноль реплик, автомасштабирование
	// отключено намеренно, это не неисправность.
	ReasonScalingDisabled = "scaling_disabled"
)

// reasonScalingDisabledCond — значение Reason условия ScalingActive, когда у цели ноль реплик.
const reasonScalingDisabledCond = "ScalingDisabled"

// specHPA — HPA версии v2 (GA с Kubernetes 1.23).
var specHPA = kindSpec{schema.GroupVersionKind{Group: "autoscaling", Version: "v2", Kind: "HorizontalPodAutoscaler"}, true}

// Цели scaleTargetRef, которые граф умеет читать (контракт §1). Всё остальное (custom resource, Argo Rollout,
// ReplicationController) — вершина Unknown/policy: граф такие виды не читает, но и ошибкой это не считается.
var hpaTargetKinds = map[string]bool{"Deployment": true, "StatefulSet": true, "ReplicaSet": true}

// hpaTargetGroup — группа API поддерживаемых целей.
const hpaTargetGroup = "apps"

func init() {
	registerKind(kindExt{Spec: specHPA, Seedable: true, Health: hpaHealth})
	registerExpander("HorizontalPodAutoscaler", expandHPA)
}

// hpaHealth выводит состояние HPA по условиям: Degraded, если ScalingActive=False не из-за ScalingDisabled.
// Условий нет (HPA только создан) — Healthy: отсутствие данных не доказывает неисправность.
func hpaHealth(obj *unstructured.Unstructured) (Health, string) {
	var h autoscalingv2.HorizontalPodAutoscaler
	if !decode(obj, &h) {
		return Unknown, ReasonUnreadable
	}
	for _, c := range h.Status.Conditions {
		if c.Type != autoscalingv2.ScalingActive || c.Status != corev1.ConditionFalse {
			continue
		}
		if c.Reason == reasonScalingDisabledCond {
			return Healthy, ReasonScalingDisabled
		}
		return Degraded, ReasonScalingInactive
	}
	return Healthy, ""
}

// expandHPA: ребро scales HPA → workload. Цели нет — заглушка Missing (ребро missing); вид вне Deployment,
// StatefulSet и ReplicaSet — вершина Unknown/policy без чтения.
func expandHPA(ctx context.Context, b *builder, it item, n Node) {
	var h autoscalingv2.HorizontalPodAutoscaler
	if !decode(n.Object, &h) {
		return
	}
	ref := h.Spec.ScaleTargetRef
	if ref.Kind == "" || ref.Name == "" || !b.reach(it.depth+1) {
		return
	}
	group := ""
	if gv, err := schema.ParseGroupVersion(ref.APIVersion); err == nil {
		group = gv.Group
	}
	if !hpaTargetKinds[ref.Kind] || group != hpaTargetGroup {
		unsupported := Ref{Group: group, Kind: ref.Kind, Namespace: n.Namespace, Name: ref.Name}
		if b.putNode(Node{Ref: unsupported, Health: Unknown, Unknown: UnknownPolicy}) {
			b.edge(n.Ref, unsupported, EdgeScales)
		}
		return
	}
	spec, ok := lookupBuiltin(group, ref.Kind)
	if !ok {
		return
	}
	target, _ := b.get(ctx, spec, n.Namespace, ref.Name)
	b.edge(n.Ref, target.Ref, EdgeScales)
	// Прочитанная цель раскрывается дальше (Deployment → ReplicaSet → Pod); у Missing и Unknown объекта нет.
	b.enqueue(target.Ref, it.depth+1, it.down)
}
