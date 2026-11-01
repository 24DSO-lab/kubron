// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMirrorWritesAuditLines(t *testing.T) {
	var buf bytes.Buffer
	SetMirror(&buf)
	t.Cleanup(func() { SetMirror(nil) })

	l := newLogger(t)
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	l.Record(Event{Time: at, User: "alice", Role: "admin", Action: "DeletePod", Result: ResultOK,
		Detail: "namespace=default name=nginx", Source: "203.0.113.9", TeamID: "t1", ClusterID: "c1"})
	l.Record(Event{Time: at, User: "bob", Action: "Login", Result: ResultDenied, Detail: "login failed password=hunter2hunter2"})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want two lines, got %d: %q", len(lines), buf.String())
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatalf("not JSON: %v: %q", err, lines[0])
	}
	for k, want := range map[string]any{
		"type": "audit", "level": "INFO", "msg": "audit", "user": "alice", "role": "admin", "action": "DeletePod",
		"result": "ok", "detail": "namespace=default name=nginx", "source": "203.0.113.9", "teamId": "t1", "clusterId": "c1",
		"time": "2026-11-01T10:00:00Z",
	} {
		if m[k] != want {
			t.Errorf("%s = %v, want %v", k, m[k], want)
		}
	}
	if strings.Contains(lines[1], "hunter2hunter2") {
		t.Errorf("a secret in detail must not reach the stream: %s", lines[1])
	}
	// Файловый журнал при этом не изменился: в нём исходный формат без type/level/msg
	p, err := l.Query(Filter{})
	if err != nil || len(p.Events) != 2 {
		t.Fatalf("query: %v %+v", err, p)
	}
}

func TestMirrorOffByDefaultAndSwitchable(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(t)
	l.Record(Event{User: "a", Action: "X", Result: ResultOK})
	if buf.Len() != 0 {
		t.Fatal("nothing must be written without SetMirror")
	}
	SetMirror(&buf)
	l.Record(Event{User: "a", Action: "Y", Result: ResultOK})
	SetMirror(nil)
	l.Record(Event{User: "a", Action: "Z", Result: ResultOK})
	if n := strings.Count(buf.String(), "\n"); n != 1 || !strings.Contains(buf.String(), `"action":"Y"`) {
		t.Fatalf("only the record made while the mirror was on is expected: %q", buf.String())
	}
}

// Отказ хранилища не скрывает действие от внешнего сборщика.
func TestMirrorSurvivesStorageFailure(t *testing.T) {
	var buf bytes.Buffer
	SetMirror(&buf)
	t.Cleanup(func() { SetMirror(nil) })

	file := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := Open(filepath.Join(file, "sub")) // каталог нельзя создать: запись в файл не удастся
	l.Record(Event{User: "a", Action: "DeletePod", Result: ResultOK})
	if !strings.Contains(buf.String(), `"action":"DeletePod"`) {
		t.Fatalf("the event must reach the stream even if storage fails: %q", buf.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

// Сломанный приёмник не мешает действию и журналу.
func TestMirrorWriteErrorIsIgnored(t *testing.T) {
	SetMirror(failingWriter{})
	t.Cleanup(func() { SetMirror(nil) })
	l := newLogger(t)
	l.Record(Event{User: "a", Action: "X", Result: ResultOK})
	p, err := l.Query(Filter{})
	if err != nil || len(p.Events) != 1 || p.WriteError != "" {
		t.Fatalf("storage must be unaffected by a broken stream: %v %+v", err, p)
	}
}
