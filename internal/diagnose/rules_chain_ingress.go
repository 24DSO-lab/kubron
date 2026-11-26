// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// Цепочка для цели Ingress (ADR-0031 §6, этап 2): backend-Service не найден, у backend нет готовых endpoints,
// IngressClass не найден или не указан, порт backend не объявлен у Service. Причину пустых endpoints в подах объясняют
// их собственные гипотезы: пост-хук 90 помечает их как объясняющие «ingress.backend_no_endpoints» (до пост-хука 100,
// который по этим связям строит цепочки Ingress > Service > Pod), а пост-хук 110 добавляет цепочки для находок, у
// которых подов нет (Ingress > Service, Ingress > IngressClass).
//
// Secret не читается. Имя TLS-Secret берётся из spec.tls[].secretName: отчёт даёт шаг для человека.

// SymptomIngressUnreachable — симптом «Ingress не доставляет трафик до backend».
const SymptomIngressUnreachable = "ingress.unreachable"

const ruleIngressChainID = "chain.ingress"

const (
	maxIngressFindings = 3  // сколько находок одного вида попадает в отчёт: остальные видны в самом Ingress
	maxBackendServices = 10 // как depgraph: сколько backend-Service читается и раскрывается у одного Ingress
)

func init() {
	registerChainRule(ruleIngressChainID, 1, ruleIngressChain)
	registerPostHook(90, "ingress_explains", ingressExplains)
	registerPostHook(110, "ingress_chains", ingressChains)
}

func ruleIngressChain(b *builder) {
	g := b.c.Graph
	t := b.c.Target
	if g == nil || t.Kind != "Ingress" {
		return
	}
	n, ok := g.Node(toGraphRef(t))
	if !ok || n.Placeholder || n.Object == nil || n.Health == depgraph.Unknown {
		return // цель не прочитана графом: пробел уже записан в Missing
	}
	ing, err := decode[networkingv1.Ingress](n.Object)
	if err != nil {
		return
	}
	ref := fromGraphRef(n.Ref)
	complete := !graphLimited(g) // выводы «ничего нет» верны, только если граф не ограничен пределами
	var raised []string
	raised = append(raised, ingressBackends(b, g, ref, ing, complete)...)
	raised = append(raised, ingressClass(b, g, ref, ing, complete)...)
	if len(raised) > 0 {
		ingressTLS(b, ref, ing, raised)
	}
}

func serviceRef(ns, name string) depgraph.Ref {
	return depgraph.Ref{Kind: "Service", Namespace: ns, Name: name}
}

func ingressClassRef(name string) depgraph.Ref {
	return depgraph.Ref{Group: groupNetworking, Kind: "IngressClass", Name: name}
}

// ingressBackends разбирает backend по их состоянию в графе. Возвращает идентификаторы выданных гипотез.
func ingressBackends(b *builder, g *depgraph.Graph, ref ResourceRef, ing *networkingv1.Ingress, complete bool) []string {
	var raised []string
	missingN, endpointsN := 0, 0
	seen := map[string]bool{}
	for _, be := range depgraph.IngressBackends(ing) {
		if !be.IsService() {
			ingressResourceBackend(b, ref, be)
			continue
		}
		if seen[be.Service] {
			continue
		}
		seen[be.Service] = true
		svc, ok := g.Node(serviceRef(ref.Namespace, be.Service))
		if !ok {
			continue
		}
		params := map[string]string{"ingress": ref.Name, "service": be.Service}
		switch {
		case svc.Health == depgraph.Missing:
			if missingN == maxIngressFindings {
				continue
			}
			missingN++
			b.symptom(SymptomIngressUnreachable)
			f := b.fact(ref, "ingress.backend_missing", Source{Kind: SourceResource, Resource: ref, Field: "spec.rules[].http.paths[].backend.service"}, params)
			h := b.hypothesis(ref, "ingress.backend_service_missing", ConfidenceHigh, params, []string{f}, nil)
			b.recommend(ref, "rec.create_backend_service", params, h)
			raised = append(raised, h)
		case svc.Health == depgraph.Degraded && complete && noEndpointsReason(svc.Reason):
			if endpointsN == maxIngressFindings {
				continue
			}
			endpointsN++
			b.symptom(SymptomIngressUnreachable)
			p := map[string]string{"ingress": ref.Name, "service": be.Service, "cause": svc.Reason}
			f := b.fact(ref, "ingress.backend_endpoints", Source{Kind: SourceDerived, Resource: fromGraphRef(svc.Ref), Field: "endpointslices"}, p)
			conf := ConfidenceHigh
			if svc.Reason == depgraph.ReasonNoEndpoints {
				// Pod'ы выбраны, а EndpointSlice нет: чаще контроллер не успел, поэтому уверенность низкая (как у Service).
				conf = ConfidenceLow
			}
			h := b.hypothesis(ref, "ingress.backend_no_endpoints", conf, params, []string{f}, nil)
			b.recommend(ref, "rec.check_pods_behind_service", map[string]string{"service": be.Service}, h)
			raised = append(raised, h)
		}
	}
	raised = append(raised, ingressPorts(b, g, ref, ing)...)
	return raised
}

