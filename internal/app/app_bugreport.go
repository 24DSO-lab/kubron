// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/bugreport"
	"github.com/24DSO-lab/kubron/internal/update"
)

var (
	errBugReportNotConfigured = errors.New("the mail server for bug reports is not configured")
	errBugReportTooOften      = errors.New("a report was sent a moment ago; wait a little and try again")
)

// Limits of the delivery. On the web server all sessions share one mail account, so a report is limited per user
// and the whole server has an hourly cap; the desktop has one user.
const (
	bugReportMinInterval = 30 * time.Second // between two reports of one user
	bugReportHourlyCap   = 30               // all users together
	bugReportMaxUsers    = 1024             // users remembered for the interval; older ones are dropped
)

// BugReportRequest is what the interface sends: the text, an optional contact address and an optional screenshot.
type BugReportRequest struct {
	Description string `json:"description"`
	Contact     string `json:"contact"`
	// Screenshot is a PNG as plain base64 (no "data:" prefix); empty means no screenshot.
	Screenshot string `json:"screenshot"`
	Locale     string `json:"locale"`
	View       string `json:"view"`
}

// BugReportSettings are the editable settings of the mail server. The password is write-only.
type BugReportSettings struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	From        string `json:"from"`
	To          string `json:"to"`
	Security    string `json:"security"`
	PasswordSet bool   `json:"passwordSet"`
}

// BugReportStatus tells the interface whether a report can be sent and with which settings.
type BugReportStatus struct {
	Configured bool              `json:"configured"`
	Source     string            `json:"source"` // "saved", "environment" or ""
	Recipient  string            `json:"recipient"`
	CanSave    bool              `json:"canSave"`  // the settings can be stored (the master key is available)
	CanDraft   bool              `json:"canDraft"` // the desktop can open the mail program with the report prepared
	Settings   BugReportSettings `json:"settings"`
}

// bugReportService holds what the reports need. Zero limits and stores are fine: it then works from the
// environment only.
type bugReportService struct {
	store    *BugReportStore
	draftDir string // folder of the screenshots and texts for the mail program; empty on the server
	send     func(context.Context, bugreport.SMTPConfig, []byte) error
	now      func() time.Time
	getenv   func(string) string

	mu      sync.Mutex
	last    map[string]time.Time // user -> time of the last delivered report
	sending map[string]bool      // user -> a delivery is in progress
	hour    []time.Time          // deliveries of the last hour, all users
}

func newBugReportService() *bugReportService {
	return &bugReportService{
		send: bugreport.Send, now: time.Now, getenv: os.Getenv,
		last: map[string]time.Time{}, sending: map[string]bool{},
	}
}

// SetBugReport gives the App the store of the mail settings; called before OnStartup. A function of the package,
// not a method of App, so that it stays out of the Wails bindings and the RPC.
func SetBugReport(a *App, store *BugReportStore) { a.bugReport.store = store }

// config returns the settings in use: the saved ones first, then the environment.
func (s *bugReportService) config() (cfg *bugreport.SMTPConfig, source string, err error) {
	if s.store != nil {
		saved, err := s.store.Load()
		if err != nil {
			return nil, "", err
		}
		if saved != nil {
			return saved, "saved", nil
		}
	}
	if env := bugReportConfigFromEnv(s.getenv); env != nil {
		return env, "environment", nil
	}
	return nil, "", nil
}

// configured reports whether a report can be sent now.
func (s *bugReportService) configured() bool {
	cfg, _, err := s.config()
	if err != nil || cfg == nil {
		return false
	}
	_, err = cfg.Normalize()
	return err == nil
}

// editable reports whether the settings can be changed from the interface: only where there is a store (desktop).
func (s *bugReportService) editable() bool { return s.store != nil && s.store.env != nil }

// bugReportCapability is the capabilities.bugReport flag: the shell supports the feature and the report can leave: the
// desktop opens the mail program or the user sets a mail server in the dialog; the web server has the mail server that
// the administrator set.
func (a *App) bugReportCapability(shell bool) bool {
	return shell && (a.bugReport.draftDir != "" || a.bugReport.editable() || a.bugReport.configured())
}

// GetBugReportStatus returns the state of the mail settings. Desktop only (it is in deniedMethods of the server).
func (a *App) GetBugReportStatus() (BugReportStatus, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return BugReportStatus{}, err
	}
	s := a.bugReport
	st := BugReportStatus{Recipient: bugreport.DefaultRecipient, CanSave: s.editable(), CanDraft: s.draftDir != ""}
	cfg, source, err := s.config()
	if err != nil {
		return st, err
	}
	if cfg == nil {
		return st, nil
	}
	n, err := cfg.Normalize()
	if err != nil { // settings that cannot be used count as none, the form shows them for a fix
		n = *cfg
	} else {
		st.Configured = true
	}
	st.Source, st.Recipient = source, n.To
	if !st.CanSave { // the server: the host and the user of the mail account are not for every user
		return st, nil
	}
	st.Settings = BugReportSettings{
		Host: n.Host, Port: n.Port, Username: n.Username, From: n.From, To: n.To,
		Security: string(n.Security), PasswordSet: n.Password != "",
	}
	return st, nil
}

