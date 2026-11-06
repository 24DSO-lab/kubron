// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"errors"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	// ErrInvalidKubeconfig means that the kubeconfig cannot be parsed or is inconsistent.
	ErrInvalidKubeconfig = errors.New("the kubeconfig could not be read")
	// ErrContextNotFound means that the requested context (or a current one) does not exist.
	ErrContextNotFound = errors.New("the kubeconfig context was not found")
)

const (
	maxKubeconfigSize = 4 << 20
	maxExecArgs       = 256
	maxExecArgLen     = 8 << 10
	maxExecEnv        = 128
)

// Keys of Descriptor.Params that Describe fills for every kind (taken from the kubeconfig cluster entry).
const (
	ParamClusterServer   = "cluster.server"
	ParamClusterCAData   = "cluster.certificate-authority-data"
	ParamClusterTLSName  = "cluster.tls-server-name"
	ParamClusterInsecure = "cluster.insecure-skip-tls-verify" // "true" only
)

// ParamExecProvideClusterInfo is the value of provideClusterInfo of the exec block ("true" or "false"). It is set only
// when the block states the field; without it the exec plugin provider passes the public cluster.* data, as before.
const ParamExecProvideClusterInfo = "exec.provideClusterInfo"

type kcFile struct {
	CurrentContext string      `yaml:"current-context"`
	Contexts       []kcContext `yaml:"contexts"`
	Clusters       []kcCluster `yaml:"clusters"`
	Users          []kcUser    `yaml:"users"`
}

type kcContext struct {
	Name    string `yaml:"name"`
	Context struct {
		Cluster string `yaml:"cluster"`
		User    string `yaml:"user"`
	} `yaml:"context"`
}

type kcCluster struct {
	Name    string `yaml:"name"`
	Cluster struct {
		Server                string `yaml:"server"`
		CertificateAuthority  string `yaml:"certificate-authority"`
		CertificateAuthData   string `yaml:"certificate-authority-data"`
		TLSServerName         string `yaml:"tls-server-name"`
		InsecureSkipTLSVerify bool   `yaml:"insecure-skip-tls-verify"`
	} `yaml:"cluster"`
}

type kcUser struct {
	Name string `yaml:"name"`
	User struct {
		Exec         *kcExec         `yaml:"exec"`
		AuthProvider *kcAuthProvider `yaml:"auth-provider"`
	} `yaml:"user"`
}

type kcExec struct {
	APIVersion      string `yaml:"apiVersion"`
	Command         string `yaml:"command"`
	Args            []string
	Env             []struct{ Name, Value string }
	InteractiveMode string `yaml:"interactiveMode"`
	InstallHint     string `yaml:"installHint"`
	// ProvideClusterInfo is a pointer: a block that does not state the field differs from one that says false.
	ProvideClusterInfo *bool `yaml:"provideClusterInfo"`
}

type kcAuthProvider struct {
	Name   string            `yaml:"name"`
	Config map[string]string `yaml:"config"`
}

