// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package execplugin

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

// Limits of one plugin run.
const (
	DefaultTimeout = 30 * time.Second
	MaxStdoutBytes = 1 << 20
	maxStderrBytes = 64 << 10
	waitDelay      = 2 * time.Second
)

// Command is one process to start. Path is already resolved.
type Command struct {
	Path    string
	Args    []string
	Env     []string
	Timeout time.Duration
}

// Output is what the process wrote. Stdout is complete (the run fails when it exceeds MaxStdoutBytes),
// Stderr is cut at 64 KiB.
type Output struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner starts processes. It is the only place where a process is created; tests replace it.
type Runner interface {
	Run(ctx context.Context, c Command) (Output, error)
}

type osRunner struct{}

// capWriter keeps at most limit bytes. With fail set, exceeding the limit is an error that ends the run.
type capWriter struct {
	buf   bytes.Buffer
	limit int
	fail  bool
	over  bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	room := w.limit - w.buf.Len()
	if len(p) > room {
		w.over = true
		if w.fail {
			return 0, errOutputTooLarge
		}
		p = p[:room]
		w.buf.Write(p)
		return len(p), nil // pretend everything was taken so the process is not blocked
	}
	w.buf.Write(p)
	return len(p), nil
}

func (osRunner) Run(ctx context.Context, c Command) (Output, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdout, stderr capWriter
	stdout.limit, stdout.fail = MaxStdoutBytes, true
	stderr.limit = maxStderrBytes

	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Env = c.Env
	cmd.Stdin = nil // the null device: the plugin is never interactive here
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay

	err := cmd.Run()
	out := Output{Stdout: stdout.buf.Bytes(), Stderr: stderr.buf.Bytes()}
	switch {
	case stdout.over:
		return Output{}, errOutputTooLarge
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return Output{Stderr: out.Stderr}, errTimeout
	case ctx.Err() != nil:
		return Output{}, ctx.Err()
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return out, nil
	case errors.As(err, &exitErr):
		out.ExitCode = exitErr.ExitCode()
		return out, nil
	default:
		return Output{}, errStart
	}
}
