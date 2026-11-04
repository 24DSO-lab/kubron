// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package execplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

const secret = "S3CR3T-control-string-7f3a"

var fixedNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

const (
	v1      = "client.authentication.k8s.io/v1"
	v1beta1 = "client.authentication.k8s.io/v1beta1"
)

// fakeRunner records every start. A test that expects "no process" asserts calls == 0.
type fakeRunner struct {
	calls []Command
	out   Output
	err   error
}

func (f *fakeRunner) Run(_ context.Context, c Command) (Output, error) {
	f.calls = append(f.calls, c)
	return f.out, f.err
}

func okOutput(version string) Output {
	return Output{Stdout: []byte(`{"kind":"ExecCredential","apiVersion":"` + version + `","status":{"token":"tok-1","expirationTimestamp":"` +
		fixedNow.Add(time.Hour).Format(time.RFC3339) + `"}}`)}
}

func newProvider(r Runner) *Provider {
	return &Provider{
		runner:   r,
		lookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		environ:  func() []string { return []string{"HOME=/home/u", "KUBERNETES_EXEC_INFO=stale"} },
		timeout:  time.Second,
		trust:    &trustStore{now: func() time.Time { return fixedNow }},
	}
}

func spec() *credential.ExecSpec {
	return &credential.ExecSpec{
		APIVersion: v1, Command: "kubelogin", Args: []string{"get-token", "--server-id", "abc"},
		Env: map[string]string{"AWS_PROFILE": "prod", "API_KEY": secret},
	}
}

