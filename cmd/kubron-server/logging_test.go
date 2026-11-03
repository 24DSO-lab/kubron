// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"strings"
	"testing"

	"github.com/24DSO-labs/kubron/internal/audit"
	"github.com/24DSO-labs/kubron/internal/logging"
	"github.com/24DSO-labs/kubron/internal/server"
)

func envMap(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func restoreLogging(t *testing.T) {
	t.Helper()
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old); logging.ResetSecrets(); audit.SetMirror(nil) })
}

func TestSetupLoggingJSONAndLegacyBridge(t *testing.T) {
	restoreLogging(t)
	var buf bytes.Buffer
	if err := setupLogging(envMap(nil), &buf); err != nil {
		t.Fatal(err)
	}
	slog.Info("started", "replica", "r-1")
	log.Printf("legacy line, password=hunter2hunter2") // код, не переведённый на slog
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want two records: %q", buf.String())
	}
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q", l)
		}
	}
	if strings.Contains(buf.String(), "hunter2hunter2") {
		t.Errorf("legacy output must be redacted: %s", buf.String())
	}
}

func TestSetupLoggingLevelAndFormat(t *testing.T) {
	restoreLogging(t)
	var buf bytes.Buffer
	if err := setupLogging(envMap(map[string]string{"KUBRON_LOG_LEVEL": "error"}), &buf); err != nil {
		t.Fatal(err)
	}
	slog.Warn("hidden")
	if buf.Len() != 0 {
		t.Errorf("warn must be filtered at level error: %q", buf.String())
	}
	buf.Reset()
	if err := setupLogging(envMap(map[string]string{"KUBRON_LOG_FORMAT": "text"}), &buf); err != nil {
		t.Fatal(err)
	}
	slog.Info("plain")
	if strings.HasPrefix(buf.String(), "{") {
		t.Errorf("text format expected: %q", buf.String())
	}
	for _, bad := range []map[string]string{{"KUBRON_LOG_LEVEL": "loud"}, {"KUBRON_LOG_FORMAT": "xml"}} {
		if err := setupLogging(envMap(bad), &buf); err == nil {
			t.Errorf("%v must be rejected", bad)
		}
	}
}

func TestRegisterSecrets(t *testing.T) {
	restoreLogging(t)
	registerSecrets(server.Config{
		SecretKey:          "k2:bmV3LWtleS1tYXRlcmlhbC0zMi1ieXRlcy1sb25n",
		PreviousSecretKeys: []string{"k1:b2xkLWtleS1tYXRlcmlhbC0zMi1ieXRlcy1sb25n"},
		DatabaseURL:        "postgres://kubron:DbPassw0rd@db/kubron",
		DBListenURL:        "postgres://kubron:ListenPassw0rd@db-direct/kubron",
	})
	for _, secret := range []string{"bmV3LWtleS1tYXRlcmlhbC0zMi1ieXRlcy1sb25n", "b2xkLWtleS1tYXRlcmlhbC0zMi1ieXRlcy1sb25n", "DbPassw0rd", "ListenPassw0rd"} {
		if got := logging.Redact("error near " + secret + " here"); strings.Contains(got, secret) {
			t.Errorf("%q is not redacted: %q", secret, got)
		}
	}
}

func TestFatalLogDoesNotExposeErrorContents(t *testing.T) {
	restoreLogging(t)
	var buf bytes.Buffer
	if err := setupLogging(envMap(nil), &buf); err != nil {
		t.Fatal(err)
	}
	logFatal("database", errors.New("failed to load key super-secret-encryption-key"))
	if strings.Contains(buf.String(), "super-secret-encryption-key") {
		t.Fatalf("fatal log leaked error contents: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "error_type") {
		t.Fatalf("fatal log must retain the error type: %s", buf.String())
	}
}

func TestEnableAuditStdout(t *testing.T) {
	restoreLogging(t)
	var buf bytes.Buffer
	if enableAuditStdout(envMap(nil), &buf) || enableAuditStdout(envMap(map[string]string{"KUBRON_AUDIT_STDOUT": "0"}), &buf) {
		t.Fatal("audit to stdout must be off by default")
	}
	if !enableAuditStdout(envMap(map[string]string{"KUBRON_AUDIT_STDOUT": "1"}), &buf) {
		t.Fatal("KUBRON_AUDIT_STDOUT=1 must enable it")
	}
	audit.Open(t.TempDir()).Record(audit.Event{User: "a", Action: "X", Result: audit.ResultOK})
	if !strings.Contains(buf.String(), `"type":"audit"`) {
		t.Errorf("audit line expected: %q", buf.String())
	}
}
