// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/readiness/kver"
)

func m(minor int) kver.Minor { return kver.Minor{Major: 1, Minor: minor} }

func mustCatalog(t *testing.T) *Catalog {
	t.Helper()
	c, err := Default()
	if err != nil {
		t.Fatalf("the embedded catalog is broken: %v", err)
	}
	return c
}

func TestEmbeddedCatalog(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	for _, comp := range c.Components {
		if !strings.HasPrefix(comp.Docs, "https://") {
			t.Errorf("%s: no link to the official compatibility documentation", comp.ID)
		}
		if comp.CheckedAt == "" {
			t.Errorf("%s: no check date", comp.ID)
		}
	}
	info := c.Info()
	if info.Entries != len(c.Components) || info.Version == "" || info.OldestCheck == "" || info.CheckedAt == "" {
		t.Errorf("catalog origin is not recorded: %+v", info)
	}
	if len(c.Components) > 20 {
		t.Errorf("the catalog is meant to stay small (ADR-0025 §4), got %d entries", len(c.Components))
	}
}

func TestEvaluateNeverInventsCompatibility(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	ms, _ := c.ByID("metrics-server")
	ing, _ := c.ByID("ingress-nginx")
	dns, _ := c.ByID("coredns")

	tests := []struct {
		name  string
		comp  *Component
		ver   string
		k8s   kver.Minor
		want  Status
		code  string
		sever Severity
	}{
		{"metrics-server 0.9 on a listed version", ms, "v0.9.1", m(35), StatusConfirmed, "", SeverityInfo},
		{"metrics-server 0.7 below the stated minimum", ms, "0.7.2", m(26), StatusInsufficient, CodeNoMatchingRule, SeverityWarning},
		{"metrics-server 0.3 beyond its documented range", ms, "v0.3.7", m(30), StatusKnownLimitation, "metrics-server.beyond-matrix", SeverityWarning},
		{"metrics-server 0.3 inside its documented range", ms, "v0.3.7", m(20), StatusConfirmed, "", SeverityInfo},
		{"ingress-nginx tested on the target", ing, "v1.15.1", m(35), StatusConfirmed, "", SeverityInfo},
		{"ingress-nginx newer than the tested row", ing, "v1.15.1", m(36), StatusInsufficient, CodeNoMatchingRule, SeverityWarning},
		{"ingress-nginx series outside the encoded rows", ing, "v1.9.6", m(35), StatusInsufficient, CodeNoMatchingRule, SeverityWarning},
		{"component without rules", dns, "v1.11.3", m(36), StatusInsufficient, CodeNoRules, SeverityWarning},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, ok := ParseVersion(tc.ver)
			if !ok {
				t.Fatalf("test version %q is not parsed", tc.ver)
			}
			got := tc.comp.Evaluate(v, true, tc.k8s)
			if got.Status != tc.want || got.Code != tc.code || got.Severity != tc.sever {
				t.Errorf("got %+v, want status=%s code=%q severity=%s", got, tc.want, tc.code, tc.sever)
			}
		})
	}

	t.Run("unknown version is insufficient data", func(t *testing.T) {
		t.Parallel()
		got := ms.Evaluate(Version{}, false, m(35))
		if got.Status != StatusInsufficient || got.Code != CodeVersionUnknown {
			t.Errorf("got %+v", got)
		}
	})
}

func TestEvaluateSkew(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	kubelet, _ := c.ByID("kubelet")
	tests := []struct {
		name         string
		kubelet, tgt kver.Minor
		want         Status
		code         string
		sever        Severity
	}{
		{"same version", m(35), m(35), StatusConfirmed, CodeSkewWithin, SeverityInfo},
		{"three behind is allowed", m(32), m(35), StatusConfirmed, CodeSkewWithin, SeverityInfo},
		{"four behind breaks the policy", m(31), m(35), StatusKnownLimitation, CodeSkewTooOld, SeverityBlocker},
		{"newer than the API server", m(36), m(35), StatusKnownLimitation, CodeSkewNewer, SeverityBlocker},
		{"before 1.25 only two behind are allowed", m(22), m(25), StatusKnownLimitation, CodeSkewTooOld, SeverityBlocker},
		{"before 1.25 two behind is allowed", m(23), m(25), StatusConfirmed, CodeSkewWithin, SeverityInfo},
		{"unknown target", m(30), kver.Minor{}, StatusInsufficient, CodeSkewUnavailable, SeverityWarning},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := kubelet.EvaluateSkew(tc.kubelet, tc.tgt)
			if got.Status != tc.want || got.Code != tc.code || got.Severity != tc.sever {
				t.Errorf("got %+v", got)
			}
		})
	}
	ms, _ := c.ByID("metrics-server")
	if got := ms.EvaluateSkew(m(30), m(35)); got.Status != StatusInsufficient {
		t.Errorf("a component without a skew rule must not be judged by it: %+v", got)
	}
}

