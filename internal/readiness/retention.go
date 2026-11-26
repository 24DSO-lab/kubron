// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness

import (
	"fmt"
	"strconv"
	"time"
)

// Переменные окружения retention (ADR-0025 §6). Пустое значение — значение по умолчанию (50 отчётов, 180 суток);
// «0» снимает соответствующее ограничение.
const (
	EnvKeepPerCluster = "KUBRON_READINESS_KEEP_PER_CLUSTER"
	EnvMaxAge         = "KUBRON_READINESS_MAX_AGE"
)

// MinMaxAge — наименьший ненулевой срок хранения: опечатка в единицах не должна стереть историю.
const MinMaxAge = 24 * time.Hour

// MaxKeepPerCluster — верхняя граница числа отчётов на кластер.
const MaxKeepPerCluster = 1000

// RetentionFromEnv читает политику хранения из окружения; getenv обычно os.Getenv.
func RetentionFromEnv(getenv func(string) string) (Retention, error) {
	r := DefaultRetention()
	if v := getenv(EnvKeepPerCluster); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > MaxKeepPerCluster {
			return Retention{}, fmt.Errorf("%s %q: must be an integer from 0 to %d", EnvKeepPerCluster, v, MaxKeepPerCluster)
		}
		r.KeepPerCluster = n
	}
	if v := getenv(EnvMaxAge); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Retention{}, fmt.Errorf("%s %q: %w", EnvMaxAge, v, err)
		}
		if d != 0 && d < MinMaxAge {
			return Retention{}, fmt.Errorf("%s %q: must be 0 (no limit) or at least 24h", EnvMaxAge, v)
		}
		r.MaxAge = d
	}
	return r, nil
}
