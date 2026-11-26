// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package signals

import (
	"reflect"
	"testing"
	"time"
)

func TestNilSetIsSafe(t *testing.T) {
	var s *Set
	if _, ok := s.Get(CPUThrottlingRatio, Subject{}); ok {
		t.Error("nil Set returned a value")
	}
	if _, ok := s.GapOf(CPUThrottlingRatio, Subject{}); ok {
		t.Error("nil Set returned a gap")
	}
	if s.Values() != nil || s.Gaps() != nil {
		t.Error("nil Set returned non-empty lists")
	}
	if s.StackReason() != "" || s.PartialSource() != "" {
		t.Error("nil Set must look like an available stack")
	}
}

func TestNewSetOrderIsDeterministic(t *testing.T) {
	a := Subject{Namespace: "ns", Pod: "a", Container: "c"}
	b := Subject{Namespace: "ns", Pod: "b", Container: "c"}
	n := Subject{Node: "n1"}
	values := []Value{
		{Signal: OOMEvents, Subject: b, Value: 1},
		{Signal: ContainerRestarts, Subject: b, Value: 2},
		{Signal: ContainerRestarts, Subject: a, Value: 3},
		{Signal: NodeCPUUtilization, Subject: n, Value: 0.4},
	}
	gaps := []Gap{
		{Signal: MemoryUsageVsLimit, Subject: b, Reason: ReasonNoSeries},
		{Signal: CPUThrottlingRatio, Subject: a, Reason: ReasonHistoryShort},
	}
	want := NewSet("", values, gaps)
	// Любая перестановка входа даёт тот же порядок выхода.
	rev := NewSet("", []Value{values[3], values[2], values[1], values[0]}, []Gap{gaps[1], gaps[0]})
	if !reflect.DeepEqual(want.Values(), rev.Values()) || !reflect.DeepEqual(want.Gaps(), rev.Gaps()) {
		t.Fatalf("order depends on input order:\n%v\n%v", want.Values(), rev.Values())
	}
	got := want.Values()
	order := []Signal{ContainerRestarts, ContainerRestarts, NodeCPUUtilization, OOMEvents}
	for i, v := range got {
		if v.Signal != order[i] {
			t.Fatalf("Values()[%d] = %s, want %s", i, v.Signal, order[i])
		}
	}
	if got[0].Subject.Pod != "a" || got[1].Subject.Pod != "b" {
		t.Errorf("subjects of one signal are not sorted: %v", got[:2])
	}
	if g := want.Gaps(); g[0].Signal != CPUThrottlingRatio || g[1].Signal != MemoryUsageVsLimit {
		t.Errorf("Gaps() order = %v", g)
	}
}

func TestSetValueBeatsGap(t *testing.T) {
	sub := Subject{Node: "n1"}
	s := NewSet("", []Value{{Signal: NodeCPUUtilization, Subject: sub, Value: 0.5, ObservedAt: time.Unix(1, 0)}},
		[]Gap{{Signal: NodeCPUUtilization, Subject: sub, Reason: ReasonNoSeries}})
	if _, ok := s.Get(NodeCPUUtilization, sub); !ok {
		t.Fatal("the value is missing")
	}
	if len(s.Gaps()) != 0 {
		t.Errorf("a gap stayed next to the value: %v", s.Gaps())
	}
}

// Нет данных — не ноль: нулевое значение отличимо от отсутствия.
func TestSetDistinguishesZeroFromMissing(t *testing.T) {
	sub := Subject{Node: "n1"}
	s := NewSet("", []Value{{Signal: NodePressureHistory, Subject: sub, Value: 0}}, nil)
	if v, ok := s.Get(NodePressureHistory, sub); !ok || v.Value != 0 {
		t.Errorf("a real zero was lost: %v %v", v, ok)
	}
	if _, ok := s.Get(NodeCPUUtilization, sub); ok {
		t.Error("a signal that was never fetched must be absent, not zero")
	}
}

func TestPartialSource(t *testing.T) {
	for _, tc := range []struct {
		stack Reason
		want  string
	}{
		{"", ""},
		{ReasonNotInstalled, ""},
		{ReasonForbidden, ""},
		{ReasonUnavailable, "metrics"},
	} {
		if got := NewSet(tc.stack, nil, nil).PartialSource(); got != tc.want {
			t.Errorf("PartialSource() with stack %q = %q, want %q", tc.stack, got, tc.want)
		}
	}
	// Отказ отдельного запроса существующего стека тоже делает операцию частичной; пустые серии и короткая
	// история — нет.
	sub := Subject{Node: "n1"}
	for _, tc := range []struct {
		reason Reason
		want   string
	}{
		{ReasonUnavailable, "metrics"},
		{ReasonNoSeries, ""},
		{ReasonHistoryShort, ""},
		{ReasonFamilyMissing, ""},
		{ReasonForbidden, ""},
		{ReasonInvalidRef, ""},
	} {
		set := NewSet("", nil, []Gap{{Signal: NodeCPUUtilization, Subject: sub, Reason: tc.reason}})
		if got := set.PartialSource(); got != tc.want {
			t.Errorf("PartialSource() with a %q gap = %q, want %q", tc.reason, got, tc.want)
		}
	}
}
