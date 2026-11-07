// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
)

// Общий построитель графа ресурсов: по списку ссылок на ресурсы достраивает связи, которые видны только в кластере
// (владение по ownerReferences, Service → Pod по селектору, Ingress → Service). Не знает ни про Argo CD, ни про Flux:
// каждый из них собирает ссылки из своего источника (status.resources, status.inventory), вызывает
// buildResourceGraph и переводит результат в свой DTO.

const (
	// graphMaxNodes — предел размера графа: больше интерфейс всё равно не покажет читаемо.
	graphMaxNodes = 500

	graphEdgeOwns    = "owns"    // владелец → дочерний объект (Deployment → ReplicaSet → Pod)
	graphEdgeSelects = "selects" // Service → Pod по селектору
	graphEdgeRoutes  = "routes"  // Ingress → Service
)

// graphRef — нейтральная ссылка на ресурс: вход построителя.
// Повтор одного ресурса (одинаковые группа, вид, namespace и имя) игнорируется: первая запись выигрывает.
type graphRef struct {
	Group     string
	Version   string
	Kind      string
	Namespace string
	Name      string
	// Health — health-статус ресурса в словаре графа (Healthy, Degraded, Progressing, Suspended, Missing или пусто).
	Health string
}

// graphNode — вершина графа.
type graphNode struct {
	ID        string // group/kind/namespace/name, см. graphID
	Group     string
	Version   string // есть только у вершин из входных ссылок
	Kind      string
	Namespace string
	Name      string
	Health    string
	// Managed: ресурс был среди входных ссылок. false — найден по связям (ReplicaSet, Pod, Service Ingress'а).
	Managed bool
}

// graphEdge — связь между вершинами.
type graphEdge struct {
	From string
	To   string
	Type string // graphEdgeOwns | graphEdgeSelects | graphEdgeRoutes
}

// resourceGraph — результат построения. Warnings — то, что не удалось прочитать (например, нет прав на list pods):
// частичный граф лучше ошибки. Срезы никогда не nil, чтобы JSON получал [] вместо null.
type resourceGraph struct {
	Nodes     []graphNode
	Edges     []graphEdge
	Truncated bool // достигнут предел graphMaxNodes
	Warnings  []string
}

// graphID — идентификатор вершины: group/kind/namespace/name (у cluster-scoped ресурсов namespace пуст).
func graphID(group, kind, ns, name string) string {
	return group + "/" + kind + "/" + ns + "/" + name
}

type graphBuilder struct {
	nodes     map[string]*graphNode
	edges     map[graphEdge]struct{}
	truncated bool
	warnings  []string
}

func (b *graphBuilder) add(n graphNode) (*graphNode, bool) {
	n.ID = graphID(n.Group, n.Kind, n.Namespace, n.Name)
	if ex, ok := b.nodes[n.ID]; ok {
		return ex, true
	}
	if len(b.nodes) >= graphMaxNodes {
		b.truncated = true
		return nil, false
	}
	b.nodes[n.ID] = &n
	return &n, true
}

func (b *graphBuilder) link(from, to, typ string) {
	if from == to {
		return
	}
	b.edges[graphEdge{From: from, To: to, Type: typ}] = struct{}{}
}

func (b *graphBuilder) warn(format string, args ...any) {
	b.warnings = append(b.warnings, fmt.Sprintf(format, args...))
}

// listWarn превращает ошибку чтения в предупреждение: нехватка прав или отсутствие ресурса не должны ломать граф.
func (b *graphBuilder) listWarn(what, ns string, err error) {
	if apierrors.IsForbidden(err) {
		b.warn("no access to %s in namespace %s", what, ns)
		return
	}
	b.warn("failed to read %s in namespace %s: %v", what, ns, err)
}

