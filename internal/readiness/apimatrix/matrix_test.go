// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package apimatrix

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/24DSO-lab/kubron/internal/readiness/kver"
)

func v(major, minor int) kver.Minor { return kver.Minor{Major: major, Minor: minor} }

func TestEmbeddedMatrix(t *testing.T) {
	t.Parallel()
	m, err := Default()
	if err != nil {
		t.Fatalf("the embedded matrix is broken, regenerate it with scripts/gen-apimatrix: %v", err)
	}
	if len(m.Window) != MaxWindow {
		t.Fatalf("the target window must hold the %d latest upstream releases, got %v", MaxWindow, m.Window)
	}
	if m.Source.Repository == "" || m.GeneratedAt == "" || m.Version == "" {
		t.Fatalf("matrix origin is not recorded: %+v", m.Info())
	}

	// Известные факты апстрима: по ним видно, что данные прочитаны, а не пусты.
	known := []struct {
		gvk                   GVK
		deprecated, removed   kver.Minor
		replacementAPIVersion string
	}{
		{GVK{"batch", "v1beta1", "CronJob"}, v(1, 21), v(1, 25), "batch/v1"},
		{GVK{"policy", "v1beta1", "PodDisruptionBudget"}, v(1, 21), v(1, 25), "policy/v1"},
		{GVK{"networking.k8s.io", "v1beta1", "Ingress"}, v(1, 19), v(1, 22), "networking.k8s.io/v1"},
		{GVK{"flowcontrol.apiserver.k8s.io", "v1beta3", "FlowSchema"}, v(1, 29), v(1, 32), "flowcontrol.apiserver.k8s.io/v1"},
	}
	for _, k := range known {
		e, ok := m.Lookup(k.gvk)
		if !ok {
			t.Errorf("%s is missing from the matrix", k.gvk)
			continue
		}
		if e.Deprecated != k.deprecated || e.Removed != k.removed {
			t.Errorf("%s: deprecated=%v removed=%v, want %v and %v", k.gvk, e.Deprecated, e.Removed, k.deprecated, k.removed)
		}
		if e.Replacement == nil || e.Replacement.APIVersion() != k.replacementAPIVersion {
			t.Errorf("%s: replacement = %+v, want %s", k.gvk, e.Replacement, k.replacementAPIVersion)
		}
	}
}

func TestStateAt(t *testing.T) {
	t.Parallel()
	e := Entry{Deprecated: v(1, 34), Removed: v(1, 37)}
	tests := []struct {
		at   kver.Minor
		want State
	}{
		{v(1, 33), StateNone},
		{v(1, 34), StateDeprecated},
		{v(1, 36), StateDeprecated},
		{v(1, 37), StateRemoved},
		{v(1, 40), StateRemoved},
	}
	for _, tc := range tests {
		if got := e.StateAt(tc.at); got != tc.want {
			t.Errorf("StateAt(%v) = %q, want %q", tc.at, got, tc.want)
		}
	}
	if got := (Entry{Deprecated: v(1, 30)}).StateAt(v(1, 40)); got != StateDeprecated {
		t.Errorf("an API without a declared removal stays deprecated, got %q", got)
	}
	if got := (Entry{}).StateAt(v(1, 40)); got != StateNone {
		t.Errorf("an API without lifecycle data must not be flagged, got %q", got)
	}
}

func TestRelevantSkipsWhatIsAlreadyGone(t *testing.T) {
	t.Parallel()
	m, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	// Кластер 1.30 → цель 1.37: CronJob v1beta1 удалён в 1.25 и обновлению уже не мешает.
	var kinds []string
	for e := range m.Relevant(v(1, 30), v(1, 37)) {
		kinds = append(kinds, e.String())
		if !e.Removed.IsZero() && e.Removed.Compare(v(1, 30)) <= 0 {
			t.Errorf("%s was removed at %v, before the current version", e.GVK, e.Removed)
		}
		if e.StateAt(v(1, 37)) == StateNone {
			t.Errorf("%s is neither deprecated nor removed at the target", e.GVK)
		}
	}
	if slices.Contains(kinds, "batch/v1beta1, CronJob") {
		t.Error("batch/v1beta1 CronJob is long gone at 1.30")
	}
	// Тот же кластер при цели 1.37 видит удаляемые в окне alpha/beta API.
	if !slices.Contains(kinds, "networking.k8s.io/v1beta1, ServiceCIDR") {
		t.Errorf("networking.k8s.io/v1beta1 ServiceCIDR is removed in 1.37, got %v", kinds)
	}
	// Для кластера 1.20 CronJob v1beta1 ещё обслуживается, и миграция ему нужна.
	found := false
	for e := range m.Relevant(v(1, 20), v(1, 25)) {
		if e.Kind == "CronJob" && e.Version == "v1beta1" {
			found = true
		}
	}
	if !found {
		t.Error("batch/v1beta1 CronJob matters for 1.20 → 1.25")
	}
}

func TestParseRejectsTampering(t *testing.T) {
	t.Parallel()
	good, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(good)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(raw); err != nil {
		t.Fatalf("a round trip must stay valid: %v", err)
	}

	mutate := func(f func(m *Matrix)) []byte {
		var m Matrix
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		f(&m)
		b, _ := json.Marshal(m)
		return b
	}
	cases := map[string][]byte{
		"edited entry without regeneration": mutate(func(m *Matrix) { m.Entries[0].Removed = v(1, 99) }),
		"bad date":                          mutate(func(m *Matrix) { m.GeneratedAt = "yesterday" }),
		"window gap":                        mutate(func(m *Matrix) { m.Window = []kver.Minor{v(1, 33), v(1, 35)} }),
		"no entries":                        mutate(func(m *Matrix) { m.Entries = nil }),
		"no source":                         mutate(func(m *Matrix) { m.Source.Repository = "" }),
		"unknown field":                     bytes.Replace(raw, []byte(`"schemaVersion"`), []byte(`"extra":1,"schemaVersion"`), 1),
		"not json":                          []byte("{"),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := Parse(data); !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
}

func TestEntriesAreSortedAndUnique(t *testing.T) {
	t.Parallel()
	m, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(m.Entries); i++ {
		if compareGVK(m.Entries[i-1].GVK, m.Entries[i].GVK) >= 0 {
			t.Fatalf("entries %d and %d are out of order", i-1, i)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add(embedded)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schemaVersion":1,"entries":[{"version":"v1","kind":"K","deprecated":"1.1"}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := Parse(data)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("an error must wrap ErrInvalid: %v", err)
			}
			return
		}
		// Принятая матрица обязана быть пригодной к использованию без паники.
		_ = m.Latest()
		_ = m.Info()
		for e := range m.Relevant(v(1, 20), m.Latest()) {
			_ = e.StateAt(m.Latest())
		}
	})
}
