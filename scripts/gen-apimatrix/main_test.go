// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/readiness/apimatrix"
	"github.com/24DSO-lab/kubron/internal/readiness/kver"
)

func TestParseLifecycleReadsRealUpstreamFile(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("testdata/lifecycle_sample.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseLifecycle(src, "networking.k8s.io", "v1beta1")
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string]apimatrix.Entry{}
	for _, e := range got {
		byKind[e.Kind] = e
	}
	if _, ok := byKind["IPAddressList"]; ok {
		t.Error("list kinds must not become entries of their own")
	}
	ing, ok := byKind["Ingress"]
	if !ok {
		t.Fatalf("Ingress is missing: %v", got)
	}
	if ing.Introduced != (kver.Minor{Major: 1, Minor: 14}) || ing.Deprecated != (kver.Minor{Major: 1, Minor: 19}) ||
		ing.Removed != (kver.Minor{Major: 1, Minor: 22}) {
		t.Errorf("Ingress lifecycle = %+v", ing)
	}
	if ing.Replacement == nil || ing.Replacement.APIVersion() != "networking.k8s.io/v1" || ing.Replacement.Kind != "Ingress" {
		t.Errorf("Ingress replacement = %+v", ing.Replacement)
	}
	ip := byKind["IPAddress"]
	if ip.Deprecated != (kver.Minor{Major: 1, Minor: 34}) || ip.Removed != (kver.Minor{Major: 1, Minor: 37}) || ip.Replacement != nil {
		t.Errorf("IPAddress lifecycle = %+v", ip)
	}
}

func TestParseLifecycleRejectsBrokenSource(t *testing.T) {
	t.Parallel()
	if _, err := parseLifecycle([]byte("package x\nfunc ("), "g", "v1"); err == nil {
		t.Error("a syntax error must fail the generation")
	}
	bad := `package x
func (in *A) APILifecycleRemoved() (major, minor int) { return 1, compute() }`
	if _, err := parseLifecycle([]byte(bad), "g", "v1"); err == nil {
		t.Error("a non-literal lifecycle value must fail instead of being skipped")
	}
}

func TestResolveWindowFollowsTheReleaseSchedule(t *testing.T) {
	t.Parallel()
	const schedule = `schedules:
- release: "1.37"
  endOfLifeDate: "2027-10-28"
- release: "1.36"
  endOfLifeDate: "2027-06-28"
- release: "1.35"
  endOfLifeDate: "2027-02-28"
- release: "1.34"
  endOfLifeDate: "2026-10-27"
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, scheduleP) {
			_, _ = w.Write([]byte(schedule))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	old := rawWebsite
	rawWebsite = srv.URL
	defer func() { rawWebsite = old }()

	f := &fetcher{client: srv.Client()}
	latest, window, err := resolveWindow(t.Context(), f, "", "2026-11-11")
	if err != nil {
		t.Fatal(err)
	}
	if latest != (kver.Minor{Major: 1, Minor: 37}) || len(window) != 3 || window[0].Minor != 35 {
		t.Fatalf("latest=%v window=%v", latest, window)
	}
	// Окно, не совпадающее с расписанием, — ошибка, а не молчаливая подгонка.
	if _, _, err := resolveWindow(t.Context(), f, "1.38", "2026-11-11"); err == nil {
		t.Error("a window that disagrees with the schedule must fail")
	}
}

func TestCoverageCheckFailsOnUnknownPreReleaseGroup(t *testing.T) {
	t.Parallel()
	const spec = `{"definitions":{
	 "a":{"x-kubernetes-group-version-kind":[{"group":"apps","version":"v1beta1","kind":"X"}]},
	 "b":{"x-kubernetes-group-version-kind":[{"group":"new.k8s.io","version":"v1alpha1","kind":"Y"}]},
	 "c":{"x-kubernetes-group-version-kind":[{"group":"apps","version":"v1","kind":"Z"}]}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(spec)) }))
	defer srv.Close()
	old := rawBase
	rawBase = srv.URL
	defer func() { rawBase = old }()

	err := checkCoverage(context.Background(), &fetcher{client: srv.Client()}, "v9.9.0")
	if err == nil || !strings.Contains(err.Error(), "new.k8s.io/v1alpha1") {
		t.Fatalf("want an error naming the missing group, got %v", err)
	}
	if strings.Contains(err.Error(), "apps/v1") {
		t.Errorf("known and GA versions must not be reported: %v", err)
	}
}
