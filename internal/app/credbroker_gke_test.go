// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"testing"

	"github.com/24DSO-labs/kubron/internal/credential"
)

const gkeKubeconfig = `apiVersion: v1
kind: Config
current-context: ctx
clusters:
- name: c
  cluster:
    server: https://203.0.113.10:6443
contexts:
- name: ctx
  context:
    cluster: c
    user: u
users:
- name: u
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: gke-gcloud-auth-plugin
      installHint: Install gke-gcloud-auth-plugin
      provideClusterInfo: true
`

// In the web a GKE kubeconfig gets the native provider: the user is asked for a service account key, and the
// command of the kubeconfig is not shown as a manual-token challenge.
func TestWebBrokerAsksGKEForAServiceAccountKey(t *testing.T) {
	b, err := BuildBroker(brokerOptions(t, false))
	if err != nil {
		t.Fatal(err)
	}
	d, err := b.Describe([]byte(gkeKubeconfig), "ctx")
	if err != nil || d.Kind != credential.KindGKE {
		t.Fatalf("kind = %q err = %v", d.Kind, err)
	}
	sub := credential.Subject{ClusterID: "c", Scope: credential.ScopeUser, OwnerID: "u1"}
	res, err := b.Acquire(context.Background(), sub, d)
	if err != nil {
		t.Fatal(err)
	}
	c := res.Challenge
	if c == nil || c.Kind != credential.ChallengeSecretFields || res.Token != nil {
		t.Fatalf("expected a secret-fields challenge, got %+v", res)
	}
	if len(c.Fields) != 1 || c.Fields[0].Name != "serviceAccountJson" || !c.Fields[0].Secret {
		t.Fatalf("fields = %+v", c.Fields)
	}
	if c.ID == "" || c.ExpiresAt.IsZero() {
		t.Fatal("the Broker must set ID and ExpiresAt")
	}

	// A value that is not a key does not use up the request: the dialog stays open.
	if _, err := b.Answer(context.Background(), sub, c.ID, credential.Answer{"serviceAccountJson": "not a key"}); !errors.Is(err, credential.ErrInvalidAnswer) {
		t.Fatalf("err = %v", err)
	}
	if _, err := b.Answer(context.Background(), sub, c.ID, credential.Answer{"serviceAccountJson": "still not a key"}); !errors.Is(err, credential.ErrInvalidAnswer) {
		t.Fatalf("the request must still be open: err = %v", err)
	}
}
