// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Ingress и IngressClass (ADR-0031 §6, этап 2). Ingress — вид графа и цель расследования: от него идут рёбра routes к
// Service из backend правил и defaultBackend и ребро binds к IngressClass. Service-backend раскрывается дальше
// стандартным расширением Service (EndpointSlice и Pod'ы), поэтому цепочка Ingress → Service → Pod строится без
// отдельного кода для подов. Хосты, пути и аннотации в граф не попадают: вершина хранит только идентификатор объекта,
// состояние и код причины (ADR-0031 §7). Secret из spec.tls не читается и вершиной не становится.

var (
	specIngress      = kindSpec{schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"}, true}
	specIngressClass = kindSpec{schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "IngressClass"}, false}
)

// Коды причин состояния Ingress (Node.Reason). Состояние выводится по рёбрам, а не по тексту объекта: Ingress сам не
// знает, существует ли его backend.
const (
	ReasonIngressBackendMissing     = "ingress_backend_missing"      // Service из backend не найден
	ReasonIngressClassMissing       = "ingress_class_missing"        // IngressClass из spec или аннотации не найден
	ReasonIngressBackendPortMissing = "ingress_backend_port_missing" // порт backend не объявлен у Service
)

// Решения реализации, не меняющие пределы графа ADR-0031 §3.
const (
	maxIngressBackends = 10               // сколько разных backend (Service и resource) раскрывается у одного Ingress
	warnBackends       = "limit.backends" // backend больше предела: остальные в граф не попали

	annIngressClass        = "kubernetes.io/ingress.class"                 // устаревший способ указать класс
	annDefaultIngressClass = "ingressclass.kubernetes.io/is-default-class" // IngressClass по умолчанию
)

func init() {
	registerKind(kindExt{Spec: specIngress, Seedable: true})
	registerKind(kindExt{Spec: specIngressClass})
	registerExpander("Ingress", expandIngress)
}

// IngressBackend — одна цель маршрутизации Ingress: Service с портом либо объект произвольного вида (resource).
type IngressBackend struct {
	Service    string // имя Service; пусто у backend типа resource
	PortName   string // порт Service по имени; пусто, если порт задан номером
	PortNumber int32  // порт Service по номеру; 0, если порт задан именем
	Resource   Ref    // у backend типа resource: Group, Kind и Name (Namespace заполняет вызывающий)
}

// IsService сообщает, что backend — Service (а не resource).
func (b IngressBackend) IsService() bool { return b.Service != "" }

func (b IngressBackend) key() string {
	if b.IsService() {
		return "S|" + b.Service + "|" + b.PortName + "|" + strconv.Itoa(int(b.PortNumber))
	}
	return "R|" + b.Resource.Group + "|" + b.Resource.Kind + "|" + b.Resource.Name
}

// IngressBackends возвращает backend Ingress: defaultBackend и backend всех путей всех правил. Повторы схлопнуты,
// порядок детерминирован (Service по имени и порту, затем resource). Пути и хосты не возвращаются.
func IngressBackends(ing *networkingv1.Ingress) []IngressBackend {
	if ing == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []IngressBackend
	add := func(be *networkingv1.IngressBackend) {
		if be == nil {
			return
		}
		var x IngressBackend
		switch {
		case be.Service != nil && be.Service.Name != "":
			x = IngressBackend{Service: be.Service.Name, PortName: be.Service.Port.Name, PortNumber: be.Service.Port.Number}
		case be.Resource != nil && be.Resource.Kind != "" && be.Resource.Name != "":
			group := ""
			if be.Resource.APIGroup != nil {
				group = *be.Resource.APIGroup
			}
			x = IngressBackend{Resource: Ref{Group: group, Kind: be.Resource.Kind, Name: be.Resource.Name}}
		default:
			return
		}
		if k := x.key(); !seen[k] {
			seen[k] = true
			out = append(out, x)
		}
	}
	add(ing.Spec.DefaultBackend)
	for _, r := range ing.Spec.Rules {
		if r.HTTP == nil {
			continue
		}
		for _, p := range r.HTTP.Paths {
			be := p.Backend
			add(&be)
		}
	}
	slices.SortFunc(out, func(a, b IngressBackend) int { return cmp.Compare(a.key(), b.key()) })
	return out
}

// IngressClassOf возвращает имя класса Ingress: spec.ingressClassName, затем устаревшая аннотация
// kubernetes.io/ingress.class. fromSpec — имя взято из spec (надёжный источник; аннотацию контроллер может
// сопоставлять и без объекта IngressClass).
func IngressClassOf(ing *networkingv1.Ingress) (name string, fromSpec bool) {
	if ing == nil {
		return "", false
	}
	if ing.Spec.IngressClassName != nil && *ing.Spec.IngressClassName != "" {
		return *ing.Spec.IngressClassName, true
	}
	return strings.TrimSpace(ing.Annotations[annIngressClass]), false
}

