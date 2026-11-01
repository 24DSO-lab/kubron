// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import "testing"

func TestNormalizeArgoURL(t *testing.T) {
	good := map[string]string{
		"":                                "",
		"  https://argocd.example.com/  ": "https://argocd.example.com",
		"https://argocd.example.com:8443": "https://argocd.example.com:8443",
		"http://argocd.internal/argo":     "http://argocd.internal/argo",
	}
	for in, want := range good {
		if got, err := normalizeArgoURL(in); err != nil || got != want {
			t.Errorf("%q: got %q, %v; expected %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"javascript:alert(1)", "data:text/html,x", "file:///etc/passwd", "ftp://argocd.example.com",
		"//argocd.example.com", "argocd.example.com", "https://", "https://user:pass@argocd.example.com",
		"https://argocd.example.com/?token=1", "https://argocd.example.com/#x", "https://argo cd.example.com",
		"https://argocd.example.com/" + repeat("x", maxArgoURLLen),
	} {
		if got, err := normalizeArgoURL(in); err == nil {
			t.Errorf("%q must be rejected, got %q", in, got)
		}
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

func TestOpenArgoUIUsesTheAddressFromClusterSettings(t *testing.T) {
	setupClustersHome(t)
	meta, err := AddClusterFromText(sampleKubeconfig, ClusterSettings{DisplayName: "argo", ArgoURL: "https://argocd.example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	if meta.ArgoURL != "https://argocd.example.com" {
		t.Fatalf("the address must be normalized and stored: %q", meta.ArgoURL)
	}

	mgr := NewManager()
	mgr.activeCtx = meta.ID
	pfm := NewPortForwardManager(mgr)
	pfm.SetMode(PortForwardModeOff) // внешний адрес не требует port-forward
	info, err := pfm.OpenArgoUI()
	if err != nil {
		t.Fatalf("the published address must open even when port-forward is disabled: %v", err)
	}
	if info.URL != "https://argocd.example.com" || info.Mode != ArgoModeExternal || info.PortForwardID != "" || info.LocalPort != 0 {
		t.Errorf("unexpected result: %+v", info)
	}

	// Адрес убран — без port-forward Argo CD снова недоступен.
	if _, err := UpdateClusterSettings(meta.ID, ClusterSettings{DisplayName: "argo"}, KubeconfigSource{}); err != nil {
		t.Fatal(err)
	}
	if _, err := pfm.OpenArgoUI(); err != ErrPortForwardDisabled {
		t.Errorf("after clearing the address the port-forward mode applies again: %v", err)
	}

	// Недопустимый адрес не сохраняется.
	if _, err := UpdateClusterSettings(meta.ID, ClusterSettings{DisplayName: "argo", ArgoURL: "javascript:alert(1)"}, KubeconfigSource{}); err == nil {
		t.Error("a non-http address must be rejected")
	}
}
