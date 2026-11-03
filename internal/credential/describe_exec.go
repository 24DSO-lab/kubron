// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"slices"
	"strings"
)

// flags is a parsed command line: values per long or short name (repeated flags keep every value) and
// whether a flag was present.
type flags map[string][]string

func (f flags) first(names ...string) string {
	for _, n := range names {
		if v := f[n]; len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

func (f flags) has(names ...string) bool {
	for _, n := range names {
		if _, ok := f[n]; ok {
			return true
		}
	}
	return false
}

func (f flags) isTrue(names ...string) bool {
	for _, n := range names {
		if v, ok := f[n]; ok && len(v) > 0 {
			switch strings.ToLower(v[len(v)-1]) {
			case "", "true", "1", "yes":
				return true
			}
		}
	}
	return false
}

// boolFlags never take a separate value (--flag value would otherwise swallow the next argument).
var boolFlags = map[string]bool{
	"oidc-use-pkce": true, "oidc-use-access-token": true, "skip-open-browser": true, "insecure-skip-tls-verify": true,
	"force-refresh": true, "is-legacy": true, "token-only": true, "use_application_default_credentials": true,
	"use-application-default-credentials": true, "legacy": true, "disable-instance-discovery": true, "v": true,
	"verbose": true,
}

// parseFlags splits args into flags. It understands --name=value, --name value, -n value and bare flags.
// Positional arguments are ignored. It never fails: anything odd is simply not recognized.
func parseFlags(args []string) flags {
	f := flags{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" || a == "--" {
			continue
		}
		name := strings.TrimLeft(a, "-")
		val := ""
		hasVal := false
		if k, v, ok := strings.Cut(name, "="); ok {
			name, val, hasVal = k, v, true
		}
		if !hasVal && !boolFlags[name] && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			val = args[i+1]
			i++
		}
		f[name] = append(f[name], val)
	}
	return f
}

// baseName returns the file name of a command without directories (both separators) and ".exe".
func baseName(cmd string) string {
	if i := strings.LastIndexAny(cmd, `/\`); i >= 0 {
		cmd = cmd[i+1:]
	}
	cmd = strings.ToLower(cmd)
	return strings.TrimSuffix(cmd, ".exe")
}

// classifyExec decides the kind of an exec block and adds the kind-specific Params.
func classifyExec(e *ExecSpec, params map[string]string) Kind {
	base := baseName(e.Command)
	f := parseFlags(e.Args)

	switch {
	case isOIDCLogin(base, e.Args, f):
		oidcParams(f, params)
		return KindOIDC
	case base == "gke-gcloud-auth-plugin":
		if f.isTrue("use_application_default_credentials", "use-application-default-credentials") {
			params["use-adc"] = "true"
		}
		return KindGKE
	case base == "aws-iam-authenticator" || base == "heptio-authenticator-aws":
		params["authenticator"] = "aws-iam-authenticator"
		set(params, "cluster-name", f.first("i", "cluster-id"))
		set(params, "role-arn", f.first("r", "role"))
		set(params, "region", e.Env["AWS_REGION"])
		set(params, "profile", e.Env["AWS_PROFILE"])
		return KindEKS
	case base == "aws" && hasSequence(e.Args, "eks", "get-token"):
		params["authenticator"] = "aws-cli"
		set(params, "cluster-name", f.first("cluster-name"))
		set(params, "region", firstNonEmpty(f.first("region"), e.Env["AWS_REGION"], e.Env["AWS_DEFAULT_REGION"]))
		set(params, "role-arn", f.first("role-arn", "role"))
		set(params, "profile", firstNonEmpty(f.first("profile"), e.Env["AWS_PROFILE"]))
		return KindEKS
	case base == "kubelogin" && (f.has("login") || f.has("server-id")) && !f.has("oidc-issuer-url"):
		for _, k := range []string{"login", "server-id", "client-id", "tenant-id", "environment", "client-secret",
			"username", "password", "authority-host", "identity-resource-id", "federated-token-file"} {
			set(params, k, f.first(k))
		}
		if f.isTrue("is-legacy", "legacy") {
			params["is-legacy"] = "true"
		}
		return KindAKS
	}
	return KindExec
}

// isOIDCLogin recognizes the int128/kubelogin plugin: `kubectl oidc-login get-token`, the plugin binary
// kubectl-oidc_login, or a standalone kubelogin that is given --oidc-issuer-url. The Azure kubelogin shares
// the binary name, so a bare "kubelogin" alone is not enough.
func isOIDCLogin(base string, args []string, f flags) bool {
	if base == "kubectl-oidc_login" || slices.Contains(args, "oidc-login") {
		return true
	}
	return base == "kubelogin" && f.has("oidc-issuer-url")
}

func oidcParams(f flags, p map[string]string) {
	grant := strings.ToLower(f.first("grant-type"))
	if grant == "" || grant == "auto" {
		grant = "authcode" // kubelogin's default (auto) opens the browser like authcode
	}
	p["grant"] = grant
	set(p, "issuer-url", f.first("oidc-issuer-url"))
	set(p, "client-id", f.first("oidc-client-id"))
	set(p, "client-secret", f.first("oidc-client-secret"))
	set(p, "scope", strings.Join(nonEmpty(f["oidc-extra-scope"]), " "))
	set(p, "ca-data", f.first("certificate-authority-data"))
	set(p, "listen-address", strings.Join(nonEmpty(f["listen-address"]), ","))
	set(p, "redirect-url-hostname", f.first("oidc-redirect-url-hostname"))
	set(p, "auth-request-extra-params", strings.Join(nonEmpty(f["oidc-auth-request-extra-params"]), "&"))
	set(p, "username", f.first("username"))
	set(p, "password", f.first("password"))
	for flag, key := range map[string]string{"oidc-use-pkce": "use-pkce", "oidc-use-access-token": "use-access-token",
		"skip-open-browser": "skip-open-browser", "insecure-skip-tls-verify": "insecure-skip-tls-verify"} {
		if f.isTrue(flag) {
			p[key] = "true"
		}
	}
}

func hasSequence(args []string, a, b string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == a && args[i+1] == b {
			return true
		}
	}
	return false
}

func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
