// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"errors"
	"slices"
	"testing"
)

func kindsWith(a fluxActions) []string {
	var out []string
	for _, k := range fluxKinds {
		if k.Actions.has(a) {
			out = append(out, k.Kind)
		}
	}
	slices.Sort(out)
	return out
}

// Таблица действий сверяется с ADR-0023, раздел 6: reconcile, suspend и resume — Kustomization, HelmRelease,
// источники, ImageRepository и ImageUpdateAutomation; delete — только Kustomization и HelmRelease;
// остальные виды (Alert, Provider, Receiver, ImagePolicy) только на чтение. Менять ожидания можно только вместе с ADR.
func TestFluxActionAllowlistMatchesADR(t *testing.T) {
	toggle := []string{
		"Bucket", "GitRepository", "HelmChart", "HelmRelease", "HelmRepository", "ImageRepository",
		"ImageUpdateAutomation", "Kustomization", "OCIRepository",
	}
	readOnly := []string{"Alert", "ImagePolicy", "Provider", "Receiver"}

	if got := kindsWith(fluxActReconcile); !slices.Equal(got, toggle) {
		t.Errorf("reconcile kinds = %v, ADR: %v", got, toggle)
	}
	if got := kindsWith(fluxActSuspend); !slices.Equal(got, toggle) {
		t.Errorf("suspend/resume kinds = %v, ADR: %v", got, toggle)
	}
	if got, want := kindsWith(fluxActDelete), []string{"HelmRelease", "Kustomization"}; !slices.Equal(got, want) {
		t.Errorf("delete kinds = %v, ADR: %v", got, want)
	}
	for _, name := range readOnly {
		k, ok := fluxKindByName(name)
		if !ok || k.Actions != fluxActReadOnly {
			t.Errorf("%s must be read-only: %+v", name, k)
		}
	}
	if len(toggle)+len(readOnly) != len(fluxKinds) {
		t.Errorf("the table has %d kinds, the ADR lists %d: update both together", len(fluxKinds), len(toggle)+len(readOnly))
	}
}

// Группы, plural и допустимые версии — ADR-0023, раздел 3.
func TestFluxKindVersionsMatchADR(t *testing.T) {
	want := map[string]struct {
		group, plural string
		versions      []string
		ctrl          fluxController
	}{
		"Kustomization":         {fluxGroupKustomize, "kustomizations", []string{"v1"}, fluxCtrlKustomize},
		"HelmRelease":           {fluxGroupHelm, "helmreleases", []string{"v2"}, fluxCtrlHelm},
		"GitRepository":         {fluxGroupSource, "gitrepositories", []string{"v1"}, fluxCtrlSource},
		"HelmRepository":        {fluxGroupSource, "helmrepositories", []string{"v1"}, fluxCtrlSource},
		"HelmChart":             {fluxGroupSource, "helmcharts", []string{"v1"}, fluxCtrlSource},
		"OCIRepository":         {fluxGroupSource, "ocirepositories", []string{"v1", "v1beta2"}, fluxCtrlSource},
		"Bucket":                {fluxGroupSource, "buckets", []string{"v1", "v1beta2"}, fluxCtrlSource},
		"Alert":                 {fluxGroupNotification, "alerts", []string{"v1beta3"}, fluxCtrlNotification},
		"Provider":              {fluxGroupNotification, "providers", []string{"v1beta3"}, fluxCtrlNotification},
		"Receiver":              {fluxGroupNotification, "receivers", []string{"v1", "v1beta2"}, fluxCtrlNotification},
		"ImageRepository":       {fluxGroupImage, "imagerepositories", []string{"v1", "v1beta2"}, fluxCtrlImage},
		"ImagePolicy":           {fluxGroupImage, "imagepolicies", []string{"v1", "v1beta2"}, fluxCtrlImage},
		"ImageUpdateAutomation": {fluxGroupImage, "imageupdateautomations", []string{"v1", "v1beta2"}, fluxCtrlImage},
	}
	if len(fluxKinds) != len(want) {
		t.Fatalf("kinds in the table: %d, expected %d", len(fluxKinds), len(want))
	}
	seenPlural := map[string]bool{}
	for _, k := range fluxKinds {
		w, ok := want[k.Kind]
		if !ok {
			t.Errorf("unexpected kind %s", k.Kind)
			continue
		}
		if k.Group != w.group || k.Plural != w.plural || !slices.Equal(k.Versions, w.versions) || k.Controller != w.ctrl || !k.Namespaced {
			t.Errorf("%s: %+v, expected %+v", k.Kind, k, w)
		}
		if seenPlural[k.Plural] {
			t.Errorf("duplicate plural %s", k.Plural)
		}
		seenPlural[k.Plural] = true
	}
}

func TestFluxKindLookupAndGVR(t *testing.T) {
	k, ok := fluxKindByName("OCIRepository")
	if !ok {
		t.Fatal("OCIRepository is not found")
	}
	if g := k.GVR("v1beta2"); g.Group != fluxGroupSource || g.Version != "v1beta2" || g.Resource != "ocirepositories" {
		t.Errorf("GVR: %+v", g)
	}
	if !k.supports("v1") || !k.supports("v1beta2") || k.supports("v1beta1") || k.supports("") {
		t.Error("supports must follow the allowlist of versions")
	}
	for _, bad := range []string{"", "kustomization", "KUSTOMIZATION", "Secret", "Pod", "kustomize.toolkit.fluxcd.io/v1/kustomizations"} {
		if _, ok := fluxKindByName(bad); ok {
			t.Errorf("a client-supplied kind %q must not be accepted", bad)
		}
	}
	if n := len(fluxKindsOf(fluxCtrlSource)); n != 5 {
		t.Errorf("source controller owns 5 kinds, got %d", n)
	}
}

func TestCheckFluxRef(t *testing.T) {
	ok := FluxRef{Kind: "Kustomization", Namespace: "flux-system", Name: "apps"}
	cases := []struct {
		name   string
		ref    FluxRef
		action fluxActions
		want   error
	}{
		{"valid delete", ok, fluxActDelete, nil},
		{"valid reconcile of a source", FluxRef{Kind: "GitRepository", Namespace: "n", Name: "x"}, fluxActReconcile, nil},
		{"delete of a source is rejected", FluxRef{Kind: "GitRepository", Namespace: "n", Name: "x"}, fluxActDelete, errFluxKindUnsupported},
		{"suspend of an Alert is rejected", FluxRef{Kind: "Alert", Namespace: "n", Name: "x"}, fluxActSuspend, errFluxKindUnsupported},
		{"reconcile of an ImagePolicy is rejected", FluxRef{Kind: "ImagePolicy", Namespace: "n", Name: "x"}, fluxActReconcile, errFluxKindUnsupported},
		{"foreign kind", FluxRef{Kind: "Secret", Namespace: "n", Name: "x"}, fluxActReconcile, errFluxKindUnsupported},
		{"no kind", FluxRef{Namespace: "n", Name: "x"}, fluxActReconcile, errFluxInvalidRef},
		{"no namespace", FluxRef{Kind: "Kustomization", Name: "x"}, fluxActReconcile, errFluxInvalidRef},
		{"no name", FluxRef{Kind: "Kustomization", Namespace: "n"}, fluxActReconcile, errFluxInvalidRef},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := checkFluxRef(tc.ref, tc.action)
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if fluxMaxActionObjects != 200 {
		t.Errorf("the ADR limit is 200 objects per call, got %d", fluxMaxActionObjects)
	}
}