func request(x *credential.ExecSpec) credential.Request {
	return credential.Request{
		Descriptor: credential.Descriptor{Kind: credential.KindExec, Exec: x, Params: map[string]string{
			credential.ParamClusterServer: "https://k8s.example.com:6443", credential.ParamClusterCAData: "Q0E=",
		}},
		Subject: credential.Subject{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1"},
	}
}

func env(dir string) credential.Env {
	return credential.Env{Now: func() time.Time { return fixedNow }, Desktop: true, DataDir: dir}
}

// begin runs Begin and returns the confirm-exec result.
func begin(t *testing.T, p *Provider, dir string, req credential.Request) credential.Result {
	t.Helper()
	res, err := p.Begin(context.Background(), env(dir), req)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	return res
}

// answer builds the Continue request that follows a confirm-exec challenge.
func answer(req credential.Request, shown credential.Result, decision string) credential.Request {
	req.Pending = shown.Pending
	req.Challenge = *shown.Challenge
	req.Answer = credential.Answer{FieldDecision: decision}
	return req
}

func TestKind(t *testing.T) {
	if New().Kind() != credential.KindExec {
		t.Fatal("execplugin must register as KindExec")
	}
}

// --- the confirmation gate ------------------------------------------------------------------------

func TestBeginAsksBeforeRunning(t *testing.T) {
	r := &fakeRunner{out: okOutput(v1)}
	res := begin(t, newProvider(r), t.TempDir(), request(spec()))
	if len(r.calls) != 0 {
		t.Fatalf("the plugin was started before the confirmation (%d calls)", len(r.calls))
	}
	c := res.Challenge
	if c == nil || c.Kind != credential.ChallengeConfirmExec || res.Token != nil {
		t.Fatalf("unexpected result: %+v", res)
	}
	if c.Exec.Command != "kubelogin" || strings.Join(c.Exec.Args, " ") != "get-token --server-id abc" {
		t.Fatalf("exec shown = %+v", c.Exec)
	}
	if len(c.Exec.Env) != 2 {
		t.Fatalf("env names = %v", c.Exec.Env)
	}
	for k, v := range c.Exec.Env {
		if v != "" {
			t.Fatalf("value of %s is shown", k)
		}
	}
	if len(res.Pending) == 0 {
		t.Fatal("Begin must keep the block key for Continue")
	}
	if strings.Contains(fmt.Sprintf("%+v", c)+string(res.Pending), secret) {
		t.Fatal("environment value leaked into the challenge")
	}
}

func TestBeginDoesNotChangeTheDescriptor(t *testing.T) {
	x := spec()
	begin(t, newProvider(&fakeRunner{}), t.TempDir(), request(x))
	if x.Env["API_KEY"] != secret {
		t.Fatal("Begin modified the descriptor")
	}
}

// No path starts a process without a confirmation made for exactly this block.
func TestNoProcessWithoutConfirmation(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{out: okOutput(v1)}
	p := newProvider(r)
	req := request(spec())
	shown := begin(t, p, dir, req)

	other := spec()
	other.Args = []string{"get-token", "--evil"}
	otherShown := begin(t, p, dir, request(other))

	cases := map[string]credential.Request{
		"no pending, once":     {Descriptor: req.Descriptor, Subject: req.Subject, Answer: credential.Answer{FieldDecision: "once"}},
		"no pending, always":   {Descriptor: req.Descriptor, Subject: req.Subject, Answer: credential.Answer{FieldDecision: "always"}},
		"garbage pending":      {Descriptor: req.Descriptor, Subject: req.Subject, Pending: []byte("x"), Answer: credential.Answer{FieldDecision: "once"}},
		"pending of other blk": {Descriptor: req.Descriptor, Subject: req.Subject, Pending: otherShown.Pending, Answer: credential.Answer{FieldDecision: "once"}},
		"empty decision":       answer(req, shown, ""),
		"unknown decision":     answer(req, shown, "yes"),
		"cancel as decision":   answer(req, shown, "cancel"),
		"upper case decision":  answer(req, shown, "ONCE"),
		"no answer at all":     {Descriptor: req.Descriptor, Subject: req.Subject, Pending: shown.Pending},
	}
	for name, c := range cases {
		res, err := p.Continue(context.Background(), env(dir), c)
		if err == nil || res.Token != nil {
			t.Errorf("%s: expected an error and no token, got err=%v token=%v", name, err, res.Token)
		}
	}
	// A block that is not confirmed in another cluster
	moved := req
	moved.Subject.ClusterID = "c2"
	if _, err := p.Continue(context.Background(), env(dir), answer(moved, shown, "once")); err == nil {
		t.Error("a confirmation for cluster c1 was accepted for cluster c2")
	}
	if len(r.calls) != 0 {
		t.Fatalf("a process was started without a valid confirmation (%d calls)", len(r.calls))
	}
	if _, err := os.Stat(filepath.Join(dir, TrustFileName)); err == nil {
		t.Fatal("the trust file was written without a valid confirmation")
	}
}

func TestContinueOnceRunsAndIsRememberedOnlyInMemory(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{out: okOutput(v1)}
	p := newProvider(r)
	req := request(spec())
	res, err := p.Continue(context.Background(), env(dir), answer(req, begin(t, p, dir, req), DecisionOnce))
	if err != nil || res.Token == nil || res.Token.Value != "tok-1" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("calls = %d", len(r.calls))
	}
	if _, err := os.Stat(filepath.Join(dir, TrustFileName)); err == nil {
		t.Fatal("\"once\" was stored")
	}
	// The next token request in the same process does not ask again.
	r.calls = nil
	res, err = p.Begin(context.Background(), env(dir), req)
	if err != nil || res.Token == nil || len(r.calls) != 1 {
		t.Fatalf("after \"once\" the second token must not need a question: res=%+v err=%v calls=%d", res, err, len(r.calls))
	}
	// A new process (a new Provider, the same data directory) asks again: "once" was never stored.
	r2 := &fakeRunner{out: okOutput(v1)}
	restarted := newProvider(r2)
	if res := begin(t, restarted, dir, req); res.Challenge == nil || len(r2.calls) != 0 {
		t.Fatal("after a restart \"once\" must be forgotten and the question asked again")
	}
}

