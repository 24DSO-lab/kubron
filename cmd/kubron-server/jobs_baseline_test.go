// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/baseline"
)

func TestBaselineRetentionFromEnv(t *testing.T) {
	t.Parallel()
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	r, err := baselineRetentionFromEnv(get(nil))
	if err != nil || r.Results != 90*24*time.Hour || r.MinVersions != baseline.MinVersionsKept {
		t.Fatalf("defaults: %+v %v", r, err)
	}
	r, err = baselineRetentionFromEnv(get(map[string]string{"KUBRON_BASELINE_RESULTS_RETENTION_DAYS": "30", "KUBRON_BASELINE_MIN_VERSIONS": "50"}))
	if err != nil || r.Results != 30*24*time.Hour || r.MinVersions != 50 {
		t.Fatalf("custom: %+v %v", r, err)
	}
	for name, env := range map[string]map[string]string{
		"days zero":        {"KUBRON_BASELINE_RESULTS_RETENTION_DAYS": "0"},
		"days text":        {"KUBRON_BASELINE_RESULTS_RETENTION_DAYS": "90d"},
		"versions too low": {"KUBRON_BASELINE_MIN_VERSIONS": "5"},
		"versions text":    {"KUBRON_BASELINE_MIN_VERSIONS": "many"},
	} {
		if _, err := baselineRetentionFromEnv(get(env)); err == nil {
			t.Errorf("%s: a wrong value must be an error, not a silent default", name)
		}
	}
}
