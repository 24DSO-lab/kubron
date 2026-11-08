// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRolloutChanges(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	yes := true
	mk := func(name, ownerKind string, age time.Duration) appsv1.ReplicaSet {
		return appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "ns",
			CreationTimestamp: metav1.NewTime(now.Add(-age)),
			Annotations:       map[string]string{"deployment.kubernetes.io/revision": "7"},
			OwnerReferences:   []metav1.OwnerReference{{Kind: ownerKind, Name: "web", Controller: &yes}},
		}, Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Image: "web:1.2"}}}}}}
	}
	got := rolloutChanges([]appsv1.ReplicaSet{
		mk("new", "Deployment", 10*time.Minute),
		mk("old", "Deployment", 3*time.Hour),
		mk("other", "StatefulSet", time.Minute),
	}, now.Add(-time.Hour))
	if len(got) != 1 || got[0].Name != "web" || got[0].Detail != "revision 7 · web:1.2" || got[0].Target != "deployment" {
		t.Errorf("%+v", got)
	}
}

func TestFinishChanges(t *testing.T) {
	base := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	var in []ChangeEvent
	for i := 0; i < maxChanges+3; i++ {
		in = append(in, ChangeEvent{Name: "x", at: base.Add(time.Duration(i) * time.Minute)})
	}
	got := finishChanges(in)
	if len(got) != maxChanges || !got[0].at.After(got[1].at) || got[0].Time == "" {
		t.Errorf("len=%d first=%+v", len(got), got[0])
	}
	if finishChanges(nil) == nil {
		t.Error("an empty result must be [], not null")
	}
}

func TestFluxChanges(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	obj := func(kind, name, last string, state FluxState, rev string) FluxObjectInfo {
		return FluxObjectInfo{Kind: kind, Name: name, Namespace: "flux-system", LastReconcileAt: last, State: state, Revision: rev}
	}
	ks := []FluxKustomizationInfo{
		{FluxObjectInfo: obj("Kustomization", "apps", at(10*time.Minute), FluxStateReady, "main@sha1:0123456789abcdef0123456789abcdef01234567")},
		{FluxObjectInfo: obj("Kustomization", "old", at(3*time.Hour), FluxStateReady, "")},
		{FluxObjectInfo: obj("Kustomization", "never", "", FluxStateUnknown, "")},
		{FluxObjectInfo: obj("Kustomization", "garbage", "yesterday", FluxStateReady, "")},
	}
	hrs := []FluxHelmReleaseInfo{
		{FluxObjectInfo: obj("HelmRelease", "podinfo", at(5*time.Minute), FluxStateFailed, "6.5.4")},
	}
	got := fluxChanges(ks, hrs, now.Add(-time.Hour))
	if len(got) != 2 {
		t.Fatalf("only recent valid entries: %+v", got)
	}
	k, h := got[0], got[1]
	if k.Kind != changeKindFlux || k.Name != "apps" || k.Namespace != "flux-system" || k.Target != "flux-kustomization" ||
		k.Detail != "Kustomization · Ready · main@sha1:0123456" || !k.at.Equal(now.Add(-10*time.Minute)) {
		t.Errorf("kustomization: %+v", k)
	}
	if h.Kind != changeKindFlux || h.Name != "podinfo" || h.Target != "flux-helmrelease" || h.Detail != "HelmRelease · Failed · 6.5.4" {
		t.Errorf("helmrelease: %+v", h)
	}
	// Без Flux и с пустыми списками записей нет.
	if got := fluxChanges(nil, nil, now.Add(-time.Hour)); len(got) != 0 {
		t.Errorf("%+v", got)
	}
	// Граница окна: запись ровно на since входит.
	edge := fluxChanges([]FluxKustomizationInfo{{FluxObjectInfo: obj("Kustomization", "e", at(time.Hour), FluxStateReady, "")}}, nil, now.Add(-time.Hour))
	if len(edge) != 1 || edge[0].Detail != "Kustomization · Ready" {
		t.Errorf("edge: %+v", edge)
	}
}

func TestFluxShortRevision(t *testing.T) {
	for in, want := range map[string]string{
		"":                           "",
		"6.5.4":                      "6.5.4",
		"main@sha1:0123456789abcdef": "main@sha1:0123456",
		"sha256:0123456789abcdef":    "sha256:0123456",
		"latest@sha256:abc":          "latest@sha256:abc",
		"v1.0.0":                     "v1.0.0",
	} {
		if got := fluxShortRevision(in); got != want {
			t.Errorf("fluxShortRevision(%q) = %q, want %q", in, got, want)
		}
	}
}
