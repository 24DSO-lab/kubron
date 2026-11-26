// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package signals

import (
	"fmt"
	"slices"
	"time"
)

// Окна и шаги запросов (ADR-0032 §4). Агрегаты за окно считаются прямо в PromQL, а не выгрузкой рядов.
const (
	windowShort  = 15 * time.Minute // троттлинг, пик CPU, загрузка CPU Node
	windowMedium = time.Hour        // пик памяти, минимум доступной памяти Node
	windowLong   = 6 * time.Hour    // рестарты, OOM, давление на Node

	cpuRateWindow = 5 * time.Minute // окно rate() внутри пика использования CPU
	cpuStep       = time.Minute     // шаг подзапроса пика CPU

	// Этап B (ADR-0032 §4).
	volumeStep       = time.Minute // шаг подзапроса пика занятости тома
	hpaStep          = time.Minute // шаг подзапроса «упирался ли HPA в максимум»
	ingressLatencyQ  = "0.95"      // квантиль задержки Ingress (p95)
	ingressRateWin   = windowShort // окно rate() гистограммы задержки: совпадает с окном сигнала
	ingress5xxStatus = "5.."       // статусы ответов, которые считаются ошибками сервера
)

// Задачи сбора и сервисы встроенного стека (internal/k8s/manifests/metrics-stack.yaml). Тест согласованности
// (internal/k8s/metrics_signals_test.go) сверяет с манифестом всё, что объявлено в Requirements().
const (
	jobCAdvisor        = "kubernetes-cadvisor"
	jobServiceEndpoint = "kubernetes-service-endpoints"
	jobKubelet         = "kubelet"       // kubelet_volume_stats_* (метки namespace и persistentvolumeclaim сохраняются)
	jobIngressNginx    = "ingress-nginx" // опциональная задача: подбирает только Pod'ы контроллера ingress-nginx

	svcKubeState    = "kube-state-metrics"
	svcNodeExporter = "node-exporter"
	svcPrometheus   = "prometheus"
)

// spec — описание сигнала: что нужно от цели и стека, окно, единицы и построитель запроса. Закрытый каталог: сигнал,
// которого здесь нет, не выполняется.
type spec struct {
	signal   Signal
	families []Family
	fields   []field
	window   time.Duration // окно агрегата; 0 — мгновенное значение
	extra    time.Duration // дополнительная предыстория внутри запроса (rate внутри подзапроса)
	unit     string
	agg      string
	build    func(s Subject, window time.Duration) string

	metrics  []string // имена метрик, которые использует запрос (проверяется тестом)
	ksmKinds []string // виды ресурсов kube-state-metrics (флаг --resources)
	jobs     []string // задачи сбора prometheus.yaml
	services []string // сервисы стека с аннотацией prometheus.io/scrape
	labels   []string // метки, которые ставит сбор и без которых запрос бессмыслен
}

// need — сколько истории нужно запросу: окно агрегата и предыстория вычисления.
func (sp *spec) need() time.Duration {
	if sp.window == 0 {
		return 0
	}
	return sp.window + sp.extra
}

// containerSel — сопоставление контейнера: все три метки строго равны (container="" и «POD» cAdvisor не попадают).
func containerSel(s Subject) string {
	return eq("namespace", s.Namespace) + "," + eq("pod", s.Pod) + "," + eq("container", s.Container)
}