// buildResourceGraph строит граф по ссылкам: вершины из refs (Managed=true), затем связи владения,
// Service → Pod и Ingress → Service. Читает только Kubernetes API; ошибки чтения становятся предупреждениями.
func buildResourceGraph(ctx context.Context, c kubernetes.Interface, refs []graphRef) *resourceGraph {
	b := &graphBuilder{nodes: map[string]*graphNode{}, edges: map[graphEdge]struct{}{}, warnings: []string{}}

	namespaces := map[string]bool{}
	for _, r := range refs {
		if _, ok := b.add(graphNode{
			Group: r.Group, Version: r.Version, Kind: r.Kind, Namespace: r.Namespace, Name: r.Name,
			Health: r.Health, Managed: true,
		}); !ok {
			break
		}
		if r.Namespace != "" {
			namespaces[r.Namespace] = true
		}
	}

	nsList := make([]string, 0, len(namespaces))
	for ns := range namespaces {
		nsList = append(nsList, ns)
	}
	sort.Strings(nsList)

	for _, ns := range nsList {
		b.expandOwners(ctx, c, ns)
		b.linkServices(ctx, c, ns)
		b.linkIngresses(ctx, c, ns)
	}
	return b.result()
}

// ownerNodeID: id владельца-контроллера объекта; "" — владельца нет.
func ownerNodeID(ns string, refs []metav1.OwnerReference) string {
	for _, o := range refs {
		if o.Controller != nil && *o.Controller {
			gv, _ := schema.ParseGroupVersion(o.APIVersion)
			return graphID(gv.Group, o.Kind, ns, o.Name)
		}
	}
	return ""
}

// expandOwners достраивает цепочки владения: ReplicaSet (от Deployment), Job (от CronJob), затем Pod.
// Порядок фиксирован, поэтому каждый следующий вид видит уже добавленных владельцев.
func (b *graphBuilder) expandOwners(ctx context.Context, c kubernetes.Interface, ns string) {
	if rss, err := c.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{}); err != nil {
		b.listWarn("replicasets", ns, err)
	} else {
		for i := range rss.Items {
			rs := &rss.Items[i]
			b.attachChild(ns, rs.OwnerReferences, "apps", "ReplicaSet", rs.Name, "")
		}
	}
	if jobs, err := c.BatchV1().Jobs(ns).List(ctx, metav1.ListOptions{}); err != nil {
		b.listWarn("jobs", ns, err)
	} else {
		for i := range jobs.Items {
			j := &jobs.Items[i]
			b.attachChild(ns, j.OwnerReferences, "batch", "Job", j.Name, "")
		}
	}
	if pods, err := c.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{}); err != nil {
		b.listWarn("pods", ns, err)
	} else {
		for i := range pods.Items {
			p := &pods.Items[i]
			b.attachChild(ns, p.OwnerReferences, "", "Pod", p.Name, podGraphHealth(p))
		}
	}
}

// attachChild добавляет объект в граф, только если его контроллер-владелец уже в графе
// (так в граф не попадают чужие поды и ReplicaSet'ы того же namespace).
func (b *graphBuilder) attachChild(ns string, owners []metav1.OwnerReference, group, kind, name, health string) {
	ownerID := ownerNodeID(ns, owners)
	if ownerID == "" {
		return
	}
	if _, ok := b.nodes[ownerID]; !ok {
		return
	}
	n, ok := b.add(graphNode{Group: group, Kind: kind, Namespace: ns, Name: name, Health: health})
	if !ok {
		return
	}
	b.link(ownerID, n.ID, graphEdgeOwns)
}

// podGraphHealth сводит состояние пода к health-статусам графа (у найденных по связям подов своего health нет).
func podGraphHealth(p *corev1.Pod) string {
	if p.Status.Phase == corev1.PodFailed {
		return "Degraded"
	}
	// Причины ожидания проверяем раньше фазы: под в ImagePullBackOff остаётся Pending, но здоровым не является.
	ready := true
	for _, cs := range p.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil {
			switch w.Reason {
			case "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "CreateContainerError", "CreateContainerConfigError":
				return "Degraded"
			}
		}
		if !cs.Ready {
			ready = false
		}
	}
	switch {
	case p.Status.Phase == corev1.PodSucceeded:
		return "Healthy"
	case p.Status.Phase == corev1.PodPending, !ready:
		return "Progressing"
	}
	return "Healthy"
}

