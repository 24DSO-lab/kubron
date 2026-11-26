// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package drift

import (
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/baseline"
)

// RulesVersion — поколение правил нормализации. Пишется в каждую версию baseline и в каждый результат проверки.
// Сравнение версии baseline с её поколением правил, отличным от текущего, молча не выполняется (ADR-0027 §3):
// ресурсы получают «не проверено» с причиной baseline.ReasonRulesMismatch. Любое изменение таблиц ниже, способное
// изменить нормализованное представление объекта, обязано увеличить эту константу.
const RulesVersion = 1

// kindSpec — поддерживаемый тип ресурса и его правила.
type kindSpec struct {
	GVK        schema.GroupVersionKind
	Namespaced bool
	rules      *kindRules
	// top — поля верхнего уровня, участвующие в сравнении, кроме metadata.labels и metadata.annotations.
	top []string
}

// Типы MVP (ADR-0027 §3). Secret сюда не входит и не будет прочитан.
var supportedKinds = []kindSpec{
	{GVK: gvk("apps", "v1", "Deployment"), Namespaced: true, rules: deploymentRules, top: []string{"spec"}},
	{GVK: gvk("apps", "v1", "StatefulSet"), Namespaced: true, rules: statefulSetRules, top: []string{"spec"}},
	{GVK: gvk("apps", "v1", "DaemonSet"), Namespaced: true, rules: daemonSetRules, top: []string{"spec"}},
	{GVK: gvk("", "v1", "Service"), Namespaced: true, rules: serviceRules, top: []string{"spec"}},
	{GVK: gvk("networking.k8s.io", "v1", "Ingress"), Namespaced: true, rules: ingressRules, top: []string{"spec"}},
	{GVK: gvk("networking.k8s.io", "v1", "NetworkPolicy"), Namespaced: true, rules: networkPolicyRules, top: []string{"spec"}},
	{GVK: gvk("", "v1", "ConfigMap"), Namespaced: true, rules: configMapRules, top: []string{"data", "binaryData", "immutable"}},
}

func gvk(g, v, k string) schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: g, Version: v, Kind: k}
}

// kindFor возвращает описание типа по группе и виду (версия API не важна: правила одинаковы для всех версий типа).
func kindFor(group, kind string) (*kindSpec, bool) {
	for i := range supportedKinds {
		if supportedKinds[i].GVK.Group == group && supportedKinds[i].GVK.Kind == kind {
			return &supportedKinds[i], true
		}
	}
	return nil, false
}

// SupportedKinds возвращает названия типов, которые можно включить в baseline.
func SupportedKinds() []string {
	out := make([]string, len(supportedKinds))
	for i, k := range supportedKinds {
		out[i] = k.GVK.Kind
	}
	return out
}

// kindRules — правила нормализации одного типа. Ключи — «схемные» пути: поля через «.», элементы списков как «[]».
type kindRules struct {
	// exclude — пути, которые не сравниваются (назначаются кластером или контроллерами).
	exclude set
	// annotations — аннотации контроллеров, которые не сравниваются: точное имя или префикс с «*» на конце.
	annotations []string
	// keyed — списки объектов с ключом (merge key): элементы упорядочиваются по ключу и адресуются в пути по ключу.
	// Ключ может быть вложенным путём («metadata.name»).
	keyed map[string][]string
	// ordered — списки, в которых порядок значим (команды, init-контейнеры): порядок сохраняется. У init-контейнеров
	// и ключ есть, и порядок значим: изменение порядка видно как отличие «@order».
	ordered set
	// sets — списки без ключа, где порядок не значим: упорядочиваются по каноническому JSON.
	sets set
	// defaults — значения по умолчанию, назначаемые сервером: поле с таким значением не сравнивается, чтобы обновление
	// кластера, добавляющее новое defaulted-поле, не превращалось в «дрейф» всей нагрузки.
	defaults map[string]string
}

type set map[string]struct{}

func newSet(items ...string) set {
	s := make(set, len(items))
	for _, i := range items {
		s[i] = struct{}{}
	}
	return s
}

func (s set) has(k string) bool { _, ok := s[k]; return ok }

func mergeSets(parts ...set) set {
	out := set{}
	for _, p := range parts {
		for k := range p {
			out[k] = struct{}{}
		}
	}
	return out
}

func mergeMaps[V any](parts ...map[string]V) map[string]V {
	out := map[string]V{}
	for _, p := range parts {
		for k, v := range p {
			out[k] = v
		}
	}
	return out
}

// controllerAnnotations — аннотации, которые ставят контроллеры и сам сервер: они меняются без участия человека.
var controllerAnnotations = []string{
	"kubectl.kubernetes.io/last-applied-configuration",
	"deployment.kubernetes.io/revision",
	"deployment.kubernetes.io/desired-replicas",
	"deployment.kubernetes.io/max-replicas",
	"deprecated.daemonset.template.generation",
	"autoscaling.alpha.kubernetes.io/*",
}