var catalog = map[Signal]*spec{
	CPUThrottlingRatio: {
		signal: CPUThrottlingRatio, families: []Family{FamilyContainer},
		fields: []field{fNamespace, fPod, fContainer}, window: windowShort, unit: "ratio", agg: "ratio",
		build: func(s Subject, w time.Duration) string {
			sel, d := containerSel(s), promDuration(w)
			return fmt.Sprintf(`sum(increase(container_cpu_cfs_throttled_periods_total{%[1]s}[%[2]s])) / `+
				`sum(increase(container_cpu_cfs_periods_total{%[1]s}[%[2]s]))`, sel, d)
		},
		metrics:  []string{"container_cpu_cfs_periods_total", "container_cpu_cfs_throttled_periods_total"},
		jobs:     []string{jobCAdvisor},
		services: nil,
	},
	CPUUsageVsLimit: {
		signal: CPUUsageVsLimit, families: []Family{FamilyContainer, FamilyKubeState},
		fields: []field{fNamespace, fPod, fContainer}, window: windowShort, extra: cpuRateWindow, unit: "ratio", agg: "max",
		build: func(s Subject, w time.Duration) string {
			sel := containerSel(s)
			return fmt.Sprintf(`max_over_time(sum(rate(container_cpu_usage_seconds_total{%[1]s}[%[2]s]))[%[3]s:%[4]s]) / `+
				`max(kube_pod_container_resource_limits{%[1]s,resource="cpu"})`,
				sel, promDuration(cpuRateWindow), promDuration(w), promDuration(cpuStep))
		},
		metrics:  []string{"container_cpu_usage_seconds_total", "kube_pod_container_resource_limits"},
		ksmKinds: []string{"pods"}, jobs: []string{jobCAdvisor, jobServiceEndpoint}, services: []string{svcKubeState},
	},
	MemoryUsageVsLimit: {
		signal: MemoryUsageVsLimit, families: []Family{FamilyContainer, FamilyKubeState},
		fields: []field{fNamespace, fPod, fContainer}, window: windowMedium, unit: "ratio", agg: "max",
		build: func(s Subject, w time.Duration) string {
			sel := containerSel(s)
			return fmt.Sprintf(`max(max_over_time(container_memory_working_set_bytes{%[1]s}[%[2]s])) / `+
				`max(kube_pod_container_resource_limits{%[1]s,resource="memory"})`, sel, promDuration(w))
		},
		metrics:  []string{"container_memory_working_set_bytes", "kube_pod_container_resource_limits"},
		ksmKinds: []string{"pods"}, jobs: []string{jobCAdvisor, jobServiceEndpoint}, services: []string{svcKubeState},
	},
	ContainerRestarts: {
		signal: ContainerRestarts, families: []Family{FamilyKubeState},
		fields: []field{fNamespace, fPod, fContainer}, window: windowLong, unit: "count", agg: "increase",
		build: func(s Subject, w time.Duration) string {
			return fmt.Sprintf(`sum(increase(kube_pod_container_status_restarts_total{%s}[%s]))`, containerSel(s), promDuration(w))
		},
		metrics:  []string{"kube_pod_container_status_restarts_total"},
		ksmKinds: []string{"pods"}, jobs: []string{jobServiceEndpoint}, services: []string{svcKubeState},
	},
	OOMEvents: {
		signal: OOMEvents, families: []Family{FamilyContainer},
		fields: []field{fNamespace, fPod, fContainer}, window: windowLong, unit: "count", agg: "increase",
		build: func(s Subject, w time.Duration) string {
			return fmt.Sprintf(`sum(increase(container_oom_events_total{%s}[%s]))`, containerSel(s), promDuration(w))
		},
		metrics: []string{"container_oom_events_total"}, jobs: []string{jobCAdvisor},
	},
	NodeMemoryAvailableRatio: {
		signal: NodeMemoryAvailableRatio, families: []Family{FamilyNode},
		fields: []field{fNode}, window: windowMedium, unit: "ratio", agg: "min",
		build: func(s Subject, w time.Duration) string {
			sel := eq("kubernetes_node", s.Node)
			return fmt.Sprintf(`min(min_over_time(node_memory_MemAvailable_bytes{%[1]s}[%[2]s])) / max(node_memory_MemTotal_bytes{%[1]s})`,
				sel, promDuration(w))
		},
		metrics: []string{"node_memory_MemAvailable_bytes", "node_memory_MemTotal_bytes"},
		jobs:    []string{jobServiceEndpoint}, services: []string{svcNodeExporter}, labels: []string{"kubernetes_node"},
	},
	NodeCPUUtilization: {
		signal: NodeCPUUtilization, families: []Family{FamilyNode},
		fields: []field{fNode}, window: windowShort, unit: "ratio", agg: "avg",
		build: func(s Subject, w time.Duration) string {
			return fmt.Sprintf(`clamp(1 - avg(rate(node_cpu_seconds_total{%s,mode="idle"}[%s])), 0, 1)`,
				eq("kubernetes_node", s.Node), promDuration(w))
		},
		metrics: []string{"node_cpu_seconds_total"},
		jobs:    []string{jobServiceEndpoint}, services: []string{svcNodeExporter}, labels: []string{"kubernetes_node"},
	},
	NodeDiskAvailableRatio: {
		signal: NodeDiskAvailableRatio, families: []Family{FamilyNode},
		fields: []field{fNode}, unit: "ratio", agg: "last",
		build: func(s Subject, _ time.Duration) string {
			sel := eq("kubernetes_node", s.Node) + `,mountpoint="/",fstype!="tmpfs"`
			return fmt.Sprintf(`min(node_filesystem_avail_bytes{%[1]s} / node_filesystem_size_bytes{%[1]s})`, sel)
		},
		metrics: []string{"node_filesystem_avail_bytes", "node_filesystem_size_bytes"},
		jobs:    []string{jobServiceEndpoint}, services: []string{svcNodeExporter}, labels: []string{"kubernetes_node"},
	},
	NodePressureHistory: {
		signal: NodePressureHistory, families: []Family{FamilyKubeState},
		fields: []field{fNode}, window: windowLong, unit: "flag", agg: "max",
		build: func(s Subject, w time.Duration) string {
			// В kube-state-metrics v2 метка Node называется node; kubernetes_node у kube_* метрик указывает на узел
			// самого kube-state-metrics и для выбора цели непригодна.
			return fmt.Sprintf(`max(max_over_time(kube_node_status_condition{%s,condition=~"MemoryPressure|DiskPressure|PIDPressure",status="true"}[%s]))`,
				eq("node", s.Node), promDuration(w))
		},
		metrics:  []string{"kube_node_status_condition"},
		ksmKinds: []string{"nodes"}, jobs: []string{jobServiceEndpoint}, services: []string{svcKubeState},
	},

	// Этап B (ADR-0032 §4).
	PVCUsageRatio: {
		signal: PVCUsageRatio, families: []Family{FamilyVolume},
		fields: []field{fNamespace, fPVC}, window: windowMedium, unit: "ratio", agg: "max",
		build: func(s Subject, w time.Duration) string {
			sel := eq("namespace", s.Namespace) + "," + eq("persistentvolumeclaim", s.PVC)
			return fmt.Sprintf(`max(max_over_time((kubelet_volume_stats_used_bytes{%[1]s} / kubelet_volume_stats_capacity_bytes{%[1]s})[%[2]s:%[3]s]))`,
				sel, promDuration(w), promDuration(volumeStep))
		},
		metrics: []string{"kubelet_volume_stats_capacity_bytes", "kubelet_volume_stats_used_bytes"},
		jobs:    []string{jobKubelet},
	},
	HPAAtMaxReplicas: {
		signal: HPAAtMaxReplicas, families: []Family{FamilyKubeState},
		fields: []field{fNamespace, fHPA}, window: windowShort, unit: "flag", agg: "max",
		build: func(s Subject, w time.Duration) string {
			sel := eq("namespace", s.Namespace) + "," + eq("horizontalpodautoscaler", s.HPA)
			return fmt.Sprintf(`max(max_over_time((kube_horizontalpodautoscaler_status_current_replicas{%[1]s} >= bool on(namespace,horizontalpodautoscaler) `+
				`kube_horizontalpodautoscaler_spec_max_replicas{%[1]s})[%[2]s:%[3]s]))`, sel, promDuration(w), promDuration(hpaStep))
		},
		metrics:  []string{"kube_horizontalpodautoscaler_spec_max_replicas", "kube_horizontalpodautoscaler_status_current_replicas"},
		ksmKinds: []string{"horizontalpodautoscalers"}, jobs: []string{jobServiceEndpoint}, services: []string{svcKubeState},
	},
	DeploymentUnavailableReplicas: {
		signal: DeploymentUnavailableReplicas, families: []Family{FamilyKubeState},
		fields: []field{fNamespace, fDeployment}, window: windowShort, unit: "count", agg: "max",
		build: func(s Subject, w time.Duration) string {
			sel := eq("namespace", s.Namespace) + "," + eq("deployment", s.Deployment)
			return fmt.Sprintf(`max(max_over_time(kube_deployment_status_replicas_unavailable{%s}[%s]))`, sel, promDuration(w))
		},
		metrics:  []string{"kube_deployment_status_replicas_unavailable"},
		ksmKinds: []string{"deployments"}, jobs: []string{jobServiceEndpoint}, services: []string{svcKubeState},
	},
	Ingress5xxRatio: {
		signal: Ingress5xxRatio, families: []Family{FamilyIngress},
		fields: []field{fNamespace, fIngress}, window: windowShort, unit: "ratio", agg: "ratio",
		build: func(s Subject, w time.Duration) string {
			sel, d := ingressSel(s), promDuration(w)
			return fmt.Sprintf(`sum(increase(nginx_ingress_controller_requests{%[1]s,status=~"`+ingress5xxStatus+`"}[%[2]s])) / `+
				`sum(increase(nginx_ingress_controller_requests{%[1]s}[%[2]s]))`, sel, d)
		},
		metrics: []string{"nginx_ingress_controller_requests"},
		jobs:    []string{jobIngressNginx},
	},
	IngressLatencyP95: {
		signal: IngressLatencyP95, families: []Family{FamilyIngress},
		fields: []field{fNamespace, fIngress}, window: windowShort, unit: "seconds", agg: "p95",
		build: func(s Subject, w time.Duration) string {
			return fmt.Sprintf(`histogram_quantile(`+ingressLatencyQ+`, sum by (le) (rate(nginx_ingress_controller_request_duration_seconds_bucket{%s}[%s])))`,
				ingressSel(s), promDuration(ingressRateWin))
		},
		metrics: []string{"nginx_ingress_controller_request_duration_seconds_bucket"},
		jobs:    []string{jobIngressNginx},
	},
}

