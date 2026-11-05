// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package execplugin

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestRunnerHelperProcess is the child process used by osRunner tests. It deliberately writes
// credentials-shaped data to stdout and diagnostics to stderr so the streams cannot be conflated.
func TestRunnerHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_RUNNER_HELPER") != "1" {
		return
	}
	switch os.Getenv("GO_RUNNER_HELPER_MODE") {
	case "streams":
		_, _ = os.Stdout.Write([]byte(`{"kind":"ExecCredential","status":{"token":"stdout-secret"}}`))
		_, _ = os.Stderr.Write([]byte("interactive diagnostic"))
	case "wait":
		time.Sleep(2 * time.Second)
	case "stdin":
		b, _ := os.ReadFile("/dev/stdin")
		_, _ = os.Stdout.Write(b)
	}
	os.Exit(0)
}

func runnerHelperCommand(t *testing.T, mode string, timeout time.Duration) Command {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"GO_WANT_RUNNER_HELPER=1",
		"GO_RUNNER_HELPER_MODE="+mode,
	)
	return Command{
		Path: exe, Args: []string{"-test.run=^TestRunnerHelperProcess$"},
		Env: env, Timeout: timeout,
	}
}

func TestOSRunnerKeepsCredentialStdoutSeparateFromDiagnostics(t *testing.T) {
	out, err := (osRunner{}).Run(context.Background(), runnerHelperCommand(t, "streams", time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Stdout, []byte(`"token":"stdout-secret"`)) {
		t.Fatalf("credential JSON missing from stdout: %q", out.Stdout)
	}
	if !bytes.Equal(out.Stderr, []byte("interactive diagnostic")) {
		t.Fatalf("stderr = %q; want only diagnostics", out.Stderr)
	}
	if bytes.Contains(out.Stderr, []byte("stdout-secret")) || bytes.Contains(out.Stdout, []byte("interactive diagnostic")) {
		t.Fatal("stdout and stderr were mixed")
	}
}

func TestOSRunnerEnforcesTimeout(t *testing.T) {
	started := time.Now()
	_, err := (osRunner{}).Run(context.Background(), runnerHelperCommand(t, "wait", 40*time.Millisecond))
	if err != errTimeout {
		t.Fatalf("Run error = %v; want errTimeout", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("runner did not stop the child promptly after timeout")
	}
}

func TestOSRunnerUsesClosedStdin(t *testing.T) {
	cmd := runnerHelperCommand(t, "stdin", time.Second)
	out, err := (osRunner{}).Run(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Stdout) != 0 {
		t.Fatalf("plugin unexpectedly received stdin data: %q", out.Stdout)
	}
}

// Keep the os/exec import visible to static analysis of the helper contract: the child must be
// launched as a separate process, not invoked in-process.
var _ = exec.ErrNotFound