func TestMatch(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	tests := []struct {
		name string
		w    Workload
		want string // пустая строка — не распознан
	}{
		{"by name in its namespace", Workload{"Deployment", "kube-system", "metrics-server", nil}, "metrics-server"},
		{"by image behind a mirror", Workload{"Deployment", "monitoring", "ms", []string{"mirror.local/proxy/registry.k8s.io/metrics-server/metrics-server:v0.7.2"}}, ""},
		{"by image in a catalog namespace", Workload{"Deployment", "kube-system", "ms-custom", []string{"mirror.local/proxy/registry.k8s.io/metrics-server/metrics-server:v0.7.2"}}, "metrics-server"},
		{"wrong kind", Workload{"StatefulSet", "kube-system", "metrics-server", nil}, ""},
		{"name in a foreign namespace", Workload{"Deployment", "default", "metrics-server", nil}, ""},
		{"daemonset component", Workload{"DaemonSet", "kube-system", "kube-proxy", []string{"registry.k8s.io/kube-proxy:v1.30.4"}}, "kube-proxy"},
		{"unknown workload", Workload{"Deployment", "kube-system", "my-agent", []string{"example.com/agent:1.0"}}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			comp, ok := c.Match(tc.w)
			if tc.want == "" {
				if ok {
					t.Fatalf("must not be recognized, got %s", comp.ID)
				}
				return
			}
			if !ok || comp.ID != tc.want {
				t.Fatalf("got %v %v, want %s", comp, ok, tc.want)
			}
		})
	}
}

func TestComponentVersionFromImage(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	dns, _ := c.ByID("coredns")
	w := func(img string) Workload { return Workload{"Deployment", "kube-system", "coredns", []string{img}} }

	v, tag, ok := dns.ComponentVersion(w("registry.k8s.io/coredns/coredns:v1.11.3"))
	if !ok || v != (Version{1, 11, 3}) || tag != "v1.11.3" {
		t.Errorf("got %v %q %v", v, tag, ok)
	}
	if _, _, ok := dns.ComponentVersion(w("registry.k8s.io/coredns/coredns@sha256:abcdef")); ok {
		t.Error("an image pinned only by digest has no known version")
	}
	if _, tag, ok := dns.ComponentVersion(w("registry.k8s.io/coredns/coredns:latest")); ok || tag != "latest" {
		t.Errorf("a non-version tag must stay unknown but be reported, got %q %v", tag, ok)
	}
}

func TestParseVersion(t *testing.T) {
	t.Parallel()
	good := map[string]Version{
		"v0.7.2": {0, 7, 2}, "1.11.1-eks-1-31-1": {1, 11, 1}, "0.7": {0, 7, 0}, "v1.2.3+build5": {1, 2, 3},
	}
	for in, want := range good {
		if got, ok := ParseVersion(in); !ok || got != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "latest", "1.2.3.4", "v1", "sha256:abc", "1.2.x", "v1.2.3rc1", "main"} {
		if _, ok := ParseVersion(in); ok {
			t.Errorf("ParseVersion(%q) must fail: such a tag does not name a version", in)
		}
	}
}

func TestSplitImage(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, repo, tag string }{
		{"registry.k8s.io/kube-proxy:v1.30.4", "registry.k8s.io/kube-proxy", "v1.30.4"},
		{"localhost:5000/app", "localhost:5000/app", ""},
		{"localhost:5000/app:1.0", "localhost:5000/app", "1.0"},
		{"repo/app:1.0@sha256:abc", "repo/app", "1.0"},
		{"repo/app@sha256:abc", "repo/app", ""},
		{"busybox", "busybox", ""},
	}
	for _, tc := range tests {
		if repo, tag := SplitImage(tc.in); repo != tc.repo || tag != tc.tag {
			t.Errorf("SplitImage(%q) = %q, %q; want %q, %q", tc.in, repo, tag, tc.repo, tc.tag)
		}
	}
}

