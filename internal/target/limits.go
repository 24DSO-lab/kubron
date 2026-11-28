// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package target

import "time"

// Limits — серверные пределы (ADR-0055 п.10). Клиент не может их повысить: ResolveOptions только сужает их, а
// сама конфигурация не может превышать жёсткие потолки DefaultLimits (Validate).
type Limits struct {
	MaxMembers              int           // явных ссылок в selector
	MaxQueries              int           // декларативных запросов в selector
	MaxClusters             int           // различных кластеров в selector
	MaxClustersPerQuery     int           // кластеров в одном запросе
	MaxNamespacesPerQuery   int           // namespace'ов в одном запросе
	MaxLabelRequirements    int           // matchLabels + matchExpressions в одном запросе
	MaxValuesPerRequirement int           // значений в одном In/NotIn
	MaxFieldRequirements    int           // field selector'ов в одном запросе
	MaxSharedWith           int           // записей sharing
	MaxQueryBudget          int           // обращений к ClusterReader за одно resolution
	MaxTargets              int           // целей в одном resolution
	MaxListPage             int           // групп на странице List
	Timeout                 time.Duration // общий timeout одного resolution
}

// DefaultLimits возвращает значения по умолчанию, они же — жёсткие потолки.
func DefaultLimits() Limits {
	return Limits{
		MaxMembers:              256,
		MaxQueries:              32,
		MaxClusters:             32,
		MaxClustersPerQuery:     32,
		MaxNamespacesPerQuery:   64,
		MaxLabelRequirements:    16,
		MaxValuesPerRequirement: 32,
		MaxFieldRequirements:    2,
		MaxSharedWith:           64,
		MaxQueryBudget:          512,
		MaxTargets:              5000,
		MaxListPage:             200,
		Timeout:                 30 * time.Second,
	}
}

// Validate проверяет, что каждый предел положителен и не превышает жёсткий потолок.
func (l Limits) Validate() error {
	c := DefaultLimits()
	pairs := []struct {
		name     string
		got, max int
	}{
		{"MaxMembers", l.MaxMembers, c.MaxMembers},
		{"MaxQueries", l.MaxQueries, c.MaxQueries},
		{"MaxClusters", l.MaxClusters, c.MaxClusters},
		{"MaxClustersPerQuery", l.MaxClustersPerQuery, c.MaxClustersPerQuery},
		{"MaxNamespacesPerQuery", l.MaxNamespacesPerQuery, c.MaxNamespacesPerQuery},
		{"MaxLabelRequirements", l.MaxLabelRequirements, c.MaxLabelRequirements},
		{"MaxValuesPerRequirement", l.MaxValuesPerRequirement, c.MaxValuesPerRequirement},
		{"MaxFieldRequirements", l.MaxFieldRequirements, c.MaxFieldRequirements},
		{"MaxSharedWith", l.MaxSharedWith, c.MaxSharedWith},
		{"MaxQueryBudget", l.MaxQueryBudget, c.MaxQueryBudget},
		{"MaxTargets", l.MaxTargets, c.MaxTargets},
		{"MaxListPage", l.MaxListPage, c.MaxListPage},
	}
	for _, p := range pairs {
		if p.got <= 0 || p.got > p.max {
			return invalid("Limits." + p.name)
		}
	}
	if l.Timeout <= 0 || l.Timeout > c.Timeout*4 {
		return invalid("Limits.Timeout")
	}
	return nil
}

// ResolveOptions — ограниченные параметры resolution от клиента. Нулевое значение означает «серверный предел».
// Значение больше серверного не отклоняется, а сужается до серверного: повысить предел нельзя.
type ResolveOptions struct {
	MaxTargets int
	Timeout    time.Duration
}

// effective вычисляет фактические пределы: min(запрошенное > 0, серверное). Отрицательные значения — ошибка.
func (l Limits) effective(o ResolveOptions) (maxTargets int, timeout time.Duration, err error) {
	if o.MaxTargets < 0 {
		return 0, 0, invalid("Options.MaxTargets")
	}
	if o.Timeout < 0 {
		return 0, 0, invalid("Options.Timeout")
	}
	maxTargets, timeout = l.MaxTargets, l.Timeout
	if o.MaxTargets > 0 && o.MaxTargets < maxTargets {
		maxTargets = o.MaxTargets
	}
	if o.Timeout > 0 && o.Timeout < timeout {
		timeout = o.Timeout
	}
	return maxTargets, timeout, nil
}
