// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package target_test

import (
	"testing"

	"github.com/24DSO-lab/kubron/internal/target"
	"github.com/24DSO-lab/kubron/internal/target/targettest"
)

// Эталонный in-memory backend обязан проходить тот же контрактный набор, что File и PostgreSQL (Lane C).
func TestMemoryBackendContract(t *testing.T) {
	targettest.Run(t, func(*testing.T) target.Backend { return targettest.NewMemoryBackend() })
}
