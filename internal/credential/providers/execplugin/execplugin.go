// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package execplugin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

const (
	// Answer field and values of the confirm-exec challenge.
	FieldDecision  = "decision"
	DecisionOnce   = "once"
	DecisionAlways = "always"

	maxMessageRunes = 4096
	redactedMark    = "[redacted]"
	minSecretLen    = 4 // shorter values would mangle ordinary text when removed
	execInfoVar     = "KUBERNETES_EXEC_INFO"
	kindExecCred    = "ExecCredential"
	modeAlways      = "Always"

	// paramProvideClusterInfo is set by the core zone from the kubeconfig field provideClusterInfo. When the
	// parameter is absent the cluster data from cluster.* is passed (it is public data of the cluster).
	paramProvideClusterInfo = "exec.provideClusterInfo"
)

var supportedAPIVersions = map[string]bool{
	"client.authentication.k8s.io/v1":      true,
	"client.authentication.k8s.io/v1beta1": true,
}

// Errors of this provider. The texts are English, registered in backend.json (credUIExec*) and never contain
// user input, plugin output, environment values or tokens.
var (
	errNoCommand         = errors.New("the kubeconfig has no plugin command to run")
	errAPIVersion        = errors.New("the plugin apiVersion is not supported")
	errInteractiveAlways = errors.New("plugins that require an interactive terminal are not supported yet")
	errNotFound          = errors.New("the plugin command was not found; install it and try again")
	errRelativeCommand   = errors.New("the plugin command is a relative path; use an absolute path or a name found in PATH")
	errBadDecision       = errors.New("the answer to the plugin question is not valid")
	errNotConfirmed      = errors.New("the plugin was not confirmed")
	errTimeout           = errors.New("the plugin did not finish in time")
	errOutputTooLarge    = errors.New("the plugin output is too large")
	errFailed            = errors.New("the plugin exited with an error")
	errStart             = errors.New("the plugin could not be started")
	errBadOutput         = errors.New("the plugin output is not a valid ExecCredential")
	errOutputKind        = errors.New("the plugin returned an unexpected kind or apiVersion")
	errNoToken           = errors.New("the plugin returned no token")
	errCertUnsupported   = errors.New("client certificates returned by a plugin are not supported")
	errExpired           = errors.New("the plugin returned an expired token")
	errBadToken          = errors.New("the plugin returned a token with invalid characters")
	errBadExpiry         = errors.New("the plugin returned an invalid expiration time")
	errNoTrustDir        = errors.New("the trust list cannot be saved on this system")
	errTrustSave         = errors.New("the trust list could not be saved")
)

// providerError ties a detail to a contract error: errors.Is matches the contract error, the text is the
// detail. The Broker reduces it to the contract error before it reaches a caller.
type providerError struct {
	kind   error
	detail error
}

func (e *providerError) Error() string        { return e.detail.Error() }
func (e *providerError) Unwrap() error        { return e.detail }
func (e *providerError) Is(target error) bool { return target == e.kind }

func rejected(detail error) error {
	return &providerError{kind: credential.ErrAnswerRejected, detail: detail}
}
func invalid(detail error) error {
	return &providerError{kind: credential.ErrInvalidAnswer, detail: detail}
}
func unsupported(detail error) error {
	return &providerError{kind: credential.ErrUnsupported, detail: detail}
}

// Provider implements credential.Provider for credential.KindExec on the desktop.
type Provider struct {
	runner   Runner
	lookPath func(string) (string, error)
	environ  func() []string
	timeout  time.Duration
	trust    *trustStore
}

// New returns the provider with the real process runner.
func New() *Provider {
	return &Provider{
		runner:   osRunner{},
		lookPath: exec.LookPath,
		environ:  os.Environ,
		timeout:  DefaultTimeout,
		trust:    &trustStore{now: time.Now},
	}
}

// Kind implements credential.Provider.
func (*Provider) Kind() credential.Kind { return credential.KindExec }

// Begin implements credential.Provider. It never starts the plugin unless the user already chose "always"
// for exactly this block.
func (p *Provider) Begin(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	x, err := p.check(req.Descriptor)
	if err != nil {
		return credential.Result{}, err
	}
	key := blockKey(x, req.Subject.ClusterID)
	if p.trust.isTrusted(env.DataDir, key) {
		return p.run(ctx, env, req, x, key)
	}
	return confirmChallenge(x, key, ""), nil
}

