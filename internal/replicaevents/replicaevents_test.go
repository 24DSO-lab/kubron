// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package replicaevents

import (
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := Message{Kind: SessionEnded, Session: "abc"}
	p, err := in.Encode("r-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p, `"user"`) || strings.Contains(p, `"team"`) {
		t.Errorf("empty fields must be omitted: %s", p)
	}
	out, err := Decode(p)
	if err != nil || out.Kind != SessionEnded || out.Session != "abc" || out.Origin != "r-1" || out.V != Version {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestDecodeRejectsGarbageAndOtherVersions(t *testing.T) {
	for _, p := range []string{"", "not json", `{"v":2,"kind":"session.ended"}`, `{"kind":"session.ended"}`} {
		if _, err := Decode(p); err == nil {
			t.Errorf("%q must be rejected", p)
		}
	}
}

func TestEncodeRejectsHugeMessages(t *testing.T) {
	if _, err := (Message{Kind: AccessChanged, Team: strings.Repeat("x", MaxPayload)}).Encode("r"); err == nil {
		t.Error("a payload above the limit must be an error")
	}
}

func TestDispatcherDropsOwnAndUnknown(t *testing.T) {
	var got []Message
	d := NewDispatcher("me", nil)
	d.Handle(SessionEnded, func(m Message) { got = append(got, m) })

	own, _ := Message{Kind: SessionEnded, Session: "s1"}.Encode("me")
	foreign, _ := Message{Kind: SessionEnded, Session: "s2"}.Encode("other")
	unknown, _ := Message{Kind: "nope"}.Encode("other")
	noHandler, _ := Message{Kind: HealthChanged}.Encode("other")

	if d.Dispatch(own) {
		t.Error("own messages must be dropped: the action was already done synchronously")
	}
	if !d.Dispatch(foreign) || len(got) != 1 || got[0].Session != "s2" {
		t.Errorf("foreign message: %+v", got)
	}
	if d.Dispatch(unknown) || d.Dispatch(noHandler) || d.Dispatch("garbage") {
		t.Error("unknown kinds, kinds without a handler and garbage must be skipped")
	}
}
