// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import "testing"

func TestDefaultLimitsAndInheritance(t *testing.T) {
	m := NewManager()
	if got := m.Limits(); got.LogStreams != logMaxStreams || got.Terminals != 0 || got.PortForwards != 0 {
		t.Fatalf("default desktop limits: %+v", got)
	}
	m.SetLimits(DefaultLimits())
	v := m.ForSession()
	if v.Limits() != (Limits{Terminals: 8, LogStreams: 20, PortForwards: 10}) {
		t.Fatalf("the view did not inherit the limits: %+v", v.Limits())
	}
	// Виды независимы: терминалы и логи у каждого свои.
	if v.terminalSessions == m.terminalSessions {
		t.Error("the view must have its own terminal manager")
	}
}

func TestLogStreamsLimitIsPerView(t *testing.T) {
	var a, b logStreams
	noop := func() *logEntry { return &logEntry{cancel: func() {}} }
	for _, id := range []string{"1", "2"} {
		if !a.add(id, noop(), 2) {
			t.Fatalf("stream %s must be accepted", id)
		}
	}
	if a.add("3", noop(), 2) {
		t.Error("a third stream over the limit of 2 must be rejected")
	}
	if !a.add("1", noop(), 2) {
		t.Error("replacing a stream with the same id does not take up the limit")
	}
	if !b.add("x", noop(), 2) {
		t.Error("one view's limit must not affect another")
	}
	if !a.add("4", noop(), 0) {
		t.Error("0 means unlimited")
	}
}

func TestTerminalTryAddLimit(t *testing.T) {
	tsm := NewTerminalSessionManager()
	if !tsm.tryAdd(&TerminalSession{ID: "a"}, 2) || !tsm.tryAdd(&TerminalSession{ID: "b"}, 2) {
		t.Fatal("the first two terminals must be accepted")
	}
	if tsm.tryAdd(&TerminalSession{ID: "c"}, 2) {
		t.Error("the third terminal is over the limit")
	}
	if !tsm.tryAdd(&TerminalSession{ID: "c"}, 0) || tsm.count() != 3 {
		t.Error("0 means unlimited")
	}
	var nilTSM *TerminalSessionManager
	if nilTSM.count() != 0 {
		t.Error("count of a nil manager")
	}
}

func TestValidStreamID(t *testing.T) {
	for id, want := range map[string]bool{
		"log_6f1c2d3e-aaaa-bbbb-cccc-0123456789ab":                   true,
		"kubron-0_ab12cd34.log_6f1c2d3e-aaaa-bbbb-cccc-0123456789ab": true, // с префиксом реплики (ADR-0016)
		"": false, "a b": false, "logs:data:1": false, "../x": false,
		"line\nbreak": false,
	} {
		if validStreamID(id) != want {
			t.Errorf("validStreamID(%q) != %v", id, want)
		}
	}
	long := make([]byte, 129)
	for i := range long {
		long[i] = 'a'
	}
	if validStreamID(string(long)) {
		t.Error("an id longer than 128 characters must be rejected")
	}
}
