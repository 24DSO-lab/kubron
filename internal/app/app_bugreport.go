// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
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

// What happened to a report: it left the machine, or it waits in the outbox for a connection.
const (
	bugReportSent   = "sent"
	bugReportQueued = "queued"
)

// Environment variable that sets the receiver of the reports (the relay): an address, or "off".
const envBugReportRelay = "KUBRON_BUGREPORT_RELAY_URL"

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

// BugReportResult tells the interface what became of a report.
type BugReportResult struct {
	Status  string `json:"status"`  // "sent" or "queued"
	Channel string `json:"channel"` // "mail" or "relay" when sent; empty when queued
	Pending int    `json:"pending"` // reports waiting for delivery after this call
}

// BugReportStatus tells the interface whether a report can be sent and with which settings.
type BugReportStatus struct {
	Configured bool              `json:"configured"` // a mail server is set
	Source     string            `json:"source"`     // "saved", "environment" or ""
	Recipient  string            `json:"recipient"`
	CanSave    bool              `json:"canSave"` // the settings can be stored (the master key is available)
	Channel    string            `json:"channel"` // where the next report goes: "mail", "relay" or "" (it waits in the outbox)
	CanQueue   bool              `json:"canQueue"`
	Pending    int               `json:"pending"` // reports that wait for delivery
	Settings   BugReportSettings `json:"settings"`
}

// bugReportService holds what the reports need. Zero limits and stores are fine: it then works from the
// environment only.
type bugReportService struct {
	store *BugReportStore
	// outbox keeps the reports that could not be delivered; nil on the server, where the user is told about the failure.
	outbox       *bugreport.Outbox
	exportDir    string // where the archive of the waiting reports is written; empty without an outbox
	relayDefault string // the receiver used when KUBRON_BUGREPORT_RELAY_URL is not set: the desktop has one, the server none
	send         func(context.Context, bugreport.SMTPConfig, []byte) error
	relay        func(ctx context.Context, endpoint, id string, r bugreport.Report) error
	now          func() time.Time
	getenv       func(string) string
	spawn        func(func())  // runs a retry in the background; tests make it synchronous
	retryFirst   time.Duration // the first retry after the start, and then every retryEvery
	retryEvery   time.Duration

	flushing bool // a retry is running

	mu      sync.Mutex
	last    map[string]time.Time // user -> time of the last delivered report
	sending map[string]bool      // user -> a delivery is in progress
	hour    []time.Time          // deliveries of the last hour, all users
}

