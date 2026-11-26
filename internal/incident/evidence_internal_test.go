// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/24DSO-lab/kubron/internal/diagnose"
)

func identity() Redactor {
	return RedactorFunc(func(in []byte) (RedactResult, error) { return RedactResult{Text: string(in), Version: 1}, nil })
}

func TestQuotaCheck(t *testing.T) {
	t.Parallel()
	mk := func(n, size int) []FragmentMeta {
		out := make([]FragmentMeta, n)
		for i := range out {
			out[i] = FragmentMeta{ID: fmt.Sprint("f", i), Bytes: size}
		}
		return out
	}
	for _, tc := range []struct {
		name      string
		existing  []FragmentMeta
		replacing string
		add       int
		ok        bool
	}{
		{"empty", nil, "", 100, true},
		{"9 fragments + 1", mk(9, 100), "", 100, true},
		{"10 fragments + 1", mk(10, 100), "", 100, false},
		{"10 fragments, replace one", mk(10, 100), "f3", 100, true},
		{"byte quota", mk(8, MaxIncidentFragmentBytes/8), "", 1, false},
		{"byte quota with replacement", mk(8, MaxIncidentFragmentBytes/8), "f0", MaxIncidentFragmentBytes / 8, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := quotaCheck(tc.existing, tc.replacing, tc.add)
			if (err == nil) != tc.ok {
				t.Errorf("err = %v", err)
			}
			if err != nil {
				if r, _ := NotSavedReason(err); r != ReasonIncidentQuota {
					t.Errorf("reason = %q", r)
				}
			}
		})
	}
}

func TestLineTimes(t *testing.T) {
	t.Parallel()
	from, to := lineTimes([]string{"no time", "2026-11-01T10:00:00Z start", "x", "2026-11-01T10:05:00.5Z end", "tail"})
	if from == nil || to == nil || from.Minute() != 0 || to.Minute() != 5 {
		t.Errorf("from=%v to=%v", from, to)
	}
	if f, tt := lineTimes([]string{"a", "b"}); f != nil || tt != nil {
		t.Error("lines without timestamps must give no interval")
	}
}

func TestTruncateUTF8NeverSplitsRunes(t *testing.T) {
	t.Parallel()
	s := strings.Repeat("щ", 10) // 2 байта на руну
	for n := range 21 {
		if got := truncateUTF8(s, n); !utf8.ValidString(got) || len(got) > n {
			t.Errorf("n=%d got %q", n, got)
		}
	}
}

func TestMergeEventsCaps(t *testing.T) {
	t.Parallel()
	var old []diagnose.Event
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range MaxSnapshotEvents + 50 {
		old = append(old, diagnose.Event{Reason: "R", Message: fmt.Sprint("m", i), LastSeen: base.Add(time.Duration(i) * time.Second)})
	}
	got := mergeEvents(old, nil)
	if len(got) != MaxSnapshotEvents || got[0].Message != "m50" {
		t.Errorf("len=%d first=%q", len(got), got[0].Message)
	}
}

// FuzzBuildFragment: на любом входе фрагмент либо не сохраняется, либо укладывается в лимиты, валиден по UTF-8,
// окно содержит якорь, а хеш и метаданные согласованы.
func FuzzBuildFragment(f *testing.F) {
	f.Add([]byte("a\nb\nerror\nc\n"), 0)
	f.Add([]byte(strings.Repeat("x", 5000)+"\n"+strings.Repeat("y\n", 500)), 250)
	f.Add([]byte("\xff\xfe"), 1)
	f.Add([]byte(""), 3)
	f.Fuzz(func(t *testing.T, raw []byte, anchor int) {
		fr, err := buildFragment(raw, anchor, identity(), time.Unix(0, 0))
		if err != nil {
			if _, ok := NotSavedReason(err); !ok {
				t.Fatalf("unexpected error type: %v", err)
			}
			return
		}
		lines := strings.Split(fr.Text, "\n")
		switch {
		case !utf8.ValidString(fr.Text), len(fr.Text) > MaxFragmentBytes, len(lines) > MaxFragmentLines:
			t.Fatalf("limits: %d bytes %d lines valid=%v", len(fr.Text), len(lines), utf8.ValidString(fr.Text))
		case fr.Lines != len(lines), fr.Bytes != len(fr.Text), fr.StartLine > fr.AnchorLine, fr.EndLine < fr.AnchorLine, fr.StartLine < 1:
			t.Fatalf("metadata: %+v", fr.FragmentMeta)
		}
		for _, l := range lines {
			if len(l) > MaxLineBytes {
				t.Fatalf("line of %d bytes", len(l))
			}
		}
	})
}
