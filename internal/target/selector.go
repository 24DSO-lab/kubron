// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package target

import (
	"slices"
	"strings"
)

// ObjectRef — явная ссылка на объект-член группы. UID не задаётся: он обязан быть получен при resolution.
// Namespace пуст для cluster-scoped объектов.
type ObjectRef struct {
	ClusterID string
	Group     string
	Version   string
	Kind      string
	Namespace string
	Name      string
}

// LabelOperator — оператор label selector'а.
type LabelOperator string

const (
	OpIn           LabelOperator = "In"
	OpNotIn        LabelOperator = "NotIn"
	OpExists       LabelOperator = "Exists"
	OpDoesNotExist LabelOperator = "DoesNotExist"
)

// LabelRequirement — одно требование matchExpressions.
type LabelRequirement struct {
	Key      string
	Operator LabelOperator
	Values   []string
}

// LabelSelector — декларативный Kubernetes label selector. Пустой допустим (совпадает со всем в пределах
// остальных ограничений запроса и серверных пределов).
type LabelSelector struct {
	MatchLabels      map[string]string
	MatchExpressions []LabelRequirement
}

// AllowedFieldSelectors — allowlist field selector'ов (только равенство).
var AllowedFieldSelectors = []string{"metadata.name", "metadata.namespace"}

// FieldRequirement — равенство поля из allowlist'а.
type FieldRequirement struct {
	Field string
	Value string
}

// QuerySelector — декларативный запрос по одному GVK в явном наборе кластеров. Скриптов, выражений, внешних
// запросов и «всех кластеров» нет: ClusterIDs обязательны. Пустые Namespaces означают «все namespace'ы» внутри
// серверных пределов (MaxTargets, query budget).
type QuerySelector struct {
	ClusterIDs []string
	Group      string
	Version    string
	Kind       string
	Namespaces []string
	Labels     LabelSelector
	Fields     []FieldRequirement
}

// SelectorDefinition — декларативное описание состава группы: явные члены и/или запросы. Пустое описание
// недопустимо (никакого «всё» по умолчанию).
type SelectorDefinition struct {
	Members []ObjectRef
	Queries []QuerySelector
}

func (r ObjectRef) validate() error {
	if !validID(r.ClusterID) {
		return invalidSelector("Selector.Members.ClusterID")
	}
	if !validAPIGroup(r.Group) {
		return invalidSelector("Selector.Members.Group")
	}
	if !validAPIVersion(r.Version) {
		return invalidSelector("Selector.Members.Version")
	}
	if !validKindName(r.Kind) {
		return invalidSelector("Selector.Members.Kind")
	}
	if r.Namespace != "" && !validDNSLabel(r.Namespace) {
		return invalidSelector("Selector.Members.Namespace")
	}
	if !validSegment(r.Name, maxNameLen) {
		return invalidSelector("Selector.Members.Name")
	}
	return nil
}