// podTemplate возвращает правила шаблона пода под префиксом prefix («spec.template.spec»).
func podTemplate(prefix string) (keyed map[string][]string, ordered, sets set, defaults map[string]string, exclude set) {
	keyed = map[string][]string{}
	defaults = map[string]string{}
	ordered = newSet()
	sets = newSet()
	exclude = newSet(prefix + ".serviceAccount") // устаревший двойник serviceAccountName

	keyed[prefix+".volumes[]"] = []string{"name"}
	keyed[prefix+".imagePullSecrets[]"] = []string{"name"}
	keyed[prefix+".hostAliases[]"] = []string{"ip"}
	keyed[prefix+".securityContext.sysctls[]"] = []string{"name"}
	keyed[prefix+".volumes[].configMap.items[]"] = []string{"key"}
	keyed[prefix+".volumes[].secret.items[]"] = []string{"key"}
	sets[prefix+".tolerations[]"] = struct{}{}
	sets[prefix+".topologySpreadConstraints[]"] = struct{}{}
	sets[prefix+".securityContext.supplementalGroups[]"] = struct{}{}
	for _, aff := range []string{"nodeAffinity", "podAffinity", "podAntiAffinity"} {
		sets[prefix+".affinity."+aff+".requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[]"] = struct{}{}
		sets[prefix+".affinity."+aff+".requiredDuringSchedulingIgnoredDuringExecution[]"] = struct{}{}
		sets[prefix+".affinity."+aff+".preferredDuringSchedulingIgnoredDuringExecution[]"] = struct{}{}
	}
	sets[prefix+".affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[].matchExpressions[]"] = struct{}{}

	defaults[prefix+".dnsPolicy"] = `"ClusterFirst"`
	defaults[prefix+".restartPolicy"] = `"Always"`
	defaults[prefix+".schedulerName"] = `"default-scheduler"`
	defaults[prefix+".terminationGracePeriodSeconds"] = `30`
	defaults[prefix+".enableServiceLinks"] = `true`
	for _, src := range []string{"configMap", "secret", "downwardAPI", "projected"} {
		defaults[prefix+".volumes[]."+src+".defaultMode"] = `420`
	}

	for _, c := range []string{"containers", "initContainers"} {
		cp := prefix + "." + c + "[]"
		keyed[prefix+"."+c+"[]"] = []string{"name"}
		keyed[cp+".env[]"] = []string{"name"}
		keyed[cp+".ports[]"] = []string{"containerPort", "protocol"}
		keyed[cp+".volumeMounts[]"] = []string{"mountPath"}
		keyed[cp+".volumeDevices[]"] = []string{"devicePath"}
		sets[cp+".envFrom[]"] = struct{}{}
		ordered[cp+".command[]"] = struct{}{}
		ordered[cp+".args[]"] = struct{}{}
		defaults[cp+".terminationMessagePath"] = `"/dev/termination-log"`
		defaults[cp+".terminationMessagePolicy"] = `"File"`
		defaults[cp+".ports[].protocol"] = `"TCP"`
		defaults[cp+".env[].valueFrom.fieldRef.apiVersion"] = `"v1"`
		for _, probe := range []string{"livenessProbe", "readinessProbe", "startupProbe"} {
			defaults[cp+"."+probe+".timeoutSeconds"] = `1`
			defaults[cp+"."+probe+".periodSeconds"] = `10`
			defaults[cp+"."+probe+".successThreshold"] = `1`
			defaults[cp+"."+probe+".failureThreshold"] = `3`
			defaults[cp+"."+probe+".httpGet.scheme"] = `"HTTP"`
		}
	}
	// init-контейнеры выполняются по порядку: порядок значим даже при адресации по имени.
	ordered[prefix+".initContainers[]"] = struct{}{}
	return keyed, ordered, sets, defaults, exclude
}

// workload собирает правила типа с шаблоном пода; extra дополняет их правилами самого типа.
func workload(extra func(r *kindRules)) *kindRules {
	const pod = "spec.template.spec"
	keyed, ordered, sets, defaults, exclude := podTemplate(pod)
	r := &kindRules{
		exclude:     exclude,
		annotations: controllerAnnotations,
		keyed:       keyed,
		ordered:     ordered,
		sets:        sets,
		defaults:    defaults,
	}
	// Метки и аннотации шаблона пода не исключаются: их изменение запускает перекатку, это значимое изменение.
	extra(r)
	return r
}

