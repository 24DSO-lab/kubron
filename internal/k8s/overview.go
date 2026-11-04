// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Статусы компонентов control plane
const (
	healthHealthy   = "healthy"
	healthUnhealthy = "unhealthy"
	healthUnknown   = "unknown"
)

const (
	// сколько последних событий отдаём на дашборд
	overviewEventsLimit = 8
	// namespace, где живут компоненты control plane
	systemNamespace = "kube-system"
)

// HealthComponent — состояние одного компонента кластера.
// Status: healthy | unhealthy | unknown (компонент не виден: например, managed-кластер).
type HealthComponent struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// ClusterHealth — сводная оценка состояния кластера.
// Score 0..100; Status: Healthy | Degraded | Critical.
type ClusterHealth struct {
	Score      int               `json:"score"`
	Status     string            `json:"status"`
	Components []HealthComponent `json:"components"`
}

// OverviewEvent — строка в блоке «Recent events».
type OverviewEvent struct {
	Type      string `json:"type"` // Normal | Warning
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	Object    string `json:"object"` // Kind/name
	Namespace string `json:"namespace"`
	Time      string `json:"time"` // RFC3339: относительное время считает фронтенд
}

// WorkloadSummary — строка в таблице Workloads (Deployment / StatefulSet / DaemonSet).
type WorkloadSummary struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Status    string `json:"status"` // Running | Degraded | Unavailable | Idle
	Ready     int32  `json:"ready"`
	Desired   int32  `json:"desired"`
	UpToDate  int32  `json:"upToDate"`
	Age       string `json:"age"`
}

type ClusterOverview struct {
	TotalNodes       int `json:"totalNodes"`
	ReadyNodes       int `json:"readyNodes"`
	TotalPods        int `json:"totalPods"`
	RunningPods      int `json:"runningPods"`
	PendingPods      int `json:"pendingPods"`
	FailedPods       int `json:"failedPods"`
	TotalDeployments int `json:"totalDeployments"`
	ReadyDeployments int `json:"readyDeployments"`
	TotalNamespaces  int `json:"totalNamespaces"`
	ActiveNamespaces int `json:"activeNamespaces"`
	TotalEvents      int `json:"totalEvents"`
	WarningEvents    int `json:"warningEvents"`

	Health       ClusterHealth     `json:"health"`
	RecentEvents []OverviewEvent   `json:"recentEvents"`
	Workloads    []WorkloadSummary `json:"workloads"`

	RestartsLast24h  int              `json:"restartsLast24h"`
	OOMKilledLast24h int              `json:"oomKilledLast24h"`
	ProblemPods      []ProblemPod     `json:"problemPods"`
	ProblemPodsTotal int              `json:"problemPodsTotal"`
	WarningsLastHour int              `json:"warningsLastHour"`
	WarningReasons   []ReasonCount    `json:"warningReasons"`
	Requests         ResourceRequests `json:"requests"`
}

