// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"bufio"
	"bytes"
)

type bufioR = bufio.Reader

func newBufio(raw []byte) *bufioR { return bufio.NewReader(bytes.NewReader(raw)) }