// "Once" is bound to the block like "always": another cluster or a changed command asks again.
func TestOnceDoesNotCoverAChangedBlock(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{out: okOutput(v1)}
	p := newProvider(r)
	req := request(spec())
	if _, err := p.Continue(context.Background(), env(dir), answer(req, begin(t, p, dir, req), DecisionOnce)); err != nil {
		t.Fatal(err)
	}
	other := request(spec())
	other.Subject.ClusterID = "c2"
	r.calls = nil
	if res := begin(t, p, dir, other); res.Challenge == nil || len(r.calls) != 0 {
		t.Fatal("\"once\" for one cluster must not cover another")
	}
	changed := spec()
	changed.Args = append(changed.Args, "--extra")
	if res := begin(t, p, dir, request(changed)); res.Challenge == nil || len(r.calls) != 0 {
		t.Fatal("\"once\" must not cover a changed block")
	}
}

func TestAlwaysIsStoredAndHonoured(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{out: okOutput(v1)}
	p := newProvider(r)
	req := request(spec())
	if _, err := p.Continue(context.Background(), env(dir), answer(req, begin(t, p, dir, req), DecisionAlways)); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, TrustFileName))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("trust file mode = %v", st.Mode().Perm())
	}
	r.calls = nil
	res, err := p.Begin(context.Background(), env(dir), req)
	if err != nil || res.Token == nil || len(r.calls) != 1 {
		t.Fatalf("a trusted block must run without a question: res=%+v err=%v calls=%d", res, err, len(r.calls))
	}
}

// Any change of the block, or another cluster, needs a new confirmation.
func TestChangedBlockAsksAgain(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{out: okOutput(v1)}
	p := newProvider(r)
	req := request(spec())
	if _, err := p.Continue(context.Background(), env(dir), answer(req, begin(t, p, dir, req), DecisionAlways)); err != nil {
		t.Fatal(err)
	}
	r.calls = nil

	mutate := map[string]func(*credential.Request){
		"command":     func(q *credential.Request) { q.Descriptor.Exec.Command = "kubelogin2" },
		"args":        func(q *credential.Request) { q.Descriptor.Exec.Args = append(q.Descriptor.Exec.Args, "--x") },
		"arg order":   func(q *credential.Request) { q.Descriptor.Exec.Args = []string{"--server-id", "abc", "get-token"} },
		"env value":   func(q *credential.Request) { q.Descriptor.Exec.Env["AWS_PROFILE"] = "dev" },
		"env name":    func(q *credential.Request) { q.Descriptor.Exec.Env["EXTRA"] = "1" },
		"env removed": func(q *credential.Request) { delete(q.Descriptor.Exec.Env, "AWS_PROFILE") },
		"apiVersion":  func(q *credential.Request) { q.Descriptor.Exec.APIVersion = v1beta1 },
		"cluster":     func(q *credential.Request) { q.Subject.ClusterID = "c2" },
	}
	for name, m := range mutate {
		q := request(spec())
		m(&q)
		res, err := p.Begin(context.Background(), env(dir), q)
		if err != nil || res.Challenge == nil || res.Token != nil {
			t.Errorf("%s: a changed block must ask again: res=%+v err=%v", name, res, err)
		}
	}
	if len(r.calls) != 0 {
		t.Fatalf("a changed block was started without a question (%d calls)", len(r.calls))
	}
}

func TestBlockKeyIsUnambiguous(t *testing.T) {
	a := &credential.ExecSpec{APIVersion: v1, Command: "ab", Args: []string{"c"}}
	b := &credential.ExecSpec{APIVersion: v1, Command: "a", Args: []string{"bc"}}
	c := &credential.ExecSpec{APIVersion: v1, Command: "a", Args: []string{"b", "c"}}
	if blockKey(a, "x") == blockKey(b, "x") || blockKey(b, "x") == blockKey(c, "x") {
		t.Fatal("different blocks share a key")
	}
	if blockKey(a, "x") == blockKey(a, "y") {
		t.Fatal("the cluster id is not part of the key")
	}
}

