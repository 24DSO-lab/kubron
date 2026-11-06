// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCloseCancelsOnlyTheSessionView(t *testing.T) {
	root := NewManager()
	view := root.ForSession()
	other := root.ForSession()

	rootCtx, viewCtx, otherCtx := root.baseCtx(), view.baseCtx(), other.baseCtx()
	view.Close()

	if err := viewCtx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("the closed view must be cancelled, got %v", err)
	}
	if rootCtx.Err() != nil || otherCtx.Err() != nil {
		t.Fatalf("closing a view must not touch the root or other views: %v %v", rootCtx.Err(), otherCtx.Err())
	}
	view.Close() // повторный вызов безопасен
}

func TestRootViewIsNeverCancelled(t *testing.T) {
	m := NewManager()
	m.Close()
	if err := m.baseCtx().Err(); err != nil {
		t.Fatalf("the root view has no cancellation, got %v", err)
	}
}

func TestClosingTheRootCancelsItsViews(t *testing.T) {
	root := NewManager()
	root.base, root.closeBase = context.WithCancel(context.Background())
	view := root.ForSession()
	root.Close()
	if err := view.baseCtx().Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("views derive from the root context, got %v", err)
	}
}

func TestSessionViewTimeoutContextEndsWithTheView(t *testing.T) {
	view := NewManager().ForSession()
	child, cancel := context.WithTimeout(view.baseCtx(), time.Hour)
	defer cancel()
	view.Close()
	if err := child.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("a request context must end with the view, got %v", err)
	}
}
