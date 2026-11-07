// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/24DSO-labs/kubron/internal/credential"
)

const idpCAPEM = "-----BEGIN CERTIFICATE-----\nIDP-CA-FILE-CONTENT\n-----END CERTIFICATE-----\n"

func oidcLoginKubeconfig(caArg ...string) string {
	args := "      - oidc-login\n      - get-token\n      - --oidc-issuer-url=https://idp.example.com\n      - --oidc-client-id=kubron\n      - --grant-type=password\n"
	for _, a := range caArg {
		args += "      - " + a + "\n"
	}
	return `apiVersion: v1
kind: Config
current-context: ctx
clusters:
- name: c
  cluster:
    server: https://203.0.113.10:6443
    insecure-skip-tls-verify: true
contexts:
- name: ctx
  context: {cluster: c, user: u}
users:
- name: u
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: kubectl
      args:
` + args
}

func describe(t *testing.T, kc []byte) credential.Descriptor {
	t.Helper()
	d, err := credential.Describe(kc, "")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestEmbedIdPFilesExecArgsAbsoluteAndRelative(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "idp-ca.pem"), []byte(idpCAPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	want := base64.StdEncoding.EncodeToString([]byte(idpCAPEM))
	for name, arg := range map[string][]string{
		"equals":   {"--certificate-authority=" + filepath.Join(dir, "idp-ca.pem")},
		"separate": {"--certificate-authority", filepath.Join(dir, "idp-ca.pem")},
		"relative": {"--certificate-authority=idp-ca.pem"},
	} {
		out, err := embedIdPFiles([]byte(oidcLoginKubeconfig(arg...)), "", dir)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		d := describe(t, out)
		if d.Params["ca-data"] != want {
			t.Errorf("%s: ca-data = %q, want the content of the file", name, d.Params["ca-data"])
		}
		if strings.Contains(string(out), "--certificate-authority=") || strings.Contains(string(out), "idp-ca.pem") {
			t.Errorf("%s: the file reference is still in the kubeconfig that goes to the broker", name)
		}
	}
}

func TestEmbedIdPFilesExplicitDataWins(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), []byte(idpCAPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := embedIdPFiles([]byte(oidcLoginKubeconfig("--certificate-authority-data=RElSRUNU", "--certificate-authority=ca.pem")), "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := describe(t, out).Params["ca-data"]; got != "RElSRUNU" {
		t.Fatalf("ca-data = %q, the explicit -data value must win", got)
	}
}

func TestEmbedIdPFilesAuthProvider(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), []byte(idpCAPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	kc := `apiVersion: v1
kind: Config
current-context: ctx
clusters:
- name: c
  cluster: {server: "https://203.0.113.10:6443"}
contexts:
- name: ctx
  context: {cluster: c, user: u}
users:
- name: u
  user:
    auth-provider:
      name: oidc
      config:
        idp-issuer-url: https://idp.example.com
        client-id: kubron
        idp-certificate-authority: ca.pem
`
	out, err := embedIdPFiles([]byte(kc), "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := describe(t, out).Params["ca-data"]; got != base64.StdEncoding.EncodeToString([]byte(idpCAPEM)) {
		t.Fatalf("ca-data = %q", got)
	}
}

func TestEmbedIdPFilesNothingToDoReturnsTheSameBytes(t *testing.T) {
	for _, kc := range []string{oidcLoginKubeconfig(), execKubeconfig, sampleKubeconfig} {
		out, err := embedIdPFiles([]byte(kc), "", t.TempDir())
		if err != nil || string(out) != kc {
			t.Fatalf("a kubeconfig without IdP files was changed: %v", err)
		}
	}
}

func TestEmbedIdPFilesErrorsDoNotLeakPathOrContent(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.pem")
	if err := os.WriteFile(big, make([]byte, maxIdPCAFileSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, arg := range map[string]string{"missing": "--certificate-authority=" + filepath.Join(dir, "secret-name.pem"), "too large": "--certificate-authority=" + big} {
		_, err := embedIdPFiles([]byte(oidcLoginKubeconfig(arg)), "", dir)
		if !errors.Is(err, ErrIdPFile) {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(err.Error(), "secret-name") || strings.Contains(err.Error(), dir) {
			t.Fatalf("%s: the path leaked: %v", name, err)
		}
	}
}

// The desktop embeds the files before the broker describes the sign-in; the web never reads them.
func TestLoadClusterAuthSourceEmbedsIdPFilesOnlyOnTheDesktop(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "idp-ca.pem")
	if err := os.WriteFile(ca, []byte(idpCAPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	SetKubronDir(t.TempDir())
	t.Cleanup(func() {
		SetKubronDir("")
		SetConnectionPolicy(ConnectionPolicy{AllowLocalFiles: true})
	})
	SetConnectionPolicy(ConnectionPolicy{AllowLocalFiles: true})
	meta, err := AddClusterFromText(oidcLoginKubeconfig("--certificate-authority="+ca), ClusterSettings{DisplayName: "c"})
	if err != nil {
		t.Fatal(err)
	}
	want := base64.StdEncoding.EncodeToString([]byte(idpCAPEM))

	SetConnectionPolicy(ConnectionPolicy{AllowLocalFiles: true})
	src, err := LoadClusterAuthSource(meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := describe(t, src.Kubeconfig).Params["ca-data"]; got != want {
		t.Fatalf("desktop: ca-data = %q, want the file embedded", got)
	}

	SetConnectionPolicy(ConnectionPolicy{}) // web: no exec, no local files
	if err := os.Remove(ca); err != nil {
		t.Fatal(err)
	}
	src, err = LoadClusterAuthSource(meta.ID)
	if err != nil {
		t.Fatalf("web: %v", err)
	}
	if got := describe(t, src.Kubeconfig).Params["ca-data"]; got != "" {
		t.Fatalf("web: ca-data = %q, files of the server machine must not be read", got)
	}
	if !strings.Contains(string(src.Kubeconfig), "--certificate-authority="+ca) {
		t.Fatal("web: the reference must stay as it is (and unused)")
	}
}
