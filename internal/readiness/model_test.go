// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness_test

import (
	"errors"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/readiness"
	"github.com/24DSO-lab/kubron/internal/readiness/kver"
	"github.com/24DSO-lab/kubron/internal/readiness/readinesstest"
)

func valid() *readiness.Report {
	return readinesstest.NewReport("c1", "rep-00000001", readinesstest.Base)
}

func TestValidReportPasses(t *testing.T) {
	t.Parallel()
	if err := valid().Validate(); err != nil {
		t.Fatal(err)
	}
}

// Отчёт без заполненного coverage невалиден (ADR-0025 §2): «не проверено» — часть структуры данных.
func TestCoverageIsMandatory(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(r *readiness.Report)
	}{
		{"empty coverage", func(r *readiness.Report) { r.Coverage = readiness.Coverage{} }},
		{"no mode", func(r *readiness.Report) { r.Coverage.Mode = "" }},
		{"no evaluated version", func(r *readiness.Report) { r.Coverage.EvaluatedAgainst = kver.Minor{} }},
		{"no limitations", func(r *readiness.Report) { r.Coverage.Limitations = nil }},
		{"provider checks not stated", func(r *readiness.Report) {
			r.Coverage.Limitations = []readiness.Message{{Code: readiness.LimitNotAGuarantee}}
		}},
		{"no matrix version", func(r *readiness.Report) { r.Coverage.APIMatrix.Version = "" }},
		{"no matrix date", func(r *readiness.Report) { r.Coverage.APIMatrix.GeneratedAt = "" }},
		{"no matrix window", func(r *readiness.Report) { r.Coverage.APIMatrix.Window = nil }},
		{"no catalog version", func(r *readiness.Report) { r.Coverage.Catalog.Version = "" }},
		{"no catalog date", func(r *readiness.Report) { r.Coverage.Catalog.CheckedAt = "" }},
		{"no sources", func(r *readiness.Report) { r.Coverage.Sources = nil }},
		{"unchecked source omitted", func(r *readiness.Report) { r.Coverage.Sources = r.Coverage.Sources[:2] }},
		{"source listed twice", func(r *readiness.Report) {
			r.Coverage.Sources = append(r.Coverage.Sources, r.Coverage.Sources[0])
		}},
		{"unchecked source without reason", func(r *readiness.Report) { r.Coverage.Sources[1].Reason = nil }},
		{"unchecked source claims methods", func(r *readiness.Report) { r.Coverage.Sources[1].Methods = []string{"x"} }},
		{"checked source without time", func(r *readiness.Report) { r.Coverage.Sources[0].ObservedAt = time.Time{} }},
		{"checked source without methods", func(r *readiness.Report) { r.Coverage.Sources[0].Methods = nil }},
		{"checked source claims completeness", func(r *readiness.Report) { r.Coverage.Sources[0].Limitations = nil }},
		{"target disagrees with coverage", func(r *readiness.Report) { r.Coverage.TargetSpecified = false }},
		{"evaluated against another version", func(r *readiness.Report) {
			r.Coverage.EvaluatedAgainst = kver.Minor{Major: 1, Minor: 37}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := valid()
			tt.mutate(r)
			err := r.Validate()
			if !errors.Is(err, readiness.ErrInvalidReport) {
				t.Fatalf("err = %v, want ErrInvalidReport", err)
			}
		})
	}
	if err := func() error { r := valid(); r.Coverage = readiness.Coverage{}; return r.Validate() }(); !errors.Is(err, readiness.ErrCoverage) {
		t.Errorf("an empty coverage must report ErrCoverage, got %v", err)
	}
}

func TestReportWithoutTargetIsEvaluatedAgainstCurrent(t *testing.T) {
	t.Parallel()
	r := valid()
	r.Target = kver.Minor{}
	r.Coverage.TargetSpecified = false
	r.Coverage.EvaluatedAgainst = r.Current.Minor
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.Coverage.EvaluatedAgainst = kver.Minor{}
	if err := r.Validate(); err == nil {
		t.Fatal("a report without a target must record the version it was evaluated against")
	}
}

func TestFindingValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(f *readiness.Finding)
	}{
		{"no key", func(f *readiness.Finding) { f.Key = "" }},
		{"bad severity", func(f *readiness.Finding) { f.Severity = "fatal" }},
		{"status of another type", func(f *readiness.Finding) { f.Status = readiness.StatusConfirmed }},
		{"no explanation", func(f *readiness.Finding) { f.Explanation = readiness.Message{} }},
		{"unknown source", func(f *readiness.Finding) { f.Source = "magic" }},
		{"no observation time", func(f *readiness.Finding) { f.ObservedAt = time.Time{} }},
		{"api without detail", func(f *readiness.Finding) { f.API = nil }},
		{"api without recommendation", func(f *readiness.Finding) { f.Recommendation = nil }},
		{"api without lifecycle version", func(f *readiness.Finding) { f.API.RemovedIn = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := valid()
			tt.mutate(&r.Findings[0])
			if err := r.Validate(); !errors.Is(err, readiness.ErrInvalidReport) {
				t.Fatalf("err = %v, want ErrInvalidReport", err)
			}
		})
	}
}

// Совместимость не может быть «подтверждена» для неизвестной версии, а распознанный компонент обязан называть
// источник и дату матрицы.
func TestComponentFindingRules(t *testing.T) {
	t.Parallel()
	mk := func(status readiness.Status, d readiness.ComponentDetail) *readiness.Report {
		r := valid()
		r.Findings = append(r.Findings, readiness.Finding{
			Key: "component/x", Type: readiness.TypeComponent, Status: status, Severity: readiness.SeverityInfo,
			Explanation: readiness.Message{Code: "c"}, Source: readiness.SourceLiveAPI, ObservedAt: readinesstest.Base,
			Component: &d,
		})
		return r
	}
	docs := readiness.ComponentDetail{ID: "x", DocsURL: "https://example.org", CheckedAt: "2026-11-11"}
	if err := mk(readiness.StatusInsufficient, docs).Validate(); err != nil {
		t.Errorf("insufficient data with a source: %v", err)
	}
	if err := mk(readiness.StatusNotRecognized, readiness.ComponentDetail{Name: "x"}).Validate(); err != nil {
		t.Errorf("not recognized needs no source: %v", err)
	}
	if err := mk(readiness.StatusConfirmed, docs).Validate(); err == nil {
		t.Error("confirmed without a detected version must be rejected")
	}
	docs.Version = "1.2.3"
	if err := mk(readiness.StatusConfirmed, docs).Validate(); err != nil {
		t.Errorf("confirmed with a version: %v", err)
	}
	if err := mk(readiness.StatusKnownLimitation, readiness.ComponentDetail{ID: "x", Version: "1"}).Validate(); err == nil {
		t.Error("a recognized component without the matrix source and date must be rejected")
	}
}

func TestDuplicateKeysAndActions(t *testing.T) {
	t.Parallel()
	r := valid()
	r.Findings = append(r.Findings, r.Findings[0])
	if err := r.Validate(); err == nil {
		t.Error("duplicate finding keys must be rejected")
	}
	r = valid()
	r.Actions[0].Related = []string{"nope"}
	if err := r.Validate(); err == nil {
		t.Error("an action related to an unknown finding must be rejected")
	}
	r = valid()
	r.Actions[0].Phase = "later"
	if err := r.Validate(); err == nil {
		t.Error("unknown action phase must be rejected")
	}
}

func TestSummaryCounts(t *testing.T) {
	t.Parallel()
	r := valid()
	r.Findings = append(r.Findings,
		readiness.Finding{Key: "w", Type: readiness.TypeSource, Status: readiness.StatusNotChecked, Severity: readiness.SeverityWarning,
			Explanation: readiness.Message{Code: "x"}, Source: readiness.SourceLiveAPI, ObservedAt: readinesstest.Base},
		readiness.Finding{Key: "i", Type: readiness.TypePlatform, Status: readiness.StatusOK, Severity: readiness.SeverityInfo,
			Explanation: readiness.Message{Code: "x"}, Source: readiness.SourceLiveAPI, ObservedAt: readinesstest.Base})
	s := r.Summary()
	if s.Blockers != 1 || s.Warnings != 1 || s.Infos != 1 {
		t.Errorf("counts = %d/%d/%d", s.Blockers, s.Warnings, s.Infos)
	}
	// Одна находка «не проверено» и два непроверенных источника (GitOps, локальные файлы).
	if s.NotChecked != 3 {
		t.Errorf("NotChecked = %d, want 3", s.NotChecked)
	}
	if s.CurrentVersion != "1.35" || s.TargetVersion != "1.36" {
		t.Errorf("versions = %s → %s", s.CurrentVersion, s.TargetVersion)
	}
}