func (q QuerySelector) validate(l Limits) error {
	if len(q.ClusterIDs) == 0 {
		return invalidSelector("Selector.Queries.ClusterIDs")
	}
	if len(q.ClusterIDs) > l.MaxClustersPerQuery {
		return limitExceeded("Selector.Queries.ClusterIDs")
	}
	for _, c := range q.ClusterIDs {
		if !validID(c) {
			return invalidSelector("Selector.Queries.ClusterIDs")
		}
	}
	if !validAPIGroup(q.Group) {
		return invalidSelector("Selector.Queries.Group")
	}
	if !validAPIVersion(q.Version) {
		return invalidSelector("Selector.Queries.Version")
	}
	if !validKindName(q.Kind) {
		return invalidSelector("Selector.Queries.Kind")
	}
	if len(q.Namespaces) > l.MaxNamespacesPerQuery {
		return limitExceeded("Selector.Queries.Namespaces")
	}
	for _, n := range q.Namespaces {
		if !validDNSLabel(n) {
			return invalidSelector("Selector.Queries.Namespaces")
		}
	}
	if len(q.Labels.MatchLabels)+len(q.Labels.MatchExpressions) > l.MaxLabelRequirements {
		return limitExceeded("Selector.Queries.Labels")
	}
	for k, v := range q.Labels.MatchLabels {
		if !validLabelKey(k) || !validLabelValue(v) {
			return invalidSelector("Selector.Queries.Labels.MatchLabels")
		}
	}
	for _, e := range q.Labels.MatchExpressions {
		if !validLabelKey(e.Key) {
			return invalidSelector("Selector.Queries.Labels.MatchExpressions")
		}
		switch e.Operator {
		case OpIn, OpNotIn:
			if len(e.Values) == 0 {
				return invalidSelector("Selector.Queries.Labels.MatchExpressions")
			}
			if len(e.Values) > l.MaxValuesPerRequirement {
				return limitExceeded("Selector.Queries.Labels.MatchExpressions")
			}
			for _, v := range e.Values {
				if !validLabelValue(v) {
					return invalidSelector("Selector.Queries.Labels.MatchExpressions")
				}
			}
		case OpExists, OpDoesNotExist:
			if len(e.Values) != 0 {
				return invalidSelector("Selector.Queries.Labels.MatchExpressions")
			}
		default:
			return invalidSelector("Selector.Queries.Labels.MatchExpressions")
		}
	}
	if len(q.Fields) > l.MaxFieldRequirements {
		return limitExceeded("Selector.Queries.Fields")
	}
	for _, f := range q.Fields {
		if !slices.Contains(AllowedFieldSelectors, f.Field) || !validSegment(f.Value, maxFieldValueLen) {
			return invalidSelector("Selector.Queries.Fields")
		}
	}
	return nil
}

// Validate выполняет каноническую валидацию на любой (не только канонической) форме и проверяет серверные
// пределы. Выполняется до сохранения и до resolution; запросы без явных кластеров и неограниченные выражения
// отвергаются.
func (d SelectorDefinition) Validate(l Limits) error {
	if len(d.Members) == 0 && len(d.Queries) == 0 {
		return invalidSelector("Selector")
	}
	if len(d.Members) > l.MaxMembers {
		return limitExceeded("Selector.Members")
	}
	if len(d.Queries) > l.MaxQueries {
		return limitExceeded("Selector.Queries")
	}
	clusters := map[string]struct{}{}
	budget := 0
	seenMembers := map[ObjectRef]struct{}{}
	for _, m := range d.Members {
		if err := m.validate(); err != nil {
			return err
		}
		clusters[m.ClusterID] = struct{}{}
		if _, dup := seenMembers[m]; !dup {
			seenMembers[m] = struct{}{}
			budget++
		}
	}
	for _, q := range d.Queries {
		if err := q.validate(l); err != nil {
			return err
		}
		distinct := map[string]struct{}{}
		for _, c := range q.ClusterIDs {
			clusters[c] = struct{}{}
			distinct[c] = struct{}{}
		}
		budget += len(distinct)
	}
	if len(clusters) > l.MaxClusters {
		return limitExceeded("Selector.Clusters")
	}
	if budget > l.MaxQueryBudget {
		return limitExceeded("Selector.QueryBudget")
	}
	return nil
}

// ClusterIDs возвращает все кластеры, упомянутые selector'ом: отсортированы, без дубликатов.
func (d SelectorDefinition) ClusterIDs() []string {
	var ids []string
	for _, m := range d.Members {
		ids = append(ids, m.ClusterID)
	}
	for _, q := range d.Queries {
		ids = append(ids, q.ClusterIDs...)
	}
	return sortedUnique(ids, strings.Compare)
}

func (r ObjectRef) key() string {
	return strings.Join([]string{r.ClusterID, r.Group, r.Version, r.Kind, r.Namespace, r.Name}, "\x00")
}

