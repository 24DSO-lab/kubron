// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

// journalAdd пишет запись в файловый журнал (так же, как трекер операций через хранилище).
func journalAdd(r HelmOperationRecord) { _ = fileHelmStore{}.AddOperation("", "", r) }

func jrec(id, op, status, rel string) HelmOperationRecord {
	return HelmOperationRecord{
		ID: id, Op: op, Status: status, Release: rel, Namespace: "prod", Cluster: "dev", Detail: "d-" + rel,
		StartedAt: "2026-10-28T10:00:00Z", FinishedAt: "2026-10-28T10:00:05Z", DurationMs: 5000, Log: []string{"line1", "line2"},
	}
}

func TestJournalAddListGet(t *testing.T) {
	setupHelmHome(t)
	m := NewManager()
	journalAdd(jrec("op-aaaaaaa1", "install", "success", "web"))
	journalAdd(jrec("op-aaaaaaa2", "rollback", "failed", "api"))
	journalAdd(jrec("op-aaaaaaa3", "upgrade", "canceled", "web"))

	all, err := m.ListHelmOperations(HelmOperationQuery{})
	if err != nil || len(all) != 3 || all[0].ID != "op-aaaaaaa3" || all[2].ID != "op-aaaaaaa1" {
		t.Fatalf("newest must come first: %+v %v", all, err)
	}
	for _, r := range all {
		if r.Log != nil {
			t.Error("the Helm log is not returned in the list")
		}
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(helmJournalPath()); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("operations.json permissions: %v %v", st, err)
		}
	}

	got, err := m.GetHelmOperation("op-aaaaaaa2")
	if err != nil || got.Release != "api" || len(got.Log) != 2 {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := m.GetHelmOperation("op-zzzzzzz9"); err == nil {
		t.Error("nonexistent entry")
	}
	if _, err := m.GetHelmOperation("../../etc"); err == nil {
		t.Error("invalid identifier")
	}
}