func TestAlwaysNeedsDataDir(t *testing.T) {
	r := &fakeRunner{out: okOutput(v1)}
	p := newProvider(r)
	req := request(spec())
	shown := begin(t, p, "", req)
	if _, err := p.Continue(context.Background(), env(""), answer(req, shown, DecisionAlways)); !errors.Is(err, credential.ErrAnswerRejected) {
		t.Fatalf("err = %v", err)
	}
	if len(r.calls) != 0 {
		t.Fatal("started although the decision could not be stored")
	}
	if _, err := p.Continue(context.Background(), env(""), answer(req, shown, DecisionOnce)); err != nil {
		t.Fatalf("\"once\" must work without DataDir: %v", err)
	}
}

func TestCorruptTrustFileMeansNotTrusted(t *testing.T) {
	dir := t.TempDir()
	for _, content := range []string{"", "{", `{"version":2,"trusted":{}}`, `{"version":1}`, `null`} {
		if err := os.WriteFile(filepath.Join(dir, TrustFileName), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		r := &fakeRunner{out: okOutput(v1)}
		res := begin(t, newProvider(r), dir, request(spec()))
		if res.Challenge == nil || len(r.calls) != 0 {
			t.Fatalf("content %q: must be untrusted", content)
		}
	}
}

func TestTrustFileHoldsNoSecrets(t *testing.T) {
	dir := t.TempDir()
	p := newProvider(&fakeRunner{out: okOutput(v1)})
	req := request(spec())
	if _, err := p.Continue(context.Background(), env(dir), answer(req, begin(t, p, dir, req), DecisionAlways)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, TrustFileName))
	for _, bad := range []string{secret, "kubelogin", "prod", "abc"} {
		if bytes.Contains(raw, []byte(bad)) {
			t.Fatalf("trust file contains %q", bad)
		}
	}
	var f trustFile
	if err := json.Unmarshal(raw, &f); err != nil || len(f.Trusted) != 1 {
		t.Fatalf("trust file = %s (%v)", raw, err)
	}
}

// --- checks before the question -------------------------------------------------------------------

func TestInteractiveModes(t *testing.T) {
	for mode, wantErr := range map[string]bool{"": false, "Never": false, "IfAvailable": false, "Always": true} {
		x := spec()
		x.InteractiveMode = mode
		r := &fakeRunner{out: okOutput(v1)}
		res, err := newProvider(r).Begin(context.Background(), env(t.TempDir()), request(x))
		if wantErr {
			if !errors.Is(err, credential.ErrUnsupported) || res.Challenge != nil {
				t.Fatalf("%q: err=%v res=%+v, want ErrUnsupported without a question", mode, err, res)
			}
		} else if err != nil || res.Challenge == nil {
			t.Fatalf("%q: err=%v", mode, err)
		}
		if len(r.calls) != 0 {
			t.Fatalf("%q: process started", mode)
		}
	}
}