func newBugReportService() *bugReportService {
	return &bugReportService{
		send: bugreport.Send, now: time.Now, getenv: os.Getenv,
		relay: func(ctx context.Context, endpoint, id string, r bugreport.Report) error {
			return bugreport.SendRelay(ctx, nil, endpoint, id, r)
		},
		spawn:      func(f func()) { go f() },
		retryFirst: time.Minute, retryEvery: 15 * time.Minute,
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

// bugReportCapability is the capabilities.bugReport flag: the shell supports the feature and the report can go
// somewhere: the desktop always can (the receiver of the project, or the outbox); the web server has the mail server or
// the receiver that the administrator set.
func (a *App) bugReportCapability(shell bool) bool {
	s := a.bugReport
	return shell && (s.outbox != nil || s.editable() || s.configured() || s.relayEndpoint() != "")
}

// GetBugReportStatus returns the state of the mail settings. Desktop only (it is in deniedMethods of the server).
func (a *App) GetBugReportStatus() (BugReportStatus, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return BugReportStatus{}, err
	}
	s := a.bugReport
	st := BugReportStatus{Recipient: bugreport.DefaultRecipient, CanSave: s.editable(), CanQueue: s.outbox != nil}
	if s.outbox != nil {
		st.Pending = s.outbox.Count()
	}
	if s.relayEndpoint() != "" {
		st.Channel = "relay"
	}
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
		st.Channel = "mail" // a mail server that was set is tried before the receiver of the project
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

// SubmitBugReport delivers the report: to the mail server if one is set, then to the receiver of the project. A report
// that could not be delivered because there is no connection is kept in the outbox (desktop) and sent later; the
// result says which of the two happened. The screenshot, if any, goes as a PNG. Nothing else from the machine is added:
// only the version, the system, the language and the name of the open section.
func (a *App) SubmitBugReport(req BugReportRequest) (BugReportResult, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return BugReportResult{}, err
	}
	s := a.bugReport
	r, user, err := a.reportFromRequest(req)
	if err != nil {
		return BugReportResult{}, err
	}
	chans, err := s.channels()
	if err != nil {
		return BugReportResult{}, err
	}
	if len(chans) == 0 && s.outbox == nil {
		return BugReportResult{}, errBugReportNotConfigured
	}
	id, err := bugreport.NewID()
	if err != nil {
		return BugReportResult{}, err
	}
	if err := s.begin(user); err != nil {
		return BugReportResult{}, err
	}
	ok := false
	defer func() { s.finish(user, ok) }()

	ctx, cancel := a.opCtx(s.deliveryTimeout(len(chans)))
	defer cancel()
	channel, retry, err := s.deliver(ctx, chans, id, r)
	switch {
	case err == nil:
		ok = true
		a.recordBugReport(len(r.Screenshot) > 0, channel)
		if s.outbox != nil && s.outbox.Count() > 0 { // there is a connection: let the waiting reports go too
			s.spawn(func() { a.flushBugReports(a.baseContext()) })
		}
		return BugReportResult{Status: bugReportSent, Channel: channel}, nil
	case s.outbox != nil && (retry || len(chans) == 0):
		if err := s.outbox.Add(id, r, s.now()); err != nil {
			return BugReportResult{}, err
		}
		ok = true
		a.recordBugReport(len(r.Screenshot) > 0, "queued")
		return BugReportResult{Status: bugReportQueued, Pending: s.outbox.Count()}, nil
	default:
		return BugReportResult{}, err
	}
}

// bugReportChannel is one way out of the machine.
type bugReportChannel struct {
	name string // "mail" or "relay"
	send func(ctx context.Context, id string, r bugreport.Report, now time.Time) error
}

// channels returns the ways to deliver a report, in the order they are tried. An invalid setting of one way is skipped
// while another way exists, and is reported only when nothing else is left: the user must be able to send a report
// even if a mail server was set wrongly.
func (s *bugReportService) channels() ([]bugReportChannel, error) {
	var out []bugReportChannel
	var bad error
	cfg, _, err := s.config()
	if err != nil {
		bad = err
	} else if cfg != nil {
		if n, err := cfg.Normalize(); err != nil {
			bad = err
		} else {
			out = append(out, bugReportChannel{"mail", func(ctx context.Context, _ string, r bugreport.Report, now time.Time) error {
				msg, err := bugreport.Build(n.From, n.To, r, now)
				if err != nil {
					return err
				}
				return s.send(ctx, n, msg)
			}})
		}
	}
	endpoint, err := bugreport.ResolveRelayURL(s.getenv(envBugReportRelay), s.relayDefault)
	if err != nil {
		bad = err
	} else if endpoint != "" {
		out = append(out, bugReportChannel{"relay", func(ctx context.Context, id string, r bugreport.Report, _ time.Time) error {
			return s.relay(ctx, endpoint, id, r)
		}})
	}
	if len(out) == 0 && bad != nil {
		return nil, bad
	}
	if bad != nil {
		slog.Warn("bug report: a delivery setting is not valid and is skipped", "reason", bad.Error())
	}
	return out, nil
}

// relayEndpoint is the address of the receiver, or "" when there is none.
func (s *bugReportService) relayEndpoint() string {
	u, err := bugreport.ResolveRelayURL(s.getenv(envBugReportRelay), s.relayDefault)
	if err != nil {
		return ""
	}
	return u
}

// deliveryTimeout is how long the user may wait: every way has its own limit.
func (s *bugReportService) deliveryTimeout(channels int) time.Duration {
	return time.Duration(max(channels, 1))*(bugreport.SendTimeout+5*time.Second) + 5*time.Second
}

// deliver tries the ways in turn. retry says that a failure was of the kind that goes away with a connection, so
// the report is worth keeping; a refusal of the receiver or a wrong password is not.
func (s *bugReportService) deliver(ctx context.Context, chans []bugReportChannel, id string, r bugreport.Report) (channel string, retry bool, err error) {
	if len(chans) == 0 { // nothing was tried, so nothing was delivered
		return "", false, errBugReportNotConfigured
	}
	for _, c := range chans {
		cerr := c.send(ctx, id, r, s.now())
		if cerr == nil {
			return c.name, false, nil
		}
		if err == nil {
			err = cerr
		}
		if errors.Is(cerr, bugreport.ErrRelayUnavailable) || errors.Is(cerr, bugreport.ErrConnect) {
			retry = true
		}
		if ctx.Err() != nil {
			break
		}
	}
	return "", retry, err
}

// auditBugReport — действие аудита «отчёт об ошибке отправлен» (подпись — audit.actions.bugreport.submit).
const auditBugReport = "bugreport.submit"

// recordBugReport пишет в журнал факт отправки и путь (mail, relay или queued). Текст отчёта, адреса и снимок не
// записываются.
func (a *App) recordBugReport(withScreenshot bool, channel string) {
	if a.audit == nil || a.auth == nil {
		return
	}
	actor := a.auth.Actor()
	detail := "channel=" + channel + " screenshot=no"
	if withScreenshot {
		detail = "channel=" + channel + " screenshot=yes"
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
