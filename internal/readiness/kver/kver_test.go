// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package kver

import (
	"errors"
	"testing"
)

func TestParseGitVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    Minor
		wantErr bool
	}{
		{"v1.35.2", Minor{1, 35}, false},
		{"v1.30.4-gke.1000", Minor{1, 30}, false},
		{"v1.29.2+k3s1", Minor{1, 29}, false},
		{"1.28.0-eks-abc", Minor{1, 28}, false},
		{"v1.37", Minor{1, 37}, false},
		{"", Minor{}, true},
		{"latest", Minor{}, true},
		{"v1", Minor{}, true},
		{"v1.x.2", Minor{}, true},
		{"v1.035.2", Minor{}, true},
		{"v0.35.2", Minor{}, true},
		{"v1.2.3.4", Minor{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseGitVersion(tc.in)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("want ErrInvalid, got %v", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestParseMinorStrictAboutSuffix(t *testing.T) {
	t.Parallel()
	if _, err := ParseMinor("1.35-gke"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a provider suffix is accepted only by ParseGitVersion, got %v", err)
	}
	m, err := ParseMinor("v1.35.4")
	if err != nil || m != (Minor{1, 35}) {
		t.Fatalf("got %v, %v", m, err)
	}
}

func TestParseInfoFallsBackToMajorMinor(t *testing.T) {
	t.Parallel()
	m, err := ParseInfo("", "1", "30+")
	if err != nil || m != (Minor{1, 30}) {
		t.Fatalf("got %v, %v", m, err)
	}
	if _, err := ParseInfo("garbage", "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestCompareAndDistance(t *testing.T) {
	t.Parallel()
	a, b := Minor{1, 30}, Minor{1, 35}
	if !a.Less(b) || b.Less(a) || a.Compare(a) != 0 || b.Compare(a) != 1 {
		t.Fatal("comparison is broken")
	}
	if d, ok := b.Distance(a); !ok || d != 5 {
		t.Fatalf("distance = %d, %v", d, ok)
	}
	if _, ok := (Minor{2, 0}).Distance(a); ok {
		t.Fatal("distance across majors must be undefined")
	}
}

func TestTextRoundTrip(t *testing.T) {
	t.Parallel()
	var m Minor
	if err := m.UnmarshalText([]byte("1.36")); err != nil || m != (Minor{1, 36}) {
		t.Fatalf("got %v, %v", m, err)
	}
	b, _ := m.MarshalText()
	if string(b) != "1.36" {
		t.Fatalf("got %q", b)
	}
	var z Minor
	if err := z.UnmarshalText(nil); err != nil || !z.IsZero() {
		t.Fatalf("empty text must give the zero version, got %v, %v", z, err)
	}
}

func FuzzParseGitVersion(f *testing.F) {
	for _, s := range []string{"v1.35.2", "v1.30.4-gke.1000", "", "1.2", "v1..2", "999999999999999999999.1.1", "v1.-1.0"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		m, err := ParseGitVersion(s)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("an error must wrap ErrInvalid: %v", err)
			}
			return
		}
		if m.Major < 1 || m.Minor < 0 {
			t.Fatalf("a successful parse gave an impossible version %v for %q", m, s)
		}
		back, err := ParseMinor(m.String())
		if err != nil || back != m {
			t.Fatalf("round trip failed for %q: %v, %v", s, back, err)
		}
	})
}
