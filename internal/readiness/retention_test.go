// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness_test

import (
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/readiness"
)

func TestRetentionFromEnv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     map[string]string
		want    readiness.Retention
		wantErr bool
	}{
		{"defaults", nil, readiness.Retention{KeepPerCluster: 50, MaxAge: 180 * 24 * time.Hour}, false},
		{"custom", map[string]string{readiness.EnvKeepPerCluster: "10", readiness.EnvMaxAge: "720h"}, readiness.Retention{KeepPerCluster: 10, MaxAge: 720 * time.Hour}, false},
		{"zero removes a limit", map[string]string{readiness.EnvKeepPerCluster: "0", readiness.EnvMaxAge: "0"}, readiness.Retention{}, false},
		{"negative count", map[string]string{readiness.EnvKeepPerCluster: "-1"}, readiness.Retention{}, true},
		{"count too large", map[string]string{readiness.EnvKeepPerCluster: "100000"}, readiness.Retention{}, true},
		{"count garbage", map[string]string{readiness.EnvKeepPerCluster: "many"}, readiness.Retention{}, true},
		{"age too short", map[string]string{readiness.EnvMaxAge: "1h"}, readiness.Retention{}, true},
		{"age garbage", map[string]string{readiness.EnvMaxAge: "soon"}, readiness.Retention{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := readiness.RetentionFromEnv(func(k string) string { return tt.env[k] })
			if (err != nil) != tt.wantErr || (!tt.wantErr && got != tt.want) {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}