// Continue implements credential.Provider. It handles the answer to confirm-exec.
func (p *Provider) Continue(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	x, err := p.check(req.Descriptor)
	if err != nil {
		return credential.Result{}, err
	}
	key := blockKey(x, req.Subject.ClusterID)
	// Pending is what Begin stored for this block when it showed the confirmation. Without it, or for a
	// changed block, nothing may run, whatever the answer says.
	if subtle.ConstantTimeCompare(req.Pending, []byte(key)) != 1 {
		return credential.Result{}, rejected(errNotConfirmed)
	}
	switch req.Answer[FieldDecision] {
	case DecisionOnce:
		// "Once" lasts until the application is closed: a repeated token request (after a 401, for example)
		// must not ask again, but nothing is written to disk.
		p.trust.addOnce(key)
	case DecisionAlways:
		if err := p.trust.add(env.DataDir, key); err != nil {
			return credential.Result{}, rejected(err)
		}
	default:
		return credential.Result{}, invalid(errBadDecision)
	}
	return p.run(ctx, env, req, x, key)
}

// check validates the block and returns it. It does not start anything.
func (p *Provider) check(d credential.Descriptor) (*credential.ExecSpec, error) {
	x := d.Exec
	if x == nil || strings.TrimSpace(x.Command) == "" {
		return nil, unsupported(errNoCommand)
	}
	if !supportedAPIVersions[x.APIVersion] {
		return nil, unsupported(errAPIVersion)
	}
	if x.InteractiveMode == modeAlways {
		return nil, unsupported(errInteractiveAlways)
	}
	if _, err := p.resolve(x.Command); err != nil {
		return nil, err
	}
	return x, nil
}

// resolve finds the program: a name is looked up in PATH, a path must be absolute (a relative path would
// depend on the directory of a kubeconfig that is not known here).
func (p *Provider) resolve(command string) (string, error) {
	if strings.ContainsAny(command, `/\`) {
		if !filepath.IsAbs(command) {
			return "", unsupported(errRelativeCommand)
		}
		if _, err := os.Stat(command); err != nil {
			return "", unsupported(errNotFound)
		}
		return command, nil
	}
	path, err := p.lookPath(command)
	if err != nil {
		return "", unsupported(errNotFound)
	}
	return path, nil
}

// confirmChallenge builds the confirm-exec challenge. The environment values are blanked, only the names go
// out. Pending carries the block key for Continue.
func confirmChallenge(x *credential.ExecSpec, key, message string) credential.Result {
	shown := &credential.ExecSpec{
		APIVersion:      x.APIVersion,
		Command:         x.Command,
		Args:            append([]string{}, x.Args...),
		InteractiveMode: x.InteractiveMode,
		InstallHint:     x.InstallHint,
	}
	if len(x.Env) > 0 {
		shown.Env = make(map[string]string, len(x.Env))
		for k := range x.Env {
			shown.Env[k] = ""
		}
	}
	return credential.Result{
		Challenge: &credential.Challenge{Kind: credential.ChallengeConfirmExec, Exec: shown, Message: message},
		Pending:   []byte(key),
	}
}

// run starts the plugin. It is called only from Begin for a trusted block and from Continue after a valid
// confirmation; the key proves which of the two it is.
func (p *Provider) run(ctx context.Context, env credential.Env, req credential.Request, x *credential.ExecSpec, key string) (credential.Result, error) {
	path, err := p.resolve(x.Command)
	if err != nil {
		return credential.Result{}, err
	}
	info, err := execInfo(x.APIVersion, req.Descriptor.Params)
	if err != nil {
		return credential.Result{}, rejected(errBadOutput)
	}
	out, err := p.runner.Run(ctx, Command{
		Path:    path,
		Args:    append([]string{}, x.Args...),
		Env:     p.buildEnv(x.Env, info),
		Timeout: p.timeout,
	})
	secrets := secretValues(x)
	if err != nil {
		return credential.Result{}, err
	}
	if out.ExitCode != 0 {
		// The plugin said why (for example "run aws sso login first"): show it and let the user retry.
		msg := cleanMessage(string(out.Stderr), secrets)
		if msg == "" {
			return credential.Result{}, errFailed
		}
		return confirmChallenge(x, key, msg), nil
	}
	tok, err := parseOutput(out.Stdout, x.APIVersion, env.Now)
	if err != nil {
		return credential.Result{}, err
	}
	return credential.Result{Token: &tok}, nil
}

// buildEnv is the process environment: the parent environment, the plugin variables and the exec info. A
// KUBERNETES_EXEC_INFO inherited from the parent is dropped so it cannot confuse the plugin.
func (p *Provider) buildEnv(plugin map[string]string, info string) []string {
	var out []string
	for _, kv := range p.environ() {
		if !strings.HasPrefix(kv, execInfoVar+"=") {
			out = append(out, kv)
		}
	}
	names := make([]string, 0, len(plugin))
	for k := range plugin {
		if k != execInfoVar {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	for _, k := range names {
		out = append(out, k+"="+plugin[k])
	}
	return append(out, execInfoVar+"="+info)
}

// execInfo builds KUBERNETES_EXEC_INFO: the request document of the protocol.
func execInfo(apiVersion string, params map[string]string) (string, error) {
	type cluster struct {
		Server                   string `json:"server,omitempty"`
		TLSServerName            string `json:"tls-server-name,omitempty"`
		InsecureSkipTLSVerify    bool   `json:"insecure-skip-tls-verify,omitempty"`
		CertificateAuthorityData string `json:"certificate-authority-data,omitempty"`
	}
	type spec struct {
		Interactive bool     `json:"interactive"`
		Cluster     *cluster `json:"cluster,omitempty"`
	}
	doc := struct {
		Kind       string `json:"kind"`
		APIVersion string `json:"apiVersion"`
		Spec       spec   `json:"spec"`
	}{Kind: kindExecCred, APIVersion: apiVersion}
	// The plugin is never given a terminal here, so interactive is false.
	if v, set := params[paramProvideClusterInfo]; (!set || v == "true") && params[credential.ParamClusterServer] != "" {
		doc.Spec.Cluster = &cluster{
			Server:                   params[credential.ParamClusterServer],
			TLSServerName:            params[credential.ParamClusterTLSName],
			InsecureSkipTLSVerify:    params[credential.ParamClusterInsecure] == "true",
			CertificateAuthorityData: params[credential.ParamClusterCAData],
		}
	}
	raw, err := json.Marshal(doc)
	return string(raw), err
}

type output struct {
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion"`
	Status     *struct {
		Token                 string `json:"token"`
		ExpirationTimestamp   string `json:"expirationTimestamp"`
		ClientCertificateData string `json:"clientCertificateData"`
		ClientKeyData         string `json:"clientKeyData"`
	} `json:"status"`
}

// parseOutput reads the plugin answer. Unknown fields are ignored (plugins add their own), the rest is strict.
func parseOutput(stdout []byte, apiVersion string, now func() time.Time) (credential.Token, error) {
	if now == nil {
		now = time.Now
	}
	var o output
	if err := json.Unmarshal(stdout, &o); err != nil { // the error text may quote the output: dropped
		return credential.Token{}, errBadOutput
	}
	if o.Kind != kindExecCred || o.APIVersion != apiVersion {
		return credential.Token{}, errOutputKind
	}
	if o.Status == nil {
		return credential.Token{}, errNoToken
	}
	if o.Status.ClientCertificateData != "" || o.Status.ClientKeyData != "" {
		return credential.Token{}, unsupported(errCertUnsupported)
	}
	if o.Status.Token == "" {
		return credential.Token{}, errNoToken
	}
	if !validToken(o.Status.Token) {
		return credential.Token{}, errBadToken
	}
	// Without expirationTimestamp the Expiry stays zero: as in client-go, the token lives in memory until the
	// API server answers 401 (the Broker's transport invalidates it then).
	var expiry time.Time
	if o.Status.ExpirationTimestamp != "" {
		t, err := time.Parse(time.RFC3339, o.Status.ExpirationTimestamp)
		if err != nil {
			return credential.Token{}, errBadExpiry
		}
		if !t.After(now()) {
			return credential.Token{}, errExpired
		}
		expiry = t
	}
	return credential.Token{Value: o.Status.Token, Expiry: expiry}, nil
}

// validToken accepts printable ASCII without spaces: the token goes into an Authorization header.
func validToken(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= 0x20 || s[i] >= 0x7f {
			return false
		}
	}
	return s != ""
}

// secretValues lists what must never appear in text shown to the user.
func secretValues(x *credential.ExecSpec) []string {
	var out []string
	for _, v := range x.Env {
		if len(v) >= minSecretLen {
			out = append(out, v)
		}
	}
	return out
}

// cleanMessage prepares plugin stderr for display: valid UTF-8, no control characters except line breaks
// and tabs, known secrets removed, bounded length.
func cleanMessage(s string, secrets []string) string {
	// Longest first, so a secret that contains another one is removed whole.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, sec := range secrets {
		s = strings.ReplaceAll(s, sec, redactedMark)
	}
	s = strings.ToValidUTF8(s, "\uFFFD")
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r == '\r' {
			continue
		}
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
			continue
		}
		if n >= maxMessageRunes {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}
