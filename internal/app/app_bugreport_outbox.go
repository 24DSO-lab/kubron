// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/bugreport"
)

// The desktop keeps a report that could not be delivered (no network, the receiver is down) in a folder of the data
// directory and sends it again later, so that the user presses the button once and never has to come back to it. Where
// nothing can be sent at all, the waiting reports can be written as one archive and passed on by hand.

var (
	errBugReportNothingToExport = errors.New("there are no reports waiting for delivery")
	errBugReportExportFailed    = errors.New("the archive of the reports could not be written")
)

const (
	exportPrefix  = "kubron-reports-"
	exportSuffix  = ".zip"
	exportKeep    = 5 // archives kept in the folder; older ones are removed
	flushTimeout  = 2 * time.Minute
	flushMaxBatch = bugreport.OutboxMaxEntries
)

// fileRevealer is implemented by the desktop platform: it shows a file in the file manager of the system.
type fileRevealer interface{ RevealFile(path string) error }

// SetBugReportOutbox turns on the delivery of the desktop: the receiver of the project becomes the default way out, and
// reports that could not be delivered wait in dir/outbox. Archives for hand delivery are written to dir/export. A
// function of the package, not a method of App, so that it stays out of the Wails bindings and the RPC. It is not
// called on the web server: there a failed delivery is shown to the user, and the administrator names the way out.
func SetBugReportOutbox(a *App, dir string) {
	s := a.bugReport
	s.outbox = bugreport.NewOutbox(filepath.Join(dir, "outbox"))
	s.exportDir = filepath.Join(dir, "export")
	s.relayDefault = bugreport.DefaultRelayURL
}

// runBugReportRetry sends the waiting reports soon after the start and then at an interval, until ctx ends. A retry
// that finds no connection stops at the first failure, so an offline machine is not asked again and again.
func (a *App) runBugReportRetry(ctx context.Context) {
	s := a.bugReport
	if s.outbox == nil {
		return
	}
	t := time.NewTimer(s.retryFirst)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.flushBugReports(ctx)
			t.Reset(s.retryEvery)
		}
	}
}

// flushBugReports delivers the waiting reports, the oldest first. A report that the receiver refused for good is
// dropped (it will never be accepted); a report that failed for a reason that may pass stays, and the run ends there.
func (a *App) flushBugReports(ctx context.Context) {
	s := a.bugReport
	if s.outbox == nil || !s.beginFlush() {
		return
	}
	defer s.endFlush()
	s.outbox.Prune(s.now())
	chans, err := s.channels()
	if err != nil || len(chans) == 0 {
		return
	}
	entries, err := s.outbox.List()
	if err != nil {
		return
	}
	if len(entries) > flushMaxBatch {
		entries = entries[:flushMaxBatch]
	}
	for _, e := range entries {
		if ctx.Err() != nil {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, s.deliveryTimeout(len(chans)))
		channel, retry, err := s.deliver(cctx, chans, e.ID, e.Report)
		cancel()
		switch {
		case err == nil:
			_ = s.outbox.Remove(e.ID)
			slog.Info("bug report: a waiting report was delivered", "channel", channel)
		case !retry && errors.Is(err, bugreport.ErrRelayRejected):
			_ = s.outbox.Remove(e.ID)
			slog.Warn("bug report: a waiting report was refused and is dropped", "id", e.ID)
		default:
			return // no connection or a setting to fix: the report stays
		}
	}
}

// beginFlush takes the right to run a retry; only one runs at a time.
func (s *bugReportService) beginFlush() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.flushing {
		return false
	}
	s.flushing = true
	return true
}

func (s *bugReportService) endFlush() {
	s.mu.Lock()
	s.flushing = false
	s.mu.Unlock()
}

// ExportBugReports writes the waiting reports as one zip archive, shows it in the file manager and returns its path.
// For a machine that cannot reach the receiver: the archive is sent by any other means. The reports stay in the outbox
// and are still delivered if a connection appears. Desktop only (it is in deniedMethods of the server).
func (a *App) ExportBugReports() (string, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return "", err
	}
	s := a.bugReport
	if s.outbox == nil || s.exportDir == "" {
		return "", errBugReportUnavailable
	}
	var buf bytes.Buffer
	n, err := s.outbox.WriteZip(&buf)
	if err != nil {
		return "", errBugReportExportFailed
	}
	if n == 0 {
		return "", errBugReportNothingToExport
	}
	now := s.now()
	path := filepath.Join(s.exportDir, exportPrefix+now.Format("20060102-150405")+exportSuffix)
	s.pruneExports(now)
	if err := bugreport.WriteFileAtomic(path, buf.Bytes()); err != nil {
		return "", errBugReportExportFailed
	}
	if rv, ok := a.platform.(fileRevealer); ok {
		if err := rv.RevealFile(path); err != nil { // the archive is written; only showing it failed
			slog.Warn("bug report: the archive could not be shown in the file manager", "error", err.Error())
		}
	}
	return path, nil
}

// pruneExports keeps the newest exportKeep-1 archives, so that with the one being written there are exportKeep.
func (s *bugReportService) pruneExports(_ time.Time) {
	entries, err := os.ReadDir(s.exportDir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), exportPrefix) && strings.HasSuffix(e.Name(), exportSuffix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // the stamp in the name sorts by time
	for i := 0; i < len(names)-(exportKeep-1); i++ {
		_ = os.Remove(filepath.Join(s.exportDir, names[i]))
	}
}
