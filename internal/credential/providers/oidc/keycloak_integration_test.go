// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/idpnet"
)

// TestKeycloakPasswordAndRefresh exercises the production provider against a real Keycloak realm.
// It is opt-in locally and enabled by the credential-keycloak GitHub Actions workflow.
func TestKeycloakPasswordAndRefresh(t *testing.T) {
	issuer := strings.TrimRight(os.Getenv("KUBRON_KEYCLOAK_ISSUER"), "/")
	if issuer == "" {
		t.Skip("set KUBRON_KEYCLOAK_ISSUER to run the Keycloak integration test")
	}
	username, password := os.Getenv("KUBRON_KEYCLOAK_USERNAME"), os.Getenv("KUBRON_KEYCLOAK_PASSWORD")
	if username == "" || password == "" {
		t.Fatal("KUBRON_KEYCLOAK_USERNAME and KUBRON_KEYCLOAK_PASSWORD are required")
	}

	httpClient, err := idpnet.NewClient(idpnet.Options{Desktop: true})
	if err != nil {
		t.Fatal(err)
	}
	provider := New()
	defer provider.Close()

	req := credential.Request{
		Descriptor: credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{
			"grant": "password", "issuer-url": issuer, "client-id": "kubron-integration",
			"username": username, "password": password,
		}},
		Subject: credential.Subject{ClusterID: "keycloak-integration", Scope: credential.ScopeUser,
			OwnerID: "keycloak-integration-user", SessionID: "keycloak-integration-session"},
	}
	env := credential.Env{HTTP: httpClient, Desktop: true}

	first, err := provider.Begin(context.Background(), env, req)
	if err != nil {
		t.Fatalf("password grant: %v", err)
	}
	if first.Token == nil || first.Token.Value == "" || first.Token.Principal == "" {
		t.Fatal("Keycloak password grant did not produce a verified ID token and principal")
	}
	if len(first.Save) == 0 {
		t.Fatal("Keycloak did not issue refresh credentials for the integration client")
	}

	// The second call must use the saved refresh token rather than asking for the password again.
	req.Descriptor.Params["username"] = ""
	req.Descriptor.Params["password"] = ""
	req.Saved = first.Save
	second, err := provider.Begin(context.Background(), env, req)
	if err != nil {
		t.Fatalf("refresh grant: %v", err)
	}
	if second.Token == nil || second.Token.Value == "" || second.Token.Principal != first.Token.Principal {
		t.Fatal("Keycloak refresh did not preserve the verified subject")
	}
}
