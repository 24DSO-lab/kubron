// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness

import (
	"errors"
	"fmt"
	"slices"
)

func validStatus(t FindingType, s Status) bool {
	switch t {
	case TypeAPI:
		return s == StatusDeprecated || s == StatusRemoved
	case TypeComponent:
		return s == StatusConfirmed || s == StatusKnownLimitation || s == StatusInsufficient || s == StatusNotRecognized
	case TypePlatform:
		return s == StatusOK || s == StatusAttention
	case TypeSource:
		return s == StatusNotChecked || s == StatusPartial
	}
	return false
}

// StatusPartial — источник прочитан частично.
const StatusPartial Status = "partial"

// Validate проверяет отчёт целиком. Отчёт без заполненного coverage невалиден: его не сохраняет хранилище и не
// экспортирует экспорт (ADR-0025 §2). Ошибка оборачивает ErrInvalidReport.
func (r *Report) Validate() error {
	if err := r.validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidReport, err)
	}
	return nil
}

func (r *Report) validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema version %d", r.SchemaVersion)
	}
	if r.ID == "" || r.ClusterID == "" || r.CreatedAt.IsZero() {
		return errors.New("id, cluster and creation time are required")
	}
	if r.Current.Minor.IsZero() || r.Current.GitVersion == "" {
		return errors.New("the current Kubernetes version is required")
	}
	if err := r.Coverage.Validate(); err != nil {
		return err
	}
	if r.Coverage.TargetSpecified == r.Target.IsZero() {
		return errors.New("coverage.targetSpecified disagrees with the target version")
	}
	if r.Target.IsZero() && r.Coverage.EvaluatedAgainst.IsZero() {
		return errors.New("a report without a target must record the version it was evaluated against")
	}
	if !r.Target.IsZero() && r.Coverage.EvaluatedAgainst != r.Target {
		return errors.New("a report with a target must evaluate against it")
	}
	keys := map[string]bool{}
	for i, f := range r.Findings {
		if err := f.validate(); err != nil {
			return fmt.Errorf("finding %d: %w", i+1, err)
		}
		if keys[f.Key] {
			return fmt.Errorf("duplicate finding key %q", f.Key)
		}
		keys[f.Key] = true
	}
	for i, a := range r.Actions {
		if a.Phase != PhaseBefore && a.Phase != PhaseAfter {
			return fmt.Errorf("action %d: unknown phase %q", i+1, a.Phase)
		}
		if a.Message.Code == "" {
			return fmt.Errorf("action %d: no message", i+1)
		}
		for _, k := range a.Related {
			if !keys[k] {
				return fmt.Errorf("action %d relates to an unknown finding %q", i+1, k)
			}
		}
	}
	return nil
}

func (f *Finding) validate() error {
	if f.Key == "" {
		return errors.New("no key")
	}
	if !f.Severity.Valid() {
		return fmt.Errorf("%q: unknown severity %q", f.Key, f.Severity)
	}
	if !validStatus(f.Type, f.Status) {
		return fmt.Errorf("%q: status %q is not valid for type %q", f.Key, f.Status, f.Type)
	}
	if f.Explanation.Code == "" {
		return fmt.Errorf("%q: no explanation", f.Key)
	}
	if !slices.Contains(AllSources, f.Source) {
		return fmt.Errorf("%q: unknown data source %q", f.Key, f.Source)
	}
	if f.ObservedAt.IsZero() {
		return fmt.Errorf("%q: no observation time", f.Key)
	}
	switch f.Type {
	case TypeAPI:
		d := f.API
		if d == nil || d.APIVersion == "" || d.Kind == "" || (d.DeprecatedIn == "" && d.RemovedIn == "") {
			return fmt.Errorf("%q: an API finding needs apiVersion, kind and the lifecycle version", f.Key)
		}
		if f.Recommendation == nil {
			return fmt.Errorf("%q: an API finding needs a migration recommendation", f.Key)
		}
	case TypeComponent:
		d := f.Component
		if d == nil {
			return fmt.Errorf("%q: a component finding needs details", f.Key)
		}
		// Источник и дата матрицы видны в отчёте у каждого распознанного компонента (ADR-0025 §4).
		if f.Status != StatusNotRecognized && (d.DocsURL == "" || d.CheckedAt == "") {
			return fmt.Errorf("%q: a recognized component must carry the compatibility source and its check date", f.Key)
		}
		if f.Status == StatusConfirmed && d.Version == "" {
			return fmt.Errorf("%q: compatibility cannot be confirmed for an unknown version", f.Key)
		}
	}
	return nil
}
