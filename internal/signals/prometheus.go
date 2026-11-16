// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package signals

import "context"

// Option настраивает Provider поверх Prometheus. Публичных опций пока нет: пределы ADR-0032 §6 заданы кодом.
type Option func(*prometheus)

// prometheus — реализация Provider поверх встроенного стека мониторинга Kubron.
type prometheus struct {
	q Querier
}

// NewPrometheus создаёт Provider поверх встроенного Prometheus. Скелет: возвращает пустой Set (стек доступен,
// сигналов нет); реализация — следующим PR.
func NewPrometheus(q Querier, opts ...Option) Provider {
	p := &prometheus{q: q}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Fetch — заглушка скелета.
func (p *prometheus) Fetch(context.Context, Request) (*Set, error) {
	return NewSet("", nil, nil), nil
}