var deploymentRules = workload(func(r *kindRules) {
	r.defaults = mergeMaps(r.defaults, map[string]string{
		"spec.progressDeadlineSeconds":               `600`,
		"spec.revisionHistoryLimit":                  `10`,
		"spec.strategy.rollingUpdate.maxSurge":       `"25%"`,
		"spec.strategy.rollingUpdate.maxUnavailable": `"25%"`,
	})
})

var statefulSetRules = workload(func(r *kindRules) {
	r.defaults = mergeMaps(r.defaults, map[string]string{
		"spec.podManagementPolicy":                              `"OrderedReady"`,
		"spec.revisionHistoryLimit":                             `10`,
		"spec.updateStrategy.type":                              `"RollingUpdate"`,
		"spec.updateStrategy.rollingUpdate.partition":           `0`,
		"spec.persistentVolumeClaimRetentionPolicy.whenDeleted": `"Retain"`,
		"spec.persistentVolumeClaimRetentionPolicy.whenScaled":  `"Retain"`,
	})
	r.keyed["spec.volumeClaimTemplates[]"] = []string{"metadata.name"}
	r.exclude = mergeSets(r.exclude, newSet("spec.volumeClaimTemplates[].status"))
	r.defaults["spec.volumeClaimTemplates[].spec.volumeMode"] = `"Filesystem"`
})

var daemonSetRules = workload(func(r *kindRules) {
	r.defaults = mergeMaps(r.defaults, map[string]string{
		"spec.revisionHistoryLimit":                        `10`,
		"spec.updateStrategy.type":                         `"RollingUpdate"`,
		"spec.updateStrategy.rollingUpdate.maxUnavailable": `1`,
		"spec.updateStrategy.rollingUpdate.maxSurge":       `0`,
	})
})

var serviceRules = &kindRules{
	// Адреса и порты назначает кластер: их смена без правки Service — не дрейф конфигурации.
	exclude: newSet("spec.clusterIP", "spec.clusterIPs", "spec.ipFamilies", "spec.healthCheckNodePort",
		"spec.ports[].nodePort"),
	annotations: append([]string{"cloud.google.com/neg-status"}, controllerAnnotations...),
	keyed:       map[string][]string{"spec.ports[]": {"port", "protocol"}},
	sets:        newSet("spec.externalIPs[]", "spec.loadBalancerSourceRanges[]"),
	defaults: map[string]string{
		"spec.type":                  `"ClusterIP"`,
		"spec.sessionAffinity":       `"None"`,
		"spec.internalTrafficPolicy": `"Cluster"`,
		"spec.ipFamilyPolicy":        `"SingleStack"`,
		"spec.ports[].protocol":      `"TCP"`,
	},
}

var ingressRules = &kindRules{
	annotations: append([]string{
		"ingress.kubernetes.io/backends", "ingress.kubernetes.io/forwarding-rule",
		"ingress.kubernetes.io/https-forwarding-rule", "ingress.kubernetes.io/https-target-proxy",
		"ingress.kubernetes.io/target-proxy", "ingress.kubernetes.io/url-map", "ingress.kubernetes.io/ssl-cert",
		"ingress.kubernetes.io/static-ip", "ingress.kubernetes.io/redirect-url-map",
		"ingress.kubernetes.io/redirect-forwarding-rule", "ingress.kubernetes.io/redirect-target-proxy",
	}, controllerAnnotations...),
	keyed: map[string][]string{
		"spec.rules[]":              {"host"},
		"spec.rules[].http.paths[]": {"path", "pathType"},
	},
	sets: newSet("spec.tls[]", "spec.tls[].hosts[]"),
}

var networkPolicyRules = &kindRules{
	annotations: controllerAnnotations,
	sets: newSet(
		"spec.policyTypes[]", "spec.ingress[]", "spec.egress[]",
		"spec.ingress[].from[]", "spec.ingress[].ports[]", "spec.egress[].to[]", "spec.egress[].ports[]",
		"spec.podSelector.matchExpressions[]", "spec.podSelector.matchExpressions[].values[]",
		"spec.ingress[].from[].podSelector.matchExpressions[]", "spec.ingress[].from[].podSelector.matchExpressions[].values[]",
		"spec.ingress[].from[].namespaceSelector.matchExpressions[]", "spec.ingress[].from[].namespaceSelector.matchExpressions[].values[]",
		"spec.egress[].to[].podSelector.matchExpressions[]", "spec.egress[].to[].podSelector.matchExpressions[].values[]",
		"spec.egress[].to[].namespaceSelector.matchExpressions[]", "spec.egress[].to[].namespaceSelector.matchExpressions[].values[]",
		"spec.ingress[].from[].ipBlock.except[]", "spec.egress[].to[].ipBlock.except[]",
	),
	defaults: map[string]string{
		"spec.ingress[].ports[].protocol": `"TCP"`,
		"spec.egress[].ports[].protocol":  `"TCP"`,
	},
}

