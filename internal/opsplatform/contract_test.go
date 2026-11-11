// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package opsplatform

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/24DSO-lab/kubron/internal/auth"
)

func TestOpRequireFailsClosedWithoutHook(t *testing.T) {
	t.Parallel()
	op := NewOp(&Op{}, Hooks{})
	if err := op.Require(auth.PermDiagnosticsLogs); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("Require without hook = %v, want ErrForbidden", err)
	}
}

func TestOpRequireDelegates(t *testing.T) {
	t.Parallel()
	op := NewOp(&Op{}, Hooks{Require: func(p auth.Permission) error {
		if p == auth.PermRead {
			return nil
		}
		return auth.ErrForbidden
	}})
	if err := op.Require(auth.PermRead); err != nil {
		t.Fatalf("Require(read) = %v", err)
	}
	if err := op.Require(auth.PermBaselineManage); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("Require(baseline.manage) = %v", err)
	}
}

func TestOpEndIsIdempotent(t *testing.T) {
	t.Parallel()
	var calls int
	op := NewOp(&Op{}, Hooks{End: func(error, ...string) { calls++ }})
	op.End(nil)
	op.End(errors.New("late"))
	if calls != 1 {
		t.Fatalf("End hook called %d times, want 1", calls)
	}
}

func TestOpNilHooksAreSafe(t *testing.T) {
	t.Parallel()
	op := NewOp(&Op{}, Hooks{})
	op.Audit("x", "y", nil, slog.String("k", "v"))
	op.Notify("e", "id")
	op.End(nil)
}

func TestNewOpCopiesFieldsNotState(t *testing.T) {
	t.Parallel()
	base := &Op{Initiator: "u", SessionID: "s", CorrelationID: "c"}
	base.End(nil) // у шаблона без hooks закрытие ничего не пишет, но флаг ставится
	op := NewOp(base, Hooks{})
	if op.Initiator != "u" || op.SessionID != "s" || op.CorrelationID != "c" {
		t.Fatalf("fields not copied: %+v", op)
	}
	var calls int
	op = NewOp(base, Hooks{End: func(error, ...string) { calls++ }})
	op.End(nil)
	if calls != 1 {
		t.Fatalf("closing state leaked from the template: calls=%d", calls)
	}
}
