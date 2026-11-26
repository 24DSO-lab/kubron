// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness

import (
	"slices"
	"strings"
)

// FindingChange — finding, изменившийся между двумя отчётами.
type FindingChange struct {
	Key      string   `json:"key"`
	Before   Finding  `json:"before"`
	After    Finding  `json:"after"`
	Severity bool     `json:"severity,omitempty"`
	Status   bool     `json:"status,omitempty"`
	Version  bool     `json:"version,omitempty"`
	Fields   []string `json:"fields,omitempty"`
}

// Diff — «что изменилось с прошлой проверки» (ADR-0025 §6).
type Diff struct {
	PreviousID string `json:"previousId"`
	CurrentID  string `json:"currentId"`

	CurrentVersionBefore string `json:"currentVersionBefore"`
	CurrentVersionAfter  string `json:"currentVersionAfter"`
	TargetBefore         string `json:"targetBefore,omitempty"`
	TargetAfter          string `json:"targetAfter,omitempty"`
	// TargetChanged — целевые версии различаются: findings двух отчётов оценены относительно разных версий,
	// и сравнение показывает разницу в постановке, а не только в кластере.
	TargetChanged bool `json:"targetChanged"`
	// MatrixChanged и CatalogChanged — отчёты построены по разным версиям матриц.
	MatrixBefore   string `json:"matrixBefore"`
	MatrixAfter    string `json:"matrixAfter"`
	MatrixChanged  bool   `json:"matrixChanged"`
	CatalogBefore  string `json:"catalogBefore"`
	CatalogAfter   string `json:"catalogAfter"`
	CatalogChanged bool   `json:"catalogChanged"`

	New       []Finding       `json:"new"`
	Resolved  []Finding       `json:"resolved"`
	Changed   []FindingChange `json:"changed"`
	Unchanged int             `json:"unchanged"`
}

// Compare сравнивает отчёт с предыдущим по ключам findings. Результат детерминирован: списки упорядочены по ключу.
func Compare(prev, cur *Report) Diff {
	d := Diff{
		PreviousID: prev.ID, CurrentID: cur.ID,
		CurrentVersionBefore: prev.Current.Minor.String(), CurrentVersionAfter: cur.Current.Minor.String(),
		TargetBefore: prev.Target.String(), TargetAfter: cur.Target.String(),
		TargetChanged: prev.Target != cur.Target,
		MatrixBefore:  prev.Coverage.APIMatrix.Version, MatrixAfter: cur.Coverage.APIMatrix.Version,
		CatalogBefore: prev.Coverage.Catalog.Version, CatalogAfter: cur.Coverage.Catalog.Version,
		New: []Finding{}, Resolved: []Finding{}, Changed: []FindingChange{},
	}
	d.MatrixChanged = d.MatrixBefore != d.MatrixAfter
	d.CatalogChanged = d.CatalogBefore != d.CatalogAfter

	before := make(map[string]Finding, len(prev.Findings))
	for _, f := range prev.Findings {
		before[f.Key] = f
	}
	seen := make(map[string]bool, len(cur.Findings))
	for _, f := range cur.Findings {
		seen[f.Key] = true
		old, ok := before[f.Key]
		if !ok {
			d.New = append(d.New, f)
			continue
		}
		if ch, differs := diffFinding(old, f); differs {
			d.Changed = append(d.Changed, ch)
		} else {
			d.Unchanged++
		}
	}
	for _, f := range prev.Findings {
		if !seen[f.Key] {
			d.Resolved = append(d.Resolved, f)
		}
	}
	byKey := func(a, b Finding) int { return strings.Compare(a.Key, b.Key) }
	slices.SortFunc(d.New, byKey)
	slices.SortFunc(d.Resolved, byKey)
	slices.SortFunc(d.Changed, func(a, b FindingChange) int { return strings.Compare(a.Key, b.Key) })
	return d
}

func diffFinding(a, b Finding) (FindingChange, bool) {
	ch := FindingChange{Key: a.Key, Before: a, After: b}
	ch.Severity = a.Severity != b.Severity
	ch.Status = a.Status != b.Status
	if a.Component != nil && b.Component != nil {
		ch.Version = a.Component.Version != b.Component.Version
		if a.Component.State != b.Component.State {
			ch.Fields = append(ch.Fields, "state")
		}
		if a.Component.NodeCount != b.Component.NodeCount {
			ch.Fields = append(ch.Fields, "nodeCount")
		}
	}
	if a.API != nil && b.API != nil {
		if a.API.ResourceCount != b.API.ResourceCount {
			ch.Fields = append(ch.Fields, "resourceCount")
		}
		if a.API.RemovedIn != b.API.RemovedIn {
			ch.Fields = append(ch.Fields, "removedIn")
		}
	}
	return ch, ch.Severity || ch.Status || ch.Version || len(ch.Fields) > 0
}