func TestCompare(t *testing.T) {
	t.Parallel()
	prev, cur := valid(), valid()
	prev.ID, cur.ID = "rep-00000001", "rep-00000002"
	mkf := func(key string, sev readiness.Severity, st readiness.Status) readiness.Finding {
		return readiness.Finding{Key: key, Type: readiness.TypePlatform, Status: st, Severity: sev,
			Explanation: readiness.Message{Code: "x"}, Source: readiness.SourceLiveAPI, ObservedAt: readinesstest.Base}
	}
	prev.Findings = []readiness.Finding{mkf("same", "info", "ok"), mkf("gone", "warning", "attention"), mkf("worse", "warning", "attention")}
	cur.Findings = []readiness.Finding{mkf("same", "info", "ok"), mkf("new", "blocker", "attention"), mkf("worse", "blocker", "attention")}
	cur.Target = kver.Minor{Major: 1, Minor: 37}
	cur.Coverage.EvaluatedAgainst = cur.Target
	cur.Coverage.APIMatrix.Version = "k8s-1.99-x"

	d := readiness.Compare(prev, cur)
	if d.PreviousID != prev.ID || d.CurrentID != cur.ID || d.Unchanged != 1 {
		t.Fatalf("diff = %+v", d)
	}
	if len(d.New) != 1 || d.New[0].Key != "new" || len(d.Resolved) != 1 || d.Resolved[0].Key != "gone" {
		t.Fatalf("new/resolved = %+v / %+v", d.New, d.Resolved)
	}
	if len(d.Changed) != 1 || d.Changed[0].Key != "worse" || !d.Changed[0].Severity {
		t.Fatalf("changed = %+v", d.Changed)
	}
	if !d.TargetChanged || !d.MatrixChanged || d.CatalogChanged {
		t.Fatalf("target/matrix/catalog flags = %v %v %v", d.TargetChanged, d.MatrixChanged, d.CatalogChanged)
	}
	// Сравнение с самим собой ничего не находит.
	same := readiness.Compare(cur, cur)
	if len(same.New)+len(same.Resolved)+len(same.Changed) != 0 || same.Unchanged != len(cur.Findings) {
		t.Errorf("self-compare = %+v", same)
	}
}

func TestCursorRoundTripAndPaging(t *testing.T) {
	t.Parallel()
	c := readiness.Cursor{CreatedAt: readinesstest.Base, ID: "rep-00000001"}
	got, ok, err := readiness.DecodeCursor(readiness.EncodeCursor(c))
	if err != nil || !ok || !got.CreatedAt.Equal(c.CreatedAt) || got.ID != c.ID {
		t.Fatalf("round trip = %+v %v %v", got, ok, err)
	}
	if _, ok, err := readiness.DecodeCursor(""); ok || err != nil {
		t.Fatalf("empty cursor = %v %v", ok, err)
	}
	for _, in := range []int{-5, 0, 1, 50, 1000} {
		if n := readiness.PageSize(in); n < 1 || n > readiness.MaxPageSize {
			t.Errorf("PageSize(%d) = %d", in, n)
		}
	}
}

func FuzzDecodeCursor(f *testing.F) {
	f.Add("")
	f.Add(readiness.EncodeCursor(readiness.Cursor{CreatedAt: readinesstest.Base, ID: "rep-00000001"}))
	f.Add("MTI6Li4v")
	f.Add("!!!")
	f.Fuzz(func(t *testing.T, s string) {
		c, ok, err := readiness.DecodeCursor(s)
		if err != nil {
			if !errors.Is(err, readiness.ErrBadCursor) {
				t.Fatalf("unexpected error type: %v", err)
			}
			return
		}
		if ok && !readiness.ValidID(c.ID) {
			t.Fatalf("a decoded cursor carries an unsafe id %q", c.ID)
		}
	})
}