func noEndpointsReason(r string) bool {
	return r == depgraph.ReasonNoEndpoints || r == depgraph.ReasonNoReadyEndpoints || r == depgraph.ReasonSelectorMatchesNothing
}

// ingressResourceBackend: backend типа resource граф не раскрывает (вершина Unknown/policy). Это факт, а не падение
// и не гипотеза: Kubron не знает такого вида и не делает вывода о его исправности.
func ingressResourceBackend(b *builder, ref ResourceRef, be depgraph.IngressBackend) {
	b.fact(ref, "ingress.resource_backend", Source{Kind: SourceResource, Resource: ref, Field: "spec.rules[].http.paths[].backend.resource"},
		map[string]string{"ingress": ref.Name, "kind": be.Resource.Kind, "name": be.Resource.Name})
}

// ingressPorts: порт backend не объявлен у существующего Service (имя или номер). Service без портов и ExternalName
// не проверяются: объявлять нечего.
func ingressPorts(b *builder, g *depgraph.Graph, ref ResourceRef, ing *networkingv1.Ingress) []string {
	var raised []string
	found := 0
	for _, be := range depgraph.IngressBackends(ing) {
		if !be.IsService() || (be.PortName == "" && be.PortNumber == 0) || found == maxIngressFindings {
			continue
		}
		n, ok := g.Node(serviceRef(ref.Namespace, be.Service))
		if !ok || n.Object == nil || n.Health == depgraph.Missing || n.Health == depgraph.Unknown {
			continue
		}
		svc, err := decode[corev1.Service](n.Object)
		if err != nil || svc.Spec.Type == corev1.ServiceTypeExternalName || len(svc.Spec.Ports) == 0 {
			continue
		}
		if slices.ContainsFunc(svc.Spec.Ports, func(p corev1.ServicePort) bool {
			return (be.PortName != "" && p.Name == be.PortName) || (be.PortName == "" && p.Port == be.PortNumber)
		}) {
			continue
		}
		found++
		port := be.PortName
		if port == "" {
			port = strconv.Itoa(int(be.PortNumber))
		}
		params := map[string]string{"ingress": ref.Name, "service": be.Service, "port": port}
		b.symptom(SymptomIngressUnreachable)
		f := b.fact(ref, "ingress.backend_port_missing", Source{Kind: SourceResource, Resource: ref, Field: "spec.rules[].http.paths[].backend.service.port"}, params)
		h := b.hypothesis(ref, "ingress.backend_port_mismatch", ConfidenceMedium, params, []string{f}, nil)
		b.recommend(ref, "rec.fix_ingress_backend_port", params, h)
		raised = append(raised, h)
	}
	return raised
}

// ingressClass: класс назван, но не найден → высокая уверенность (из spec) или средняя (из устаревшей аннотации:
// контроллер может сопоставлять её и без объекта IngressClass). Класс не указан и класса по умолчанию нет → факт и
// гипотеза уровнем ниже. Если IngressClass не удалось прочитать, вывода «класса нет» нет.
func ingressClass(b *builder, g *depgraph.Graph, ref ResourceRef, ing *networkingv1.Ingress, complete bool) []string {
	name, fromSpec := depgraph.IngressClassOf(ing)
	if name != "" {
		class, ok := g.Node(ingressClassRef(name))
		if !ok || class.Health != depgraph.Missing {
			return nil
		}
		conf := ConfidenceHigh
		if !fromSpec {
			conf = ConfidenceMedium
		}
		params := map[string]string{"ingress": ref.Name, "class": name}
		b.symptom(SymptomIngressUnreachable)
		field := "spec.ingressClassName"
		if !fromSpec {
			field = "metadata.annotations"
		}
		f := b.fact(ref, "ingress.class_missing", Source{Kind: SourceResource, Resource: ref, Field: field}, params)
		h := b.hypothesis(ref, "ingress.class_not_found", conf, params, []string{f}, nil)
		b.recommend(ref, "rec.fix_ingress_class", params, h)
		return []string{h}
	}
	if !complete || len(g.EdgesFrom(toGraphRef(ref), depgraph.EdgeBinds)) > 0 || classListFailed(g) {
		return nil
	}
	params := map[string]string{"ingress": ref.Name}
	b.symptom(SymptomIngressUnreachable)
	f := b.fact(ref, "ingress.class_unspecified", Source{Kind: SourceResource, Resource: ref, Field: "spec.ingressClassName"}, params)
	h := b.hypothesis(ref, "ingress.class_not_found", ConfidenceMedium, params, []string{f}, nil)
	b.recommend(ref, "rec.set_ingress_class", params, h)
	return []string{h}
}