// ingressSel — сопоставление Ingress в метриках ingress-nginx: namespace и имя Ingress. Задача сбора не задаёт целевых
// меток namespace и ingress, поэтому метки приходят от контроллера как есть (проверяет тест согласованности стека).
func ingressSel(s Subject) string {
	return eq("namespace", s.Namespace) + "," + eq("ingress", s.Ingress)
}

// Характерные метрики семейств для ленивой проверки наличия (ADR-0032 §5): один агрегат count, возвращающий только число.
// Это единственные запросы без привязки к цели: имена объектов они не раскрывают.
var familyProbe = map[Family]string{
	FamilyContainer: `count(container_memory_working_set_bytes{namespace!="",pod!="",container!=""})`,
	FamilyKubeState: `count(kube_pod_container_status_restarts_total)`,
	FamilyNode:      `count(node_memory_MemTotal_bytes{kubernetes_node!=""})`,
	FamilyVolume:    `count(kubelet_volume_stats_capacity_bytes{namespace!="",persistentvolumeclaim!=""})`,
	FamilyIngress:   `count(nginx_ingress_controller_requests{namespace!="",ingress!=""})`,
}

// probeFamilies — порядок опроса семейств: фиксирован, чтобы отчёт и тесты были детерминированы.
var probeFamilies = []Family{FamilyContainer, FamilyKubeState, FamilyNode, FamilyVolume, FamilyIngress}