// GetClusterOverview собирает данные для главного экрана.
// Все запросы к API идут параллельно; если часть из них упала, остальное всё равно отдаём.
func (m *Manager) GetClusterOverview() (*ClusterOverview, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var (
		wg      sync.WaitGroup
		nodes   *corev1.NodeList
		pods    *corev1.PodList
		deps    *appsv1.DeploymentList
		stss    *appsv1.StatefulSetList
		dss     *appsv1.DaemonSetList
		nsList  *corev1.NamespaceList
		events  *corev1.EventList
		readyz  []byte
		readyEr error
	)

	// Каждая горутина пишет только в свою переменную, поэтому мьютекс не нужен
	run := func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f()
		}()
	}

	// Из локального кеша informers (если он уже синхронизирован), иначе прямым запросом к API
	oc := conn.overviewCache(func() { m.emitCluster(EventClusterChanged, conn) })

	run(func() {
		if l, ok := oc.nodeList(); ok {
			nodes = l
		} else if l, err := conn.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
			nodes = l
		}
	})
	run(func() {
		if l, ok := oc.podList(); ok {
			pods = l
		} else if l, err := conn.Client.CoreV1().Pods("").List(ctx, metav1.ListOptions{}); err == nil {
			pods = l
		}
	})
	run(func() {
		if l, ok := oc.deploymentList(); ok {
			deps = l
		} else if l, err := conn.Client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{}); err == nil {
			deps = l
		}
	})
	run(func() {
		if l, ok := oc.statefulSetList(); ok {
			stss = l
		} else if l, err := conn.Client.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{}); err == nil {
			stss = l
		}
	})
	run(func() {
		if l, ok := oc.daemonSetList(); ok {
			dss = l
		} else if l, err := conn.Client.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{}); err == nil {
			dss = l
		}
	})
	run(func() {
		if l, ok := oc.namespaceList(); ok {
			nsList = l
		} else if l, err := conn.Client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{}); err == nil {
			nsList = l
		}
	})
	run(func() {
		if l, ok := oc.eventList(); ok {
			events = l
		} else if l, err := conn.Client.CoreV1().Events("").List(ctx, metav1.ListOptions{}); err == nil {
			events = l
		}
	})
	run(func() {
		readyz, readyEr = conn.Client.CoreV1().RESTClient().Get().
			AbsPath("/readyz").Param("verbose", "true").DoRaw(ctx)
	})

	wg.Wait()

	overview := &ClusterOverview{
		RecentEvents:   []OverviewEvent{},
		Workloads:      []WorkloadSummary{},
		ProblemPods:    []ProblemPod{},
		WarningReasons: []ReasonCount{},
	}

	// Nodes
	if nodes != nil {
		overview.TotalNodes = len(nodes.Items)
		for i := range nodes.Items {
			if isNodeReady(&nodes.Items[i]) {
				overview.ReadyNodes++
			}
		}
	}

	// Pods
	succeededPods := 0
	if pods != nil {
		overview.TotalPods = len(pods.Items)
		for i := range pods.Items {
			switch pods.Items[i].Status.Phase {
			case corev1.PodRunning:
				overview.RunningPods++
			case corev1.PodPending:
				overview.PendingPods++
			case corev1.PodFailed:
				overview.FailedPods++
			case corev1.PodSucceeded:
				succeededPods++
			}
		}
	}

	// Workloads (+ счётчик Deployments)
	if deps != nil {
		overview.TotalDeployments = len(deps.Items)
		for i := range deps.Items {
			d := &deps.Items[i]
			desired := int32(1)
			if d.Spec.Replicas != nil {
				desired = *d.Spec.Replicas
			}
			if d.Status.ReadyReplicas == desired {
				overview.ReadyDeployments++
			}
			overview.Workloads = append(overview.Workloads, newWorkload(
				"Deployment", d.Name, d.Namespace, d.CreationTimestamp.Time,
				desired, d.Status.ReadyReplicas, d.Status.UpdatedReplicas,
			))
		}
	}
	if stss != nil {
		for i := range stss.Items {
			s := &stss.Items[i]
			desired := int32(1)
			if s.Spec.Replicas != nil {
				desired = *s.Spec.Replicas
			}
			overview.Workloads = append(overview.Workloads, newWorkload(
				"StatefulSet", s.Name, s.Namespace, s.CreationTimestamp.Time,
				desired, s.Status.ReadyReplicas, s.Status.UpdatedReplicas,
			))
		}
	}
	if dss != nil {
		for i := range dss.Items {
			d := &dss.Items[i]
			overview.Workloads = append(overview.Workloads, newWorkload(
				"DaemonSet", d.Name, d.Namespace, d.CreationTimestamp.Time,
				d.Status.DesiredNumberScheduled, d.Status.NumberReady, d.Status.UpdatedNumberScheduled,
			))
		}
	}
	sortWorkloads(overview.Workloads)

	// Namespaces
	if nsList != nil {
		overview.TotalNamespaces = len(nsList.Items)
		for i := range nsList.Items {
			if nsList.Items[i].Status.Phase == corev1.NamespaceActive {
				overview.ActiveNamespaces++
			}
		}
	}

	// Events
	if events != nil {
		overview.TotalEvents = len(events.Items)
		for i := range events.Items {
			if events.Items[i].Type == corev1.EventTypeWarning {
				overview.WarningEvents++
			}
		}
		overview.RecentEvents = recentEvents(events.Items, overviewEventsLimit)
	}

	// Сигналы для главного экрана (только по данным API, без Prometheus)
	now := time.Now()
	if pods != nil {
		ins := analyzePods(pods.Items, now)
		overview.RestartsLast24h, overview.OOMKilledLast24h = ins.Restarts, ins.OOMKilled
		overview.ProblemPods, overview.ProblemPodsTotal = ins.Problems, ins.ProblemsTotal
	}
	if events != nil {
		overview.WarningsLastHour, overview.WarningReasons = warningsByReason(events.Items, now)
	}
	var nodeItems []corev1.Node
	if nodes != nil {
		nodeItems = nodes.Items
	}
	var podItems []corev1.Pod
	if pods != nil {
		podItems = pods.Items
	}
	overview.Requests = clusterRequests(nodeItems, podItems)

	// Health
	var systemPods []corev1.Pod
	if pods != nil {
		for i := range pods.Items {
			if pods.Items[i].Namespace == systemNamespace {
				systemPods = append(systemPods, pods.Items[i])
			}
		}
	}
	overview.Health = buildHealth(
		readyz, readyEr, systemPods,
		overview.ReadyNodes, overview.TotalNodes,
		overview.RunningPods+succeededPods, overview.TotalPods,
	)

	return overview, nil
}

