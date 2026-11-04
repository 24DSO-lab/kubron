// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/credential/filestore"
	"github.com/24DSO-labs/kubron/internal/secretbox"
	"github.com/24DSO-labs/kubron/internal/secrets"
)

const awsKubeconfig = `apiVersion: v1
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
      command: aws
      args: ["eks", "get-token", "--cluster-name", "demo"]
`

func brokerOptions(t *testing.T, desktop bool) BrokerOptions {
	t.Helper()
	key, err := secrets.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	kr, err := secretbox.NewKeyring(key)
	if err != nil {
		t.Fatal(err)
	}
	env, err := secrets.NewEnvelope(kr)
	if err != nil {
		t.Fatal(err)
	}
	st, err := filestore.New(filepath.Join(t.TempDir(), "credentials.json"), credential.NewSealer(env))
	if err != nil {
		t.Fatal(err)
	}
	return BrokerOptions{Desktop: desktop, Keyring: kr, Store: st, Challenges: credential.NewMemoryChallengeStore(), DataDir: t.TempDir()}
}

func TestBuildBrokerRejectsIncompleteOptions(t *testing.T) {
	if _, err := BuildBroker(BrokerOptions{}); !errors.Is(err, ErrBrokerOptions) {
		t.Fatalf("got %v", err)
	}
	o := brokerOptions(t, false)
	o.Challenges = nil
	if _, err := BuildBroker(o); !errors.Is(err, ErrBrokerOptions) {
		t.Fatalf("got %v", err)
	}
}

// In the web an exec block is never run: a plugin without a native provider becomes a manual-token challenge.
func TestWebBrokerTurnsAnExecKubeconfigIntoManualToken(t *testing.T) {
	b, err := BuildBroker(brokerOptions(t, false))
	if err != nil {
		t.Fatal(err)
	}
	d, err := b.Describe([]byte(awsKubeconfig), "ctx")
	if err != nil {
		t.Fatal(err)
	}
	res, err := b.Acquire(context.Background(), credential.Subject{ClusterID: "c", Scope: credential.ScopeUser, OwnerID: "u1"}, d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Challenge == nil || res.Challenge.Kind != credential.ChallengeManualToken {
		t.Fatalf("expected a manual-token challenge, got %+v", res)
	}
}

func TestBrokerStaticCredentialsNeedNoSignIn(t *testing.T) {
	b, err := BuildBroker(brokerOptions(t, false))
	if err != nil {
		t.Fatal(err)
	}
	res, err := b.Acquire(context.Background(), credential.Subject{ClusterID: "c", Scope: credential.ScopeTeam, OwnerID: "t"},
		credential.Descriptor{Kind: credential.KindStatic})
	if err != nil || res.Challenge != nil || res.Token != nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestBrokerRegistersNativeAKSProvider(t *testing.T) {
	b, err := BuildBroker(brokerOptions(t, false))
	if err != nil { t.Fatal(err) }
	d := credential.Descriptor{Kind: credential.KindAKS}
	res, err := b.Acquire(context.Background(), credential.Subject{ClusterID: "aks", Scope: credential.ScopeUser, OwnerID: "u1"}, d)
	if err != nil { t.Fatal(err) }
	if res.Challenge == nil || res.Challenge.Kind != credential.ChallengeCredentials ||
		len(res.Challenge.Fields) != 1 || res.Challenge.Fields[0].Name != "tenantId" {
		t.Fatalf("expected a tenant-id challenge from the native AKS provider, got %+v", res)
	}
}
