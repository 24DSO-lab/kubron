// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/readiness"
	"github.com/24DSO-lab/kubron/internal/readiness/readinesstest"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

func TestExportContainsCoverage(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	rep, err := svc.Run(t.Context(), req(componentCluster(t), "1.36"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := readiness.ExportJSON(rep)
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	var inner map[string]json.RawMessage
	_ = json.Unmarshal(env["report"], &inner)
	if _, ok := inner["coverage"]; !ok || !strings.Contains(string(env["format"]), readiness.ExportFormatID) {
		t.Fatalf("JSON export lacks coverage or format: %s", raw)
	}
	md, err := readiness.ExportMarkdown(rep)
	if err != nil {
		t.Fatal(err)
	}
	s := string(md)
	for _, want := range []string{"## Coverage", "NOT CHECKED", "generic Kubernetes", "API matrix", "Component catalog", "## Blockers"} {
		if !strings.Contains(s, want) {
			t.Errorf("Markdown lacks %q", want)
		}
	}
	if strings.Index(s, "## Coverage") > strings.Index(s, "## Blockers") {
		t.Error("coverage must precede findings")
	}
	got, err := svc.Export(t.Context(), readinessScope(), rep.ID, readiness.FormatMarkdown)
	if err != nil || got.MIME != "text/markdown" || !strings.HasSuffix(got.Filename, ".md") {
		t.Fatalf("Export = %+v, %v", got, err)
	}
	if _, err := svc.Export(t.Context(), readinessScope(), rep.ID, "pdf"); !errors.Is(err, readiness.ErrUnknownFormat) {
		t.Errorf("err = %v", err)
	}
}

// Экспорт без coverage невозможен ни в одном формате.
func TestExportWithoutCoverageIsImpossible(t *testing.T) {
	t.Parallel()
	rep := valid()
	rep.Coverage = readiness.Coverage{}
	if _, err := readiness.ExportJSON(rep); !errors.Is(err, readiness.ErrCoverage) {
		t.Errorf("JSON err = %v", err)
	}
	if _, err := readiness.ExportMarkdown(rep); !errors.Is(err, readiness.ErrCoverage) {
		t.Errorf("Markdown err = %v", err)
	}
	if _, err := readiness.ExportReport(rep, readiness.FormatJSON); !errors.Is(err, readiness.ErrInvalidReport) {
		t.Errorf("ExportReport err = %v", err)
	}
	if _, err := readiness.ExportJSON(nil); err == nil {
		t.Error("nil report")
	}
}

func readinessScope() tenant.Scope { return readinesstest.Scope("c1") }
