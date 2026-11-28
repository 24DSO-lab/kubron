// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package target

import (
	"slices"
	"strings"
	"time"
)

// TargetIdentity — идентичность Kubernetes-объекта (ADR-0040): ClusterID, GVK, namespace/name и UID.
// ResourceVersion сюда намеренно НЕ входит: это наблюдаемая версия, а не идентичность. Тип сравним (==) и
// пригоден как ключ карты. После delete/recreate новый UID — другая идентичность при тех же namespace/name.
type TargetIdentity struct {
	ClusterID string
	Group     string
	Version   string
	Kind      string
	Namespace string
	Name      string
	UID       string
}

func (t TargetIdentity) validate() error {
	if !validID(t.ClusterID) {
		return invalid("Target.ClusterID")
	}
	if !validAPIGroup(t.Group) || !validAPIVersion(t.Version) || !validKindName(t.Kind) {
		return invalid("Target.GVK")
	}
	if t.Namespace != "" && !validDNSLabel(t.Namespace) {
		return invalid("Target.Namespace")
	}
	if !validSegment(t.Name, maxNameLen) {
		return invalid("Target.Name")
	}
	if !validSegment(t.UID, maxUIDLen) {
		return invalid("Target.UID") // UID обязателен: namespace/name его не заменяют
	}
	return nil
}

func compareIdentity(a, b TargetIdentity) int {
	for _, p := range [][2]string{
		{a.ClusterID, b.ClusterID}, {a.Group, b.Group}, {a.Version, b.Version}, {a.Kind, b.Kind},
		{a.Namespace, b.Namespace}, {a.Name, b.Name}, {a.UID, b.UID},
	} {
		if c := strings.Compare(p[0], p[1]); c != 0 {
			return c
		}
	}
	return 0
}

// ResolvedTarget — разрешённая цель: идентичность + ОТДЕЛЬНО наблюдаемая ResourceVersion (возможный
// precondition действия, не identity).
type ResolvedTarget struct {
	Identity        TargetIdentity
	ResourceVersion string
}

// OwnerObservation — каноническое наблюдение владельца кластера из доверенного каталога на момент resolution.
type OwnerObservation struct {
	ClusterID string
	Owner     OwnerScope
}

// Outcome — исход resolution (ADR-0055). Ошибки, при которых безопасный report сформировать нельзя
// (invalid request, сбой хранилища), возвращаются как доменные ошибки, а не как Outcome.
type Outcome string

const (
	OutcomeComplete Outcome = "complete"
	OutcomePartial  Outcome = "partial"
)

// ClusterCoverage — исход по одному кластеру. Reason пуст у complete. Содержит только ClusterID из selector'а
// самой группы и счётчик: имён целей и метаданных чужих tenant'ов нет.
type ClusterCoverage struct {
	ClusterID string
	Outcome   Outcome
	Reason    ReasonCode
	Targets   int
}

// Issue — агрегированная диагностика: код причины и число затронутых элементов в кластере (без имён).
type Issue struct {
	ClusterID string
	Reason    ReasonCode
	Count     int
}

// ResolutionReport — ДИАГНОСТИЧЕСКИЙ результат resolution (может быть partial). Никогда не используется как
// executable scope: из него нельзя получить TargetSnapshot иначе, чем через NewSnapshot, который отклоняет
// любой неполный report. Targets partial-report'а — только для preview.
type ResolutionReport struct {
	GroupID       string
	GroupRevision uint64
	Owner         OwnerScope
	ResolvedAt    time.Time
	Outcome       Outcome
	Coverage      []ClusterCoverage
	Issues        []Issue
	Targets       []ResolvedTarget
	Owners        []OwnerObservation // только совместимые кластеры: чужие owner'ы в report не попадают
}

// Clone — глубокая копия report'а (срезы не разделяют память с исходным).
func (r ResolutionReport) Clone() ResolutionReport {
	r.Coverage = slices.Clone(r.Coverage)
	r.Issues = slices.Clone(r.Issues)
	r.Targets = slices.Clone(r.Targets)
	r.Owners = slices.Clone(r.Owners)
	return r
}

// IsComplete сообщает, что report полный: Outcome complete, нет issues, все кластеры покрыты.
func (r ResolutionReport) IsComplete() bool {
	if r.Outcome != OutcomeComplete || len(r.Issues) != 0 || len(r.Coverage) == 0 {
		return false
	}
	for _, c := range r.Coverage {
		if c.Outcome != OutcomeComplete {
			return false
		}
	}
	return true
}

// TargetSnapshot — immutable результат ПОЛНОГО успешного resolution (ADR-0055): у каждого target есть UID,
// все обязательные источники покрыты. Snapshot — не authorization: ownership, grants, delegation, credentials и
// Kubernetes RBAC перепроверяются будущими слоями. После публикации не изменяется.
type TargetSnapshot struct {
	ID            string
	GroupID       string
	GroupRevision uint64
	Owner         OwnerScope // владелец группы-источника
	RequestedBy   Actor      // provenance
	ResolvedAt    time.Time
	Owners        []OwnerObservation
	Coverage      []ClusterCoverage
	Targets       []ResolvedTarget
}

