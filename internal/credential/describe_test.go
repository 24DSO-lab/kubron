// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".kubeconfig"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The golden files are reviewed by hand when they change: they are the documentation of what Describe
// returns for real-world kubeconfigs. The table test below is the independent oracle for the key facts.
func TestDescribeGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.kubeconfig"))
	if err != nil || len(files) < 20 {
		t.Fatalf("corpus is missing: %d files, %v", len(files), err)
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".kubeconfig")
		t.Run(name, func(t *testing.T) {
			d, derr := Describe(readTestdata(t, name), "")
			var out any = d
			if derr != nil {
				out = map[string]string{"error": derr.Error()}
			}
			got, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			golden := filepath.Join("testdata", name+".golden.json")
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("no golden file (run with -update and review it): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("golden mismatch for %s\n--- got\n%s--- want\n%s", name, got, want)
			}
		})
	}
}

func TestDescribeKinds(t *testing.T) {
	tests := []struct {
		file    string
		kind    Kind
		params  map[string]string // must be present with exactly these values
		absent  []string          // must not be present
		hasExec bool
		execCmd string
	}{
		{"static-token", KindStatic, nil, []string{"grant"}, false, ""},
		{"static-token-openshift", KindStatic, nil, nil, false, ""},
		{"static-client-cert", KindStatic, nil, nil, false, ""},
		{"static-basic", KindStatic, nil, nil, false, ""},
		{"static-rancher-token", KindStatic, nil, nil, false, ""},
		{"anonymous-no-user", KindStatic, map[string]string{ParamClusterInsecure: "true", ParamClusterTLSName: "api.internal"}, nil, false, ""},
		{"oidc-login-password", KindOIDC, map[string]string{"grant": "password", "issuer-url": "https://idp.example.com/realms/dev",
			"client-id": "kubron", "client-secret": "CLIENT-SECRET-1", "scope": "email groups", "username": "alice", "password": "hunter2"}, nil, true, "kubectl"},
		{"oidc-login-authcode", KindOIDC, map[string]string{"grant": "authcode", "use-pkce": "true", "ca-data": "SURQLUNB",
			"listen-address": "127.0.0.1:8000,127.0.0.1:18000", "redirect-url-hostname": "localhost", "skip-open-browser": "true",
			"auth-request-extra-params": "prompt=login"}, nil, true, "kubectl"},
		{"oidc-login-authcode-keyboard", KindOIDC, map[string]string{"grant": "authcode-keyboard"}, nil, true, "kubectl"},
		{"oidc-login-device-code", KindOIDC, map[string]string{"grant": "device-code"}, nil, true, "kubectl"},
		{"oidc-login-default-grant", KindOIDC, map[string]string{"grant": "authcode"}, []string{"use-pkce"}, true, "kubectl"}, // --oidc-use-pkce=false
		{"oidc-login-auto-grant", KindOIDC, map[string]string{"grant": "authcode"}, nil, true, "kubectl"},
		{"oidc-login-standalone-binary", KindOIDC, map[string]string{"grant": "password"}, nil, true, "/usr/local/bin/kubelogin"},
		{"oidc-login-plugin-binary-windows", KindOIDC, map[string]string{"grant": "authcode"}, nil, true, ""},
		{"auth-provider-oidc-legacy", KindOIDC, map[string]string{"grant": "legacy-refresh", "refresh-token": "REFRESH-TOKEN-VALUE",
			"id-token": "ID.TOKEN.VALUE", "issuer-url": "https://idp.example.com", "scope": "groups email"}, nil, false, ""},
		{"eks-aws-cli", KindEKS, map[string]string{"authenticator": "aws-cli", "cluster-name": "prod-eks", "region": "eu-west-1",
			"role-arn": "arn:aws:iam::123456789012:role/Admin", "profile": "prod"}, nil, true, "aws"},
		{"eks-aws-cli-global-flags", KindEKS, map[string]string{"cluster-name": "demo", "region": "us-east-2"}, nil, true, ""},
		{"eks-iam-authenticator", KindEKS, map[string]string{"authenticator": "aws-iam-authenticator", "cluster-name": "legacy-eks",
			"role-arn": "arn:aws:iam::123456789012:role/Dev"}, nil, true, "aws-iam-authenticator"},
		{"gke-plugin", KindGKE, nil, nil, true, "gke-gcloud-auth-plugin"},
		{"aks-kubelogin-devicecode", KindAKS, map[string]string{"login": "devicecode", "tenant-id": "72f988bf-86f1-41af-91ab-2d7cd011db47",
			"environment": "AzurePublicCloud"}, []string{"issuer-url"}, true, "kubelogin"},
		{"aks-kubelogin-spn", KindAKS, map[string]string{"login": "spn", "client-secret": "AKS-CLIENT-SECRET", "is-legacy": "true"}, nil, true, "kubelogin"},
		{"exec-teleport", KindExec, nil, nil, true, "tsh"},
		{"exec-rancher-kube-api-auth", KindExec, nil, nil, true, "kube-api-auth"},
		{"exec-kubelogin-unknown-flavor", KindExec, nil, nil, true, "kubelogin"},
		{"exec-yandex", KindExec, nil, nil, true, "yc"},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			d, err := Describe(readTestdata(t, tc.file), "")
			if err != nil {
				t.Fatal(err)
			}
			if d.Kind != tc.kind {
				t.Fatalf("kind = %q, want %q", d.Kind, tc.kind)
			}
			for k, v := range tc.params {
				if d.Params[k] != v {
					t.Errorf("param %s = %q, want %q", k, d.Params[k], v)
				}
			}
			for _, k := range tc.absent {
				if v, ok := d.Params[k]; ok {
					t.Errorf("param %s = %q, want it absent", k, v)
				}
			}
			if (d.Exec != nil) != tc.hasExec {
				t.Fatalf("Exec present = %v, want %v", d.Exec != nil, tc.hasExec)
			}
			if tc.execCmd != "" && d.Exec.Command != tc.execCmd {
				t.Errorf("exec command = %q", d.Exec.Command)
			}
			// Every descriptor carries the cluster facts.
			if d.Params[ParamClusterServer] != "https://k8s.example.com:6443" {
				t.Errorf("cluster.server = %q", d.Params[ParamClusterServer])
			}
		})
	}
}