func isNodeReady(node *corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// ─── Workloads ───

func newWorkload(kind, name, namespace string, created time.Time, desired, ready, upToDate int32) WorkloadSummary {
	return WorkloadSummary{
		Name:      name,
		Namespace: namespace,
		Kind:      kind,
		Status:    workloadStatus(desired, ready),
		Ready:     ready,
		Desired:   desired,
		UpToDate:  upToDate,
		Age:       formatAge(created),
	}
}

func workloadStatus(desired, ready int32) string {
	switch {
	case desired == 0:
		return "Idle"
	case ready >= desired:
		return "Running"
	case ready == 0:
		return "Unavailable"
	default:
		return "Degraded"
	}
}

// sortWorkloads: проблемные сверху, дальше по namespace и имени
func sortWorkloads(list []WorkloadSummary) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		aBad, bBad := a.Status != "Running" && a.Status != "Idle", b.Status != "Running" && b.Status != "Idle"
		if aBad != bBad {
			return aBad
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Kind < b.Kind
	})
}

// ─── Events ───

func eventTimestamp(ev *corev1.Event) time.Time {
	if ev.Series != nil && !ev.Series.LastObservedTime.IsZero() {
		return ev.Series.LastObservedTime.Time
	}
	if !ev.LastTimestamp.IsZero() {
		return ev.LastTimestamp.Time
	}
	if !ev.EventTime.IsZero() {
		return ev.EventTime.Time
	}
	return ev.CreationTimestamp.Time
}

