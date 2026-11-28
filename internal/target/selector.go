// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package target

import (
	"slices"
	"strings"
)

// ObjectRef — явная ссылка на объект-member группы. UID не входит: он определяется при resolution и
// проверяется в snapshot. Пустой Namespace — cluster-scoped объект.
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

// LabelSelector — декларативный Kubernetes label selector (matchLabels AND matchExpressions).
type LabelSelector struct {
	MatchLabels      map[string]string
	MatchExpressions []LabelRequirement
}

// FieldRequirement — равенство по полю из allowlist (AllowedFields). Других операторов и полей нет.
type FieldRequirement struct {
	Field string
	Value string
}

// AllowedFields — allowlist field selector'ов: только идентифицирующие поля metadata.
var AllowedFields = []string{"metadata.name", "metadata.namespace"}

// QuerySelector — ограниченный декларативный запрос: один GVK в явном списке кластеров. Скриптов, выражений,
// функций и внешних запросов нет.
type QuerySelector struct {
	ClusterIDs []string
	Group      string
	Version    string
	Kind       string
	Namespaces []string // пусто — все namespace'ы (результат всё равно ограничен MaxTargets)
	Labels     LabelSelector
	Fields     []FieldRequirement
}

// SelectorDefinition — декларативное определение состава группы: объединение (OR) members и queries.
// Пустое определение недопустимо: «все цели» не существует.
type SelectorDefinition struct {
	Members []ObjectRef
	Queries []QuerySelector
}

func (r ObjectRef) key() string {
	return strings.Join([]string{r.ClusterID, r.Group, r.Version, r.Kind, r.Namespace, r.Name}, "\x00")
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

func (q QuerySelector) key() string {
	var b strings.Builder
	w := func(parts ...string) {
		for _, p := range parts {
			b.WriteString(p)
			b.WriteByte(0)
		}
		b.WriteByte(1)
	}
	w(q.ClusterIDs...)
	w(q.Group, q.Version, q.Kind)
	w(q.Namespaces...)
	keys := make([]string, 0, len(q.Labels.MatchLabels))
	for k := range q.Labels.MatchLabels {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		w(k, q.Labels.MatchLabels[k])
	}
	for _, e := range q.Labels.MatchExpressions {
		w(append([]string{e.Key, string(e.Operator)}, e.Values...)...)
	}
	for _, f := range q.Fields {
		w(f.Field, f.Value)
	}
	return b.String()
}

func (q QuerySelector) validate(l Limits) error {
	if len(q.ClusterIDs) == 0 || len(q.ClusterIDs) > l.MaxClustersPerQuery {
		return limitOrInvalid(len(q.ClusterIDs) > l.MaxClustersPerQuery, "Selector.Queries.ClusterIDs")
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
			return invalidSelector("Selector.Queries.Labels")
		}
	}
	for _, e := range q.Labels.MatchExpressions {
		if !validLabelKey(e.Key) {
			return invalidSelector("Selector.Queries.Labels")
		}
		switch e.Operator {
		case OpIn, OpNotIn:
			if len(e.Values) == 0 {
				return invalidSelector("Selector.Queries.Labels")
			}
			if len(e.Values) > l.MaxValuesPerRequirement {
				return limitExceeded("Selector.Queries.Labels")
			}
		case OpExists, OpDoesNotExist:
			if len(e.Values) != 0 {
				return invalidSelector("Selector.Queries.Labels")
			}
		default:
			return invalidSelector("Selector.Queries.Labels")
		}
		for _, v := range e.Values {
			if !validLabelValue(v) {
				return invalidSelector("Selector.Queries.Labels")
			}
		}
	}
	if len(q.Fields) > l.MaxFieldRequirements {
		return limitExceeded("Selector.Queries.Fields")
	}
	for _, f := range q.Fields {
		if !slices.Contains(AllowedFields, f.Field) || !validSegment(f.Value, maxFieldValueLen) {
			return invalidSelector("Selector.Queries.Fields")
		}
	}
	return nil
}

func limitOrInvalid(limit bool, field string) error {
	if limit {
		return limitExceeded(field)
	}
	return invalidSelector(field)
}

