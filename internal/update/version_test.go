// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package update

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"1.2.0", "1.3.0", true},
		{"1.2.0", "v1.3.0", true},
		{"v1.2.0", "1.2.1", true},
		{"1.2.0", "2.0.0", true},
		{"1.2.0", "1.2.0", false},
		{"1.3.0", "1.2.0", false},
		// 1.10 новее 1.9: сравнение числовое, не строковое
		{"1.9.0", "1.10.0", true},
		{"1.10.0", "1.9.0", false},
		// предрелиз младше релиза
		{"1.3.0-rc.1", "1.3.0", true},
		{"1.3.0", "1.3.0-rc.1", false},
		{"1.3.0-rc.1", "1.3.0-rc.2", true},
		// метаданные сборки на порядок не влияют
		{"1.3.0+abc", "1.3.0+def", false},
		// сборка из исходников и мусор: предлагать нечего
		{"dev", "1.3.0", false},
		{"", "1.3.0", false},
		{"1.2.0", "latest", false},
		{"1.2.0", "", false},
		// неполные версии не принимаются: тег релиза обязан быть полной версией
		{"1.2", "1.3.0", false},
		{"1.2.0", "1.3", false},
	}
	for _, tc := range cases {
		if got := IsNewer(tc.current, tc.latest); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestNormalizeVersion(t *testing.T) {
	for in, want := range map[string]string{"1.2.3": "1.2.3", "v1.2.3": "1.2.3", " V1.2.3-rc.1 ": "1.2.3-rc.1"} {
		got, ok := NormalizeVersion(in)
		if !ok || got != want {
			t.Errorf("NormalizeVersion(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "dev", "1.2", "1", "v", "1.2.3; rm -rf /", "1.2.3 4", "01.2.3"} {
		if got, ok := NormalizeVersion(bad); ok {
			t.Errorf("NormalizeVersion(%q) = %q, want failure", bad, got)
		}
	}
}

func TestImageTagDropsBuildMetadata(t *testing.T) {
	if got := imageTag("1.3.0+build.5"); got != "1.3.0" {
		t.Errorf("imageTag = %q", got)
	}
	if got := imageTag("1.3.0-rc.1"); got != "1.3.0-rc.1" {
		t.Errorf("imageTag keeps the pre-release: %q", got)
	}
}