// coverageProbe — начало истории Prometheus в секундах Unix. Prometheus отдаёт метрику в секундах
// (prometheus_tsdb_lowest_timestamp_seconds) и в миллисекундах (prometheus_tsdb_lowest_timestamp); запрос принимает
// любую, приводя к секундам. Метрику собирает задача сервиса prometheus (он размечен prometheus.io/scrape).
const coverageProbe = `min(prometheus_tsdb_lowest_timestamp_seconds or (prometheus_tsdb_lowest_timestamp / 1000))`

// coverageSlack — допуск на выравнивание первого скрейпа: покрытие, короче окна не более чем на эту величину, достаточно.
const coverageSlack = time.Minute

// Пределы одной операции (ADR-0032 §6): не более 12 запросов сигналов и 6 проверок — пять семейств (этап B добавил
// volume и ingress) и проверка покрытия истории. Каждая проверка — один count без привязки к цели.
const (
	MaxSignalQueries = 12
	MaxProbeQueries  = 6
)

// Requirement — что сигнал требует от встроенного стека. Для теста согласованности с манифестом: каталог и манифест
// не должны разойтись молча.
type Requirement struct {
	Signal   Signal // пусто — требование проверки покрытия истории
	Metrics  []string
	KubeKind []string // виды ресурсов kube-state-metrics
	Jobs     []string // задачи сбора
	Services []string // сервисы стека, которые должны собираться
	Labels   []string // метки, выставляемые сбором
}

// Requirements возвращает требования всех сигналов в порядке имён и требование проверки покрытия последним.
func Requirements() []Requirement {
	out := make([]Requirement, 0, len(catalog)+1)
	for _, sp := range catalog {
		out = append(out, Requirement{
			Signal: sp.signal, Metrics: slices.Clone(sp.metrics), KubeKind: slices.Clone(sp.ksmKinds),
			Jobs: slices.Clone(sp.jobs), Services: slices.Clone(sp.services), Labels: slices.Clone(sp.labels),
		})
	}
	slices.SortFunc(out, func(a, b Requirement) int {
		switch {
		case a.Signal < b.Signal:
			return -1
		case a.Signal > b.Signal:
			return 1
		}
		return 0
	})
	return append(out, Requirement{
		Metrics:  []string{"prometheus_tsdb_lowest_timestamp", "prometheus_tsdb_lowest_timestamp_seconds"},
		Jobs:     []string{jobServiceEndpoint},
		Services: []string{svcPrometheus},
	})
}

// Known сообщает, входит ли сигнал в каталог (этапы A и B).
func Known(s Signal) bool { _, ok := catalog[s]; return ok }
