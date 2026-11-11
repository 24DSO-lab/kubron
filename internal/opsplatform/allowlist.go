// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package opsplatform

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// kindInfo — как вид ресурса превращается в запрос: имя ресурса во множественном числе и область.
type kindInfo struct {
	resource   string
	namespaced bool
}

// allowedKinds — allowlist чтения (ADR-0026 §2): только эти пары «группа, вид» и только операции get и list.
// Версия API не ограничивается: проверке совместимости нужны и устаревшие версии. Secret в список не входит
// и запрещён отдельно (см. isSecret). Шаблон расширения: добавить запись сюда и тест в allowlist_test.go.
//
// Состав — минимум для ADR-0024 (диагностика), ADR-0025 (readiness) и ADR-0027 (drift).
var allowedKinds = map[schema.GroupKind]kindInfo{
	// core
	{Group: "", Kind: "Pod"}:                   {"pods", true},
	{Group: "", Kind: "Namespace"}:             {"namespaces", false},
	{Group: "", Kind: "Node"}:                  {"nodes", false},
	{Group: "", Kind: "Event"}:                 {"events", true},
	{Group: "", Kind: "Service"}:               {"services", true},
	{Group: "", Kind: "Endpoints"}:             {"endpoints", true},
	{Group: "", Kind: "ConfigMap"}:             {"configmaps", true},
	{Group: "", Kind: "PersistentVolumeClaim"}: {"persistentvolumeclaims", true},
	{Group: "", Kind: "PersistentVolume"}:      {"persistentvolumes", false},
	{Group: "", Kind: "ResourceQuota"}:         {"resourcequotas", true},
	{Group: "", Kind: "LimitRange"}:            {"limitranges", true},
	// workloads
	{Group: "apps", Kind: "Deployment"}:  {"deployments", true},
	{Group: "apps", Kind: "ReplicaSet"}:  {"replicasets", true},
	{Group: "apps", Kind: "StatefulSet"}: {"statefulsets", true},
	{Group: "apps", Kind: "DaemonSet"}:   {"daemonsets", true},
	{Group: "batch", Kind: "Job"}:        {"jobs", true},
	{Group: "batch", Kind: "CronJob"}:    {"cronjobs", true},
	// сеть и автомасштабирование
	{Group: "networking.k8s.io", Kind: "Ingress"}:                     {"ingresses", true},
	{Group: "networking.k8s.io", Kind: "IngressClass"}:                {"ingressclasses", false},
	{Group: "networking.k8s.io", Kind: "NetworkPolicy"}:               {"networkpolicies", true},
	{Group: "discovery.k8s.io", Kind: "EndpointSlice"}:                {"endpointslices", true},
	{Group: "autoscaling", Kind: "HorizontalPodAutoscaler"}:           {"horizontalpodautoscalers", true},
	{Group: "policy", Kind: "PodDisruptionBudget"}:                    {"poddisruptionbudgets", true},
	{Group: "events.k8s.io", Kind: "Event"}:                           {"events", true},
	{Group: "storage.k8s.io", Kind: "StorageClass"}:                   {"storageclasses", false},
	{Group: "scheduling.k8s.io", Kind: "PriorityClass"}:               {"priorityclasses", false},
	{Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"}: {"customresourcedefinitions", false},
	// вебхуки admission: ADR-0025, проверка компонентов перед обновлением
	{Group: "admissionregistration.k8s.io", Kind: "ValidatingWebhookConfiguration"}: {"validatingwebhookconfigurations", false},
	{Group: "admissionregistration.k8s.io", Kind: "MutatingWebhookConfiguration"}:   {"mutatingwebhookconfigurations", false},
}

// isSecret — вид Secret в любой группе (защита второго эшелона: даже если Secret случайно попал бы в таблицу).
func isSecret(gk schema.GroupKind) bool { return gk.Kind == "Secret" }

// resolve проверяет вид по allowlist и возвращает ресурс. Неизвестный вид, Secret и пустой вид дают ErrForbidden.
func resolve(gvk schema.GroupVersionKind) (schema.GroupVersionResource, kindInfo, error) {
	gk := gvk.GroupKind()
	if gvk.Version == "" || gk.Kind == "" || isSecret(gk) {
		return schema.GroupVersionResource{}, kindInfo{}, ErrForbidden
	}
	info, ok := allowedKinds[gk]
	if !ok {
		return schema.GroupVersionResource{}, kindInfo{}, ErrForbidden
	}
	return gvk.GroupVersion().WithResource(info.resource), info, nil
}

// AllowedKinds возвращает виды, доступные Reader (для документации и тестов доменов).
func AllowedKinds() []schema.GroupKind {
	out := make([]schema.GroupKind, 0, len(allowedKinds))
	for gk := range allowedKinds {
		out = append(out, gk)
	}
	return out
}