func (q QuerySelector) key() string {
	var b strings.Builder
	b.WriteString(strings.Join(q.ClusterIDs, "\x01"))
	b.WriteString("\x00" + q.Group + "\x00" + q.Version + "\x00" + q.Kind + "\x00")
	b.WriteString(strings.Join(q.Namespaces, "\x01"))
	b.WriteString("\x00")
	keys := make([]string, 0, len(q.Labels.MatchLabels))
	for k := range q.Labels.MatchLabels {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		b.WriteString(k + "=" + q.Labels.MatchLabels[k] + "\x01")
	}
	b.WriteString("\x00")
	for _, e := range q.Labels.MatchExpressions {
		b.WriteString(e.key() + "\x01")
	}
	b.WriteString("\x00")
	for _, f := range q.Fields {
		b.WriteString(f.Field + "=" + f.Value + "\x01")
	}
	return b.String()
}

func (e LabelRequirement) key() string {
	return e.Key + "\x02" + string(e.Operator) + "\x02" + strings.Join(e.Values, "\x03")
}

// sortedUnique — отсортированная копия без дубликатов; nil для пустого входа (единая каноническая форма).
func sortedUnique[T any](in []T, cmp func(a, b T) int) []T {
	if len(in) == 0 {
		return nil
	}
	out := slices.Clone(in)
	slices.SortFunc(out, cmp)
	return slices.CompactFunc(out, func(a, b T) bool { return cmp(a, b) == 0 })
}

func (q QuerySelector) canonical() QuerySelector {
	out := QuerySelector{
		ClusterIDs: sortedUnique(q.ClusterIDs, strings.Compare),
		Group:      q.Group, Version: q.Version, Kind: q.Kind,
		Namespaces: sortedUnique(q.Namespaces, strings.Compare),
	}
	if len(q.Labels.MatchLabels) > 0 {
		out.Labels.MatchLabels = make(map[string]string, len(q.Labels.MatchLabels))
		for k, v := range q.Labels.MatchLabels {
			out.Labels.MatchLabels[k] = v
		}
	}
	if len(q.Labels.MatchExpressions) > 0 {
		exprs := make([]LabelRequirement, 0, len(q.Labels.MatchExpressions))
		for _, e := range q.Labels.MatchExpressions {
			exprs = append(exprs, LabelRequirement{
				Key: e.Key, Operator: e.Operator, Values: sortedUnique(e.Values, strings.Compare),
			})
		}
		out.Labels.MatchExpressions = sortedUnique(exprs, func(a, b LabelRequirement) int {
			return strings.Compare(a.key(), b.key())
		})
	}
	if len(q.Fields) > 0 {
		out.Fields = sortedUnique(q.Fields, func(a, b FieldRequirement) int {
			if c := strings.Compare(a.Field, b.Field); c != 0 {
				return c
			}
			return strings.Compare(a.Value, b.Value)
		})
	}
	return out
}

// Canonical возвращает глубокую каноническую копию: члены и запросы отсортированы и без дубликатов, списки
// внутри запросов отсортированы. Идемпотентна; не разделяет память с исходным значением. Одинаковые по смыслу
// определения имеют одинаковую каноническую форму (Equal).
func (d SelectorDefinition) Canonical() SelectorDefinition {
	var out SelectorDefinition
	if len(d.Members) > 0 {
		out.Members = sortedUnique(d.Members, func(a, b ObjectRef) int { return strings.Compare(a.key(), b.key()) })
	}
	if len(d.Queries) > 0 {
		qs := make([]QuerySelector, 0, len(d.Queries))
		for _, q := range d.Queries {
			qs = append(qs, q.canonical())
		}
		slices.SortFunc(qs, func(a, b QuerySelector) int { return strings.Compare(a.key(), b.key()) })
		out.Queries = slices.CompactFunc(qs, func(a, b QuerySelector) bool { return a.key() == b.key() })
	}
	return out
}

// Equal сравнивает определения по каноническим формам.
func (d SelectorDefinition) Equal(o SelectorDefinition) bool {
	a, b := d.Canonical(), o.Canonical()
	if len(a.Members) != len(b.Members) || len(a.Queries) != len(b.Queries) {
		return false
	}
	for i := range a.Members {
		if a.Members[i] != b.Members[i] {
			return false
		}
	}
	for i := range a.Queries {
		if a.Queries[i].key() != b.Queries[i].key() {
			return false
		}
	}
	return true
}