// linkServices: Service → Pod по spec.selector среди подов, которые уже есть в графе.
func (b *graphBuilder) linkServices(ctx context.Context, c kubernetes.Interface, ns string) {
	var svcs []*graphNode
	hasPods := false
	for _, n := range b.nodes {
		if n.Namespace != ns {
			continue
		}
		switch {
		case n.Kind == "Service" && n.Group == "":
			svcs = append(svcs, n)
		case n.Kind == "Pod" && n.Group == "":
			hasPods = true
		}
	}
	if len(svcs) == 0 || !hasPods {
		return
	}
	pods, err := c.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		b.listWarn("pods", ns, err)
		return
	}
	for _, sn := range svcs {
		svc, err := c.CoreV1().Services(ns).Get(ctx, sn.Name, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				b.listWarn("service "+sn.Name, ns, err)
			}
			continue
		}
		if len(svc.Spec.Selector) == 0 {
			continue
		}
		sel := labels.SelectorFromSet(svc.Spec.Selector)
		for i := range pods.Items {
			p := &pods.Items[i]
			id := graphID("", "Pod", ns, p.Name)
			if _, inGraph := b.nodes[id]; inGraph && sel.Matches(labels.Set(p.Labels)) {
				b.link(sn.ID, id, graphEdgeSelects)
			}
		}
	}
}

// linkIngresses: Ingress → Service по backend'ам правил и defaultBackend (Service в том же namespace).
func (b *graphBuilder) linkIngresses(ctx context.Context, c kubernetes.Interface, ns string) {
	var ings []*graphNode
	for _, n := range b.nodes {
		if n.Namespace == ns && n.Kind == "Ingress" && n.Group == "networking.k8s.io" {
			ings = append(ings, n)
		}
	}
	for _, in := range ings {
		ing, err := c.NetworkingV1().Ingresses(ns).Get(ctx, in.Name, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				b.listWarn("ingress "+in.Name, ns, err)
			}
			continue
		}
		backends := map[string]bool{}
		if ing.Spec.DefaultBackend != nil && ing.Spec.DefaultBackend.Service != nil {
			backends[ing.Spec.DefaultBackend.Service.Name] = true
		}
		for _, r := range ing.Spec.Rules {
			if r.HTTP == nil {
				continue
			}
			for _, p := range r.HTTP.Paths {
				if p.Backend.Service != nil {
					backends[p.Backend.Service.Name] = true
				}
			}
		}
		for svcName := range backends {
			// Service вне входных ссылок тоже показываем (Managed=false): без него маршрут выглядел бы оборванным.
			sn, ok := b.add(graphNode{Kind: "Service", Namespace: ns, Name: svcName})
			if ok {
				b.link(in.ID, sn.ID, graphEdgeRoutes)
			}
		}
	}
}

func (b *graphBuilder) result() *resourceGraph {
	g := &resourceGraph{
		Nodes:     make([]graphNode, 0, len(b.nodes)),
		Edges:     make([]graphEdge, 0, len(b.edges)),
		Truncated: b.truncated,
		Warnings:  b.warnings,
	}
	for _, n := range b.nodes {
		g.Nodes = append(g.Nodes, *n)
	}
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	for e := range b.edges {
		// Рёбра к вершинам, не поместившимся в лимит, не оставляем.
		if _, ok := b.nodes[e.From]; !ok {
			continue
		}
		if _, ok := b.nodes[e.To]; !ok {
			continue
		}
		g.Edges = append(g.Edges, e)
	}
	sort.Slice(g.Edges, func(i, j int) bool {
		a, c := g.Edges[i], g.Edges[j]
		if a.From != c.From {
			return a.From < c.From
		}
		if a.To != c.To {
			return a.To < c.To
		}
		return a.Type < c.Type
	})
	return g
}
