// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"errors"
	"testing"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/24DSO-labs/kubron/internal/oidcpassword"
)

func kubeconfigWith(ai *clientcmdapi.AuthInfo, cl *clientcmdapi.Cluster) *clientcmdapi.Config {
	if cl == nil {
		cl = &clientcmdapi.Cluster{Server: "https://k8s.example:6443"}
	}
	return &clientcmdapi.Config{
		Clusters:  map[string]*clientcmdapi.Cluster{"c": cl},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{"u": ai},
		Contexts:  map[string]*clientcmdapi.Context{"ctx": {Cluster: "c", AuthInfo: "u"}},
	}
}

func oidcExec(args ...string) *clientcmdapi.AuthInfo {
	return &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "kubectl", Args: append([]string{"oidc-login", "get-token"}, args...)}}
}

// restrictive включает политику веб-сервера на время теста.
func restrictive(t *testing.T) {
	t.Helper()
	SetConnectionPolicy(ConnectionPolicy{})
	t.Cleanup(func() { SetConnectionPolicy(ConnectionPolicy{AllowExec: true, AllowLocalFiles: true}) })
}

func TestClassifyAuth(t *testing.T) {
	cases := []struct {
		name string
		ai   *clientcmdapi.AuthInfo
		want AuthKind
	}{
		{"token", &clientcmdapi.AuthInfo{Token: "t"}, AuthStatic},
		{"no credentials", &clientcmdapi.AuthInfo{}, AuthStatic},
		{"oidc password", oidcExec("--oidc-issuer-url=https://idp", "--oidc-client-id=k8s", "--grant-type=password"), AuthOIDCPassword},
		{"oidc authcode by default", oidcExec("--oidc-issuer-url=https://idp", "--oidc-client-id=k8s"), AuthOIDCAuthCode},
		{"oidc without issuer", oidcExec("--grant-type=password"), AuthExec},
		{"aws", &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "aws", Args: []string{"eks", "get-token"}}}, AuthExec},
	}
	for _, c := range cases {
		kind, params := ClassifyAuth(kubeconfigWith(c.ai, nil), "ctx")
		if kind != c.want {
			t.Errorf("%s: got %s, want %s", c.name, kind, c.want)
		}
		if (kind == AuthOIDCPassword) != (params != nil) {
			t.Errorf("%s: parameters must come with the password grant only", c.name)
		}
	}
	if kind, _ := ClassifyAuth(kubeconfigWith(&clientcmdapi.AuthInfo{}, nil), "missing"); kind != AuthStatic {
		t.Errorf("unknown context: %s", kind)
	}
}

func TestPolicyAllowsEverythingOnDesktop(t *testing.T) {
	cfg := kubeconfigWith(&clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "aws"}, ClientKey: "/home/u/key"},
		&clientcmdapi.Cluster{Server: "https://x", CertificateAuthority: "/home/u/ca.crt"})
	if err := CheckKubeconfigPolicy(cfg, "ctx", false); err != nil {
		t.Fatalf("desktop must behave like kubectl: %v", err)
	}
}

func TestWebPolicy(t *testing.T) {
	restrictive(t)
	aws := &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "aws", Args: []string{"eks", "get-token"}}}
	login := oidcExec("--oidc-issuer-url=https://idp", "--oidc-client-id=k8s", "--grant-type=password")
	browser := oidcExec("--oidc-issuer-url=https://idp", "--oidc-client-id=k8s")

	for _, c := range []struct {
		name  string
		cfg   *clientcmdapi.Config
		creds bool
		want  error
	}{
		{"token is fine", kubeconfigWith(&clientcmdapi.AuthInfo{Token: "t"}, nil), false, nil},
		{"embedded certificates are fine", kubeconfigWith(&clientcmdapi.AuthInfo{ClientCertificateData: []byte("c"), ClientKeyData: []byte("k")},
			&clientcmdapi.Cluster{Server: "https://x", CertificateAuthorityData: []byte("ca")}), false, nil},
		{"exec is forbidden", kubeconfigWith(aws, nil), false, ErrExecForbidden},
		{"exec stays forbidden even with oidc credentials", kubeconfigWith(aws, nil), true, ErrExecForbidden},
		{"oidc login asks for credentials", kubeconfigWith(login, nil), false, ErrOIDCLoginRequired},
		{"oidc login with credentials is accepted", kubeconfigWith(login, nil), true, nil},
		{"browser sign-in is explained", kubeconfigWith(browser, nil), false, ErrOIDCAuthCode},
		{"browser sign-in is not rescued by credentials", kubeconfigWith(browser, nil), true, ErrOIDCAuthCode},
		{"ca file", kubeconfigWith(&clientcmdapi.AuthInfo{Token: "t"}, &clientcmdapi.Cluster{Server: "https://x", CertificateAuthority: "/etc/ssl/ca.pem"}), false, ErrLocalFilesForbidden},
		{"client key file", kubeconfigWith(&clientcmdapi.AuthInfo{ClientCertificateData: []byte("c"), ClientKey: "/root/.ssh/id"}, nil), false, ErrLocalFilesForbidden},
		{"token file", kubeconfigWith(&clientcmdapi.AuthInfo{TokenFile: "/var/run/secrets/token"}, nil), false, ErrLocalFilesForbidden},
	} {
		if err := CheckKubeconfigPolicy(c.cfg, "ctx", c.creds); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

func TestNewOIDCSecretValidation(t *testing.T) {
	p := oidcpassword.ExecParams{IssuerURL: "https://idp.example/realms/x", ClientID: "k8s", Scopes: []string{"groups"}}
	sec, err := newOIDCSecret(p, OIDCCredentials{Username: " vasya ", Password: "pw"})
	if err != nil || sec.Username != "vasya" || sec.Password != "pw" || sec.ClientID != "k8s" {
		t.Fatalf("%+v %v", sec, err)
	}
	for name, c := range map[string]struct {
		p    oidcpassword.ExecParams
		cred OIDCCredentials
	}{
		"no login":    {p, OIDCCredentials{Password: "pw"}},
		"no password": {p, OIDCCredentials{Username: "v"}},
		"http issuer": {oidcpassword.ExecParams{IssuerURL: "http://idp", ClientID: "k8s"}, OIDCCredentials{Username: "v", Password: "pw"}},
		"no host":     {oidcpassword.ExecParams{IssuerURL: "https://", ClientID: "k8s"}, OIDCCredentials{Username: "v", Password: "pw"}},
	} {
		if _, err := newOIDCSecret(c.p, c.cred); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestInClusterAvailability(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	if InClusterAvailable() {
		t.Fatal("outside a cluster")
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	if !InClusterAvailable() {
		t.Fatal("inside a cluster")
	}
}
