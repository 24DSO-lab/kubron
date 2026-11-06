// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// TestKeycloakKindRBAC verifies that a real Keycloak ID token is authenticated by a real kind
// API server and that Kubernetes RBAC keeps two users' namespace permissions separate.
func TestKeycloakKindRBAC(t *testing.T) {
	issuer := os.Getenv("KUBRON_KEYCLOAK_ISSUER")
	server := os.Getenv("KUBRON_KEYCLOAK_KIND_SERVER")
	kindCA := os.Getenv("KUBRON_KEYCLOAK_KIND_CA")
	alicePassword := os.Getenv("KUBRON_KEYCLOAK_ALICE_PASSWORD")
	bobPassword := os.Getenv("KUBRON_KEYCLOAK_BOB_PASSWORD")
	if issuer == "" || server == "" || kindCA == "" || alicePassword == "" || bobPassword == "" {
		t.Skip("Keycloak + kind integration environment is not configured")
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}
	aliceToken := keycloakPasswordIDToken(t, httpClient, issuer, "alice", alicePassword)
	bobToken := keycloakPasswordIDToken(t, httpClient, issuer, "bob", bobPassword)

	alice := keycloakKindClient(t, server, kindCA, aliceToken)
	bob := keycloakKindClient(t, server, kindCA, bobToken)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	if err := waitForKeycloakAuthentication(ctx, alice, "alice-ops"); err != nil {
		t.Fatalf("alice should authenticate and list pods in alice-ops: %v", err)
	}
	if _, err := alice.CoreV1().Pods("bob-ops").List(ctx, metav1.ListOptions{}); !apierrors.IsForbidden(err) {
		t.Fatalf("alice should be forbidden from bob-ops, got %v", err)
	}
	if err := waitForKeycloakAuthentication(ctx, bob, "bob-ops"); err != nil {
		t.Fatalf("bob should authenticate and list pods in bob-ops: %v", err)
	}
	if _, err := bob.CoreV1().Pods("alice-ops").List(ctx, metav1.ListOptions{}); !apierrors.IsForbidden(err) {
		t.Fatalf("bob should be forbidden from alice-ops, got %v", err)
	}
}

// waitForKeycloakAuthentication allows the API server a short window to refresh issuer/JWKS discovery
// after startup. A persistent 401 still fails the integration test; only transient startup 401s are retried.
func waitForKeycloakAuthentication(ctx context.Context, client *kubernetes.Clientset, namespace string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		_, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
		if err == nil {
			return nil
		}
		if !apierrors.IsUnauthorized(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("OIDC authentication did not become ready within 20 seconds: %w", err)
		case <-ticker.C:
		}
	}
}

func keycloakPasswordIDToken(t *testing.T, client *http.Client, issuer, username, password string) string {
	t.Helper()
	form := url.Values{
		"grant_type": {"password"},
		"client_id":  {"kubron-integration"},
		"username":   {username},
		"password":   {password},
		"scope":      {"openid"},
	}
	req, err := http.NewRequest(http.MethodPost, issuer+"/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("create token request for %s: %v", username, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request ID token for %s: %v", username, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read ID token response for %s: %v", username, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Keycloak token endpoint for %s returned HTTP %d: %s", username, resp.StatusCode, body)
	}
	var token struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &token); err != nil {
		t.Fatalf("decode ID token response for %s: %v", username, err)
	}
	if token.IDToken == "" {
		t.Fatalf("Keycloak returned an empty ID token for %s", username)
	}
	assertKeycloakIDTokenClaims(t, token.IDToken, issuer, username)
	return token.IDToken
}

func assertKeycloakIDTokenClaims(t *testing.T, raw, issuer, username string) {
	t.Helper()
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatal("Keycloak returned a malformed ID token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode ID token claims: %v", err)
	}
	var claims struct {
		Issuer            string          `json:"iss"`
		Audience          json.RawMessage `json:"aud"`
		PreferredUsername string          `json:"preferred_username"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("decode ID token claims: %v", err)
	}
	var audience string
	if err := json.Unmarshal(claims.Audience, &audience); err != nil {
		var values []string
		if err := json.Unmarshal(claims.Audience, &values); err != nil {
			t.Fatalf("Keycloak ID token has an invalid audience claim")
		}
		if !slices.Contains(values, "kubron-integration") {
			t.Fatalf("Keycloak ID token audience does not include kubron-integration: %v", values)
		}
	} else if audience != "kubron-integration" {
		t.Fatalf("Keycloak ID token audience is %q, want kubron-integration", audience)
	}
	if claims.Issuer != issuer {
		t.Fatalf("Keycloak ID token issuer is %q, want %q", claims.Issuer, issuer)
	}
	if claims.PreferredUsername != username {
		t.Fatalf("Keycloak ID token preferred_username is %q, want %q", claims.PreferredUsername, username)
	}
}

func keycloakKindClient(t *testing.T, server, caFile, token string) *kubernetes.Clientset {
	t.Helper()
	client, err := kubernetes.NewForConfig(&rest.Config{
		Host:        server,
		BearerToken: token,
		Timeout:     15 * time.Second,
		TLSClientConfig: rest.TLSClientConfig{
			CAFile: caFile,
		},
	})
	if err != nil {
		t.Fatalf("create Kubernetes client: %v", err)
	}
	return client
}
