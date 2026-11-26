// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"slices"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// Цепочка для цели Service (ADR-0031 §6, этап 1): селектор не совпал ни с одним Pod, выбранные Pod'ы не Ready, у
// Service нет EndpointSlice, порт Service не совпал с портом контейнера. Причину пустых endpoints в самих Pod'ах
// объясняют их собственные гипотезы (правила подов): пост-хук связей (chains.go) помечает их как объясняющие
// «service.no_ready_endpoints», и именно они становятся кандидатами первопричины.

// SymptomServiceNoEndpoints — симптом «у Service нет рабочих endpoints».
const SymptomServiceNoEndpoints = "service.no_endpoints"

const ruleServiceChainID = "service.chain"

func init() { registerChainRule(ruleServiceChainID, 1, ruleServiceChain) }

// maxPortFindings — сколько несовпавших портов попадает в отчёт: остальные видны в самом Service.
const maxPortFindings = 3

func ruleServiceChain(b *builder) {
	g := b.c.Graph
	t := b.c.Target
	if g == nil || t.Kind != "Service" {
		return
	}
	n, ok := g.Node(toGraphRef(t))
	if !ok || n.Placeholder || n.Object == nil || n.Health == depgraph.Unknown {
		return // цель не прочитана графом: пробел уже записан в Missing
	}
	svc, err := decode[corev1.Service](n.Object)
	if err != nil {
		return
	}
	ref := fromGraphRef(n.Ref)
	// Выводы «ничего нет» (селектор не совпал, нет endpoints) верны, только если граф не ограничен пределами: иначе
	// отсутствие объекта в графе ничего не доказывает (см. graphLimited).
	complete := !graphLimited(g)
	switch {
	case !complete:
	case n.Reason == depgraph.ReasonSelectorMatchesNothing:
		selectorNoMatch(b, ref, svc)
	case n.Reason == depgraph.ReasonNoReadyEndpoints:
		noReadyEndpoints(b, g, ref)
	case n.Reason == depgraph.ReasonNoEndpoints:
		noEndpoints(b, ref)
	}
	if n.Health != depgraph.Healthy {
		portMismatch(b, ref, svc)
	}
}

func selectorLabels(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ","
		}
		out += k + "=" + m[k]
	}
	return out
}

func selectorNoMatch(b *builder, ref ResourceRef, svc *corev1.Service) {
	b.symptom(SymptomServiceNoEndpoints)
	sel := selectorLabels(svc.Spec.Selector)
	f := b.fact(ref, "service.selector", Source{Kind: SourceResource, Resource: ref, Field: "spec.selector"},
		map[string]string{"service": ref.Name, "selector": sel})
	h := b.hypothesis(ref, "service.selector_no_match", ConfidenceHigh, map[string]string{"service": ref.Name}, []string{f}, nil)
	b.recommend(ref, "rec.fix_service_selector", map[string]string{"service": ref.Name}, h)
}

// readyPods считает Pod'ы контекста (для Service это выбранные по селектору) и среди них готовые.
func readyPods(c *Context) (total, ready int) {
	for _, p := range c.Pods {
		total++
		if podReady(p) {
			ready++
		}
	}
	return total, ready
}

// endpointCounts считает адреса в EndpointSlice Service: всего и готовых. Пустое условие ready по смыслу API
// означает «готов», поэтому считается готовым.
func endpointCounts(g *depgraph.Graph, svc depgraph.Ref) (total, ready int) {
	for _, e := range g.EdgesFrom(svc, depgraph.EdgeBackedBy) {
		n, ok := g.Node(e.To)
		if !ok || n.Object == nil {
			continue
		}
		slice, err := decode[discoveryv1.EndpointSlice](n.Object)
		if err != nil {
			continue
		}
		for _, ep := range slice.Endpoints {
			total++
			if ep.Conditions.Ready == nil || *ep.Conditions.Ready {
				ready++
			}
		}
	}
	return total, ready
}

