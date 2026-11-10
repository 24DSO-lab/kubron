// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package update

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPreferencesDefaultToAutoCheck(t *testing.T) {
	if !LoadPreferences(t.TempDir()).AutoCheck {
		t.Error("without a file the background check is on")
	}
}

func TestPreferencesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := SavePreferences(dir, Preferences{AutoCheck: false}); err != nil {
		t.Fatal(err)
	}
	if LoadPreferences(dir).AutoCheck {
		t.Error("the saved 'off' was lost")
	}
	if err := SavePreferences(dir, Preferences{AutoCheck: true}); err != nil {
		t.Fatal(err)
	}
	if !LoadPreferences(dir).AutoCheck {
		t.Error("the saved 'on' was lost")
	}
}

func TestPreferencesCorruptOrIncompleteFileFallsBackToDefault(t *testing.T) {
	for name, content := range map[string]string{"garbage": "not json", "empty object": "{}", "wrong type": `{"autoCheck":"no"}`} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, prefsFile), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if !LoadPreferences(dir).AutoCheck {
			t.Errorf("%s: a broken settings file must not turn the check off", name)
		}
	}
}

func TestSavePreferencesLeavesNoTempFilesAndIsPrivate(t *testing.T) {
	dir := t.TempDir()
	if err := SavePreferences(dir, Preferences{AutoCheck: false}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != prefsFile {
		t.Errorf("directory = %v, want only %s", entries, prefsFile)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(dir, prefsFile))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", st.Mode().Perm())
		}
	}
}

func TestSavePreferencesFailsInMissingDirectory(t *testing.T) {
	if err := SavePreferences(filepath.Join(t.TempDir(), "nope"), Preferences{}); err == nil {
		t.Error("an error is expected when the directory does not exist")
	}
}
