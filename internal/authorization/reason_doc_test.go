// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package authorization_test

import (
	"os"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/authorization"
)

// REASONS.md — публичный контракт: каждый код описан, лишних кодов нет, коды уникальны.
func TestReasonCodesDocumented(t *testing.T) {
	doc, err := os.ReadFile("REASONS.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	seen := map[string]bool{}
	for _, c := range authorization.ReasonCodes() {
		if seen[c] {
			t.Errorf("duplicate reason code %q", c)
		}
		seen[c] = true
		if !strings.Contains(text, "| `"+c+"` |") {
			t.Errorf("reason code %q is not documented in REASONS.md", c)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "| `") {
			code := strings.Trim(strings.SplitN(line, "|", 3)[1], " `")
			if !seen[code] {
				t.Errorf("REASONS.md documents unknown code %q", code)
			}
		}
	}
}
