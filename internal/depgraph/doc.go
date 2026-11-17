// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package depgraph — граф зависимостей ресурсов Kubernetes для диагностики (ADR-0031 §1–3, §7).
//
// Граф строится от цели расследования (Pod, Deployment, ReplicaSet, Service, PVC, Node), ограниченно и по снимку:
// без informers и кэша между вызовами. Пакет не знает про платформу, хранилище и диагностику; чтение кластера идёт
// только через узкий интерфейс Reader. Secret не читается никогда: ссылка на него — вершина Unknown/policy.
// Владелец пакета — D2.
//
// # Что строится
//
// Вверх: Pod → ReplicaSet → Deployment (а также StatefulSet, DaemonSet, Job → CronJob) по ownerReferences и
// Pod ← Service по селектору. Вниз и вбок: Pod → PVC → PV и StorageClass, Pod → Node, Node → Pod'ы на ней,
// Service → EndpointSlice. Для цели добавляются дети и потребители: Deployment → ReplicaSet → Pod, Service → Pod,
// PVC ← Pod. Расширения этапа 2 (Ingress, HPA) выключены; при включении флага граф только предупреждает
// «not_implemented:…». Содержимое ConfigMap отбрасывается при чтении (SanitizeConfigMap).
//
// # Пределы (ADR-0031 §3)
//
// DefaultLimits: 200 вершин, 600 рёбер, глубина 4, 40 запросов чтения. Решения реализации: выборка до 100 объектов,
// из найденных списком Pod'ов дальше раскрываются первые 5, Service над одним Pod'ом — до 10, EndpointSlice — до 10.
// Любое усечение выставляет Truncated и код в Warnings.
//
// # Коды предупреждений (Graph.Warnings)
//
//	limit.nodes, limit.edges, limit.depth, limit.reads, limit.services — достигнут предел
//	list_truncated:<Kind>     — выборка не уместилась в 100 объектов
//	forbidden:<Kind>          — RBAC запретил чтение; unavailable:<Kind>, too_large:<Kind> — аналогично
//	canceled                  — отмена контекста; граф частичный
//	not_implemented:ingress|hpa — включён флаг расширения этапа 2
//
// # Коды причин состояния (Node.Reason, Edge.Reason)
//
// Pod: terminating, completed, failed, evicted, oom_killed, crash_loop, image_pull_failed, config_error,
// init_failed, container_failed, pending_unscheduled, pending, container_creating, not_ready.
// ReplicaSet/Deployment/StatefulSet/DaemonSet: scaled_to_zero, paused, replicas_unavailable, replica_failure,
// rollout_in_progress, progress_deadline_exceeded, not_available. Job/CronJob: job_failed, running, suspended.
// PVC/PV: unbound, lost, released, failed, pending, waiting_first_consumer, no_default_storageclass.
// Service: selector_matches_nothing, no_endpoints, no_ready_endpoints. Node: node_not_ready, node_unreachable,
// node_pressure_memory, node_pressure_disk, node_pressure_pid, node_network_unavailable, node_unschedulable.
// Прочее: not_found (Missing), unreadable (объект не разобран), default_class (StorageClass выбран по умолчанию).
//
// У вершины Unknown причина — в Node.Unknown: forbidden, unavailable, too_large, limit, policy.
//
// # Состояние рёбер
//
// Вычисляется после построения по итоговому здоровью вершин: ok; broken — обе стороны есть, но связь не работает
// (Pod не Ready за Service, PVC не привязан, Node не Ready); missing — цель ссылки не найдена; unknown — прочитать
// не удалось. Для owns «целью ссылки» считается владелец.
package depgraph