func TestParseRejectsBadCatalogs(t *testing.T) {
	t.Parallel()
	const head = "schemaVersion: 1\ncheckedAt: \"2026-11-11\"\ncomponents:\n"
	const okComp = "  - {id: a, name: A, type: nodes, docs: \"https://example.com/d\", checkedAt: \"2026-11-01\"}\n"
	if _, err := Parse([]byte(head + okComp)); err != nil {
		t.Fatalf("the baseline catalog must parse: %v", err)
	}
	bad := map[string]string{
		"unknown field":                 head + "  - {id: a, name: A, type: nodes, docs: \"https://example.com\", checkedAt: \"2026-11-01\", oops: 1}\n",
		"http docs":                     head + "  - {id: a, name: A, type: nodes, docs: \"http://example.com\", checkedAt: \"2026-11-01\"}\n",
		"entry newer than catalog":      head + "  - {id: a, name: A, type: nodes, docs: \"https://example.com\", checkedAt: \"2026-12-01\"}\n",
		"duplicate id":                  head + okComp + okComp,
		"unknown type":                  head + "  - {id: a, name: A, type: magic, docs: \"https://example.com\", checkedAt: \"2026-11-01\"}\n",
		"workload without detect":       head + "  - {id: a, name: A, type: workload, docs: \"https://example.com\", checkedAt: \"2026-11-01\"}\n",
		"rule may not say insufficient": head + "  - id: a\n    name: A\n    type: nodes\n    docs: \"https://example.com\"\n    checkedAt: \"2026-11-01\"\n    rules:\n      - {versions: {from: \"1.0.0\"}, kubernetes: {from: \"1.30\"}, status: insufficient_data}\n",
		"limitation without a note":     head + "  - id: a\n    name: A\n    type: nodes\n    docs: \"https://example.com\"\n    checkedAt: \"2026-11-01\"\n    rules:\n      - {versions: {from: \"1.0.0\"}, kubernetes: {from: \"1.30\"}, status: known_limitation}\n",
		"unbounded rule":                head + "  - id: a\n    name: A\n    type: nodes\n    docs: \"https://example.com\"\n    checkedAt: \"2026-11-01\"\n    rules:\n      - {versions: {}, kubernetes: {from: \"1.30\"}, status: confirmed}\n",
		"inverted versions":             head + "  - id: a\n    name: A\n    type: nodes\n    docs: \"https://example.com\"\n    checkedAt: \"2026-11-01\"\n    rules:\n      - {versions: {from: \"2.0.0\", before: \"1.0.0\"}, kubernetes: {from: \"1.30\"}, status: confirmed}\n",
		"no components":                 "schemaVersion: 1\ncheckedAt: \"2026-11-11\"\ncomponents: []\n",
		"wrong schema":                  "schemaVersion: 2\ncheckedAt: \"2026-11-11\"\ncomponents: []\n",
	}
	for name, src := range bad {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := Parse([]byte(src)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
}

func FuzzParseVersion(f *testing.F) {
	for _, s := range []string{"v0.7.2", "1.11.1-eks", "", "1.2.3.4", "999999999.1", "v1.-2"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, ok := ParseVersion(s)
		if ok && (v.Major < 0 || v.Minor < 0 || v.Patch < 0) {
			t.Fatalf("negative version %v from %q", v, s)
		}
	})
}

func FuzzSplitImage(f *testing.F) {
	for _, s := range []string{"a/b:c", "a@sha256:x", ":", "@", "a:b:c", "a/b/c:1@d"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		repo, tag := SplitImage(s)
		if strings.Contains(repo, "@") || strings.Contains(tag, "@") {
			t.Fatalf("digest leaked: %q → %q %q", s, repo, tag)
		}
	})
}

func FuzzParseCatalog(f *testing.F) {
	f.Add(embedded)
	f.Add([]byte("schemaVersion: 1"))
	f.Fuzz(func(t *testing.T, data []byte) {
		c, err := Parse(data)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("an error must wrap ErrInvalid: %v", err)
			}
			return
		}
		_ = c.Info()
		_ = c.Namespaces()
	})
}