// Describe determines how a context of a kubeconfig signs in. It is a pure function: it reads nothing but
// its arguments (no files, no network, no environment) and never runs the exec plugin. An empty
// contextName selects the current context.
//
// The result carries only what the kubeconfig states. Params may contain secrets (client secret, refresh
// token); Descriptor redacts them when printed or logged, but callers must still keep it out of the API.
// Errors never include kubeconfig content.
//
// Params keys by kind (all values are strings):
//
//	all kinds   cluster.server, cluster.certificate-authority-data, cluster.tls-server-name,
//	            cluster.insecure-skip-tls-verify ("true" only)
//	KindOIDC    grant, issuer-url, client-id, client-secret, scope (space separated), use-pkce,
//	            use-access-token, ca-data, listen-address (comma separated), redirect-url-hostname,
//	            skip-open-browser, auth-request-extra-params (k=v joined by "&"), username, password,
//	            insecure-skip-tls-verify; for grant legacy-refresh also id-token and refresh-token
//	KindEKS     authenticator ("aws-cli" or "aws-iam-authenticator"), cluster-name, region, role-arn, profile
//	exec block  exec.provideClusterInfo ("true" or "false"), only when the block states provideClusterInfo
//	KindGKE     use-adc ("true" only)
//	KindAKS     login, server-id, client-id, tenant-id, environment, client-secret, username, password,
//	            authority-host, identity-resource-id, federated-token-file, is-legacy
//
// The static kind has no kind-specific Params. Exec is filled for every exec-based kind, including the
// known plugins, because the desktop needs it.
func Describe(kubeconfig []byte, contextName string) (Descriptor, error) {
	if len(kubeconfig) == 0 || len(kubeconfig) > maxKubeconfigSize {
		return Descriptor{}, ErrInvalidKubeconfig
	}
	var kc kcFile
	if err := yaml.Unmarshal(kubeconfig, &kc); err != nil {
		return Descriptor{}, ErrInvalidKubeconfig
	}
	name := contextName
	if name == "" {
		name = kc.CurrentContext
	}
	if name == "" {
		return Descriptor{}, ErrContextNotFound
	}
	var ctx *kcContext
	for i := range kc.Contexts {
		if kc.Contexts[i].Name == name {
			ctx = &kc.Contexts[i]
			break
		}
	}
	if ctx == nil {
		return Descriptor{}, ErrContextNotFound
	}

	d := Descriptor{Kind: KindStatic, Params: map[string]string{}}

	var cl *kcCluster
	for i := range kc.Clusters {
		if kc.Clusters[i].Name == ctx.Context.Cluster {
			cl = &kc.Clusters[i]
			break
		}
	}
	if cl == nil {
		return Descriptor{}, ErrInvalidKubeconfig
	}
	set(d.Params, ParamClusterServer, cl.Cluster.Server)
	set(d.Params, ParamClusterCAData, cl.Cluster.CertificateAuthData)
	set(d.Params, ParamClusterTLSName, cl.Cluster.TLSServerName)
	if cl.Cluster.InsecureSkipTLSVerify {
		d.Params[ParamClusterInsecure] = "true"
	}

	if ctx.Context.User == "" {
		return d, nil // no user entry: anonymous access, nothing to sign in
	}
	var u *kcUser
	for i := range kc.Users {
		if kc.Users[i].Name == ctx.Context.User {
			u = &kc.Users[i]
			break
		}
	}
	if u == nil {
		return Descriptor{}, ErrInvalidKubeconfig
	}

	switch {
	case u.User.Exec != nil:
		spec, err := execSpec(u.User.Exec)
		if err != nil {
			return Descriptor{}, err
		}
		d.Exec = spec
		d.Kind = classifyExec(spec, d.Params)
		if v := u.User.Exec.ProvideClusterInfo; v != nil {
			d.Params[ParamExecProvideClusterInfo] = strconv.FormatBool(*v)
		}
	case u.User.AuthProvider != nil:
		if err := describeAuthProvider(u.User.AuthProvider, &d); err != nil {
			return Descriptor{}, err
		}
	}
	return d, nil
}

func set(m map[string]string, k, v string) {
	if v != "" {
		m[k] = v
	}
}

func execSpec(e *kcExec) (*ExecSpec, error) {
	if e.Command == "" || len(e.Args) > maxExecArgs || len(e.Env) > maxExecEnv || len(e.Command) > maxExecArgLen {
		return nil, ErrInvalidKubeconfig
	}
	spec := &ExecSpec{APIVersion: e.APIVersion, Command: e.Command, InteractiveMode: e.InteractiveMode, InstallHint: e.InstallHint}
	spec.Args = make([]string, 0, len(e.Args))
	for _, a := range e.Args {
		if len(a) > maxExecArgLen {
			return nil, ErrInvalidKubeconfig
		}
		spec.Args = append(spec.Args, a)
	}
	if len(e.Env) > 0 {
		spec.Env = make(map[string]string, len(e.Env))
		for _, kv := range e.Env {
			spec.Env[kv.Name] = kv.Value
		}
	}
	return spec, nil
}

// describeAuthProvider handles the legacy auth-provider block. Only "oidc" is supported; the gcp and azure
// providers were removed from client-go long ago and their kubeconfigs need a new plugin-based login.
func describeAuthProvider(p *kcAuthProvider, d *Descriptor) error {
	if p.Name != "oidc" {
		return ErrUnsupported
	}
	d.Kind = KindOIDC
	d.Params["grant"] = "legacy-refresh"
	c := p.Config
	set(d.Params, "issuer-url", c["idp-issuer-url"])
	set(d.Params, "client-id", c["client-id"])
	set(d.Params, "client-secret", c["client-secret"])
	set(d.Params, "id-token", c["id-token"])
	set(d.Params, "refresh-token", c["refresh-token"])
	set(d.Params, "ca-data", c["idp-certificate-authority-data"])
	set(d.Params, "scope", strings.ReplaceAll(c["extra-scopes"], ",", " "))
	return nil
}