func TestInteractiveAlwaysRefusedEvenWhenTrusted(t *testing.T) {
	x := spec()
	x.InteractiveMode = "Always"
	r := &fakeRunner{out: okOutput(v1)}
	p := newProvider(r)
	req := request(x)
	// pretend the user trusted it earlier
	dir := t.TempDir()
	if err := p.trust.add(dir, blockKey(x, "c1")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Begin(context.Background(), env(dir), req); !errors.Is(err, credential.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
	if len(r.calls) != 0 {
		t.Fatal("process started")
	}
}

func TestUnsupportedBlocks(t *testing.T) {
	cases := map[string]func(*credential.ExecSpec){
		"no command":       func(x *credential.ExecSpec) { x.Command = " " },
		"unknown version":  func(x *credential.ExecSpec) { x.APIVersion = "client.authentication.k8s.io/v2" },
		"empty version":    func(x *credential.ExecSpec) { x.APIVersion = "" },
		"relative path":    func(x *credential.ExecSpec) { x.Command = "./plugin" },
		"relative subpath": func(x *credential.ExecSpec) { x.Command = "bin/plugin" },
		"missing absolute": func(x *credential.ExecSpec) { x.Command = filepath.Join(os.TempDir(), "definitely-not-here-7f3a") },
	}
	for name, m := range cases {
		x := spec()
		m(x)
		r := &fakeRunner{}
		if _, err := newProvider(r).Begin(context.Background(), env(t.TempDir()), request(x)); !errors.Is(err, credential.ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
		if len(r.calls) != 0 {
			t.Errorf("%s: process started", name)
		}
	}
	if _, err := newProvider(&fakeRunner{}).Begin(context.Background(), env(""), credential.Request{}); !errors.Is(err, credential.ErrUnsupported) {
		t.Errorf("no exec block: err = %v", err)
	}
	p := newProvider(&fakeRunner{})
	p.lookPath = func(string) (string, error) { return "", errors.New("not in PATH") }
	if _, err := p.Begin(context.Background(), env(t.TempDir()), request(spec())); !errors.Is(err, credential.ErrUnsupported) {
		t.Errorf("not in PATH: err = %v", err)
	}
}

// --- the protocol ---------------------------------------------------------------------------------

func runOnce(t *testing.T, p *Provider, req credential.Request) (credential.Result, error) {
	t.Helper()
	dir := t.TempDir()
	return p.Continue(context.Background(), env(dir), answer(req, begin(t, p, dir, req), DecisionOnce))
}

func TestProtocolInput(t *testing.T) {
	for _, version := range []string{v1, v1beta1} {
		x := spec()
		x.APIVersion = version
		r := &fakeRunner{out: okOutput(version)}
		if _, err := runOnce(t, newProvider(r), request(x)); err != nil {
			t.Fatalf("%s: %v", version, err)
		}
		c := r.calls[0]
		if c.Path != "/usr/bin/kubelogin" || strings.Join(c.Args, " ") != "get-token --server-id abc" || c.Timeout != time.Second {
			t.Fatalf("%s: command = %+v", version, c)
		}
		var info struct {
			Kind, APIVersion string
			Spec             struct {
				Interactive bool
				Cluster     map[string]any
			}
		}
		var raw string
		for _, kv := range c.Env {
			if v, ok := strings.CutPrefix(kv, "KUBERNETES_EXEC_INFO="); ok {
				if raw != "" {
					t.Fatal("KUBERNETES_EXEC_INFO is set twice")
				}
				raw = v
			}
		}
		if err := json.Unmarshal([]byte(raw), &info); err != nil {
			t.Fatalf("exec info: %v", err)
		}
		if info.Kind != "ExecCredential" || info.APIVersion != version || info.Spec.Interactive {
			t.Fatalf("exec info = %s", raw)
		}
		if info.Spec.Cluster["server"] != "https://k8s.example.com:6443" || info.Spec.Cluster["certificate-authority-data"] != "Q0E=" {
			t.Fatalf("cluster info = %v", info.Spec.Cluster)
		}
		env := strings.Join(c.Env, "\n")
		for _, want := range []string{"HOME=/home/u", "AWS_PROFILE=prod", "API_KEY=" + secret} {
			if !strings.Contains(env, want) {
				t.Fatalf("%s: environment lacks %s", version, want)
			}
		}
		if strings.Contains(env, "stale") {
			t.Fatal("the inherited KUBERNETES_EXEC_INFO was passed on")
		}
	}
}

func TestProvideClusterInfoSwitch(t *testing.T) {
	req := request(spec())
	req.Descriptor.Params[paramProvideClusterInfo] = "false"
	r := &fakeRunner{out: okOutput(v1)}
	if _, err := runOnce(t, newProvider(r), req); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(r.calls[0].Env, "\n"), "k8s.example.com") {
		t.Fatal("cluster info was passed although provideClusterInfo is false")
	}
}

func TestProtocolOutput(t *testing.T) {
	good := func(status string) []byte {
		return []byte(`{"kind":"ExecCredential","apiVersion":"` + v1 + `","spec":{},"extra":1,"status":` + status + `}`)
	}
	exp := fixedNow.Add(time.Hour).Format(time.RFC3339)
	past := fixedNow.Add(-time.Minute).Format(time.RFC3339)
	cases := []struct {
		name    string
		stdout  []byte
		wantErr error // nil: success
		wantIs  error // contract error expected, if any
	}{
		{"ok", good(`{"token":"t","expirationTimestamp":"` + exp + `"}`), nil, nil},
		{"ok without expiry", good(`{"token":"t"}`), nil, nil},
		{"not json", []byte("hello"), errBadOutput, nil},
		{"empty", nil, errBadOutput, nil},
		{"wrong kind", []byte(`{"kind":"x","apiVersion":"` + v1 + `","status":{"token":"t"}}`), errOutputKind, nil},
		{"version mismatch", []byte(`{"kind":"ExecCredential","apiVersion":"` + v1beta1 + `","status":{"token":"t"}}`), errOutputKind, nil},
		{"no status", []byte(`{"kind":"ExecCredential","apiVersion":"` + v1 + `"}`), errNoToken, nil},
		{"no token", good(`{}`), errNoToken, nil},
		{"certificate", good(`{"clientCertificateData":"a","clientKeyData":"b"}`), errCertUnsupported, credential.ErrUnsupported},
		{"expired", good(`{"token":"t","expirationTimestamp":"` + past + `"}`), errExpired, nil},
		{"bad timestamp", good(`{"token":"t","expirationTimestamp":"soon"}`), errBadExpiry, nil},
		{"bad token", good(`{"token":"a b"}`), errBadToken, nil},
		{"newline in token", good(`{"token":"a\nb"}`), errBadToken, nil},
	}
	for _, c := range cases {
		r := &fakeRunner{out: Output{Stdout: c.stdout}}
		res, err := runOnce(t, newProvider(r), request(spec()))
		if c.wantErr == nil {
			if err != nil || res.Token == nil {
				t.Errorf("%s: err=%v", c.name, err)
			}
			continue
		}
		if !errors.Is(err, c.wantErr) || res.Token != nil {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.wantErr)
		}
		if c.wantIs != nil && !errors.Is(err, c.wantIs) {
			t.Errorf("%s: err = %v, want a %v", c.name, err, c.wantIs)
		}
	}
}

// Without expirationTimestamp the token has no expiry (client-go semantics: valid until a 401).
func TestTokenWithoutExpirationTimestampHasNoExpiry(t *testing.T) {
	r := &fakeRunner{out: Output{Stdout: []byte(`{"kind":"ExecCredential","apiVersion":"` + v1 + `","status":{"token":"t"}}`)}}
	res, err := runOnce(t, newProvider(r), request(spec()))
	if err != nil || res.Token == nil || !res.Token.Expiry.IsZero() {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestPluginFailureShowsStderr(t *testing.T) {
	stderr := "Error: token expired, run 'aws sso login'\n\x1b[31mred\x00\x07 key=" + secret + "\r\n"
	r := &fakeRunner{out: Output{ExitCode: 1, Stderr: []byte(stderr)}}
	res, err := runOnce(t, newProvider(r), request(spec()))
	if err != nil || res.Token != nil || res.Challenge == nil || res.Challenge.Kind != credential.ChallengeConfirmExec {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	m := res.Challenge.Message
	if !strings.Contains(m, "aws sso login") || strings.Contains(m, secret) || strings.ContainsAny(m, "\x00\x07\x1b\r") {
		t.Fatalf("message = %q", m)
	}
	if len(res.Pending) == 0 || strings.Contains(fmt.Sprintf("%+v", res.Challenge.Exec.Env), secret) {
		t.Fatal("retry challenge is not safe")
	}
}

func TestPluginFailureWithoutStderr(t *testing.T) {
	r := &fakeRunner{out: Output{ExitCode: 2}}
	if _, err := runOnce(t, newProvider(r), request(spec())); !errors.Is(err, errFailed) {
		t.Fatalf("err = %v", err)
	}
}

func TestMessageIsBounded(t *testing.T) {
	r := &fakeRunner{out: Output{ExitCode: 1, Stderr: []byte(strings.Repeat("é", 100000))}}
	res, _ := runOnce(t, newProvider(r), request(spec()))
	if n := len([]rune(res.Challenge.Message)); n > maxMessageRunes {
		t.Fatalf("message has %d runes", n)
	}
}

func TestRunnerErrorsPassThroughWithoutSecrets(t *testing.T) {
	for _, e := range []error{errTimeout, errOutputTooLarge, errStart, context.Canceled} {
		r := &fakeRunner{err: e}
		if _, err := runOnce(t, newProvider(r), request(spec())); !errors.Is(err, e) {
			t.Fatalf("err = %v, want %v", err, e)
		}
	}
}

// --- real processes (POSIX) -----------------------------------------------------------------------

func script(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	path := filepath.Join(t.TempDir(), "plugin.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func realProvider() *Provider {
	p := New()
	p.timeout = 2 * time.Second
	return p
}

func realRun(t *testing.T, p *Provider, command string, env map[string]string) (credential.Result, error) {
	t.Helper()
	x := &credential.ExecSpec{APIVersion: v1, Command: command, Env: env}
	return runOnce(t, p, request(x))
}

func TestRealPlugin(t *testing.T) {
	cmd := script(t, `printf '{"kind":"ExecCredential","apiVersion":"client.authentication.k8s.io/v1","status":{"token":"%s"}}' "$PLUGIN_TOKEN"`)
	res, err := realRun(t, realProvider(), cmd, map[string]string{"PLUGIN_TOKEN": "real-token"})
	if err != nil || res.Token == nil || res.Token.Value != "real-token" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRealPluginSeesExecInfo(t *testing.T) {
	cmd := script(t, `case "$KUBERNETES_EXEC_INFO" in *'"interactive":false'*'k8s.example.com'*) T=info-ok;; *) T=info-bad;; esac
printf '{"kind":"ExecCredential","apiVersion":"client.authentication.k8s.io/v1","status":{"token":"%s"}}' "$T"`)
	res, err := realRun(t, realProvider(), cmd, nil)
	if err != nil || res.Token.Value != "info-ok" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRealPluginFailure(t *testing.T) {
	cmd := script(t, `echo "login required" >&2; exit 3`)
	res, err := realRun(t, realProvider(), cmd, nil)
	if err != nil || res.Challenge == nil || !strings.Contains(res.Challenge.Message, "login required") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRealPluginTimeout(t *testing.T) {
	cmd := script(t, `sleep 30`)
	p := realProvider()
	p.timeout = 200 * time.Millisecond
	start := time.Now()
	if _, err := realRun(t, p, cmd, nil); !errors.Is(err, errTimeout) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("the plugin was not stopped in time")
	}
}

func TestRealPluginStdoutLimit(t *testing.T) {
	cmd := script(t, `head -c 3000000 /dev/zero | tr '\0' 'a'`)
	if _, err := realRun(t, realProvider(), cmd, nil); !errors.Is(err, errOutputTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestRealPluginStderrLimit(t *testing.T) {
	cmd := script(t, `head -c 500000 /dev/zero | tr '\0' 'e' >&2; exit 1`)
	res, err := realRun(t, realProvider(), cmd, nil)
	if err != nil || res.Challenge == nil || len(res.Challenge.Message) > maxMessageRunes {
		t.Fatalf("err=%v", err)
	}
}

func TestRealPluginHasNoStdin(t *testing.T) {
	cmd := script(t, `if read -r line; then T=has-stdin; else T=no-stdin; fi
printf '{"kind":"ExecCredential","apiVersion":"client.authentication.k8s.io/v1","status":{"token":"%s"}}' "$T"`)
	res, err := realRun(t, realProvider(), cmd, nil)
	if err != nil || res.Token.Value != "no-stdin" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// --- secrets --------------------------------------------------------------------------------------

// The control string sits in the plugin environment, in the plugin output and in stderr. It may reach the
// process and Result.Token, nowhere else.
func TestSecretNeverLeaks(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	dir := t.TempDir()
	var texts []string
	collect := func(res credential.Result, err error) {
		if err != nil {
			texts = append(texts, err.Error(), fmt.Sprintf("%v %+v %#v", err, err, err))
		}
		if res.Challenge != nil {
			texts = append(texts, fmt.Sprintf("%+v", *res.Challenge), res.Challenge.Message)
			if b, e := json.Marshal(res.Challenge); e == nil {
				texts = append(texts, string(b))
			}
		}
		texts = append(texts, string(res.Pending), string(res.Save))
	}

	x := spec() // API_KEY holds the secret
	req := request(x)

	// 1. confirmation
	p := newProvider(&fakeRunner{out: okOutput(v1)})
	shown, err := p.Begin(context.Background(), env(dir), req)
	collect(shown, err)

	// 2. failure with the secret in stderr
	pf := newProvider(&fakeRunner{out: Output{ExitCode: 1, Stderr: []byte("token=" + secret + " failed")}})
	res, err := pf.Continue(context.Background(), env(dir), answer(req, shown, DecisionAlways))
	collect(res, err)

	// 3. bad outputs that quote the secret
	for _, out := range []string{
		`{"kind":"` + secret + `"`,
		`{"kind":"ExecCredential","apiVersion":"` + secret + `"}`,
		`{"kind":"ExecCredential","apiVersion":"` + v1 + `","status":{"token":"` + secret + ` x"}}`,
		`{"kind":"ExecCredential","apiVersion":"` + v1 + `","status":{"token":"` + secret + `","expirationTimestamp":"` + secret + `"}}`,
		`{"kind":"ExecCredential","apiVersion":"` + v1 + `","status":{"clientCertificateData":"` + secret + `"}}`,
	} {
		pb := newProvider(&fakeRunner{out: Output{Stdout: []byte(out)}})
		res, err := pb.Continue(context.Background(), env(dir), answer(req, shown, DecisionOnce))
		collect(res, err)
	}

	// 4. a successful run: the secret token is only in Result.Token
	token := `{"kind":"ExecCredential","apiVersion":"` + v1 + `","status":{"token":"` + secret + `"}}`
	ps := newProvider(&fakeRunner{out: Output{Stdout: []byte(token)}})
	ok, err := ps.Continue(context.Background(), env(dir), answer(req, shown, DecisionAlways))
	if err != nil || ok.Token == nil || ok.Token.Value != secret {
		t.Fatalf("res=%+v err=%v", ok, err)
	}
	collect(credential.Result{Challenge: ok.Challenge, Pending: ok.Pending, Save: ok.Save}, nil)
	texts = append(texts, fmt.Sprintf("%v %+v %#v %s", *ok.Token, *ok.Token, *ok.Token, *ok.Token))

	for i, s := range texts {
		if strings.Contains(s, secret) {
			t.Fatalf("text %d leaks the secret: %q", i, s)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, TrustFileName)); err == nil && bytes.Contains(raw, []byte(secret)) {
		t.Fatal("the trust file leaks the secret")
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatalf("logs contain the secret: %q", logs.String())
	}
}
