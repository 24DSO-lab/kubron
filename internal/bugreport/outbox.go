// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package bugreport

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Limits of the outbox: a report that could not be delivered waits for a while and then is forgotten, so that the
// folder (it holds screenshots) does not grow without end.
const (
	OutboxMaxEntries = 20
	OutboxMaxAge     = 14 * 24 * time.Hour
	outboxSuffix     = ".json"
	maxOutboxFile    = 16 << 20 // a stored report is at most a screenshot (6 MiB, as base64 8 MiB) and the text
)

// ErrOutboxUnavailable is returned when the folder of the outbox cannot be used. The cause is not shown to the user.
var ErrOutboxUnavailable = errors.New("the folder for the reports that wait for delivery is not available")

// Outbox keeps reports that could not be delivered, one file per report, in a folder that only the user can read.
// It is safe to use from one process; the files are written atomically.
type Outbox struct{ dir string }

// NewOutbox returns the outbox in dir. The folder is created on the first Add.
func NewOutbox(dir string) *Outbox { return &Outbox{dir: dir} }

// Entry is a waiting report.
type Entry struct {
	ID      string
	Created time.Time
	Report  Report
}

type storedReport struct {
	ID          string      `json:"id"`
	Created     time.Time   `json:"created"`
	Description string      `json:"description"`
	Contact     string      `json:"contact,omitempty"`
	Screenshot  []byte      `json:"screenshot,omitempty"` // encoded as base64 by encoding/json
	Diagnostics Diagnostics `json:"diagnostics"`
}

func (o *Outbox) path(id string) string { return filepath.Join(o.dir, id+outboxSuffix) }

// Add stores a normalized report under id and forgets the oldest entries over the limits.
func (o *Outbox) Add(id string, r Report, now time.Time) error {
	if !ValidID(id) {
		return ErrOutboxUnavailable
	}
	data, err := json.Marshal(storedReport{
		ID: id, Created: now.UTC(), Description: r.Description, Contact: r.Contact,
		Screenshot: r.Screenshot, Diagnostics: r.Diagnostics,
	})
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(o.path(id), data); err != nil {
		return ErrOutboxUnavailable
	}
	o.prune(now, id+outboxSuffix)
	return nil
}

// Remove deletes a report; a report that is not there is not an error.
func (o *Outbox) Remove(id string) error {
	if !ValidID(id) {
		return nil
	}
	if err := os.Remove(o.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrOutboxUnavailable
	}
	return nil
}

// List returns the waiting reports, the oldest first. A file that cannot be read as a report is removed: it would
// block the queue for ever.
func (o *Outbox) List() ([]Entry, error) {
	names, err := o.names()
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, name := range names {
		e, ok := o.load(filepath.Join(o.dir, name))
		if !ok {
			_ = os.Remove(filepath.Join(o.dir, name))
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out, nil
}

// Count is the number of waiting reports; it does not read them.
func (o *Outbox) Count() int {
	names, err := o.names()
	if err != nil {
		return 0
	}
	return len(names)
}

func (o *Outbox) names() ([]string, error) {
	entries, err := os.ReadDir(o.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrOutboxUnavailable
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, outboxSuffix) || !ValidID(strings.TrimSuffix(n, outboxSuffix)) {
			continue
		}
		names = append(names, n)
	}
	return names, nil
}

func (o *Outbox) load(path string) (Entry, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxOutboxFile {
		return Entry{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, false
	}
	var s storedReport
	if json.Unmarshal(data, &s) != nil || !ValidID(s.ID) || s.ID+outboxSuffix != filepath.Base(path) {
		return Entry{}, false
	}
	r := Report{Description: s.Description, Contact: s.Contact, Screenshot: s.Screenshot, Diagnostics: s.Diagnostics}
	if r.Normalize() != nil { // the file was changed by hand or by another version: not trusted
		return Entry{}, false
	}
	return Entry{ID: s.ID, Created: s.Created, Report: r}, true
}

// Prune forgets the reports older than OutboxMaxAge and all but the newest OutboxMaxEntries.
func (o *Outbox) Prune(now time.Time) { o.prune(now, "") }

// prune forgets the reports older than OutboxMaxAge and all but the newest OutboxMaxEntries. The report that was just
// added (keep) is never forgotten, whatever the clock says. Errors are ignored: a file that stays is harmless.
func (o *Outbox) prune(now time.Time, keep string) {
	names, err := o.names()
	if err != nil {
		return
	}
	type file struct {
		path string
		mod  time.Time
	}
	files := make([]file, 0, len(names))
	for _, n := range names {
		if n == keep {
			continue
		}
		if fi, err := os.Stat(filepath.Join(o.dir, n)); err == nil {
			files = append(files, file{filepath.Join(o.dir, n), fi.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for i, f := range files {
		if i >= OutboxMaxEntries-1 || now.Sub(f.mod) > OutboxMaxAge { // one place is the new report's
			_ = os.Remove(f.path)
		}
	}
}

// WriteZip writes the waiting reports as a zip archive for a person to pass on by any means: for each report a folder
// with report.txt (the text and the facts) and screenshot.png. It returns how many reports it wrote.
func (o *Outbox) WriteZip(w io.Writer) (int, error) {
	entries, err := o.List()
	if err != nil {
		return 0, err
	}
	zw := zip.NewWriter(w)
	for _, e := range entries {
		dir := e.Created.UTC().Format("20060102-150405") + "-" + e.ID[:8] + "/"
		f, err := zw.Create(dir + "report.txt")
		if err != nil {
			return 0, err
		}
		if _, err := io.WriteString(f, PlainBody(e.Report, e.Created)); err != nil {
			return 0, err
		}
		if len(e.Report.Screenshot) > 0 {
			f, err := zw.Create(dir + "screenshot.png")
			if err != nil {
				return 0, err
			}
			if _, err := f.Write(e.Report.Screenshot); err != nil {
				return 0, err
			}
		}
	}
	return len(entries), zw.Close()
}

// WriteFileAtomic writes data next to path and renames it, so a reader never sees half a file.
func WriteFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".outbox-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(name)
		}
	}()
	if err = tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
