// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
)

// Descriptor, ExecSpec, Request and Result hold secrets in memory (client secret, refresh token, plugin
// environment values, saved sign-in data). They are redacted in every textual form (%v, %+v, %#v, %s) and
// in structured logs (slog), so that a stray log line or error cannot leak them. JSON encoding of the
// contract types is not affected.

const redacted = "[redacted]"

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// String shows the command and the argument count; arguments may carry secrets, environment values are hidden.
func (e ExecSpec) String() string {
	return fmt.Sprintf("ExecSpec{command:%s args:%d env:%s}", e.Command, len(e.Args), strings.Join(sortedKeys(e.Env), ","))
}

// GoString implements fmt.GoStringer.
func (e ExecSpec) GoString() string { return e.String() }

// LogValue implements slog.LogValuer.
func (e ExecSpec) LogValue() slog.Value { return slog.StringValue(e.String()) }

// String shows the kind and the names of the parameters, never their values.
func (d Descriptor) String() string {
	exec := "none"
	if d.Exec != nil {
		exec = d.Exec.String()
	}
	return fmt.Sprintf("Descriptor{kind:%s params:%s exec:%s}", d.Kind, strings.Join(sortedKeys(d.Params), ","), exec)
}

// GoString implements fmt.GoStringer.
func (d Descriptor) GoString() string { return d.String() }

// LogValue implements slog.LogValuer.
func (d Descriptor) LogValue() slog.Value { return slog.StringValue(d.String()) }

func (r Request) String() string {
	return fmt.Sprintf("Request{%s subject:%s/%s saved:%t pending:%t answer:%s}",
		r.Descriptor, r.Subject.ClusterID, r.Subject.Scope, len(r.Saved) > 0, len(r.Pending) > 0, redacted)
}

// GoString implements fmt.GoStringer.
func (r Request) GoString() string { return r.String() }

// LogValue implements slog.LogValuer.
func (r Request) LogValue() slog.Value { return slog.StringValue(r.String()) }

func (r Result) String() string {
	return fmt.Sprintf("Result{token:%t challenge:%t save:%t pending:%t}", r.Token != nil, r.Challenge != nil, r.Save != nil, len(r.Pending) > 0)
}

// GoString implements fmt.GoStringer.
func (r Result) GoString() string { return r.String() }

// LogValue implements slog.LogValuer.
func (r Result) LogValue() slog.Value { return slog.StringValue(r.String()) }