func TestDescribeKeepsExecDataForKnownPlugins(t *testing.T) {
	d, err := Describe(readTestdata(t, "eks-aws-cli"), "")
	if err != nil {
		t.Fatal(err)
	}
	if d.Exec == nil || d.Exec.APIVersion != "client.authentication.k8s.io/v1beta1" || d.Exec.InteractiveMode != "IfAvailable" ||
		d.Exec.Env["AWS_PROFILE"] != "prod" || len(d.Exec.Args) != 8 {
		t.Fatalf("exec = %#v", d.Exec)
	}
	d, _ = Describe(readTestdata(t, "oidc-login-device-code"), "")
	if !strings.Contains(d.Exec.InstallHint, "krew install oidc-login") {
		t.Fatalf("install hint = %q", d.Exec.InstallHint)
	}
}

func TestDescribeContextSelection(t *testing.T) {
	kc := []byte(`apiVersion: v1
current-context: b
clusters:
- {name: ca, cluster: {server: "https://a"}}
- {name: cb, cluster: {server: "https://b"}}
contexts:
- {name: a, context: {cluster: ca, user: ua}}
- {name: b, context: {cluster: cb, user: ub}}
users:
- {name: ua, user: {token: x}}
- {name: ub, user: {exec: {apiVersion: client.authentication.k8s.io/v1, command: gke-gcloud-auth-plugin}}}
`)
	d, err := Describe(kc, "")
	if err != nil || d.Kind != KindGKE || d.Params[ParamClusterServer] != "https://b" {
		t.Fatalf("current context: %v %v", d, err)
	}
	d, err = Describe(kc, "a")
	if err != nil || d.Kind != KindStatic || d.Params[ParamClusterServer] != "https://a" {
		t.Fatalf("explicit context: %v %v", d, err)
	}
	if _, err := Describe(kc, "nope"); !errors.Is(err, ErrContextNotFound) {
		t.Fatalf("missing context: %v", err)
	}
}