func noReadyEndpoints(b *builder, g *depgraph.Graph, ref ResourceRef) {
	b.symptom(SymptomServiceNoEndpoints)
	total, ready := endpointCounts(g, toGraphRef(ref))
	f := b.fact(ref, "service.endpoints_not_ready", Source{Kind: SourceDerived, Resource: ref, Field: "endpointslices[].endpoints[].conditions.ready"},
		map[string]string{"service": ref.Name, "total": strconv.Itoa(total), "ready": strconv.Itoa(ready)})
	// Пустые готовые endpoints наблюдаются в EndpointSlice; но если выбранные Pod'ы при этом готовы, картина
	// противоречива (обычно это задержка контроллера), и уверенность ниже.
	conf := ConfidenceHigh
	if _, podsReady := readyPods(b.c); podsReady > 0 {
		conf = ConfidenceMedium
	}
	h := b.hypothesis(ref, "service.no_ready_endpoints", conf, map[string]string{"service": ref.Name}, []string{f}, nil)
	b.recommend(ref, "rec.check_pods_behind_service", map[string]string{"service": ref.Name}, h)
}

func noEndpoints(b *builder, ref ResourceRef) {
	b.symptom(SymptomServiceNoEndpoints)
	f := b.fact(ref, "service.no_endpoint_slices", Source{Kind: SourceDerived, Resource: ref, Field: "endpointslices"},
		map[string]string{"service": ref.Name})
	// Pod'ы выбраны, а EndpointSlice нет: чаще всего контроллер ещё не успел, поэтому уверенность низкая.
	h := b.hypothesis(ref, "service.no_endpoints", ConfidenceLow, map[string]string{"service": ref.Name}, []string{f}, nil)
	b.recommend(ref, "rec.check_pods_behind_service", map[string]string{"service": ref.Name}, h)
}

// containerPorts — порты, объявленные контейнерами выбранных Pod'ов: имена и номера.
func containerPorts(pods []*corev1.Pod) (names map[string]bool, numbers map[int32]bool) {
	names, numbers = map[string]bool{}, map[int32]bool{}
	for _, p := range pods {
		for _, c := range p.Spec.Containers {
			for _, cp := range c.Ports {
				numbers[cp.ContainerPort] = true
				if cp.Name != "" {
					names[cp.Name] = true
				}
			}
		}
	}
	return names, numbers
}

// portMismatch: targetPort Service не найден среди портов выбранных Pod'ов. Именованный порт, которого нет ни у
// одного контейнера, — серьёзнее (имя не разрешится); числовой порт при объявленных других — слабее: объявлять
// порты контейнера необязательно, поэтому уверенность низкая. Если портов никто не объявил, сказать нечего.
func portMismatch(b *builder, ref ResourceRef, svc *corev1.Service) {
	if len(b.c.Pods) == 0 {
		return
	}
	names, numbers := containerPorts(b.c.Pods)
	found := 0
	for _, p := range svc.Spec.Ports {
		if found == maxPortFindings {
			return
		}
		target := p.TargetPort
		if target.Type == intstr.Int && target.IntVal == 0 {
			target = intstr.FromInt32(p.Port) // targetPort по умолчанию равен port
		}
		var conf Confidence
		switch {
		case target.Type == intstr.String && !names[target.StrVal]:
			conf = ConfidenceMedium
		case target.Type == intstr.Int && len(numbers) > 0 && !numbers[target.IntVal]:
			conf = ConfidenceLow
		default:
			continue
		}
		found++
		b.symptom(SymptomServiceNoEndpoints)
		params := map[string]string{"service": ref.Name, "port": strconv.Itoa(int(p.Port)), "target": target.String()}
		f := b.fact(ref, "service.port_mismatch", Source{Kind: SourceResource, Resource: ref, Field: "spec.ports[].targetPort"}, params)
		h := b.hypothesis(ref, "service.target_port_mismatch", conf, params, []string{f}, nil)
		b.recommend(ref, "rec.fix_service_port", params, h)
	}
}
