// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/24DSO-lab/kubron/internal/credential"
)

// This is an API-server contract test for the boundary that matters to informer isolation:
// each view must send the token belonging to its own OIDC subject, even when both use the
// same stored kubeconfig and the same cluster endpoint. The Keycloak/kind gate exercises
// the real issuer and API-server authenticator; this test stays fast and deterministic.
func TestOIDCViewsUseTheirOwnIdentityAtTheAPI(t *testing.T) {
	var seenMu sync.Mutex
	var seen []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces" {
			http.NotFound(w, r)
			return
		}
		identity := r.Header.Get("Authorization")
		seenMu.Lock()
		seen = append(seen, identity)
		seenMu.Unlock()

		var namespace string
		switch identity {
		case "Bearer " + canary + "-alice":
			namespace = "alice-only"
		case "Bearer " + canary + "-bob":
			namespace = "bob-only"
		default:
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&corev1.NamespaceList{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "NamespaceList"},
			Items: []corev1.Namespace{{
				ObjectMeta: metav1.ObjectMeta{Name: namespace},
			}},
		})
	}))
	defer api.Close()

	SetKubronDir(t.TempDir())
	t.Cleanup(func() { SetKubronDir("") })
	meta, err := AddClusterFromText(signinKubeconfig(api.URL, true), ClusterSettings{
		DisplayName: "oidc-subject-isolation",
		ContextName: "ctx",
	})
	if err != nil {
		t.Fatal(err)
	}

	broker := &scriptBroker{kind: credential.KindOIDC}
	listNamespaces := func(user string) []string {
		t.Helper()
		plan, err := PlanFor(AuthSession{Broker: broker, UserID: user}, meta.ID)
		if err != nil {
			t.Fatalf("%s plan: %v", user, err)
		}
		if plan.Subject.Scope != credential.ScopeUser || plan.Subject.OwnerID != user {
			t.Fatalf("%s got subject %+v", user, plan.Subject)
		}
		cfg, err := ConfigFor(plan)
		if err != nil {
			t.Fatalf("%s config: %v", user, err)
		}
		client, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			t.Fatalf("%s client: %v", user, err)
		}
		got, err := client.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
		if err != nil {
			t.Fatalf("%s list namespaces: %v", user, err)
		}
		names := make([]string, 0, len(got.Items))
		for _, item := range got.Items {
			names = append(names, item.Name)
		}
		return names
	}

	if got := listNamespaces("alice"); len(got) != 1 || got[0] != "alice-only" {
		t.Fatalf("alice saw namespaces %v, want [alice-only]", got)
	}
	if got := listNamespaces("bob"); len(got) != 1 || got[0] != "bob-only" {
		t.Fatalf("bob saw namespaces %v, want [bob-only]", got)
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	if len(seen) != 2 || seen[0] != "Bearer "+canary+"-alice" || seen[1] != "Bearer "+canary+"-bob" {
		t.Fatalf("API server saw identities %v", seen)
	}
}