func TestDescribeErrors(t *testing.T) {
	const canary = "CANARY-TOKEN-DO-NOT-LEAK"
	tests := map[string]struct {
		in   string
		ctx  string
		want error
	}{
		"empty":            {"", "", ErrInvalidKubeconfig},
		"not yaml":         {"a: [unclosed " + canary, "", ErrInvalidKubeconfig},
		"no current":       {"clusters: []\ncontexts: []\n", "", ErrContextNotFound},
		"missing cluster":  {"contexts:\n- {name: x, context: {cluster: gone, user: u}}\nusers:\n- {name: u, user: {token: " + canary + "}}\n", "x", ErrInvalidKubeconfig},
		"missing user":     {"clusters:\n- {name: c, cluster: {server: s}}\ncontexts:\n- {name: x, context: {cluster: c, user: gone}}\n", "x", ErrInvalidKubeconfig},
		"exec no command":  {"clusters:\n- {name: c, cluster: {server: s}}\ncontexts:\n- {name: x, context: {cluster: c, user: u}}\nusers:\n- {name: u, user: {exec: {apiVersion: v1}}}\n", "x", ErrInvalidKubeconfig},
		"provider unknown": {"clusters:\n- {name: c, cluster: {server: s}}\ncontexts:\n- {name: x, context: {cluster: c, user: u}}\nusers:\n- {name: u, user: {auth-provider: {name: gcp, config: {access-token: " + canary + "}}}}\n", "x", ErrUnsupported},
		"too large":        {strings.Repeat("a", maxKubeconfigSize+1), "", ErrInvalidKubeconfig},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			d, err := Describe([]byte(tc.in), tc.ctx)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if strings.Contains(err.Error(), canary) {
				t.Fatal("kubeconfig content leaked into the error")
			}
			if d.Kind != "" || d.Params != nil || d.Exec != nil {
				t.Fatalf("descriptor returned together with an error: %v", d)
			}
		})
	}
}

func TestDescribeRejectsHugeExecBlocks(t *testing.T) {
	args := strings.Repeat("- x\n", maxExecArgs+1)
	kc := "clusters:\n- {name: c, cluster: {server: s}}\ncontexts:\n- {name: x, context: {cluster: c, user: u}}\nusers:\n- name: u\n  user:\n    exec:\n      command: tool\n      args:\n" +
		strings.ReplaceAll(args, "- x", "      - x")
	if _, err := Describe([]byte(kc), "x"); !errors.Is(err, ErrInvalidKubeconfig) {
		t.Fatalf("err = %v", err)
	}
}

// Describe is a pure function: it must not even be able to touch files, the network, the environment or
// processes. The check is on the imports of its source files.
func TestDescribeHasNoSideEffectImports(t *testing.T) {
	forbidden := map[string]bool{"os": true, "os/exec": true, "net": true, "net/http": true, "io/ioutil": true,
		"path/filepath": true, "syscall": true, "net/url": false}
	for _, f := range []string{"describe.go", "describe_exec.go"} {
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if forbidden[path] {
				t.Errorf("%s imports %q", f, path)
			}
		}
	}
}

