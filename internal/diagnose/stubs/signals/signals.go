// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package signals — ВРЕМЕННАЯ заглушка контракта internal/signals (docs/prompts/contracts.md §2).
//
// Существует только пока скелет D1 не слит в refactor. Нужна ядру diagnose ради типов Context.Signals и
// CollectOptions.Signals; набор запросов и Prometheus-провайдер реализует D1. Замена на настоящий пакет —
// механическая: в context.go и collect.go заменить путь импорта «internal/diagnose/stubs/signals» на
// «internal/signals» и удалить этот каталог.
package signals

import (
	"context"
	"time"
)

// Signal — имя сигнала.
type Signal string

const (
	CPUThrottlingRatio       Signal = "cpu_throttling_ratio"
	CPUUsageVsLimit          Signal = "cpu_usage_vs_limit"
	MemoryUsageVsLimit       Signal = "memory_usage_vs_limit"
	ContainerRestarts        Signal = "container_restarts"
	OOMEvents                Signal = "oom_events"
	NodeMemoryAvailableRatio Signal = "node_memory_available_ratio"
	NodeCPUUtilization       Signal = "node_cpu_utilization"
	NodeDiskAvailableRatio   Signal = "node_disk_available_ratio"
	NodePressureHistory      Signal = "node_pressure_history"
)

// Reason — причина отсутствия сигнала или стека метрик.
type Reason string

// Subject — к чему относится сигнал.
type Subject struct{ Namespace, Pod, Container, Node, PVC string }

// Want — запрошенный сигнал.
type Want struct {
	Signal  Signal
	Subject Subject
}

// Request — запрос набора сигналов на момент At.
type Request struct {
	At   time.Time
	Want []Want
}

// Value — измеренное значение.
type Value struct {
	Signal     Signal
	Subject    Subject
	Value      float64
	Unit       string
	Window     time.Duration
	Agg        string
	Limit      *float64
	ObservedAt time.Time
}

// Gap — сигнал, который получить не удалось.
type Gap struct {
	Signal  Signal
	Subject Subject
	Reason  Reason
}

// Set — результат запроса. Нулевое значение и nil безопасны: «сигналов нет».
type Set struct{}

// Get возвращает значение сигнала; nil-безопасно.
func (s *Set) Get(Signal, Subject) (Value, bool) { return Value{}, false }

// Values — все значения (отсортированы).
func (s *Set) Values() []Value { return nil }

// Gaps — все пропуски (отсортированы).
func (s *Set) Gaps() []Gap { return nil }

// StackReason — почему стек метрик недоступен; пусто — доступен.
func (s *Set) StackReason() Reason { return "" }

// PartialSource — «metrics», если стек существует, но недоступен; иначе пусто.
func (s *Set) PartialSource() string { return "" }

// Provider получает сигналы. Ошибка — только программная.
type Provider interface {
	Fetch(ctx context.Context, req Request) (*Set, error)
}