// SaveBugReportSettings saves the mail server. An empty password keeps the saved one for the same host and user.
func (a *App) SaveBugReportSettings(in BugReportSettings) error {
	if err := a.authz(auth.PermRead); err != nil {
		return err
	}
	if a.bugReport.store == nil {
		return errBugReportUnavailable
	}
	return a.bugReport.store.Save(bugreport.SMTPConfig{
		Host: in.Host, Port: in.Port, Username: in.Username, Password: in.Password,
		From: in.From, To: in.To, Security: bugreport.Security(in.Security),
	})
}

// ClearBugReportSettings removes the saved mail server.
func (a *App) ClearBugReportSettings() error {
	if err := a.authz(auth.PermRead); err != nil {
		return err
	}
	if a.bugReport.store == nil {
		return errBugReportUnavailable
	}
	return a.bugReport.store.Clear()
}

// SubmitBugReport sends the report by e-mail. The screenshot, if any, is attached as a PNG. Nothing else from the
// machine is added: only the version, the system, the language and the name of the open section.
func (a *App) SubmitBugReport(req BugReportRequest) error {
	if err := a.authz(auth.PermRead); err != nil {
		return err
	}
	s := a.bugReport
	r, user, err := a.reportFromRequest(req)
	if err != nil {
		return err
	}
	cfg, _, err := s.config()
	if err != nil {
		return err
	}
	if cfg == nil {
		return errBugReportNotConfigured
	}
	n, err := cfg.Normalize()
	if err != nil {
		return err
	}
	msg, err := bugreport.Build(n.From, n.To, r, s.now())
	if err != nil {
		return err
	}
	if err := s.begin(user); err != nil {
		return err
	}
	ok := false
	defer func() { s.finish(user, ok) }()
	ctx, cancel := a.opCtx(bugreport.SendTimeout + 5*time.Second)
	defer cancel()
	if err := s.send(ctx, n, msg); err != nil {
		return err
	}
	ok = true
	a.recordBugReport(len(r.Screenshot) > 0)
	return nil
}

// auditBugReport — действие аудита «отчёт об ошибке отправлен» (подпись — audit.actions.bugreport.submit).
const auditBugReport = "bugreport.submit"

// recordBugReport пишет в журнал факт отправки. Текст отчёта, адреса и снимок не записываются.
func (a *App) recordBugReport(withScreenshot bool) {
	if a.audit == nil || a.auth == nil {
		return
	}
	actor := a.auth.Actor()
	detail := "screenshot=no"
	if withScreenshot {
		detail = "screenshot=yes"
	}
	a.audit.Record(audit.Event{
		User: actor.Username, Role: string(actor.Role), Action: auditBugReport, Result: audit.ResultOK,
		Detail: detail, Source: actor.Source,
	})
}

// reportFromRequest turns what the interface sent into a checked report. user is the login of the web user.
func (a *App) reportFromRequest(req BugReportRequest) (r bugreport.Report, user string, err error) {
	shell := "desktop"
	if a.sessionID != "" { // a view of a web session
		shell = "web"
		if a.auth != nil {
			user = a.auth.Actor().Username
		}
	}
	r = bugreport.Report{
		Description: req.Description, Contact: req.Contact,
		Diagnostics: bugreport.Diagnostics{
			AppVersion: a.appVersion(), Shell: shell, User: user, OS: runtime.GOOS, Arch: runtime.GOARCH,
			Locale: req.Locale, View: req.View,
		},
	}
	if req.Screenshot != "" {
		if len(req.Screenshot) > base64.StdEncoding.EncodedLen(bugreport.MaxScreenshotBytes) {
			return r, user, bugreport.ErrScreenshotTooLarge
		}
		png, err := base64.StdEncoding.DecodeString(req.Screenshot)
		if err != nil {
			return r, user, bugreport.ErrScreenshotInvalid
		}
		r.Screenshot = png
	}
	return r, user, r.Normalize()
}

// begin takes the right to send for a user: one delivery at a time, not more often than bugReportMinInterval, and
// not more than bugReportHourlyCap deliveries an hour on the whole server.
func (s *bugReportService) begin(user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.sending[user] {
		return errBugReportTooOften
	}
	if t, ok := s.last[user]; ok && now.Sub(t) < bugReportMinInterval {
		return errBugReportTooOften
	}
	keep := s.hour[:0]
	for _, t := range s.hour {
		if now.Sub(t) < time.Hour {
			keep = append(keep, t)
		}
	}
	s.hour = keep
	if len(s.hour) >= bugReportHourlyCap {
		return errBugReportTooOften
	}
	s.sending[user] = true
	return nil
}

// finish ends a delivery; only a delivered report starts the waiting time, so a failed one can be retried.
func (s *bugReportService) finish(user string, delivered bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sending, user)
	if !delivered {
		return
	}
	now := s.now()
	if len(s.last) >= bugReportMaxUsers {
		for u, t := range s.last { // drop what no longer limits anyone
			if now.Sub(t) >= bugReportMinInterval {
				delete(s.last, u)
			}
		}
	}
	s.last[user] = now
	s.hour = append(s.hour, now)
}

// appVersion is the version of the build that the update check knows (main.version, set by the release).
func (a *App) appVersion() string {
	if a.updates != nil {
		if v := a.updates.Version().Version; v != "" {
			return v
		}
	}
	return update.DevVersion
}