var configMapRules = &kindRules{annotations: controllerAnnotations}

// annotationExcluded сообщает, что аннотация ставится контроллером и не сравнивается.
func (r *kindRules) annotationExcluded(key string) bool {
	for _, a := range r.annotations {
		if p, ok := strings.CutSuffix(a, "*"); ok {
			if strings.HasPrefix(key, p) {
				return true
			}
		} else if key == a {
			return true
		}
	}
	return false
}

// keepEmpty — имена полей, у которых пустой объект значим (emptyDir: {} выбирает тип тома, podSelector: {} — все поды).
var keepEmpty = newSet("emptyDir", "podSelector", "namespaceSelector", "ipBlock", "selector", "downwardAPI")

// --- приоритеты ------------------------------------------------------------------------------------------------

type prio struct {
	prefix string
	p      baseline.Priority
}

// containerFields — поля контейнеров, у которых приоритет выше обычного.
const (
	podPath = "spec.template.spec"
)

// priorityRules — приоритет изменения по правилам типа (ADR-0027 §5). Побеждает самый длинный совпавший префикс.
// Не совпавшее под metadata — низкий приоритет, остальное — средний. AI не используется.
var priorityRules = map[string][]prio{
	"workload": {
		{podPath + ".containers[].image", baseline.PriorityHigh},
		{podPath + ".initContainers[].image", baseline.PriorityHigh},
		{podPath + ".containers[].resources", baseline.PriorityHigh},
		{podPath + ".initContainers[].resources", baseline.PriorityHigh},
		{podPath + ".containers[].securityContext", baseline.PriorityHigh},
		{podPath + ".initContainers[].securityContext", baseline.PriorityHigh},
		{podPath + ".securityContext", baseline.PriorityHigh},
		{podPath + ".hostNetwork", baseline.PriorityHigh},
		{podPath + ".hostPID", baseline.PriorityHigh},
		{podPath + ".hostIPC", baseline.PriorityHigh},
		{podPath + ".serviceAccountName", baseline.PriorityHigh},
		{podPath + ".containers[].command", baseline.PriorityHigh},
		{podPath + ".containers[].args", baseline.PriorityHigh},
		{"spec.template.metadata", baseline.PriorityLow},
		{"spec.selector", baseline.PriorityHigh},
	},
	"Service": {
		{"spec.type", baseline.PriorityHigh},
		{"spec.ports", baseline.PriorityHigh},
		{"spec.selector", baseline.PriorityHigh},
		{"spec.externalIPs", baseline.PriorityHigh},
		{"spec.loadBalancerSourceRanges", baseline.PriorityHigh},
		{"spec.externalTrafficPolicy", baseline.PriorityMedium},
	},
	"Ingress":       {{"spec.rules", baseline.PriorityHigh}, {"spec.tls", baseline.PriorityHigh}, {"spec.defaultBackend", baseline.PriorityHigh}},
	"NetworkPolicy": {{"spec", baseline.PriorityHigh}},
	"ConfigMap":     {{"data", baseline.PriorityMedium}, {"binaryData", baseline.PriorityMedium}},
}

// priorityFor возвращает приоритет изменения пути path у ресурса типа kind.
func priorityFor(kind, path string) baseline.Priority {
	group := kind
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet":
		group = "workload"
	}
	sp := schemaOf(path)
	best, bestLen := baseline.Priority(""), -1
	for _, r := range priorityRules[group] {
		if matchesPrefix(sp, r.prefix) && len(r.prefix) > bestLen {
			best, bestLen = r.p, len(r.prefix)
		}
	}
	switch {
	case bestLen >= 0:
		return best
	case sp == "metadata" || strings.HasPrefix(sp, "metadata."):
		return baseline.PriorityLow
	default:
		return baseline.PriorityMedium
	}
}

// matchesPrefix проверяет префикс схемного пути по границе сегмента.
func matchesPrefix(path, prefix string) bool {
	if path == prefix {
		return true
	}
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	switch path[len(prefix)] {
	case '.', '[':
		return true
	}
	return false
}

// isSensitiveSchema сообщает, что значение листа по схемному пути хранится только как keyed hash (ADR-0027 §4):
// литеральное значение переменной окружения и данные ConfigMap.
func isSensitiveSchema(kind, sp string) bool {
	switch kind {
	case "ConfigMap":
		return strings.HasPrefix(sp, "data.") || strings.HasPrefix(sp, "binaryData.")
	case "Deployment", "StatefulSet", "DaemonSet":
		return strings.HasSuffix(sp, ".env[].value") &&
			(strings.HasPrefix(sp, podPath+".containers[]") || strings.HasPrefix(sp, podPath+".initContainers[]"))
	}
	return false
}
