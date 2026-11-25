// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package signals

import (
	"context"
	"errors"
	"time"
)

// Signal — идентификатор сигнала каталога (ADR-0032 §4). Закрытый набор: новый сигнал — изменение кода и тестов.
type Signal string

// Сигналы этапов A и B (ADR-0032 §4).
const (
	CPUThrottlingRatio       Signal = "cpu_throttling_ratio"        // доля периодов CFS с троттлингом
	CPUUsageVsLimit          Signal = "cpu_usage_vs_limit"          // пик использования CPU к лимиту
	MemoryUsageVsLimit       Signal = "memory_usage_vs_limit"       // пик рабочего набора к лимиту памяти
	ContainerRestarts        Signal = "container_restarts"          // число рестартов за окно
	OOMEvents                Signal = "oom_events"                  // OOM-события контейнера за окно
	NodeMemoryAvailableRatio Signal = "node_memory_available_ratio" // минимум доступной памяти Node
	NodeCPUUtilization       Signal = "node_cpu_utilization"        // средняя загрузка CPU Node
	NodeDiskAvailableRatio   Signal = "node_disk_available_ratio"   // доля свободного места корневого раздела
	NodePressureHistory      Signal = "node_pressure_history"       // находилась ли Node под давлением
)

// Сигналы этапа B (ADR-0032 §4): хранилище, автомасштабирование, доступность реплик и входящий трафик.
const (
	PVCUsageRatio                 Signal = "pvc_usage_ratio"                 // доля занятого места тома PVC
	HPAAtMaxReplicas              Signal = "hpa_at_max_replicas"             // HPA упирался в maxReplicas
	DeploymentUnavailableReplicas Signal = "deployment_unavailable_replicas" // число недоступных реплик Deployment
	Ingress5xxRatio               Signal = "ingress_5xx_ratio"               // доля ответов 5xx на Ingress
	IngressLatencyP95             Signal = "ingress_latency_p95"             // 95-й процентиль задержки Ingress, секунды
)

// Family — семейство источников метрик; наличие семейства проверяется лениво перед запросами сигналов (ADR-0032 §5).
type Family string

const (
	FamilyContainer Family = "container"  // cAdvisor (метки namespace, pod, container)
	FamilyKubeState Family = "kube_state" // kube-state-metrics v2
	FamilyNode      Family = "node"       // node-exporter (метка kubernetes_node)
	FamilyVolume    Family = "volume"     // kubelet_volume_stats_* (этап B)
	FamilyIngress   Family = "ingress"    // ingress-nginx (этап B); опционален: без контроллера — причина family_missing
)

// Reason — причина, по которой значения сигнала нет (ADR-0032 §7). Закрытый перечень.
type Reason string

const (
	ReasonNotInstalled  Reason = "not_installed"  // сервис стека не найден
	ReasonForbidden     Reason = "forbidden"      // RBAC сессии не даёт доступ к прокси
	ReasonUnavailable   Reason = "unavailable"    // таймаут, ошибка Prometheus, нет бюджета
	ReasonFamilyMissing Reason = "family_missing" // нужного семейства метрик нет
	ReasonNoSeries      Reason = "no_series"      // по цели нет серий
	ReasonHistoryShort  Reason = "history_short"  // данных за окно нет (покрытие короче окна)
	ReasonInvalidRef    Reason = "invalid_ref"    // параметр не прошёл проверку DNS-1123
)

// Subject — цель сигнала: параметры берутся только из ResourceRef цели и узлов графа. Поля, не нужные сигналу, пусты.
// Deployment, HPA и Ingress — имена объектов в Namespace (ADR-0032 §4, этап B). Тип сравним: он служит ключом Set.
type Subject struct{ Namespace, Pod, Container, Node, PVC, Deployment, HPA, Ingress string }

// Want — запрос одного сигнала по одной цели.
type Want struct {
	Signal  Signal
	Subject Subject
}

// Request — что нужно получить. At — момент операции (Context.ObservedAt): запросы вычисляются на него.
type Request struct {
	At   time.Time
	Want []Want
}

// Value — полученное значение сигнала: агрегат за окно, а не ряд. Нет данных — это Gap, а не Value со значением 0.
type Value struct {
	Signal     Signal
	Subject    Subject
	Value      float64
	Unit       string        // ratio, count, flag, seconds
	Window     time.Duration // 0 — мгновенное значение
	Agg        string        // max, min, avg, ratio, increase, last, p95
	Limit      *float64      // лимит, если провайдер его знает; сигналы этапа A не заполняют
	ObservedAt time.Time     // момент вычисления (Request.At)
}

// Gap — сигнал, значения которого нет, и причина.
type Gap struct {
	Signal  Signal
	Subject Subject
	Reason  Reason
}

// Provider выдаёт сигналы. Ошибка — только программная (nil-запрос и т. п.): любая недоступность метрик
// выражается причинами в Set, а расследование из-за неё не падает.
type Provider interface {
	Fetch(ctx context.Context, req Request) (*Set, error)
}

// Sample — одна серия мгновенного вектора.
type Sample struct {
	Labels map[string]string
	Value  float64
	At     time.Time
}

// Querier выполняет мгновенный запрос PromQL на момент at и возвращает вектор. Реализацию даёт слой связки поверх
// opsplatform.MetricsReader; ошибки приводятся к ErrNotFound, ErrForbidden, ErrUnavailable и ErrTooLarge.
type Querier interface {
	Query(ctx context.Context, expr string, at time.Time) ([]Sample, error)
}

// Ошибки Querier. Provider сравнивает их через errors.Is и не разбирает текст.
var (
	// ErrNotFound — стек (сервис Prometheus) не найден.
	ErrNotFound = errors.New("metrics stack not found")
	// ErrForbidden — RBAC сессии не даёт доступ к прокси Prometheus.
	ErrForbidden = errors.New("access to the metrics stack is forbidden")
	// ErrUnavailable — таймаут, сеть, ошибка Prometheus.
	ErrUnavailable = errors.New("metrics stack is unavailable")
	// ErrTooLarge — ответ превысил пределы размера или числа серий.
	ErrTooLarge = errors.New("metrics response is too large")
)
