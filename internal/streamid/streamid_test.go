// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package streamid

import (
	"errors"
	"strings"
	"testing"
)

func TestNewCarriesOwner(t *testing.T) {
	id := New("kubron-0_ab12cd34")
	if o, ok := Owner(id); !ok || o != "kubron-0_ab12cd34" {
		t.Fatalf("owner of %q = %q %v", id, o, ok)
	}
	if rest := strings.SplitN(id, ".", 2)[1]; len(rest) != 32 {
		t.Fatalf("random part must be 128 bits in hex, got %q", rest)
	}
	if New("a") == New("a") {
		t.Fatal("ids must be unique")
	}
}

func TestNewWithoutReplica(t *testing.T) {
	id := New("")
	if strings.Contains(id, ".") || len(id) != 32 {
		t.Fatalf("got %q", id)
	}
}

func TestCheck(t *testing.T) {
	mine, other := New("a"), New("b")
	cases := []struct {
		name, self, id string
		elsewhere      bool
	}{
		{"own stream", "a", mine, false},
		{"foreign replica", "a", other, true},
		{"no prefix with HA", "a", "0123456789abcdef", true},
		{"empty id", "a", "", true},
		{"prefix of a longer name is not a match", "a", New("ab"), true},
		{"single replica accepts anything", "", other, false},
	}
	for _, c := range cases {
		err := Check(c.self, c.id)
		if got := errors.Is(err, ErrElsewhere); got != c.elsewhere {
			t.Errorf("%s: err=%v, want elsewhere=%v", c.name, err, c.elsewhere)
		}
	}
}
