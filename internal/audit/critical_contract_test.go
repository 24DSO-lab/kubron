// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"testing"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/audit/criticaltest"
)

func TestFileCriticalRecorder_Contract(t *testing.T) {
	criticaltest.Run(t, func(t *testing.T) audit.CriticalRecorder {
		r, err := audit.OpenCriticalFile(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Close() })
		return r
	})
}
