// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// Two users of one cluster have different rights in it. The API server below answers by the token it receives; each
// user's connection (and so each user's informers, which are built on it) must send only that user's token and
// show only that user's data. A connection shared between them would show one user the other's namespaces.
func TestTwoUsersWithDifferentRightsDoNotSeeEachOthersData(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		mu.Lock()
		seen[tok]++
		mu.Unlock()
		var ns string
		switch {
		case strings.HasSuffix(tok, "-alice"):
			ns = "alice-only"
		case strings.HasSuffix(tok, "-bob"):
			ns = "bob-only"
		default:
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind": "NamespaceList", "apiVersion": "v1", "metadata": map[string]any{},
			"items": []map[string]any{{"metadata": map[string]any{"name": ns}}},
		})
	}))
	defer ts.Close()
	id := clusterAt(t, ts.URL, true)
	m := NewManager()
	b := &scriptBroker{kind: credential.KindOIDC}
	alice, bob := viewFor(m, b, "alice"), viewFor(m, b, "bob")
	for _, v := range []*Manager{alice, bob} {
		if err := v.ConnectToCluster(id); err != nil { // the real path: PlanFor, ConfigFor, the broker transport
			t.Fatal(err)
		}
	}
	names := func(v *Manager) []string {
		conn, err := v.GetActiveConnection()
		if err != nil {
			t.Fatal(err)
		}
		l, err := conn.Client.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, n := range l.Items {
			out = append(out, n.Name)
		}
		return out
	}
	if got := names(alice); len(got) != 1 || got[0] != "alice-only" {
		t.Fatalf("alice sees %v", got)
	}
	if got := names(bob); len(got) != 1 || got[0] != "bob-only" {
		t.Fatalf("bob sees %v", got)
	}
	ca, _ := alice.GetActiveConnection()
	cb, _ := bob.GetActiveConnection()
	if ca == cb {
		t.Fatal("the two users share one connection")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("the API server must see exactly the two personal tokens, saw %v", seen)
	}
}
