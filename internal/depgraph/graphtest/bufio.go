// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package graphtest

import (
	"bufio"
	"strings"
)

func bufioOf(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }
