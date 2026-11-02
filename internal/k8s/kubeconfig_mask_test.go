// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

const (
	secretToken  = "s3cr3t-token-value"
	secretPass   = "s3cr3t-password-value"
	secretKeyRaw = "-----BEGIN PRIVATE KEY-----fake-----END PRIVATE KEY-----"
	secretEnv    = "s3cr3t-env-value"
	secretArg    = "s3cr3t-arg-value"
	secretRefr   = "s3cr3t-refresh-value"
)

func maskFixture() string {
	return `apiVersion: v1
kind: Config
current-context: ctx
clusters:
- name: c
  cluster:
    server: https://10.0.0.1:6443
    certificate-authority-data: Q0E=
contexts:
- name: ctx
  context:
    cluster: c
    user: u
- name: ctx2
  context:
    cluster: c
    user: u3
users:
# комментарий сохраняется
- name: u
  user:
    token: ` + secretToken + `
- name: u1
  user:
    username: admin
    password: ` + secretPass + `
- name: u2
  user:
    client-certificate-data: Q0VSVA==
    client-key-data: ` + base64.StdEncoding.EncodeToString([]byte(secretKeyRaw)) + `
- name: u3
  user:
    auth-provider:
      name: oidc
      config:
        idp-issuer-url: https://idp.example
        refresh-token: ` + secretRefr + `
- name: u4
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: kubelogin
      args:
      - get-token
      - --oidc-client-secret=` + secretArg + `
      env:
      - name: TOKEN
        value: ` + secretEnv + `
`
}

func allSecrets() []string {
	return []string{secretToken, secretPass, base64.StdEncoding.EncodeToString([]byte(secretKeyRaw)), secretKeyRaw, secretEnv, secretArg, secretRefr}
}

func TestMaskKubeconfigSecretsHidesEverything(t *testing.T) {
	out, err := MaskKubeconfigSecrets(maskFixture())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range allSecrets() {
		if strings.Contains(out, s) {
			t.Errorf("the secret %q leaked into the masked text:\n%s", s, out)
		}
	}
	if n := strings.Count(out, MaskPlaceholder); n != 6 {
		t.Errorf("expected 6 placeholders, got %d:\n%s", n, out)
	}
	for _, keep := range []string{"https://10.0.0.1:6443", "Q0E=", "Q0VSVA==", "# комментарий сохраняется", "idp-issuer-url: https://idp.example", "--oidc-client-secret=" + MaskPlaceholder} {
		if !strings.Contains(out, keep) {
			t.Errorf("%q must stay in the masked text:\n%s", keep, out)
		}
	}
	if len(LeakedSecrets(out)) != 0 {
		t.Errorf("LeakedSecrets found secrets in the masked text")
	}
}

func TestMaskKubeconfigSecretsIsIdempotentAndEmptySafe(t *testing.T) {
	once, err := MaskKubeconfigSecrets(maskFixture())
	if err != nil {
		t.Fatal(err)
	}
	twice, err := MaskKubeconfigSecrets(once)
	if err != nil || twice != once {
		t.Fatalf("masking twice changed the text: %v", err)
	}
	if out, err := MaskKubeconfigSecrets(""); err != nil || out != "" {
		t.Errorf("empty text: %q %v", out, err)
	}
}

func TestExpandKubeconfigSecretsRestoresStored(t *testing.T) {
	stored := maskFixture()
	masked, _ := MaskKubeconfigSecrets(stored)
	out, err := ExpandKubeconfigSecrets(masked, stored)
	if err != nil {
		t.Fatal(err)
	}
	if HasSecretPlaceholder(out) {
		t.Fatalf("placeholder left after expansion:\n%s", out)
	}
	if KubeconfigSecretsChanged(out, stored) {
		t.Errorf("expanded text must carry the same secrets as the stored one")
	}
	for _, s := range []string{secretToken, secretPass, secretEnv, secretArg, secretRefr} {
		if !strings.Contains(out, s) {
			t.Errorf("secret %q was not restored", s)
		}
	}
}

func TestExpandKubeconfigSecretsKeepsReplacementValue(t *testing.T) {
	stored := maskFixture()
	masked, _ := MaskKubeconfigSecrets(stored)
	edited := strings.Replace(masked, "token: "+MaskPlaceholder, "token: brand-new-token", 1)
	out, err := ExpandKubeconfigSecrets(edited, stored)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "brand-new-token") || strings.Contains(out, secretToken) {
		t.Errorf("the typed value must replace the stored token:\n%s", out)
	}
	if !strings.Contains(out, secretPass) {
		t.Errorf("other hidden secrets must still be restored")
	}
	if !KubeconfigSecretsChanged(out, stored) {
		t.Errorf("a replaced token must be reported as changed")
	}
}

// Секрет нельзя перенести на другой сервер: подстановка отказывает, если адрес кластера контекста изменился.
func TestExpandKubeconfigSecretsRefusesChangedServer(t *testing.T) {
	stored := maskFixture()
	masked, _ := MaskKubeconfigSecrets(stored)
	moved := strings.Replace(masked, "https://10.0.0.1:6443", "https://evil.example:6443", 1)
	out, err := ExpandKubeconfigSecrets(moved, stored)
	if !errors.Is(err, ErrSecretPlaceholderRebound) {
		t.Fatalf("expected ErrSecretPlaceholderRebound, got %v", err)
	}
	if out != "" {
		t.Errorf("no text may be returned on refusal")
	}
}

// Секрет нельзя перенести на другого пользователя: плейсхолдер у нового пользователя не имеет сохранённого значения.
func TestExpandKubeconfigSecretsRefusesRenamedUser(t *testing.T) {
	stored := maskFixture()
	masked, _ := MaskKubeconfigSecrets(stored)
	renamed := strings.Replace(masked, "- name: u\n", "- name: stolen\n", 1)
	renamed = strings.Replace(renamed, "user: u\n", "user: stolen\n", 1)
	if _, err := ExpandKubeconfigSecrets(renamed, stored); err == nil {
		t.Fatalf("a renamed user must not inherit hidden secrets")
	}
}

func TestExpandKubeconfigSecretsPlaceholderWithoutCounterpart(t *testing.T) {
	stored := strings.Replace(maskFixture(), "password: "+secretPass, "", 1)
	masked, _ := MaskKubeconfigSecrets(maskFixture())
	if _, err := ExpandKubeconfigSecrets(masked, stored); err == nil {
		t.Fatalf("a placeholder without a saved counterpart must be an error")
	}
}

func TestExpandKubeconfigSecretsMisplacedPlaceholder(t *testing.T) {
	stored := maskFixture()
	masked, _ := MaskKubeconfigSecrets(stored)
	bad := strings.Replace(masked, "https://10.0.0.1:6443", "https://"+MaskPlaceholder, 1)
	if _, err := ExpandKubeconfigSecrets(bad, stored); err == nil {
		t.Fatalf("a placeholder outside a secret field must be refused")
	}
}

func TestExpandWithoutPlaceholderIsNoop(t *testing.T) {
	text := maskFixture()
	if out, err := ExpandKubeconfigSecrets(text, "unrelated: ["); err != nil || out != text {
		t.Errorf("text without placeholders must pass unchanged: %v", err)
	}
}

func TestKubeconfigSourceReadExpandsFromBaseCluster(t *testing.T) {
	setupClustersHome(t)
	useClusterStore(t, newModeTestStore())
	meta, err := AddClusterFromText(maskFixture(), ClusterSettings{DisplayName: "m", ContextName: "ctx"})
	if err != nil {
		t.Fatal(err)
	}
	masked, err := ReadClusterKubeconfig(meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range allSecrets() {
		if strings.Contains(masked, s) {
			t.Fatalf("ReadClusterKubeconfig leaked %q in service mode", s)
		}
	}
	if _, err := (KubeconfigSource{Text: masked}).read(); !errors.Is(err, ErrSecretPlaceholderNoCluster) {
		t.Errorf("placeholders without a base cluster: %v", err)
	}
	data, err := (KubeconfigSource{Text: masked, BaseClusterID: meta.ID}).read()
	if err != nil || HasSecretPlaceholder(string(data)) || !strings.Contains(string(data), secretToken) {
		t.Errorf("expansion from the base cluster failed: %v", err)
	}
}

// Обновление кластера: секреты не меняются (CredentialsChanged=false) и сохраняются, замена токена фиксируется.
func TestUpdateClusterSettingsKeepsHiddenSecrets(t *testing.T) {
	setupClustersHome(t)
	st := newModeTestStore()
	useClusterStore(t, st)
	meta, err := AddClusterFromText(maskFixture(), ClusterSettings{DisplayName: "m", ContextName: "ctx"})
	if err != nil {
		t.Fatal(err)
	}
	masked, _ := ReadClusterKubeconfig(meta.ID)

	res, err := UpdateClusterSettings(meta.ID, ClusterSettings{DisplayName: "m2", ContextName: "ctx"}, KubeconfigSource{Text: masked + "# edit\n"})
	if err != nil {
		t.Fatal(err)
	}
	if res.CredentialsChanged {
		t.Errorf("secrets were not changed")
	}
	if kc, _ := st.Kubeconfig(meta.ID); !strings.Contains(string(kc), secretToken) || HasSecretPlaceholder(string(kc)) {
		t.Errorf("the stored kubeconfig must keep the real token and no placeholder")
	}

	edited := strings.Replace(masked, "token: "+MaskPlaceholder, "token: rotated", 1)
	res, err = UpdateClusterSettings(meta.ID, ClusterSettings{DisplayName: "m2", ContextName: "ctx"}, KubeconfigSource{Text: edited})
	if err != nil || !res.CredentialsChanged {
		t.Fatalf("a replaced token must be reported: %+v %v", res, err)
	}
	// Чужой BaseClusterID в запросе игнорируется: секреты берутся у редактируемого кластера.
	res, err = UpdateClusterSettings(meta.ID, ClusterSettings{DisplayName: "m2", ContextName: "ctx"}, KubeconfigSource{Text: masked, BaseClusterID: "other"})
	if err != nil {
		t.Fatalf("BaseClusterID must be forced to the edited cluster: %v", err)
	}
	_ = res
}

// Файловое хранилище (десктоп, режим одного пользователя) возвращает текст как раньше.
func TestReadClusterKubeconfigUnmaskedOnFileStore(t *testing.T) {
	setupClustersHome(t)
	useClusterStore(t, newMemClusterStore())
	meta, err := AddClusterFromText(sampleKubeconfig, ClusterSettings{DisplayName: "f", ContextName: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if text, err := ReadClusterKubeconfig(meta.ID); err != nil || text != sampleKubeconfig {
		t.Errorf("desktop text must stay as is: %v", err)
	}
}