func TestDescriptorIsRedacted(t *testing.T) {
	const canary = "CANARY-SECRET-VALUE-42"
	d, err := Describe(readTestdata(t, "oidc-login-password"), "")
	if err != nil {
		t.Fatal(err)
	}
	d.Params["client-secret"] = canary
	d.Exec.Env = map[string]string{"AWS_SECRET_ACCESS_KEY": canary}
	d.Exec.Args = append(d.Exec.Args, "--password="+canary)

	req := Request{Descriptor: d, Subject: Subject{ClusterID: "c1", Scope: ScopeUser, OwnerID: "u1"},
		Saved: []byte(canary), Pending: []byte(canary), Answer: Answer{"password": canary}}
	res := Result{Token: &Token{Value: canary}, Save: []byte(canary), Pending: []byte(canary)}

	var textLog, jsonLog bytes.Buffer
	slog.New(slog.NewTextHandler(&textLog, nil)).Info("x", "d", d, "exec", d.Exec, "req", req, "res", res)
	slog.New(slog.NewJSONHandler(&jsonLog, nil)).Info("x", "d", d, "exec", *d.Exec, "req", req, "res", res)

	outputs := map[string]string{
		"%v d": sprint("%v", d), "%+v d": sprint("%+v", d), "%#v d": sprint("%#v", d), "%s d": sprint("%s", d),
		"%v req": sprint("%v", req), "%+v req": sprint("%+v", req), "%#v req": sprint("%#v", req),
		"%v res": sprint("%v", res), "%+v res": sprint("%+v", res), "%#v res": sprint("%#v", res),
		"%v exec": sprint("%v", d.Exec), "%+v *exec": sprint("%+v", d.Exec), "%#v exec": sprint("%#v", *d.Exec),
		"text log": textLog.String(), "json log": jsonLog.String(),
	}
	for name, out := range outputs {
		if strings.Contains(out, canary) {
			t.Errorf("%s leaks the secret: %s", name, out)
		}
	}
	if s := sprint("%v", d); !strings.Contains(s, "client-secret") || !strings.Contains(s, "oidc") {
		t.Errorf("the redacted form must still be useful for debugging: %s", s)
	}
}

func TestParseFlags(t *testing.T) {
	f := parseFlags([]string{"get-token", "--a=1", "--b", "2", "--oidc-use-pkce", "positional", "-i", "cl", "--c=", "--rep=x", "--rep", "y", "--last"})
	if f.first("a") != "1" || f.first("b") != "2" || f.first("i") != "cl" || !f.isTrue("oidc-use-pkce") ||
		strings.Join(f["rep"], ",") != "x,y" || !f.has("c", "last") || f.has("positional") {
		t.Fatalf("flags = %#v", f)
	}
	if f.first("oidc-use-pkce") != "" {
		t.Fatalf("a boolean flag swallowed the next argument: %#v", f)
	}
}

func FuzzDescribe(f *testing.F) {
	files, _ := filepath.Glob(filepath.Join("testdata", "*.kubeconfig"))
	for _, p := range files {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b, "")
		}
	}
	f.Add([]byte("{}"), "x")
	f.Add([]byte("a: &a [*a]"), "")
	f.Add([]byte("\x00\xff"), "")
	f.Fuzz(func(t *testing.T, in []byte, ctx string) {
		d, err := Describe(in, ctx)
		if err != nil {
			if !errors.Is(err, ErrInvalidKubeconfig) && !errors.Is(err, ErrContextNotFound) && !errors.Is(err, ErrUnsupported) {
				t.Fatalf("unexpected error %v", err)
			}
			if d.Kind != "" || d.Params != nil || d.Exec != nil {
				t.Fatal("descriptor together with an error")
			}
			return
		}
		switch d.Kind {
		case KindStatic, KindOIDC, KindEKS, KindGKE, KindAKS, KindExec:
		default:
			t.Fatalf("unknown kind %q", d.Kind)
		}
		if d.Params == nil {
			t.Fatal("nil Params")
		}
		if (d.Kind == KindEKS || d.Kind == KindGKE || d.Kind == KindAKS || d.Kind == KindExec) && (d.Exec == nil || d.Exec.Command == "") {
			t.Fatalf("kind %s without an exec block", d.Kind)
		}
		_ = d.String()
	})
}

func sprint(format string, v any) string { return fmt.Sprintf(format, v) }
