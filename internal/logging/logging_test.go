// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"testing"
)

func newBuf(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	ResetSecrets()
	t.Cleanup(ResetSecrets)
	var buf bytes.Buffer
	return New(Options{Writer: &buf, Level: slog.LevelDebug}), &buf
}

func lines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l == "" {
			continue
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line is not JSON: %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func TestJSONRecordShape(t *testing.T) {
	l, buf := newBuf(t)
	l.Info("hello", "method", "GetPods", "n", 3)
	r := lines(t, buf)
	if len(r) != 1 || r[0]["msg"] != "hello" || r[0]["level"] != "INFO" || r[0]["method"] != "GetPods" || r[0]["n"] != float64(3) || r[0]["time"] == nil {
		t.Fatalf("record: %v", r)
	}
}

func TestSensitiveKeysAreRedacted(t *testing.T) {
	l, buf := newBuf(t)
	l.Info("login", "password", "hunter2hunter2", "Authorization", "Bearer abcdefghij", "csrf_token", "c-s-r-f-value",
		"session-cookie", "kubron_session=abc", "kubeconfig", "apiVersion: v1 clusters: ...", "databaseUrl", "postgres://x",
		"user", "alice")
	out := buf.String()
	for _, secret := range []string{"hunter2hunter2", "abcdefghij", "c-s-r-f-value", "kubron_session=abc", "apiVersion", "postgres://x"} {
		if strings.Contains(out, secret) {
			t.Errorf("%q leaked: %s", secret, out)
		}
	}
	if !strings.Contains(out, `"user":"alice"`) {
		t.Errorf("harmless attributes must stay readable: %s", out)
	}
}

func TestRedactPatterns(t *testing.T) {
	for in, leak := range map[string]string{
		"dial postgres://kubron:s3cr3tPass@db:5432/kubron failed":                          "s3cr3tPass",
		"host=db user=kubron password=s3cr3tPass dbname=x":                                 "s3cr3tPass",
		`{"token":"abc123abc123"}`:                                                         "abc123abc123",
		"Authorization: Bearer abc.def.ghi-jkl":                                            "ghi-jkl",
		"got eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.sig":                         "eyJzdWIiOiIxMjM0NTY3ODkwIn0",
		"Cookie: __Host-kubron_session=deadbeefdeadbeef; other=1":                          "deadbeefdeadbeef",
		"key: -----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY----- done": "MIIEow",
		"client_secret=zzzzzzzz&grant_type=password":                                       "zzzzzzzz",
	} {
		got := Redact(in)
		if strings.Contains(got, leak) {
			t.Errorf("Redact(%q) = %q: still contains %q", in, got, leak)
		}
		if !strings.Contains(got, Redacted) {
			t.Errorf("Redact(%q) = %q: nothing was redacted", in, got)
		}
	}
	// обычный текст не портится
	for _, ok := range []string{"rpc GetPods: forbidden", "the token was not rotated", "cluster prod unreachable: i/o timeout"} {
		if got := Redact(ok); got != ok {
			t.Errorf("Redact(%q) changed harmless text: %q", ok, got)
		}
	}
}

func TestRegisteredSecretsAreRedactedEverywhere(t *testing.T) {
	l, buf := newBuf(t)
	AddKeySecret("k1:QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU=")
	AddURLSecrets("postgres://kubron:Sup3rDbPass@db:5432/kubron")
	AddSecret("tiny") // короче minSecretLen: не регистрируется, обычный текст не искажается
	err := fmt.Errorf("decrypt: key QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU= rejected; dsn user=kubron pass=Sup3rDbPass")
	l.Error("rpc failed: "+err.Error(), "error", err, "detail", struct{ K string }{"k1:QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU="})
	out := buf.String()
	for _, secret := range []string{"QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU", "Sup3rDbPass"} {
		if strings.Contains(out, secret) {
			t.Errorf("%q leaked: %s", secret, out)
		}
	}
	if got := Redact("a tiny word"); got != "a tiny word" {
		t.Errorf("short values must not be registered: %q", got)
	}
}

func TestErrorAndStructValuesAreRedacted(t *testing.T) {
	l, buf := newBuf(t)
	l.Warn("x", "err", errors.New("failed: password=hunter2hunter2"), "obj", map[string]string{"note": "token=abcdefgh1234"})
	if out := buf.String(); strings.Contains(out, "hunter2hunter2") || strings.Contains(out, "abcdefgh1234") {
		t.Fatalf("leak: %s", out)
	}
}

func TestStandardLogIsBridgedAndRedacted(t *testing.T) {
	l, buf := newBuf(t)
	old := slog.Default()
	slog.SetDefault(l) // перенаправляет и log.Printf
	t.Cleanup(func() { slog.SetDefault(old); log.SetOutput(log.Writer()) })
	log.Printf("legacy line with password=hunter2hunter2")
	r := lines(t, buf)
	if len(r) != 1 || strings.Contains(fmt.Sprint(r[0]["msg"]), "hunter2hunter2") {
		t.Fatalf("legacy log.Printf must come out as redacted JSON: %v", r)
	}
}

func TestRequestIDFromContext(t *testing.T) {
	l, buf := newBuf(t)
	ctx := WithRequestID(context.Background(), "abcd1234abcd1234")
	l.InfoContext(ctx, "with")
	l.Info("without")
	r := lines(t, buf)
	if r[0]["request_id"] != "abcd1234abcd1234" {
		t.Errorf("request_id missing: %v", r[0])
	}
	if _, ok := r[1]["request_id"]; ok {
		t.Errorf("no request_id without context: %v", r[1])
	}
	// logger.With не теряет request_id
	l.With("component", "x").InfoContext(ctx, "child")
	if r := lines(t, buf); r[len(r)-1]["request_id"] != "abcd1234abcd1234" {
		t.Errorf("child logger lost request_id: %v", r[len(r)-1])
	}
}

func TestLevelFilter(t *testing.T) {
	ResetSecrets()
	var buf bytes.Buffer
	l := New(Options{Writer: &buf, Level: slog.LevelWarn})
	l.Info("hidden")
	l.Warn("shown")
	if r := lines(t, &buf); len(r) != 1 || r[0]["msg"] != "shown" {
		t.Fatalf("level filter: %v", r)
	}
	for in, want := range map[string]slog.Level{"": slog.LevelInfo, "DEBUG": slog.LevelDebug, "warning": slog.LevelWarn, "error": slog.LevelError} {
		if got, err := ParseLevel(in); err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Error("unknown level must be an error")
	}
}

func TestRequestIDValidation(t *testing.T) {
	for _, ok := range []string{"abcd1234", "req-1234.5678_x", NewRequestID()} {
		if !ValidRequestID(ok) {
			t.Errorf("%q must be accepted", ok)
		}
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", 65), "id with space", "id\nnewline-injection", `a"b"c"d"e"f"g"`, "идентификатор1"} {
		if ValidRequestID(bad) {
			t.Errorf("%q must be rejected", bad)
		}
	}
	if a, b := NewRequestID(), NewRequestID(); a == b || len(a) != 16 {
		t.Errorf("ids must be unique 16-char values: %q %q", a, b)
	}
}