// Validate проверяет определение: формат, allowlist, серверные пределы и стоимость (число обращений к кластерам
// не больше MaxQueryBudget). Работает на любой, не только канонической форме; не паникует на мусоре.
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
	budget := len(d.Members)
	for _, m := range d.Members {
		if err := m.validate(); err != nil {
			return err
		}
		clusters[m.ClusterID] = struct{}{}
	}
	for _, q := range d.Queries {
		if err := q.validate(l); err != nil {
			return err
		}
		own := map[string]struct{}{}
		for _, c := range q.ClusterIDs {
			clusters[c] = struct{}{}
			own[c] = struct{}{}
		}
		budget += len(own)
	}
	if len(clusters) > l.MaxClusters {
		return limitExceeded("Selector.Clusters")
	}
	if budget > l.MaxQueryBudget {
		return limitExceeded("Selector.Budget")
	}
	return nil
}

// ClusterIDs возвращает отсортированный список различных кластеров определения.
func (d SelectorDefinition) ClusterIDs() []string {
	var ids []string
	for _, m := range d.Members {
		ids = append(ids, m.ClusterID)
	}
	for _, q := range d.Queries {
		ids = append(ids, q.ClusterIDs...)
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

func sortedUniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

func (q QuerySelector) canonical() QuerySelector {
	out := QuerySelector{
		ClusterIDs: sortedUniqueStrings(q.ClusterIDs),
		Group:      q.Group, Version: q.Version, Kind: q.Kind,
		Namespaces: sortedUniqueStrings(q.Namespaces),
	}
	if len(q.Labels.MatchLabels) > 0 {
		out.Labels.MatchLabels = make(map[string]string, len(q.Labels.MatchLabels))
		for k, v := range q.Labels.MatchLabels {
			out.Labels.MatchLabels[k] = v
		}
	}
	for _, e := range q.Labels.MatchExpressions {
		out.Labels.MatchExpressions = append(out.Labels.MatchExpressions,
			LabelRequirement{Key: e.Key, Operator: e.Operator, Values: sortedUniqueStrings(e.Values)})
	}
	slices.SortFunc(out.Labels.MatchExpressions, func(a, b LabelRequirement) int {
		return strings.Compare(LabelSelector{MatchExpressions: []LabelRequirement{a}}.exprKey(),
			LabelSelector{MatchExpressions: []LabelRequirement{b}}.exprKey())
	})
	out.Labels.MatchExpressions = slices.CompactFunc(out.Labels.MatchExpressions, func(a, b LabelRequirement) bool {
		return a.Key == b.Key && a.Operator == b.Operator && slices.Equal(a.Values, b.Values)
	})
	if len(out.Labels.MatchExpressions) == 0 {
		out.Labels.MatchExpressions = nil
	}
	out.Fields = slices.Clone(q.Fields)
	slices.SortFunc(out.Fields, func(a, b FieldRequirement) int {
		if c := strings.Compare(a.Field, b.Field); c != 0 {
			return c
		}
		return strings.Compare(a.Value, b.Value)
	})
	out.Fields = slices.Compact(out.Fields)
	if len(out.Fields) == 0 {
		out.Fields = nil
	}
	return out
}

func (s LabelSelector) exprKey() string {
	var b strings.Builder
	for _, e := range s.MatchExpressions {
		b.WriteString(e.Key)
		b.WriteByte(0)
		b.WriteString(string(e.Operator))
		b.WriteByte(0)
		for _, v := range e.Values {
			b.WriteString(v)
			b.WriteByte(0)
		}
	}
	return b.String()
}

// Canonical возвращает глубокую каноническую копию: списки отсортированы и без дубликатов, пустые — nil.
// Каноническая форма единственна для семантически равных определений и не разделяет память с исходным.
func (d SelectorDefinition) Canonical() SelectorDefinition {
	var out SelectorDefinition
	out.Members = slices.Clone(d.Members)
	slices.SortFunc(out.Members, func(a, b ObjectRef) int { return strings.Compare(a.key(), b.key()) })
	out.Members = slices.Compact(out.Members)
	if len(out.Members) == 0 {
		out.Members = nil
	}
	for _, q := range d.Queries {
		out.Queries = append(out.Queries, q.canonical())
	}
	slices.SortFunc(out.Queries, func(a, b QuerySelector) int { return strings.Compare(a.key(), b.key()) })
	out.Queries = slices.CompactFunc(out.Queries, func(a, b QuerySelector) bool { return a.key() == b.key() })
	return out
}

// Equal сообщает семантическое равенство (по каноническим формам).
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
