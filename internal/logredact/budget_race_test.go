// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

//go:build race

package logredact

import "time"

// adversarialBudget — предел времени на один враждебный вход. Детектор гонок замедляет regexp примерно в 20 раз,
// поэтому бюджет шире; катастрофический откат всё равно укладывается в минуты и часы и будет пойман.
const adversarialBudget = 120 * time.Second
