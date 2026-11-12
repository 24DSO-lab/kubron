// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness

import (
	"slices"
	"strings"
)

// Коды сообщений отчёта. Сервис кладёт в отчёт код и параметры, подпись по коду переводит интерфейс
// (locales/*/readiness.json, раздел codes). Английский текст ниже нужен только экспорту в Markdown и как
// запасной вариант для неизвестного интерфейсу кода. Код не должен быть префиксом другого кода: в файле
// переводов коды — вложенные ключи (тест TestCodesAreNotPrefixes).
const (
	// Объяснения API findings.
	CodeAPIRemoved    = "api.removed"
	CodeAPIDeprecated = "api.deprecated"
	// Рекомендации.
	CodeRecMigrate           = "api.migrate"
	CodeRecMigrateNoReplace  = "api.migrateNoReplacement"
	CodeRecComponentLimit    = "rec.component.limitation"
	CodeRecComponentVerify   = "rec.component.verify"
	CodeRecComponentUnknown  = "rec.component.unrecognized"
	CodeRecComponentUnhealth = "rec.component.unhealthy"
	CodeRecStepwise          = "rec.platform.stepwise"
	CodeRecSupportedVersion  = "rec.platform.supportedVersion"
	CodeRecRerun             = "rec.source.rerun"
	CodeRecReadManually      = "rec.source.checkManually"

	// Компоненты.
	CodeComponentConfirmed       = "component.confirmed"
	CodeComponentKnownLimitation = "component.knownLimitation"
	CodeComponentInsufficient    = "component.insufficientData"
	CodeComponentNotRecognized   = "component.notRecognized"
	CodeComponentNotReady        = "component.notReady"

	// Платформа.
	CodePlatformPathOK       = "platform.path.ok"
	CodePlatformPathSkips    = "platform.path.skipsMinor"
	CodePlatformUnsupported  = "platform.currentUnsupported"
	CodePlatformSameVersion  = "platform.path.sameVersion"
	CodeSourceResources      = "source.resources.unavailable"
	CodeSourceResourcesPart  = "source.resources.partial"
	CodeSourceListUnavail    = "source.list.unavailable"
	CodeSourceListTruncated  = "source.list.truncated"
	CodeSourceNotCovered     = "source.api.notCovered"
	CodeSourceNodes          = "source.nodes.unavailable"
	CodeSourceWorkloads      = "source.workloads.unavailable"
	CodeSourceWorkloadsTrunc = "source.workloads.truncated"

	// Действия.
	CodeActMigrateRemoved   = "action.migrateRemovedAPIs"
	CodeActMigrateDeprecate = "action.migrateDeprecatedAPIs"
	CodeActComponents       = "action.resolveComponents"
	CodeActVerifyDocs       = "action.verifyComponentDocs"
	CodeActUnrecognized     = "action.reviewUnrecognized"
	CodeActUnhealthy        = "action.fixUnhealthy"
	CodeActStepwise         = "action.planStepwise"
	CodeActNotChecked       = "action.reviewNotChecked"
	CodeActReleaseNotes     = "action.readReleaseNotes"
	CodeActGitOps           = "action.checkManifests"
	CodeActBackup           = "action.backup"
	CodeActNodesReady       = "action.verifyNodesReady"
	CodeActWorkloads        = "action.verifyWorkloads"
	CodeActRerun            = "action.rerunReadiness"
	CodeActAddons           = "action.verifyAddons"

	// Дополнительные коды ограничений coverage.
	LimitClusterOlderThanMatrix = "coverage.matrix.clusterOlderThanData"
)