// recentEvents возвращает limit самых свежих событий (новые сверху)
func recentEvents(items []corev1.Event, limit int) []OverviewEvent {
	type timed struct {
		ev *corev1.Event
		at time.Time
	}
	list := make([]timed, 0, len(items))
	for i := range items {
		list = append(list, timed{ev: &items[i], at: eventTimestamp(&items[i])})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].at.After(list[j].at) })
	if len(list) > limit {
		list = list[:limit]
	}

	out := make([]OverviewEvent, 0, len(list))
	for _, t := range list {
		out = append(out, OverviewEvent{
			Type:      t.ev.Type,
			Reason:    t.ev.Reason,
			Message:   strings.TrimSpace(t.ev.Message),
			Object:    t.ev.InvolvedObject.Kind + "/" + t.ev.InvolvedObject.Name,
			Namespace: t.ev.Namespace,
			Time:      t.at.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// ─── Health ───

// buildHealth собирает состояние компонентов и сводную оценку.
//
// Компоненты:
//   - API Server и etcd — из /readyz?verbose (если недоступен — по подам kube-system);
//   - Scheduler, Controllers, CoreDNS — по подам kube-system. На managed-кластерах
//     (GKE, EKS, AKS) control plane не виден: такие компоненты получают статус unknown
//     и в оценку не входят.
//
// Оценка — взвешенная доля «здорового»: компоненты 40%, ноды 30%, поды 30%.
func buildHealth(
	readyz []byte, readyErr error, systemPods []corev1.Pod,
	readyNodes, totalNodes, okPods, totalPods int,
) ClusterHealth {
	byComponent := map[string][]corev1.Pod{}
	for i := range systemPods {
		p := systemPods[i]
		switch {
		case p.Labels["component"] != "":
			byComponent[p.Labels["component"]] = append(byComponent[p.Labels["component"]], p)
		case p.Labels["k8s-app"] == "kube-dns" || p.Labels["k8s-app"] == "coredns":
			byComponent["coredns"] = append(byComponent["coredns"], p)
		}
	}

	api, etcd := parseReadyz(readyz, readyErr)
	if api == healthUnknown {
		api = podsHealth(byComponent["kube-apiserver"])
	}
	if etcd == healthUnknown {
		etcd = podsHealth(byComponent["etcd"])
	}

	components := []HealthComponent{
		{Name: "API Server", Status: api},
		{Name: "Scheduler", Status: podsHealth(byComponent["kube-scheduler"])},
		{Name: "Controllers", Status: podsHealth(byComponent["kube-controller-manager"])},
		{Name: "etcd", Status: etcd},
		{Name: "CoreDNS", Status: podsHealth(byComponent["coredns"])},
	}

	var weighted, weights float64
	add := func(weight float64, ok, total int) {
		if total <= 0 {
			return
		}
		weighted += weight * float64(ok) / float64(total)
		weights += weight
	}
	known, healthy := 0, 0
	for _, c := range components {
		if c.Status == healthUnknown {
			continue
		}
		known++
		if c.Status == healthHealthy {
			healthy++
		}
	}
	add(0.4, healthy, known)
	add(0.3, readyNodes, totalNodes)
	add(0.3, okPods, totalPods)

	score := 0
	if weights > 0 {
		score = int(math.Round(100 * weighted / weights))
	}

	status := "Critical"
	switch {
	case score >= 90:
		status = "Healthy"
	case score >= 70:
		status = "Degraded"
	}

	return ClusterHealth{Score: score, Status: status, Components: components}
}

// parseReadyz разбирает ответ /readyz?verbose.
// Строки вида "[+]etcd ok" / "[-]etcd failed: reason withheld".
func parseReadyz(body []byte, err error) (api, etcd string) {
	api, etcd = healthUnknown, healthUnknown

	switch {
	case err == nil:
		api = healthHealthy
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err), apierrors.IsNotFound(err):
		// эндпоинт закрыт или отсутствует: судить об API Server по нему нельзя
	default:
		api = healthUnhealthy
	}

	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "[+]etcd ok"):
			etcd = healthHealthy
		case line == "[-]etcd" || strings.HasPrefix(line, "[-]etcd "):
			etcd = healthUnhealthy
		}
	}
	return api, etcd
}

// podsHealth: компонент здоров, если есть хотя бы один готовый под.
// Завершённые (Succeeded) и вытесненные (Failed) поды не учитываются.
func podsHealth(pods []corev1.Pod) string {
	live, ready, failed := 0, 0, 0
	for i := range pods {
		switch pods[i].Status.Phase {
		case corev1.PodSucceeded:
			continue
		case corev1.PodFailed:
			failed++
			continue
		}
		live++
		if isPodReady(&pods[i]) {
			ready++
		}
	}
	switch {
	case live == 0 && failed > 0:
		return healthUnhealthy
	case live == 0:
		return healthUnknown
	case ready > 0:
		return healthHealthy
	default:
		return healthUnhealthy
	}
}

func isPodReady(p *corev1.Pod) bool {
	if p.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}
