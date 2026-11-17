// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package opsplatform

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

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

// NewOp переносит Metrics шаблона: иначе подставка Op в тестах доменов теряла бы метрики.
func TestNewOpCopiesMetrics(t *testing.T) {
	m := stubMetrics{}
	op := NewOp(&Op{Metrics: m}, Hooks{})
	if op.Metrics != m {
		t.Error("NewOp did not copy Metrics")
	}
	if NewOp(&Op{}, Hooks{}).Metrics != nil {
		t.Error("an Op without metrics must keep Metrics nil")
	}
}

func TestMetricsQueriesWithoutCounter(t *testing.T) {
	var nilOp *Op
	if nilOp.MetricsQueries() != 0 || (&Op{}).MetricsQueries() != 0 || (&Op{Metrics: stubMetrics{}}).MetricsQueries() != 0 {
		t.Error("MetricsQueries must be 0 when there are no metrics or no counter")
	}
}

type stubMetrics struct{}

func (stubMetrics) Query(context.Context, string, time.Time) ([]MetricSample, error) { return nil, nil }