func TestJournalFilters(t *testing.T) {
	setupHelmHome(t)
	m := NewManager()
	journalAdd(jrec("op-bbbbbbb1", "install", "success", "web"))
	journalAdd(jrec("op-bbbbbbb2", "rollback", "failed", "api"))
	journalAdd(jrec("op-bbbbbbb3", "install", "failed", "web-2"))

	count := func(q HelmOperationQuery) int {
		got, err := m.ListHelmOperations(q)
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	if n := count(HelmOperationQuery{Op: "install"}); n != 2 {
		t.Errorf("op=install: %d", n)
	}
	if n := count(HelmOperationQuery{Status: "failed"}); n != 2 {
		t.Errorf("status=failed: %d", n)
	}
	if n := count(HelmOperationQuery{Op: "install", Status: "failed"}); n != 1 {
		t.Errorf("combination: %d", n)
	}
	if n := count(HelmOperationQuery{Search: "WEB"}); n != 2 {
		t.Errorf("case-insensitive search: %d", n)
	}
	if n := count(HelmOperationQuery{Search: "web 2"}); n != 1 { // все слова должны находиться в одной записи: web-2
		t.Errorf("multi-word search: %d", n)
	}
	if n := count(HelmOperationQuery{Search: "web api"}); n != 0 {
		t.Errorf("words from different entries do not match: %d", n)
	}
	if n := count(HelmOperationQuery{Limit: 1}); n != 1 {
		t.Errorf("limit: %d", n)
	}
	if _, err := m.ListHelmOperations(HelmOperationQuery{Op: "drop"}); err == nil {
		t.Error("unknown operation")
	}
	if _, err := m.ListHelmOperations(HelmOperationQuery{Status: "weird"}); err == nil {
		t.Error("unknown status")
	}
}

func TestJournalCapAndClear(t *testing.T) {
	setupHelmHome(t)
	m := NewManager()
	for i := 0; i < helmJournalMax+5; i++ {
		journalAdd(jrec(fmt.Sprintf("op-%08d", i), "install", "success", "web"))
	}
	all, err := m.ListHelmOperations(HelmOperationQuery{Limit: 1000})
	if err != nil || len(all) != helmJournalMax {
		t.Fatalf("the journal must keep at most %d entries: %d %v", helmJournalMax, len(all), err)
	}
	if all[0].ID != fmt.Sprintf("op-%08d", helmJournalMax+4) {
		t.Errorf("the newest entry first: %s", all[0].ID)
	}
	if err := m.ClearHelmOperations(); err != nil {
		t.Fatal(err)
	}
	if all, _ := m.ListHelmOperations(HelmOperationQuery{}); len(all) != 0 {
		t.Errorf("after clearing: %d", len(all))
	}
	if err := m.ClearHelmOperations(); err != nil {
		t.Errorf("clearing again is safe: %v", err)
	}
}

func TestJournalRecoversFromCorruption(t *testing.T) {
	setupHelmHome(t)
	m := NewManager()
	if err := os.MkdirAll(helmHomeDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helmJournalPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ListHelmOperations(HelmOperationQuery{}); err == nil {
		t.Error("a corrupted journal must give a clear error")
	}
	journalAdd(jrec("op-ccccccc1", "install", "success", "web")) // запись заменяет повреждённый файл
	if all, err := m.ListHelmOperations(HelmOperationQuery{}); err != nil || len(all) != 1 {
		t.Errorf("the journal is readable again after a write: %v %v", all, err)
	}
}

func TestTrackerFinishWritesJournal(t *testing.T) {
	setupHelmHome(t)
	m := NewManager()

	run := func(id string, err error) HelmOperationRecord {
		_, tr, e := m.beginHelmOp(context.Background(), id, "upgrade", "prod", "web", true)
		if e != nil {
			t.Fatal(e)
		}
		tr.note("values change")
		tr.setRevision(7)
		tr.addLine("waiting for resources count=3")
		tr.finish(err)
		rec, e := m.GetHelmOperation(id)
		if e != nil {
			t.Fatal(e)
		}
		return *rec
	}

	ok := run("op-ddddddd1", nil)
	if ok.Status != "success" || ok.Error != "" || ok.Revision != 7 || ok.Detail != "values change" ||
		ok.Op != "upgrade" || ok.Release != "web" || len(ok.Log) != 1 || ok.StartedAt == "" || ok.FinishedAt == "" {
		t.Errorf("success: %+v", ok)
	}
	if bad := run("op-ddddddd2", errors.New("timed out waiting")); bad.Status != "failed" || !strings.Contains(bad.Error, "timed out") {
		t.Errorf("failed: %+v", bad)
	}
	if cn := run("op-ddddddd3", context.Canceled); cn.Status != "canceled" || cn.Error != "operation canceled" {
		t.Errorf("canceled: %+v", cn)
	}
	// каждая операция — ровно одна запись
	all, _ := m.ListHelmOperations(HelmOperationQuery{})
	if len(all) != 3 {
		t.Errorf("entries: %d", len(all))
	}
}

func TestTrackerLogRingKeepsLastLines(t *testing.T) {
	setupHelmHome(t)
	m := NewManager()
	_, tr, _ := m.beginHelmOp(context.Background(), "op-eeeeeee1", "install", "prod", "web", false)
	for i := 0; i < helmJournalLogLines+20; i++ {
		tr.addLine(fmt.Sprintf("line %d", i))
	}
	tr.finish(nil)
	rec, err := m.GetHelmOperation("op-eeeeeee1")
	if err != nil || len(rec.Log) != helmJournalLogLines {
		t.Fatalf("%d lines: %v", len(rec.Log), err)
	}
	if rec.Log[len(rec.Log)-1] != fmt.Sprintf("line %d", helmJournalLogLines+19) || rec.Log[0] != "line 20" {
		t.Errorf("the last lines must be kept: %q … %q", rec.Log[0], rec.Log[len(rec.Log)-1])
	}
}
