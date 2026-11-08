// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"errors"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/24DSO-lab/kubron/internal/credential"
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

func clientcmdapiToYAML(cfg *clientcmdapi.Config) ([]byte, error) { return clientcmd.Write(*cfg) }

func oidcExec(args ...string) *clientcmdapi.AuthInfo {
	return &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "kubectl", Args: append([]string{"oidc-login", "get-token"}, args...)}}
}

// restrictive включает политику веб-сервера на время теста.
func restrictive(t *testing.T) {
	t.Helper()
	SetConnectionPolicy(ConnectionPolicy{})
	t.Cleanup(func() { SetConnectionPolicy(ConnectionPolicy{AllowLocalFiles: true}) })
}

func TestPolicyAllowsEverythingOnDesktop(t *testing.T) {
	cfg := kubeconfigWith(&clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "aws"}, ClientKey: "/home/u/key"},
		&clientcmdapi.Cluster{Server: "https://x", CertificateAuthority: "/home/u/ca.crt"})
	if err := CheckKubeconfigPolicy(cfg, "ctx"); err != nil {
		t.Fatalf("desktop must behave like kubectl: %v", err)
	}
}

func TestWebPolicy(t *testing.T) {
	restrictive(t)
	aws := &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "aws", Args: []string{"eks", "get-token"}}}
	login := oidcExec("--oidc-issuer-url=https://idp", "--oidc-client-id=k8s", "--grant-type=password")
	browser := oidcExec("--oidc-issuer-url=https://idp", "--oidc-client-id=k8s")

	// Adding a kubeconfig: the sign-in method is the broker's business, so exec kinds are accepted (and stored);
	// only the address and local files are checked.
	for _, c := range []struct {
		name string
		cfg  *clientcmdapi.Config
		want error
	}{
		{"token is fine", kubeconfigWith(&clientcmdapi.AuthInfo{Token: "t"}, nil), nil},
		{"embedded certificates are fine", kubeconfigWith(&clientcmdapi.AuthInfo{ClientCertificateData: []byte("c"), ClientKeyData: []byte("k")},
			&clientcmdapi.Cluster{Server: "https://x", CertificateAuthorityData: []byte("ca")}), nil},
		{"cloud exec is accepted", kubeconfigWith(aws, nil), nil},
		{"oidc password is accepted", kubeconfigWith(login, nil), nil},
		{"oidc browser sign-in is accepted", kubeconfigWith(browser, nil), nil},
		{"ca file", kubeconfigWith(&clientcmdapi.AuthInfo{Token: "t"}, &clientcmdapi.Cluster{Server: "https://x", CertificateAuthority: "/etc/ssl/ca.pem"}), ErrLocalFilesForbidden},
		{"client key file", kubeconfigWith(&clientcmdapi.AuthInfo{ClientCertificateData: []byte("c"), ClientKey: "/root/.ssh/id"}, nil), ErrLocalFilesForbidden},
		{"token file", kubeconfigWith(&clientcmdapi.AuthInfo{TokenFile: "/var/run/secrets/token"}, nil), ErrLocalFilesForbidden},
	} {
		if err := CheckKubeconfigPolicy(c.cfg, "ctx"); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

func TestAuthKindOfFollowsTheBrokersDescription(t *testing.T) {
	for _, c := range []struct {
		name string
		ai   *clientcmdapi.AuthInfo
		want credential.Kind
	}{
		{"token", &clientcmdapi.AuthInfo{Token: "t"}, credential.KindStatic},
		{"no credentials", &clientcmdapi.AuthInfo{}, credential.KindStatic},
		{"oidc password", oidcExec("--oidc-issuer-url=https://idp", "--oidc-client-id=k8s", "--grant-type=password"), credential.KindOIDC},
		{"oidc browser", oidcExec("--oidc-issuer-url=https://idp", "--oidc-client-id=k8s"), credential.KindOIDC},
		{"other exec", &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "my-plugin", APIVersion: "client.authentication.k8s.io/v1"}}, credential.KindExec},
	} {
		cfg := kubeconfigWith(c.ai, nil)
		data, err := clientcmdapiToYAML(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if got := authKindOf(cfg, data, "ctx"); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestSharedAccountMode(t *testing.T) {
	shared := ClusterSettings{SharedAccount: true}
	for _, c := range []struct {
		name    string
		current string
		s       ClusterSettings
		teamID  string
		signsIn bool
		want    string
		wantErr error
	}{
		{"off", "", ClusterSettings{}, "t1", true, "", nil},
		{"on", "", shared, "t1", true, ModeTeamAccount, nil},
		{"on without a team", "", shared, "", true, "", errSharedNeedsTeam},
		{"on for static credentials", "", shared, "t1", false, "", errSharedNeedsSignIn},
		{"already on is not checked again", ModeTeamAccount, shared, "t1", false, ModeTeamAccount, nil},
		{"switched off", ModeTeamAccount, ClusterSettings{}, "t1", true, "", nil},
		{"in-cluster is never changed", ModeInCluster, shared, "t1", true, ModeInCluster, nil},
		{"in-cluster stays without the flag", ModeInCluster, ClusterSettings{}, "t1", false, ModeInCluster, nil},
	} {
		got, err := modeForSettings(c.current, c.s, c.teamID, c.signsIn)
		if !errors.Is(err, c.wantErr) || got != c.want {
			t.Errorf("%s: got %q, %v; want %q, %v", c.name, got, err, c.want, c.wantErr)
		}
	}
}

func TestContextSignsIn(t *testing.T) {
	if contextSignsIn(kubeconfigWith(&clientcmdapi.AuthInfo{Token: "t"}, nil), "ctx") {
		t.Error("a token does not sign in")
	}
	if !contextSignsIn(kubeconfigWith(&clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "aws"}}, nil), "ctx") {
		t.Error("exec signs in")
	}
	if !contextSignsIn(kubeconfigWith(&clientcmdapi.AuthInfo{AuthProvider: &clientcmdapi.AuthProviderConfig{Name: "oidc"}}, nil), "ctx") {
		t.Error("auth-provider signs in")
	}
	if contextSignsIn(kubeconfigWith(&clientcmdapi.AuthInfo{}, nil), "missing") {
		t.Error("unknown context")
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
