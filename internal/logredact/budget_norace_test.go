// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

//go:build !race

package logredact

import "time"

// adversarialBudget — предел времени на один враждебный вход (без детектора гонок вход в 1 МиБ занимает около секунды).
const adversarialBudget = 10 * time.Second
