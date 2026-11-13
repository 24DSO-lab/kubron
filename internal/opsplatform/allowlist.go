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
	// ADR-0025: виды с устаревшими и удалёнными версиями из встроенной матрицы readiness (internal/readiness/apimatrix).
	// В список входят только хранимые виды, которые можно прочитать get/list; «виртуальные» (Review, Scale, Eviction,
	// DeploymentRollback, JobTemplate…) читать нечем и в проверке остаются «не проверено». Страж — TestAllowlistCoversReadinessMatrix
	// в internal/app: новый вид в матрице обязан попасть сюда или в явный список исключений.
	{Group: "admissionregistration.k8s.io", Kind: "MutatingAdmissionPolicy"}:          {"mutatingadmissionpolicies", false},
	{Group: "admissionregistration.k8s.io", Kind: "MutatingAdmissionPolicyBinding"}:   {"mutatingadmissionpolicybindings", false},
	{Group: "admissionregistration.k8s.io", Kind: "ValidatingAdmissionPolicy"}:        {"validatingadmissionpolicies", false},
	{Group: "admissionregistration.k8s.io", Kind: "ValidatingAdmissionPolicyBinding"}: {"validatingadmissionpolicybindings", false},
	{Group: "apiregistration.k8s.io", Kind: "APIService"}:                             {"apiservices", false},
	{Group: "apps", Kind: "ControllerRevision"}:                                       {"controllerrevisions", true},
	{Group: "certificates.k8s.io", Kind: "CertificateSigningRequest"}:                 {"certificatesigningrequests", false},
	{Group: "certificates.k8s.io", Kind: "ClusterTrustBundle"}:                        {"clustertrustbundles", false},
	{Group: "certificates.k8s.io", Kind: "PodCertificateRequest"}:                     {"podcertificaterequests", true},
	{Group: "coordination.k8s.io", Kind: "Lease"}:                                     {"leases", true},
	{Group: "coordination.k8s.io", Kind: "LeaseCandidate"}:                            {"leasecandidates", true},
	{Group: "extensions", Kind: "DaemonSet"}:                                          {"daemonsets", true},
	{Group: "extensions", Kind: "Deployment"}:                                         {"deployments", true},
	{Group: "extensions", Kind: "Ingress"}:                                            {"ingresses", true},
	{Group: "extensions", Kind: "NetworkPolicy"}:                                      {"networkpolicies", true},
	{Group: "extensions", Kind: "PodSecurityPolicy"}:                                  {"podsecuritypolicies", false},
	{Group: "extensions", Kind: "ReplicaSet"}:                                         {"replicasets", true},
	{Group: "flowcontrol.apiserver.k8s.io", Kind: "FlowSchema"}:                       {"flowschemas", false},
	{Group: "flowcontrol.apiserver.k8s.io", Kind: "PriorityLevelConfiguration"}:       {"prioritylevelconfigurations", false},
	{Group: "networking.k8s.io", Kind: "ClusterCIDR"}:                                 {"clustercidrs", false},
	{Group: "networking.k8s.io", Kind: "IPAddress"}:                                   {"ipaddresses", false},
	{Group: "networking.k8s.io", Kind: "ServiceCIDR"}:                                 {"servicecidrs", false},
	{Group: "node.k8s.io", Kind: "RuntimeClass"}:                                      {"runtimeclasses", false},
	{Group: "policy", Kind: "PodSecurityPolicy"}:                                      {"podsecuritypolicies", false},
	{Group: "rbac.authorization.k8s.io", Kind: "ClusterRole"}:                         {"clusterroles", false},
	{Group: "rbac.authorization.k8s.io", Kind: "ClusterRoleBinding"}:                  {"clusterrolebindings", false},
	{Group: "rbac.authorization.k8s.io", Kind: "Role"}:                                {"roles", true},
	{Group: "rbac.authorization.k8s.io", Kind: "RoleBinding"}:                         {"rolebindings", true},
	{Group: "resource.k8s.io", Kind: "DeviceClass"}:                                   {"deviceclasses", false},
	{Group: "resource.k8s.io", Kind: "DeviceTaintRule"}:                               {"devicetaintrules", false},
	{Group: "resource.k8s.io", Kind: "ResourceClaim"}:                                 {"resourceclaims", true},
	{Group: "resource.k8s.io", Kind: "ResourceClaimTemplate"}:                         {"resourceclaimtemplates", true},
	{Group: "resource.k8s.io", Kind: "ResourcePoolStatusRequest"}:                     {"resourcepoolstatusrequests", false},
	{Group: "resource.k8s.io", Kind: "ResourceSlice"}:                                 {"resourceslices", false},
	{Group: "scheduling.k8s.io", Kind: "PodGroup"}:                                    {"podgroups", true},
	{Group: "scheduling.k8s.io", Kind: "Workload"}:                                    {"workloads", true},
	{Group: "storage.k8s.io", Kind: "CSIDriver"}:                                      {"csidrivers", false},
	{Group: "storage.k8s.io", Kind: "CSINode"}:                                        {"csinodes", false},
	{Group: "storage.k8s.io", Kind: "CSIStorageCapacity"}:                             {"csistoragecapacities", true},
	{Group: "storage.k8s.io", Kind: "VolumeAttachment"}:                               {"volumeattachments", false},
	{Group: "storage.k8s.io", Kind: "VolumeAttributesClass"}:                          {"volumeattributesclasses", false},
	{Group: "storagemigration.k8s.io", Kind: "StorageVersionMigration"}:               {"storageversionmigrations", false},
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