// NewSnapshot создаёт snapshot из report'а. Неполный report (partial, issues, непокрытый кластер) →
// ErrIncomplete: partial никогда автоматически не становится snapshot.
func NewSnapshot(r ResolutionReport, id string, requestedBy Actor) (TargetSnapshot, error) {
	if !r.IsComplete() {
		return TargetSnapshot{}, &Error{Kind: KindIncomplete}
	}
	s := TargetSnapshot{
		ID: id, GroupID: r.GroupID, GroupRevision: r.GroupRevision, Owner: r.Owner,
		RequestedBy: requestedBy, ResolvedAt: r.ResolvedAt,
		Owners: r.Owners, Coverage: r.Coverage, Targets: r.Targets,
	}.Canonical()
	if err := s.Validate(); err != nil {
		return TargetSnapshot{}, err
	}
	return s, nil
}

// Canonical возвращает глубокую каноническую копию: всё отсортировано, время UTC.
func (s TargetSnapshot) Canonical() TargetSnapshot {
	s.ResolvedAt = canonTime(s.ResolvedAt)
	s.Owners = slices.Clone(s.Owners)
	slices.SortFunc(s.Owners, func(a, b OwnerObservation) int { return strings.Compare(a.ClusterID, b.ClusterID) })
	s.Coverage = slices.Clone(s.Coverage)
	slices.SortFunc(s.Coverage, func(a, b ClusterCoverage) int { return strings.Compare(a.ClusterID, b.ClusterID) })
	s.Targets = slices.Clone(s.Targets)
	slices.SortFunc(s.Targets, func(a, b ResolvedTarget) int { return compareIdentity(a.Identity, b.Identity) })
	return s
}

// Clone — глубокая копия (хранилища обязаны отдавать копии: snapshot неизменяем).
func (s TargetSnapshot) Clone() TargetSnapshot { return s.Canonical() }

// Validate проверяет инварианты snapshot'а: полнота, UID у каждой цели, уникальность идентичностей,
// непротиворечивость coverage/owners. Не зависит от конфигурации сервера.
func (s TargetSnapshot) Validate() error {
	if !validID(s.ID) {
		return invalid("Snapshot.ID")
	}
	if !validID(s.GroupID) {
		return invalid("Snapshot.GroupID")
	}
	if s.GroupRevision == 0 {
		return invalid("Snapshot.GroupRevision")
	}
	if err := s.Owner.Validate(); err != nil {
		return err
	}
	if err := s.RequestedBy.Validate(); err != nil {
		return err
	}
	if s.ResolvedAt.IsZero() {
		return invalid("Snapshot.ResolvedAt")
	}
	if len(s.Coverage) == 0 || len(s.Coverage) > DefaultLimits().MaxClusters || len(s.Owners) != len(s.Coverage) {
		return invalid("Snapshot.Coverage")
	}
	c := s.Canonical()
	covered := map[string]struct{}{}
	for i, cov := range c.Coverage {
		if !validID(cov.ClusterID) || cov.Outcome != OutcomeComplete || cov.Reason != "" || cov.Targets < 0 {
			return invalid("Snapshot.Coverage") // snapshot — только полный результат
		}
		if i > 0 && c.Coverage[i-1].ClusterID == cov.ClusterID {
			return invalid("Snapshot.Coverage")
		}
		covered[cov.ClusterID] = struct{}{}
		o := c.Owners[i]
		if o.ClusterID != cov.ClusterID || o.Owner != s.Owner {
			return invalid("Snapshot.Owners") // наблюдаемый owner обязан совпадать с owner группы
		}
	}
	if len(c.Targets) > DefaultLimits().MaxTargets {
		return invalid("Snapshot.Targets")
	}
	perCluster := map[string]int{}
	uids := map[[2]string]struct{}{}
	names := map[[2]nameKey]struct{}{}
	for i, t := range c.Targets {
		if err := t.Identity.validate(); err != nil {
			return err
		}
		if !validResourceVersion(t.ResourceVersion) {
			return invalid("Target.ResourceVersion")
		}
		if _, ok := covered[t.Identity.ClusterID]; !ok {
			return invalid("Snapshot.Targets")
		}
		if i > 0 && compareIdentity(c.Targets[i-1].Identity, t.Identity) == 0 {
			return invalid("Snapshot.Targets") // дубликат identity
		}
		k := [2]string{t.Identity.ClusterID, t.Identity.UID}
		if _, dup := uids[k]; dup {
			return invalid("Snapshot.Targets") // один UID — одна идентичность
		}
		uids[k] = struct{}{}
		// Одно имя — один UID: иначе identity неоднозначна, а namespace/name не должны подменять UID.
		nk := [2]nameKey{{group: t.Identity.ClusterID}, {t.Identity.Group, t.Identity.Version, t.Identity.Kind, t.Identity.Namespace, t.Identity.Name}}
		if _, dup := names[nk]; dup {
			return invalid("Snapshot.Targets")
		}
		names[nk] = struct{}{}
		perCluster[t.Identity.ClusterID]++
	}
	for _, cov := range c.Coverage {
		if perCluster[cov.ClusterID] != cov.Targets {
			return invalid("Snapshot.Coverage")
		}
	}
	return nil
}