// IngressTLSSecrets возвращает имена Secret из spec.tls[].secretName: отсортированные, без повторов и пустых.
// Только имена: сам Secret не читается нигде.
func IngressTLSSecrets(ing *networkingv1.Ingress) []string {
	if ing == nil {
		return nil
	}
	set := map[string]bool{}
	for _, t := range ing.Spec.TLS {
		if t.SecretName != "" {
			set[t.SecretName] = true
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// ingressFindings — что расширение узнало об Ingress; по ним ставится состояние вершины.
type ingressFindings struct{ backendMissing, classMissing, portMissing bool }

// expandIngress: Ingress → Service (routes) и Ingress → IngressClass (binds). Состояние Ingress — Degraded, если
// backend или класс отсутствуют (приоритет: backend, класс, порт). Backend типа resource — вершина Unknown/policy без
// чтения: граф не знает такого вида, и это не падение и не «нет объекта».
func expandIngress(ctx context.Context, b *builder, it item, n Node) {
	var ing networkingv1.Ingress
	if !decode(n.Object, &ing) {
		return
	}
	d := it.depth + 1
	if !b.reach(d) {
		return
	}
	var f ingressFindings
	f.classMissing = b.expandIngressClass(ctx, n, &ing)
	b.expandIngressBackends(ctx, it, n, &ing, &f)
	switch {
	case f.backendMissing:
		b.setHealth(n.Ref, Degraded, ReasonIngressBackendMissing)
	case f.classMissing:
		b.setHealth(n.Ref, Degraded, ReasonIngressClassMissing)
	case f.portMissing:
		b.setHealth(n.Ref, Degraded, ReasonIngressBackendPortMissing)
	}
}

// expandIngressClass связывает Ingress с классом. Возвращает true, если названный класс не существует. Класс не
// указан — ищется класс по умолчанию (один List кластера); если его нет, вершины и ребра нет (это факт отчёта, а не
// отсутствующий объект).
func (b *builder) expandIngressClass(ctx context.Context, n Node, ing *networkingv1.Ingress) bool {
	if name, _ := IngressClassOf(ing); name != "" {
		class, _ := b.get(ctx, specIngressClass, "", name)
		b.edge(n.Ref, class.Ref, EdgeBinds)
		return class.Placeholder || class.Health == Missing
	}
	res, err := b.list(ctx, specIngressClass, ListOptions{})
	if err != nil {
		return false
	}
	for _, c := range sortedItems(res.Items) {
		if c.GetAnnotations()[annDefaultIngressClass] != "true" {
			continue
		}
		if cn, ok := b.addObject(specIngressClass, &c); ok {
			b.edge(n.Ref, cn.Ref, EdgeBinds)
			b.g.edges[edgeKey{n.Key(), cn.Key(), EdgeBinds}] = Edge{From: n.Ref, To: cn.Ref, Type: EdgeBinds, State: EdgeOK, Reason: "default_class"}
		}
		break
	}
	return false
}

func (b *builder) expandIngressBackends(ctx context.Context, it item, n Node, ing *networkingv1.Ingress, f *ingressFindings) {
	all := IngressBackends(ing)
	// Одному Service может соответствовать несколько портов: вершина и расширение одни, порты проверяются вместе.
	ports := map[string][]IngressBackend{}
	var services []string
	var resources []IngressBackend
	for _, be := range all {
		if !be.IsService() {
			resources = append(resources, be)
			continue
		}
		if _, seen := ports[be.Service]; !seen {
			services = append(services, be.Service)
		}
		ports[be.Service] = append(ports[be.Service], be)
	}
	slices.Sort(services)
	if len(services)+len(resources) > maxIngressBackends {
		b.g.SetTruncated(warnBackends)
	}
	d := it.depth + 1
	taken := 0
	for _, name := range services {
		if taken == maxIngressBackends || !b.reach(d) {
			return
		}
		taken++
		svc, obj := b.get(ctx, specService, n.Namespace, name)
		b.edge(n.Ref, svc.Ref, EdgeRoutes)
		switch {
		case svc.Placeholder || svc.Health == Missing:
			f.backendMissing = true
		case obj != nil:
			b.enqueue(svc.Ref, d, true)
			if !servicePortsMatch(obj, ports[name]) {
				f.portMissing = true
			}
		}
	}
	for _, be := range resources {
		if taken == maxIngressBackends || !b.reach(d) {
			return
		}
		taken++
		ref := Ref{Group: be.Resource.Group, Kind: be.Resource.Kind, Namespace: n.Namespace, Name: be.Resource.Name}
		if _, exists := b.g.nodes[ref.Key()]; !exists {
			b.putNode(Node{Ref: ref, Health: Unknown, Unknown: UnknownPolicy})
		}
		b.edge(n.Ref, ref, EdgeRoutes)
	}
}

// servicePortsMatch проверяет, что каждый порт backend объявлен у Service: по имени или по номеру. Service без портов
// (ручные Endpoints) и ExternalName не проверяются: объявлять нечего. Объект, который не разобрался, не даёт вывода.
func servicePortsMatch(obj *unstructured.Unstructured, want []IngressBackend) bool {
	var svc corev1.Service
	if !decode(obj, &svc) || svc.Spec.Type == corev1.ServiceTypeExternalName || len(svc.Spec.Ports) == 0 {
		return true
	}
	for _, be := range want {
		if be.PortName == "" && be.PortNumber == 0 {
			continue
		}
		if !slices.ContainsFunc(svc.Spec.Ports, func(p corev1.ServicePort) bool {
			return (be.PortName != "" && p.Name == be.PortName) || (be.PortName == "" && p.Port == be.PortNumber)
		}) {
			return false
		}
	}
	return true
}
