// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestAnchorLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		lines []string
		want  int
	}{
		{"empty log", nil, -1},
		{"only blank lines", []string{"", "  ", "\t"}, -1},
		{"last error wins", []string{"ok", "ERROR one", "ok", "panic: two", "bye"}, 3},
		{"no error → last non-empty line", []string{"a", "b", "c", ""}, 2},
		{"single line", []string{"x"}, 0},
		{"out of memory", []string{"start", "java.lang.OutOfMemoryError: out of memory", "exit"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := AnchorLine(tc.lines); got != tc.want {
				t.Errorf("AnchorLine = %d, want %d", got, tc.want)
			}
		})
	}
}

func FuzzAnchorLine(f *testing.F) {
	f.Add("a\nb\nerror here\nc")
	f.Add("")
	f.Add("\x00\xff\n\n")
	f.Fuzz(func(t *testing.T, s string) {
		lines := strings.Split(s, "\n")
		i := AnchorLine(lines)
		if i < -1 || i >= len(lines) {
			t.Fatalf("index %d out of range for %d lines", i, len(lines))
		}
		if i >= 0 && strings.TrimSpace(lines[i]) == "" {
			t.Fatalf("anchor on a blank line %d", i)
		}
	})
}

func TestNormalizeEvent(t *testing.T) {
	t.Parallel()
	t1 := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	t2 := t1.Add(10 * time.Minute)
	for _, tc := range []struct {
		name      string
		in        *corev1.Event
		wantFirst time.Time
		wantLast  time.Time
		wantCount int32
	}{
		{"legacy timestamps", &corev1.Event{FirstTimestamp: metav1.NewTime(t1), LastTimestamp: metav1.NewTime(t2), Count: 3}, t1, t2, 3},
		{"eventTime only", &corev1.Event{EventTime: metav1.NewMicroTime(t2)}, t2, t2, 1},
		{"series wins", &corev1.Event{FirstTimestamp: metav1.NewTime(t1),
			Series: &corev1.EventSeries{Count: 9, LastObservedTime: metav1.NewMicroTime(t2)}}, t1, t2, 9},
		{"creation fallback", &corev1.Event{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(t1)}}, t1, t1, 1},
		{"no time at all", &corev1.Event{}, time.Time{}, time.Time{}, 1},
		{"nil", nil, time.Time{}, time.Time{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := NormalizeEvent(tc.in)
			if !got.FirstSeen.Equal(tc.wantFirst) || !got.LastSeen.Equal(tc.wantLast) || got.Count != tc.wantCount {
				t.Errorf("got %+v", got)
			}
		})
	}
}

func FuzzNormalizeEvent(f *testing.F) {
	f.Add([]byte(`{"kind":"Event","reason":"X","count":2,"lastTimestamp":"2026-01-01T00:00:00Z"}`))
	f.Add([]byte(`{"count":"oops"}`))
	f.Add([]byte(`{"series":{"count":-5},"involvedObject":null}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		e, err := decode[corev1.Event](&unstructured.Unstructured{Object: m})
		if err != nil {
			return // нечитаемое событие пропускается сборщиком
		}
		if ev := NormalizeEvent(e); ev.Count < 1 {
			t.Fatalf("count %d < 1", ev.Count)
		}
	})
}

// FuzzAnalyzePod: правила не паникуют на произвольном Pod и сохраняют инварианты отчёта.
func FuzzAnalyzePod(f *testing.F) {
	f.Add([]byte(`{"metadata":{"name":"p","namespace":"n"},"status":{"phase":"Pending"}}`))
	f.Add([]byte(`{"metadata":{"name":"p","namespace":"n"},"status":{"containerStatuses":[{"name":"a","restartCount":9,` +
		`"state":{"waiting":{"reason":"CrashLoopBackOff"}},"lastState":{"terminated":{"reason":"OOMKilled","exitCode":137}}}]}}`))
	f.Add([]byte(`{"metadata":{"name":"p","namespace":"n"},"spec":{"containers":[{"name":"a","resources":{"limits":{"memory":"1"}}}]}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		p, err := decode[corev1.Pod](&unstructured.Unstructured{Object: m})
		if err != nil {
			return
		}
		c := &Context{Target: ResourceRef{Kind: "Pod", Namespace: "n", Name: "p"}, ObservedAt: fixedNow, Pods: []*corev1.Pod{p},
			Nodes: map[string]*corev1.Node{}}
		checkInvariants(t, Analyze(c))
	})
}