// englishTexts — английские шаблоны сообщений с параметрами {name}. Запасной текст экспорта; единственный
// источник текста для Markdown, потому что бэкенд язык интерфейса не знает.
var englishTexts = map[string]string{
	CodeAPIRemoved:    "{kind} ({apiVersion}) is removed in Kubernetes {target} (removed in {removedIn}); {count} object(s) in the cluster were created or managed with this API version.",
	CodeAPIDeprecated: "{kind} ({apiVersion}) is deprecated since Kubernetes {deprecatedIn}; {count} object(s) in the cluster were created or managed with this API version.",

	CodeRecMigrate:           "Migrate these manifests to {replacement} before upgrading.",
	CodeRecMigrateNoReplace:  "Migrate to a supported API version; the upstream data names no direct replacement. See the deprecation guide.",
	CodeRecComponentLimit:    "Review the documented limitation and upgrade or replace the component before upgrading Kubernetes.",
	CodeRecComponentVerify:   "Check the project's compatibility documentation for this component and version against the target Kubernetes version.",
	CodeRecComponentUnknown:  "Check manually whether this component supports the target Kubernetes version.",
	CodeRecComponentUnhealth: "Bring the component to a healthy state before upgrading.",
	CodeRecStepwise:          "Upgrade the control plane one minor version at a time.",
	CodeRecSupportedVersion:  "Plan the move to a Kubernetes version that upstream still supports.",
	CodeRecRerun:             "Repeat the check when the source is available, or check this area manually.",
	CodeRecReadManually:      "Check this area manually.",

	CodeComponentConfirmed:       "{name} {version} is documented by the project as compatible with Kubernetes {target}.",
	CodeComponentKnownLimitation: "The project documents a limitation for {name} {version} with Kubernetes {target}.",
	CodeComponentInsufficient:    "The compatibility of {name} {version} with Kubernetes {target} could not be confirmed from the catalog: not enough data. This is not a confirmation of compatibility.",
	CodeComponentNotRecognized:   "{name} is not in the component catalog; its compatibility with Kubernetes {target} was not checked.",
	CodeComponentNotReady:        "{name} is not fully ready ({ready} of {desired} instances).",

	CodePlatformPathOK:       "The upgrade from {current} to {target} is a single minor version step.",
	CodePlatformPathSkips:    "The upgrade from {current} to {target} spans {steps} minor versions; Kubernetes supports upgrading the control plane one minor version at a time.",
	CodePlatformSameVersion:  "The target version equals the current version; only patch-level changes are possible.",
	CodePlatformUnsupported:  "Kubernetes {current} is older than the versions upstream currently supports ({window}).",
	CodeSourceResources:      "The list of served APIs could not be read; deprecated and removed API usage was not checked.",
	CodeSourceResourcesPart:  "The list of served APIs was read only partially (unavailable: {failed}).",
	CodeSourceListUnavail:    "Objects of {kinds} could not be read; their API usage was not checked.",
	CodeSourceListTruncated:  "The list of {kinds} was larger than the read limit; only the first objects were checked.",
	CodeSourceNotCovered:     "{count} API kind(s) with deprecated or removed versions cannot be read by Kubron; usage of them was not checked: {kinds}.",
	CodeSourceNodes:          "Nodes could not be read; kubelet versions were not checked.",
	CodeSourceWorkloads:      "Workloads in {namespaces} could not be read; the components there were not checked.",
	CodeSourceWorkloadsTrunc: "The list of workloads in {namespaces} was larger than the read limit; some components may be missing from the report.",

	CodeActMigrateRemoved:   "Migrate all objects that use APIs removed in the target version, and update the manifests that produce them.",
	CodeActMigrateDeprecate: "Plan the migration of objects that use deprecated APIs.",
	CodeActComponents:       "Resolve the documented component limitations before upgrading.",
	CodeActVerifyDocs:       "Confirm compatibility of the components marked \"not enough data\" in their projects' documentation.",
	CodeActUnrecognized:     "Review the components that are not in the catalog; Kubron did not check their compatibility.",
	CodeActUnhealthy:        "Fix the components that are not ready.",
	CodeActStepwise:         "Split the upgrade into single minor version steps.",
	CodeActNotChecked:       "Review the areas marked \"not checked\"; the report does not cover them.",
	CodeActReleaseNotes:     "Read the release notes of the target Kubernetes version.",
	CodeActGitOps:           "Check the manifests in Git or GitOps repositories for deprecated and removed APIs: Kubron analyzes only live cluster objects.",
	CodeActBackup:           "Make sure you can restore the cluster: take a backup using the procedure of your distribution or provider.",
	CodeActNodesReady:       "After the upgrade, check that all nodes are Ready and run the target version.",
	CodeActWorkloads:        "After the upgrade, check that system workloads and your applications are healthy.",
	CodeActRerun:            "After the upgrade, run the readiness check again and compare it with this report.",
	CodeActAddons:           "After the upgrade, check the add-ons (CNI, DNS, ingress, metrics) against their documentation.",

	LimitProviderNotChecked:     "Generic Kubernetes mode: provider-specific checks (EKS, GKE, AKS and others) are not performed, and the report is not a full check of a managed service.",
	LimitNotAGuarantee:          "The report is an assessment, not a guarantee that the upgrade will succeed.",
	LimitNoTarget:               "No target version was chosen: APIs are evaluated against the current version, so only already deprecated APIs are reported.",
	LimitLiveOnly:               "Only live cluster objects are analyzed.",
	LimitLiveManagedFields:      "API usage is detected from managedFields and the last-applied-configuration annotation of live objects; objects without them are not covered.",
	LimitLiveReadOnlyClients:    "Clients that only read an API (controllers, scripts) leave no trace in objects and are not detected.",
	LimitLiveKindsScanned:       "Only API kinds that Kubron is allowed to read were scanned (see the list of scanned and not covered kinds).",
	LimitLiveCustomResources:    "Custom resources are not scanned for API usage.",
	LimitLiveNoHelmReleases:     "Helm releases and chart templates are not analyzed.",
	LimitLiveComponents:         "Components are matched against a limited catalog by workload name and image; anything else is reported as not recognized.",
	LimitNotAnalyzed:            "This source is not analyzed in this version, so nothing was checked in it.",
	LimitStaleMatrix:            "The cluster runs a Kubernetes version newer than the API matrix knows; the matrix is out of date for this cluster.",
	LimitClusterOlderThanMatrix: "The cluster version is older than the oldest release the API matrix covers; APIs removed earlier are not listed.",

	// Пояснения вердиктов каталога.
	"version.unknown":              "The component version could not be determined from the image tag.",
	"catalog.noRules":              "The catalog has no compatibility rules for this component: its project documentation does not allow deriving them.",
	"catalog.noMatchingRule":       "The catalog has no rule that covers this component version with the target Kubernetes version.",
	"skew.within":                  "The version is within the Kubernetes version skew policy for the target version.",
	"skew.tooOld":                  "The version is older than the Kubernetes version skew policy allows for the target version.",
	"skew.newerThanAPIServer":      "The version is newer than the target kube-apiserver, which the version skew policy does not allow.",
	"skew.targetUnknown":           "The version could not be compared with the target version.",
	"metrics-server.beyond-matrix": "The project lists this Metrics Server series as supported only up to Kubernetes 1.21.",
	"ingress-nginx.retired":        "Ingress NGINX is retired by the Kubernetes project: no further releases or security fixes.",
}

// EnglishText возвращает английский текст сообщения с подставленными параметрами. Неизвестный код отдаётся как есть.
func EnglishText(m Message) string {
	tpl, ok := englishTexts[m.Code]
	if !ok {
		return m.Code
	}
	return expand(tpl, m.Params)
}

// KnownCode сообщает, что для кода есть английский текст.
func KnownCode(code string) bool { _, ok := englishTexts[code]; return ok }

// Codes возвращает все известные коды по возрастанию (для тестов и проверки переводов).
func Codes() []string {
	out := make([]string, 0, len(englishTexts))
	for c := range englishTexts {
		out = append(out, c)
	}
	slices.Sort(out)
	return out
}

func expand(tpl string, params map[string]string) string {
	if len(params) == 0 {
		return tpl
	}
	pairs := make([]string, 0, 2*len(params))
	for k, v := range params {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(tpl)
}
