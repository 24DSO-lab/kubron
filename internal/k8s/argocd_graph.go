// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
)

const (
	// argoGraphMaxNodes — предел размера графа: больше интерфейс всё равно не покажет читаемо.
	argoGraphMaxNodes = 500

	ArgoEdgeOwns    = "owns"    // владелец → дочерний объект (Deployment → ReplicaSet → Pod)
	ArgoEdgeSelects = "selects" // Service → Pod по селектору
	ArgoEdgeRoutes  = "routes"  // Ingress → Service
)

// ArgoGraphNode — вершина графа ресурсов приложения.
type ArgoGraphNode struct {
	ID        string `json:"id"` // group/kind/namespace/name
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Status    string `json:"status"` // sync: Synced | OutOfSync | ""
	Health    string `json:"health"`
	Message   string `json:"message"`
	Hook      bool   `json:"hook"`
	// Managed: ресурс есть в status.resources приложения. false — найден по связям (ReplicaSet, Pod, Service Ingress'а).
	Managed bool `json:"managed"`
}

// ArgoGraphEdge — связь между вершинами.
type ArgoGraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"` // owns | selects | routes
}

// ArgoGraph — граф ресурсов приложения. Warnings — то, что не удалось прочитать (например, нет прав на list pods):
// частичный граф лучше ошибки.
type ArgoGraph struct {
	Nodes     []ArgoGraphNode `json:"nodes"`
	Edges     []ArgoGraphEdge `json:"edges"`
	Truncated bool            `json:"truncated"` // достигнут предел argoGraphMaxNodes
	Warnings  []string        `json:"warnings"`
}

// GetArgoApplicationGraph строит граф ресурсов Application: status.resources плюс связи владения,
// Service → Pod и Ingress → Service. Читает только Kubernetes API.
func (m *Manager) GetArgoApplicationGraph(namespace, name string) (*ArgoGraph, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), 30*time.Second)
	defer cancel()

	u, err := conn.DynamicClient.Resource(kindTable["application"].gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get application %s/%s: %w", namespace, name, err)
	}
	return buildArgoGraph(ctx, conn.Client, argoResources(u.Object)), nil
}

func graphID(group, kind, ns, name string) string {
	return group + "/" + kind + "/" + ns + "/" + name
}

type graphBuilder struct {
	nodes     map[string]*ArgoGraphNode
	edges     map[ArgoGraphEdge]struct{}
	truncated bool
	warnings  []string
}

func (b *graphBuilder) add(n ArgoGraphNode) (*ArgoGraphNode, bool) {
	n.ID = graphID(n.Group, n.Kind, n.Namespace, n.Name)
	if ex, ok := b.nodes[n.ID]; ok {
		return ex, true
	}
	if len(b.nodes) >= argoGraphMaxNodes {
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
	b.edges[ArgoGraphEdge{From: from, To: to, Type: typ}] = struct{}{}
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

// buildArgoGraph — чистая по отношению к Application часть: принимает уже разобранные status.resources.
func buildArgoGraph(ctx context.Context, c kubernetes.Interface, refs []ArgoResourceRef) *ArgoGraph {
	b := &graphBuilder{nodes: map[string]*ArgoGraphNode{}, edges: map[ArgoGraphEdge]struct{}{}, warnings: []string{}}

	namespaces := map[string]bool{}
	for _, r := range refs {
		if _, ok := b.add(ArgoGraphNode{
			Group: r.Group, Kind: r.Kind, Namespace: r.Namespace, Name: r.Name,
			Status: r.Status, Health: r.Health, Message: r.Message, Hook: r.Hook, Managed: true,
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
	n, ok := b.add(ArgoGraphNode{Group: group, Kind: kind, Namespace: ns, Name: name, Health: health})
	if !ok {
		return
	}
	b.link(ownerID, n.ID, ArgoEdgeOwns)
}

// podGraphHealth сводит состояние пода к health-статусам Argo CD (у найденных по связям подов своего health нет).
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
	var svcs []*ArgoGraphNode
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
				b.link(sn.ID, id, ArgoEdgeSelects)
			}
		}
	}
}

// linkIngresses: Ingress → Service по backend'ам правил и defaultBackend (Service в том же namespace).
func (b *graphBuilder) linkIngresses(ctx context.Context, c kubernetes.Interface, ns string) {
	var ings []*ArgoGraphNode
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
			// Service вне приложения тоже показываем (managed=false): без него маршрут выглядел бы оборванным.
			sn, ok := b.add(ArgoGraphNode{Kind: "Service", Namespace: ns, Name: svcName})
			if ok {
				b.link(in.ID, sn.ID, ArgoEdgeRoutes)
			}
		}
	}
}

func (b *graphBuilder) result() *ArgoGraph {
	g := &ArgoGraph{
		Nodes:     make([]ArgoGraphNode, 0, len(b.nodes)),
		Edges:     make([]ArgoGraphEdge, 0, len(b.edges)),
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