// classListFailed: список IngressClass не прочитан (запрет, недоступность), значит «класса по умолчанию нет» не доказано.
func classListFailed(g *depgraph.Graph) bool {
	for _, w := range g.Warnings() {
		if k, arg, _ := strings.Cut(w, ":"); arg == "IngressClass" && graphWarnKinds[k] != "" {
			return true
		}
	}
	return false
}

// ingressTLS: Secret не читается, поэтому проверка сертификата — шаг для человека, привязанный к выданным гипотезам.
func ingressTLS(b *builder, ref ResourceRef, ing *networkingv1.Ingress, hyps []string) {
	for i, secret := range depgraph.IngressTLSSecrets(ing) {
		if i == maxIngressFindings {
			return
		}
		b.step(ref, "step.check_tls_secret", map[string]string{"ingress": ref.Name, "secret": secret}, hyps...)
	}
}

// ingressHypotheses — гипотезы правила chain.ingress; правило запускается только для цели Ingress.
func ingressHypotheses(b *builder) []Hypothesis {
	var out []Hypothesis
	for _, h := range b.rep.Hypotheses {
		if h.RuleID == ruleIngressChainID {
			out = append(out, h)
		}
	}
	return out
}

// ingressExplains (пост-хук 90, до связей и цепочек хука 100): неготовый Pod за backend-Service объясняет «у backend
// нет готовых endpoints». Связь допустима, только если Pod достижим от Ingress именно через этот Service.
func ingressExplains(b *builder) {
	g := b.c.Graph
	if g == nil || b.c.Target.Kind != "Ingress" {
		return
	}
	ingRef := toGraphRef(b.c.Target)
	for _, eff := range ingressHypotheses(b) {
		if eff.Code != "ingress.backend_no_endpoints" {
			continue
		}
		for i := range b.rep.Hypotheses {
			h := &b.rep.Hypotheses[i]
			if h.Resource.Kind != "Pod" || isChainRule(h.RuleID) || slices.Contains(h.Explains, eff.ID) {
				continue
			}
			path := dependencyPath(g, ingRef, toGraphRef(h.Resource))
			if len(path) >= 3 && path[1].Kind == "Service" && path[1].Name == eff.Params["service"] {
				h.Explains = append(h.Explains, eff.ID)
			}
		}
	}
}

// ingressChains (пост-хук 110, после хука 100): цепочки для находок Ingress, у которых нет объясняющих подов. Звено
// цели — Service или IngressClass, продлённый до сломанной зависимости; общий предел maxChains сохраняется.
func ingressChains(b *builder) {
	g := b.c.Graph
	if g == nil || b.c.Target.Kind != "Ingress" {
		return
	}
	ingRef := toGraphRef(b.c.Target)
	for _, h := range ingressHypotheses(b) {
		if len(b.rep.Chains) >= maxChains {
			return
		}
		// Гипотезу, которую объясняют поды, показывает цепочка хука 100 (Ingress > Service > Pod): вторая такая же лишняя.
		if slices.ContainsFunc(b.rep.Hypotheses, func(x Hypothesis) bool { return slices.Contains(x.Explains, h.ID) }) {
			continue
		}
		var to depgraph.Ref
		switch {
		case h.Params["service"] != "":
			to = serviceRef(b.c.Target.Namespace, h.Params["service"])
		case h.Params["class"] != "":
			to = ingressClassRef(h.Params["class"])
		default:
			continue
		}
		if path := dependencyPath(g, ingRef, to); len(path) >= 2 {
			b.rep.Chains = append(b.rep.Chains, Chain{Path: chainSteps(extendToBroken(g, path)), Candidate: h.ID})
		}
	}
}
