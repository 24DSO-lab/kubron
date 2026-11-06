// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// newCreateManager — Manager с одним «подключением» к фейковому dynamic-клиенту.
func newCreateManager(t *testing.T) (*Manager, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	m := NewManager()
	m.pool.put("test", &ClusterConnection{Name: "test", DynamicClient: dyn})
	m.activeCtx = "test"
	m.activeKey = "test"
	return m, dyn
}

// deploymentYAML — манифест; на место %s подставляется (или убирается вместе с переводом строки) metadata.namespace.
const deploymentYAML = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
%s
spec:
  replicas: 1
`

// plainDeployment — тот же манифест без namespace.
func plainDeployment() string { return strings.Replace(deploymentYAML, "%s\n", "", 1) }

func TestCreateResourceYAML(t *testing.T) {
	ctx := context.Background()

	t.Run("default namespace is default", func(t *testing.T) {
		m, dyn := newCreateManager(t)
		name, err := m.CreateResourceYAML("", "deployment", plainDeployment())
		if err != nil || name != "web" {
			t.Fatalf("name=%q err=%v", name, err)
		}
		got, err := dyn.Resource(kindTable["deployment"].gvr).Namespace("default").Get(ctx, "web", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("object was not created in default: %v", err)
		}
		if got.GetNamespace() != "default" {
			t.Errorf("namespace = %q", got.GetNamespace())
		}
	})

	t.Run("manifest namespace wins over the argument", func(t *testing.T) {
		m, dyn := newCreateManager(t)
		yml := strings.Replace(deploymentYAML, "%s", "  namespace: prod", 1)
		if _, err := m.CreateResourceYAML("staging", "deployment", yml); err != nil {
			t.Fatal(err)
		}
		if _, err := dyn.Resource(kindTable["deployment"].gvr).Namespace("prod").Get(ctx, "web", metav1.GetOptions{}); err != nil {
			t.Errorf("expected the object in prod: %v", err)
		}
	})

	t.Run("namespace argument when the manifest has none", func(t *testing.T) {
		m, dyn := newCreateManager(t)
		if _, err := m.CreateResourceYAML("staging", "deployment", plainDeployment()); err != nil {
			t.Fatal(err)
		}
		if _, err := dyn.Resource(kindTable["deployment"].gvr).Namespace("staging").Get(ctx, "web", metav1.GetOptions{}); err != nil {
			t.Errorf("expected the object in staging: %v", err)
		}
	})

	t.Run("creating again is an error, not an overwrite", func(t *testing.T) {
		m, _ := newCreateManager(t)
		yml := plainDeployment()
		if _, err := m.CreateResourceYAML("", "deployment", yml); err != nil {
			t.Fatal(err)
		}
		_, err := m.CreateResourceYAML("", "deployment", yml)
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("expected an \"already exists\" error, got %v", err)
		}
	})

	t.Run("a foreign apiVersion is rejected", func(t *testing.T) {
		m, _ := newCreateManager(t)
		yml := "apiVersion: v1\nkind: Deployment\nmetadata:\n  name: web\n"
		if _, err := m.CreateResourceYAML("", "deployment", yml); err == nil || !strings.Contains(err.Error(), "apiVersion") {
			t.Fatalf("expected an apiVersion error, got %v", err)
		}
	})

	t.Run("without a name", func(t *testing.T) {
		m, _ := newCreateManager(t)
		yml := "apiVersion: apps/v1\nkind: Deployment\nmetadata: {}\n"
		if _, err := m.CreateResourceYAML("", "deployment", yml); err == nil || !strings.Contains(err.Error(), "metadata.name") {
			t.Fatalf("expected an error about metadata.name, got %v", err)
		}
	})

	t.Run("without kind", func(t *testing.T) {
		m, _ := newCreateManager(t)
		yml := "apiVersion: apps/v1\nmetadata:\n  name: web\n"
		if _, err := m.CreateResourceYAML("", "deployment", yml); err == nil || !strings.Contains(strings.ToLower(err.Error()), "kind") {
			t.Fatalf("expected an error about kind, got %v", err)
		}
	})

	t.Run("invalid YAML", func(t *testing.T) {
		m, _ := newCreateManager(t)
		if _, err := m.CreateResourceYAML("", "deployment", "apiVersion: [unclosed"); err == nil {
			t.Fatal("expected a parse error")
		}
	})

	t.Run("unknown type", func(t *testing.T) {
		m, _ := newCreateManager(t)
		if _, err := m.CreateResourceYAML("", "nosuchkind", plainDeployment()); err == nil {
			t.Fatal("expected an error: type is not supported")
		}
	})

	t.Run("cluster-scoped resource: namespace is dropped", func(t *testing.T) {
		m, dyn := newCreateManager(t)
		yml := "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: viewer\n  namespace: default\nrules: []\n"
		name, err := m.CreateResourceYAML("default", "clusterrole", yml)
		if err != nil || name != "viewer" {
			t.Fatalf("name=%q err=%v", name, err)
		}
		got, err := dyn.Resource(kindTable["clusterrole"].gvr).Get(ctx, "viewer", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got.GetNamespace() != "" {
			t.Errorf("a cluster-scoped object kept namespace %q", got.GetNamespace())
		}
	})

	t.Run("no active connection", func(t *testing.T) {
		if _, err := NewManager().CreateResourceYAML("", "deployment", plainDeployment()); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestManifestNamespaceAndName(t *testing.T) {
	const pod = "apiVersion: v1\nkind: Pod\nmetadata:\n  name: p\n  namespace: n\n"
	if ns := ManifestNamespace(pod, "fb"); ns != "n" {
		t.Errorf("namespace from the manifest: %q", ns)
	}
	if name := ManifestName(pod); name != "p" {
		t.Errorf("name: %q", name)
	}
	if ns := ManifestNamespace("apiVersion: v1\nkind: Pod\nmetadata:\n  name: p\n", "fb"); ns != "fb" {
		t.Errorf("expected the fallback without a namespace in the manifest, got %q", ns)
	}
	if ns := ManifestNamespace("apiVersion: [unclosed", "fb"); ns != "fb" {
		t.Errorf("expected the fallback for garbage, got %q", ns)
	}
	if name := ManifestName("apiVersion: [unclosed"); name != "" {
		t.Errorf("expected an empty name for garbage, got %q", name)
	}
}
