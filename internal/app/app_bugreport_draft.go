// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/bugreport"
)

// The way of the desktop that needs no mail server: the user's own mail program is opened with the report prepared
// (a mailto link). A link cannot carry an attachment, so the screenshot is saved as a file, and the interface also
// puts it on the clipboard. A description that does not fit into the link is saved as a text file.

var (
	errBugReportDraftUnavailable = errors.New("the folder for the bug report files is not available")
	errBugReportRevealFailed     = errors.New("the folder could not be opened")
	errBugReportFileUnknown      = errors.New("the file is not a file of a bug report")
)

const (
	draftPrefix   = "kubron-bug-"
	draftMaxFiles = 20
	draftMaxAge   = 7 * 24 * time.Hour
)

// BugReportDraft tells the interface what was prepared. Paths are empty for what was not needed.
type BugReportDraft struct {
	ScreenshotPath string `json:"screenshotPath"`
	TextPath       string `json:"textPath"`
	Truncated      bool   `json:"truncated"` // the description was shortened in the mail; the full text is in TextPath
}

// fileRevealer is implemented by the desktop platform: it shows a file in the file manager of the system.
type fileRevealer interface{ RevealFile(path string) error }

// SetBugReportDraftDir enables the mail-program way and gives the folder for its files. A function of the package,
// not a method of App, so that it stays out of the Wails bindings and the RPC.
func SetBugReportDraftDir(a *App, dir string) { a.bugReport.draftDir = dir }

// OpenBugReportDraft opens the mail program with the report prepared and saves the screenshot (and, if the text was
// shortened, the full text) as files. Desktop only (it is in deniedMethods of the server).
func (a *App) OpenBugReportDraft(req BugReportRequest) (BugReportDraft, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return BugReportDraft{}, err
	}
	s := a.bugReport
	if s.draftDir == "" {
		return BugReportDraft{}, errBugReportDraftUnavailable
	}
	r, _, err := a.reportFromRequest(req)
	if err != nil {
		return BugReportDraft{}, err
	}
	to := bugreport.DefaultRecipient
	if cfg, _, err := s.config(); err == nil && cfg != nil { // the recipient the user chose for the mail server, if any
		if n, err := cfg.Normalize(); err == nil {
			to = n.To
		}
	}
	link, truncated, err := bugreport.Mailto(to, r, bugreport.MaxMailtoLen)
	if err != nil {
		return BugReportDraft{}, err
	}

	d := BugReportDraft{Truncated: truncated}
	now := s.now()
	stamp := now.Format("20060102-150405")
	s.pruneDrafts(now)
	if len(r.Screenshot) > 0 {
		d.ScreenshotPath = filepath.Join(s.draftDir, draftPrefix+stamp+".png")
		if err := writeFileAtomic(d.ScreenshotPath, r.Screenshot); err != nil {
			return BugReportDraft{}, errBugReportDraftUnavailable
		}
	}
	if truncated {
		d.TextPath = filepath.Join(s.draftDir, draftPrefix+stamp+".txt")
		if err := writeFileAtomic(d.TextPath, []byte(bugreport.PlainBody(r, now))); err != nil {
			return BugReportDraft{}, errBugReportDraftUnavailable
		}
	}
	a.platform.OpenURL(link)
	return d, nil
}

// RevealBugReportFile shows a file that OpenBugReportDraft saved in the file manager. Any other path is refused: the
// method must not become a way to open arbitrary files.
func (a *App) RevealBugReportFile(path string) error {
	if err := a.authz(auth.PermRead); err != nil {
		return err
	}
	s := a.bugReport
	if s.draftDir == "" {
		return errBugReportDraftUnavailable
	}
	clean := filepath.Clean(path)
	base := filepath.Base(clean)
	ext := filepath.Ext(base)
	if filepath.Dir(clean) != filepath.Clean(s.draftDir) || !strings.HasPrefix(base, draftPrefix) || (ext != ".png" && ext != ".txt") {
		return errBugReportFileUnknown
	}
	if fi, err := os.Lstat(clean); err != nil || !fi.Mode().IsRegular() {
		return errBugReportFileUnknown
	}
	rv, ok := a.platform.(fileRevealer)
	if !ok || rv.RevealFile(clean) != nil {
		return errBugReportRevealFailed
	}
	return nil
}

// pruneDrafts removes the files of old reports so that the folder does not grow: older than a week, and all but the
// newest draftMaxFiles. Errors are ignored: a file that stays is harmless.
func (s *bugReportService) pruneDrafts(now time.Time) {
	entries, err := os.ReadDir(s.draftDir)
	if err != nil {
		return
	}
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), draftPrefix) {
			continue
		}
		if fi, err := e.Info(); err == nil {
			files = append(files, file{filepath.Join(s.draftDir, e.Name()), fi.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for i, f := range files {
		if i >= draftMaxFiles-2 || now.Sub(f.mod) > draftMaxAge { // room for the two files of this report
			_ = os.Remove(f.path)
		}
	}
}
