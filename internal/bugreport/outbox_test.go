// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package bugreport

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func idN(n int) string { return fmt.Sprintf("%032x", n) }

func TestOutboxRoundTrip(t *testing.T) {
	ob := NewOutbox(filepath.Join(t.TempDir(), "outbox"))
	if ob.Count() != 0 {
		t.Fatal("a folder that is not there holds nothing")
	}
	if list, err := ob.List(); err != nil || len(list) != 0 {
		t.Fatalf("got %v, %v", list, err)
	}
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	r := sampleReport(t)
	if err := ob.Add(testID, r, now); err != nil {
		t.Fatal(err)
	}
	list, err := ob.List()
	if err != nil || len(list) != 1 || ob.Count() != 1 {
		t.Fatalf("got %v, %v", list, err)
	}
	e := list[0]
	if e.ID != testID || !e.Created.Equal(now) || e.Report.Description != r.Description ||
		string(e.Report.Screenshot) != string(r.Screenshot) || e.Report.Diagnostics != r.Diagnostics || e.Report.Contact != r.Contact {
		t.Errorf("the entry differs: %+v", e)
	}
	if err := ob.Remove(testID); err != nil || ob.Count() != 0 {
		t.Fatalf("remove: %v, count %d", err, ob.Count())
	}
	if err := ob.Remove(testID); err != nil {
		t.Fatalf("removing a missing report is not an error: %v", err)
	}
}

func TestOutboxFilesAreForTheUserOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes of files are not meaningful on Windows")
	}
	dir := filepath.Join(t.TempDir(), "outbox")
	ob := NewOutbox(dir)
	if err := ob.Add(testID, sampleReport(t), time.Now()); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("folder mode %v", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Join(dir, testID+".json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", fi.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".outbox-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestOutboxListSortsOldestFirstAndDropsBrokenFiles(t *testing.T) {
	dir := t.TempDir()
	ob := NewOutbox(dir)
	base := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	for _, n := range []int{3, 1, 2} { // added out of order
		if err := ob.Add(idN(n), sampleReport(t), base.Add(time.Duration(n)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	bad := filepath.Join(dir, idN(9)+".json")
	_ = os.WriteFile(bad, []byte("{not json"), 0o600)
	other := filepath.Join(dir, "notes.txt") // not ours: left alone
	_ = os.WriteFile(other, []byte("x"), 0o600)
	// A file whose name and content disagree is not trusted either.
	swapped, _ := os.ReadFile(filepath.Join(dir, idN(1)+".json"))
	liar := filepath.Join(dir, idN(8)+".json")
	_ = os.WriteFile(liar, swapped, 0o600)

	list, err := ob.List()
	if err != nil || len(list) != 3 {
		t.Fatalf("got %d entries, %v", len(list), err)
	}
	for i, want := range []int{1, 2, 3} {
		if list[i].ID != idN(want) {
			t.Errorf("position %d: %s", i, list[i].ID)
		}
	}
	for _, p := range []string{bad, liar} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s must be removed", filepath.Base(p))
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("a foreign file must stay")
	}
}

func TestOutboxRefusesAnInvalidReportOnDisk(t *testing.T) {
	dir := t.TempDir()
	ob := NewOutbox(dir)
	_ = ob.Add(testID, sampleReport(t), time.Now())
	p := filepath.Join(dir, testID+".json")
	data, _ := os.ReadFile(p)
	// a screenshot that is not a PNG: the file was changed outside of the application
	data = bytes.Replace(data, []byte(`"screenshot":"`), []byte(`"screenshot":"AAAA`), 1)
	_ = os.WriteFile(p, data, 0o600)
	if list, _ := ob.List(); len(list) != 0 {
		t.Fatalf("a changed report must not be sent: %v", list)
	}
}

func TestOutboxKeepsTheNewestAndForgetsTheOld(t *testing.T) {
	dir := t.TempDir()
	ob := NewOutbox(dir)
	now := time.Now()
	for i := 1; i <= OutboxMaxEntries+5; i++ {
		if err := ob.Add(idN(i), sampleReport(t), now); err != nil {
			t.Fatal(err)
		}
		// give each file its own modification time: the newest has the biggest i
		_ = os.Chtimes(filepath.Join(dir, idN(i)+".json"), now, now.Add(time.Duration(i)*time.Second))
	}
	if n := ob.Count(); n > OutboxMaxEntries {
		t.Fatalf("%d entries are kept, the limit is %d", n, OutboxMaxEntries)
	}
	if _, err := os.Stat(filepath.Join(dir, idN(OutboxMaxEntries+5)+".json")); err != nil {
		t.Error("the newest report must stay")
	}

	old := filepath.Join(dir, idN(1000)+".json")
	_ = ob.Add(idN(1000), sampleReport(t), now)
	past := now.Add(-OutboxMaxAge - time.Hour)
	_ = os.Chtimes(old, past, past)
	_ = ob.Add(idN(1001), sampleReport(t), now) // adding prunes
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("a report older than the limit must be forgotten")
	}
}

func TestOutboxAddRefusesABadID(t *testing.T) {
	ob := NewOutbox(t.TempDir())
	for _, id := range []string{"", "../escape", "x"} {
		if err := ob.Add(id, sampleReport(t), time.Now()); err == nil {
			t.Errorf("%q must be refused", id)
		}
	}
	if err := ob.Remove("../escape"); err != nil {
		t.Errorf("removing a bad id must be a no-op, got %v", err)
	}
}

func TestOutboxWriteZip(t *testing.T) {
	ob := NewOutbox(t.TempDir())
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	withShot := sampleReport(t)
	noShot := sampleReport(t)
	noShot.Screenshot = nil
	noShot.Description = "second report"
	_ = ob.Add(idN(1), withShot, now)
	_ = ob.Add(idN(2), noShot, now.Add(time.Minute))

	var buf bytes.Buffer
	n, err := ob.WriteZip(&buf)
	if err != nil || n != 2 {
		t.Fatalf("got %d, %v", n, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(b)
	}
	if len(files) != 3 {
		t.Fatalf("want 3 files, got %v", files)
	}
	var shotPath string
	for name, body := range files {
		if strings.HasSuffix(name, "screenshot.png") {
			shotPath = name
			if body != string(withShot.Screenshot) {
				t.Error("the screenshot differs")
			}
		}
		if strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
			t.Errorf("unsafe name %q", name)
		}
	}
	if !strings.HasPrefix(shotPath, "20261110-080000-00000000/") {
		t.Errorf("folder name %q", shotPath)
	}
	var texts int
	for name, body := range files {
		if strings.HasSuffix(name, "report.txt") {
			texts++
			if !strings.Contains(body, "Application: Kubron 1.2.3") {
				t.Errorf("%s lacks the facts: %q", name, body)
			}
		}
	}
	if texts != 2 {
		t.Errorf("want 2 texts, got %d", texts)
	}
}

func TestOutboxWriteZipOfNothing(t *testing.T) {
	var buf bytes.Buffer
	n, err := NewOutbox(t.TempDir()).WriteZip(&buf)
	if err != nil || n != 0 {
		t.Fatalf("got %d, %v", n, err)
	}
}
