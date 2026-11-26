// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"testing"

	"github.com/24DSO-lab/kubron/internal/readiness"
	"github.com/24DSO-lab/kubron/internal/readiness/readinesstest"
)

// Тот же набор контрактных тестов, что и у файловой реализации (ADR-0026 §4).
func TestReadinessBackendContract(t *testing.T) {
	readinesstest.Run(t, func(t *testing.T) readiness.Backend {
		s := newTestStore(t, keyString("k1"))
		return s.Readiness()
	})
}
