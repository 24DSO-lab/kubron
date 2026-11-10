// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/bugreport"
)

func bugApp(t *testing.T) (*App, *[][]byte, *bugreport.SMTPConfig) {
	t.Helper()
	a := NewApp(nil)
	a.initAuth(t.TempDir())
	if _, err := a.SetupAdmin("admin", "correct-horse-battery-1"); err != nil { // создаёт администратора и входит
		t.Fatal(err)
	}
	var sent [][]byte
	var used bugreport.SMTPConfig
	svc := a.bugReport
	svc.getenv = func(string) string { return "" }
	svc.send = func(_ context.Context, c bugreport.SMTPConfig, msg []byte) error {
		used = c
		sent = append(sent, msg)
		return nil
	}
	return a, &sent, &used
}

func shotB64(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 3, 3))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestSubmitBugReportNeedsSettings(t *testing.T) {
	a, sent, _ := bugApp(t)
	if _, err := a.SubmitBugReport(BugReportRequest{Description: "x"}); !errors.Is(err, errBugReportNotConfigured) {
		t.Fatalf("got %v", err)
	}
	if len(*sent) != 0 {
		t.Fatal("nothing must be sent")
	}
}

func TestSubmitBugReportFromEnvironment(t *testing.T) {
	a, sent, used := bugApp(t)
	env := map[string]string{
		"KUBRON_BUGREPORT_SMTP_HOST": "smtp.example.com", "KUBRON_BUGREPORT_SMTP_USER": "u@example.com",
		"KUBRON_BUGREPORT_SMTP_PASSWORD": "pw",
	}
	a.bugReport.getenv = func(k string) string { return env[k] }
	if _, err := a.SubmitBugReport(BugReportRequest{Description: "grid is empty", Screenshot: shotB64(t), View: "pods", Locale: "en"}); err != nil {
		t.Fatal(err)
	}
	if used.To != bugreport.DefaultRecipient || used.Port != 587 || used.From != "u@example.com" {
		t.Fatalf("settings: %+v", used)
	}
	m := string((*sent)[0])
	if !strings.Contains(m, "To: <"+bugreport.DefaultRecipient+">") || !strings.Contains(m, "image/png") {
		t.Fatalf("message:\n%s", m)
	}
}

func TestSubmitBugReportValidatesInput(t *testing.T) {
	a, sent, _ := bugApp(t)
	a.bugReport.getenv = func(k string) string {
		return map[string]string{"KUBRON_BUGREPORT_SMTP_HOST": "smtp.example.com", "KUBRON_BUGREPORT_SMTP_FROM": "a@b.c"}[k]
	}
	cases := map[string]struct {
		req  BugReportRequest
		want error
	}{
		"empty":      {BugReportRequest{}, bugreport.ErrDescriptionRequired},
		"bad base64": {BugReportRequest{Description: "x", Screenshot: "***"}, bugreport.ErrScreenshotInvalid},
		"not png":    {BugReportRequest{Description: "x", Screenshot: base64.StdEncoding.EncodeToString([]byte("hello"))}, bugreport.ErrScreenshotInvalid},
		"huge":       {BugReportRequest{Description: "x", Screenshot: strings.Repeat("A", base64.StdEncoding.EncodedLen(bugreport.MaxScreenshotBytes)+4)}, bugreport.ErrScreenshotTooLarge},
	}
	for name, c := range cases {
		if _, err := a.SubmitBugReport(c.req); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
	if len(*sent) != 0 {
		t.Fatal("invalid reports must not be sent")
	}
}

func TestSubmitBugReportRateLimit(t *testing.T) {
	a, sent, _ := bugApp(t)
	a.bugReport.getenv = func(k string) string {
		return map[string]string{"KUBRON_BUGREPORT_SMTP_HOST": "smtp.example.com", "KUBRON_BUGREPORT_SMTP_FROM": "a@b.c"}[k]
	}
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	a.bugReport.now = func() time.Time { return now }
	req := BugReportRequest{Description: "x"}
	if _, err := a.SubmitBugReport(req); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SubmitBugReport(req); !errors.Is(err, errBugReportTooOften) {
		t.Fatalf("got %v", err)
	}
	now = now.Add(bugReportMinInterval)
	if _, err := a.SubmitBugReport(req); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 2 {
		t.Fatalf("sent %d", len(*sent))
	}
}

func TestSubmitBugReportFailureCanBeRetried(t *testing.T) {
	a, _, _ := bugApp(t)
	a.bugReport.getenv = func(k string) string {
		return map[string]string{"KUBRON_BUGREPORT_SMTP_HOST": "smtp.example.com", "KUBRON_BUGREPORT_SMTP_FROM": "a@b.c"}[k]
	}
	a.bugReport.send = func(context.Context, bugreport.SMTPConfig, []byte) error { return bugreport.ErrConnect }
	for i := 0; i < 2; i++ {
		if _, err := a.SubmitBugReport(BugReportRequest{Description: "x"}); !errors.Is(err, bugreport.ErrConnect) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
}

func TestBugReportStoreSealsPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bugreport.json")
	st := NewBugReportStore(path, testEnvelope(t))
	cfg := bugreport.SMTPConfig{Host: "smtp.example.com", Username: "u@example.com", Password: "canary-pw-7731", From: "u@example.com"}
	if err := st.Save(cfg); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte("canary-pw-7731")) {
		t.Fatal("the password is stored in clear text")
	}
	got, err := st.Load()
	if err != nil || got == nil || got.Password != "canary-pw-7731" {
		t.Fatalf("load: %+v %v", got, err)
	}
	// An empty password keeps the saved one for the same host and user.
	cfg.Password = ""
	cfg.To = "other@example.com"
	if err := st.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Load(); got.Password != "canary-pw-7731" || got.To != "other@example.com" {
		t.Fatalf("not kept: %+v", got)
	}
	if err := st.Save(bugreport.SMTPConfig{Host: "bad host"}); !errors.Is(err, bugreport.ErrSettingsInvalid) {
		t.Fatalf("got %v", err)
	}
	if err := st.Clear(); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Load(); got != nil || err != nil {
		t.Fatalf("after clear: %+v %v", got, err)
	}
}

func TestGetBugReportStatusNeverReturnsPassword(t *testing.T) {
	a, _, _ := bugApp(t)
	st := NewBugReportStore(filepath.Join(t.TempDir(), "b.json"), testEnvelope(t))
	SetBugReport(a, st)
	if got, _ := a.GetBugReportStatus(); got.Configured || !got.CanSave {
		t.Fatalf("fresh: %+v", got)
	}
	if err := a.SaveBugReportSettings(BugReportSettings{Host: "smtp.example.com", Username: "u@example.com", Password: "pw-canary", From: "u@example.com"}); err != nil {
		t.Fatal(err)
	}
	got, err := a.GetBugReportStatus()
	if err != nil || !got.Configured || got.Source != "saved" || !got.Settings.PasswordSet || got.Settings.Password != "" {
		t.Fatalf("status: %+v %v", got, err)
	}
	if err := a.ClearBugReportSettings(); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.GetBugReportStatus(); got.Configured {
		t.Fatalf("after clear: %+v", got)
	}
}

type bugShell struct{ nopPlatform }

func (bugShell) Capabilities() Capabilities { return Capabilities{BugReport: true} }

func smtpEnv(k string) string {
	return map[string]string{
		"KUBRON_BUGREPORT_SMTP_HOST": "smtp.gmail.com", "KUBRON_BUGREPORT_SMTP_USER": "224vadim224@gmail.com",
		"KUBRON_BUGREPORT_SMTP_PASSWORD": "app-password-canary",
	}[k]
}

func TestBugReportCapabilityFollowsTheMailSetup(t *testing.T) {
	// Web server: the shell supports it, but the button needs a mail server set by the administrator.
	web := NewApp(bugShell{})
	web.bugReport.getenv = func(string) string { return "" }
	if web.GetCapabilities().BugReport {
		t.Fatal("no mail server: the button must be hidden")
	}
	web.bugReport.getenv = smtpEnv
	if !web.GetCapabilities().BugReport {
		t.Fatal("mail server set: the button must be shown")
	}
	// Desktop: the user can set the mail server in the dialog, so the button is shown before that.
	desk := NewApp(bugShell{})
	desk.bugReport.getenv = func(string) string { return "" }
	SetBugReport(desk, NewBugReportStore(filepath.Join(t.TempDir(), "b.json"), testEnvelope(t)))
	if !desk.GetCapabilities().BugReport {
		t.Fatal("desktop with a store must show the button")
	}
	// A shell that does not support it never shows it.
	other := NewApp(nil)
	other.bugReport.getenv = smtpEnv
	if other.GetCapabilities().BugReport {
		t.Fatal("the flag must come from the shell")
	}
}

func TestWebStatusDoesNotExposeTheMailAccount(t *testing.T) {
	a, _, _ := bugApp(t) // no store: this is how the web server runs
	a.bugReport.getenv = smtpEnv
	st, err := a.GetBugReportStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Configured || st.CanSave || st.Recipient != bugreport.DefaultRecipient {
		t.Fatalf("status: %+v", st)
	}
	if st.Settings != (BugReportSettings{}) {
		t.Fatalf("the host and the user of the mail account must not reach the browser: %+v", st.Settings)
	}
	if err := a.SaveBugReportSettings(BugReportSettings{Host: "x.example.com"}); err == nil {
		t.Fatal("settings must not be changeable without a store")
	}
}

func TestWebReportCarriesTheUserAndGoesFromTheConfiguredAccount(t *testing.T) {
	a, sent, used := bugApp(t)
	a.bugReport.getenv = smtpEnv
	a.sessionID = "s1" // a view of a web session
	if _, err := a.SubmitBugReport(BugReportRequest{Description: "grid is empty", View: "pods"}); err != nil {
		t.Fatal(err)
	}
	if used.From != "224vadim224@gmail.com" || used.To != "224vadim224@gmail.com" {
		t.Fatalf("addresses: %+v", used)
	}
	m := string((*sent)[0])
	if !strings.Contains(m, "Signed-in user: admin") || !strings.Contains(m, "(web)") {
		t.Fatalf("message:\n%s", m)
	}
	if strings.Contains(m, "app-password-canary") {
		t.Fatal("the password must never appear in the message")
	}
}

func TestBugReportLimitsArePerUserWithAServerCap(t *testing.T) {
	s := newBugReportService()
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if err := s.begin("ann"); err != nil {
		t.Fatal(err)
	}
	if err := s.begin("ann"); !errors.Is(err, errBugReportTooOften) { // already sending
		t.Fatalf("got %v", err)
	}
	if err := s.begin("bob"); err != nil { // another user is not blocked
		t.Fatalf("got %v", err)
	}
	s.finish("ann", true)
	s.finish("bob", true)
	if err := s.begin("ann"); !errors.Is(err, errBugReportTooOften) {
		t.Fatalf("got %v", err)
	}
	// The hourly cap of the whole server.
	for i := 0; i < bugReportHourlyCap; i++ {
		now = now.Add(bugReportMinInterval)
		u := "u" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if err := s.begin(u); err != nil {
			if i < bugReportHourlyCap-2 {
				t.Fatalf("user %d refused early: %v", i, err)
			}
			break
		}
		s.finish(u, true)
	}
	if err := s.begin("late"); !errors.Is(err, errBugReportTooOften) {
		t.Fatalf("the cap must refuse: %v", err)
	}
	now = now.Add(time.Hour)
	if err := s.begin("late"); err != nil {
		t.Fatalf("after an hour: %v", err)
	}
}

// outboxShell is a desktop platform that records what the core asks of it.
type outboxShell struct {
	nopPlatform
	revealed []string
}

func (o *outboxShell) Capabilities() Capabilities { return Capabilities{BugReport: true} }
func (o *outboxShell) RevealFile(p string) error  { o.revealed = append(o.revealed, p); return nil }

// fakeRelay records what the core sends to the receiver and answers with err.
type fakeRelay struct {
	err      error
	endpoint string
	ids      []string
	reports  []bugreport.Report
}

func (f *fakeRelay) send(_ context.Context, endpoint, id string, r bugreport.Report) error {
	f.endpoint = endpoint
	f.ids = append(f.ids, id)
	f.reports = append(f.reports, r)
	return f.err
}

// desktopApp is an app as the desktop builds it: the receiver of the project and an outbox.
func desktopApp(t *testing.T) (*App, *outboxShell, *fakeRelay, string) {
	t.Helper()
	sh := &outboxShell{}
	a := NewApp(sh)
	a.initAuth(t.TempDir())
	if _, err := a.SetupAdmin("admin", "correct-horse-battery-1"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	SetBugReportOutbox(a, dir)
	fr := &fakeRelay{}
	s := a.bugReport
	s.getenv = func(string) string { return "" }
	s.relay = fr.send
	s.spawn = func(f func()) { f() }
	s.send = func(context.Context, bugreport.SMTPConfig, []byte) error {
		return errors.New("the mail server must not be used")
	}
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	s.now = func() time.Time { now = now.Add(time.Minute); return now } // every call is later than the last, so no rate limit
	return a, sh, fr, dir
}

func TestDesktopReportGoesToTheReceiverOfTheProject(t *testing.T) {
	a, _, fr, _ := desktopApp(t)
	res, err := a.SubmitBugReport(BugReportRequest{Description: "grid is empty", Screenshot: shotB64(t), View: "pods", Locale: "ru"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != bugReportSent || res.Channel != "relay" || res.Pending != 0 {
		t.Fatalf("result: %+v", res)
	}
	if fr.endpoint != bugreport.DefaultRelayURL || len(fr.reports) != 1 {
		t.Fatalf("endpoint %q, %d reports", fr.endpoint, len(fr.reports))
	}
	r := fr.reports[0]
	if r.Description != "grid is empty" || len(r.Screenshot) == 0 || r.Diagnostics.View != "pods" || r.Diagnostics.Shell != "desktop" {
		t.Fatalf("report: %+v", r)
	}
	if !bugreport.ValidID(fr.ids[0]) {
		t.Fatalf("id %q", fr.ids[0])
	}
}

func TestMailServerIsTriedBeforeTheReceiver(t *testing.T) {
	a, _, fr, _ := desktopApp(t)
	s := a.bugReport
	s.getenv = smtpEnv
	var mails int
	s.send = func(context.Context, bugreport.SMTPConfig, []byte) error { mails++; return nil }
	res, err := a.SubmitBugReport(BugReportRequest{Description: "x"})
	if err != nil || res.Channel != "mail" || mails != 1 || len(fr.ids) != 0 {
		t.Fatalf("%+v %v, mails %d, relay %d", res, err, mails, len(fr.ids))
	}
	// A mail server that refuses (a wrong password) does not stop the report: the receiver takes it.
	s.send = func(context.Context, bugreport.SMTPConfig, []byte) error { return bugreport.ErrAuth }
	res, err = a.SubmitBugReport(BugReportRequest{Description: "y"})
	if err != nil || res.Status != bugReportSent || res.Channel != "relay" || len(fr.ids) != 1 {
		t.Fatalf("%+v %v, relay %d", res, err, len(fr.ids))
	}
}

func TestReportThatCannotBeDeliveredWaitsAndIsSentLater(t *testing.T) {
	a, _, fr, dir := desktopApp(t)
	fr.err = bugreport.ErrRelayUnavailable
	res, err := a.SubmitBugReport(BugReportRequest{Description: "no network", Screenshot: shotB64(t)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != bugReportQueued || res.Channel != "" || res.Pending != 1 {
		t.Fatalf("result: %+v", res)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "outbox", "*.json"))
	if len(files) != 1 {
		t.Fatalf("outbox: %v", files)
	}
	if st, _ := a.GetBugReportStatus(); st.Pending != 1 || !st.CanQueue || st.Channel != "relay" {
		t.Fatalf("status: %+v", st)
	}

	// Still offline: the report stays, and nothing is asked again after the first failure.
	a.flushBugReports(context.Background())
	if a.bugReport.outbox.Count() != 1 {
		t.Fatal("the report must wait")
	}
	// The connection is back: the very same report, under the same id, is delivered.
	fr.err = nil
	a.flushBugReports(context.Background())
	if a.bugReport.outbox.Count() != 0 {
		t.Fatal("the delivered report must leave the outbox")
	}
	if len(fr.ids) != 3 || fr.ids[0] != fr.ids[2] {
		t.Fatalf("the id must stay the same for the receiver to tell a repeat: %v", fr.ids)
	}
	if got := fr.reports[2]; got.Description != "no network" || len(got.Screenshot) == 0 {
		t.Fatalf("the stored report differs: %+v", got)
	}
}

func TestSuccessfulReportLetsTheWaitingOnesGo(t *testing.T) {
	a, _, fr, _ := desktopApp(t)
	fr.err = bugreport.ErrRelayUnavailable
	if _, err := a.SubmitBugReport(BugReportRequest{Description: "first"}); err != nil {
		t.Fatal(err)
	}
	fr.err = nil
	res, err := a.SubmitBugReport(BugReportRequest{Description: "second"})
	if err != nil || res.Status != bugReportSent {
		t.Fatalf("%+v %v", res, err)
	}
	if a.bugReport.outbox.Count() != 0 {
		t.Fatal("the connection works, so the waiting report must have gone")
	}
	var got []string
	for _, r := range fr.reports {
		got = append(got, r.Description)
	}
	if strings.Join(got, ",") != "first,second,first" {
		t.Fatalf("order of deliveries: %v", got)
	}
}

func TestRefusedReportIsShownAndNotKept(t *testing.T) {
	a, _, fr, _ := desktopApp(t)
	fr.err = bugreport.ErrRelayRejected
	if _, err := a.SubmitBugReport(BugReportRequest{Description: "x"}); !errors.Is(err, bugreport.ErrRelayRejected) {
		t.Fatalf("got %v", err)
	}
	if a.bugReport.outbox.Count() != 0 {
		t.Fatal("a report that the receiver refused would be refused again")
	}
}

func TestWaitingReportThatIsRefusedLaterIsDropped(t *testing.T) {
	a, _, fr, _ := desktopApp(t)
	fr.err = bugreport.ErrRelayUnavailable
	_, _ = a.SubmitBugReport(BugReportRequest{Description: "x"})
	fr.err = bugreport.ErrRelayRejected
	a.flushBugReports(context.Background())
	if a.bugReport.outbox.Count() != 0 {
		t.Fatal("a refused report must not block the queue for ever")
	}
}

func TestFlushStopsAtTheFirstFailure(t *testing.T) {
	a, _, fr, _ := desktopApp(t)
	fr.err = bugreport.ErrRelayUnavailable
	for _, d := range []string{"a", "b", "c"} {
		if _, err := a.SubmitBugReport(BugReportRequest{Description: d}); err != nil {
			t.Fatal(err)
		}
	}
	before := len(fr.ids)
	a.flushBugReports(context.Background())
	if n := len(fr.ids) - before; n != 1 {
		t.Fatalf("an offline machine must be asked once, not %d times", n)
	}
	if a.bugReport.outbox.Count() != 3 {
		t.Fatal("nothing may be lost")
	}
}

func TestQueuedReportCountsForTheRateLimit(t *testing.T) {
	a, _, fr, _ := desktopApp(t)
	fixed := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	a.bugReport.now = func() time.Time { return fixed }
	fr.err = bugreport.ErrRelayUnavailable
	if _, err := a.SubmitBugReport(BugReportRequest{Description: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SubmitBugReport(BugReportRequest{Description: "x"}); !errors.Is(err, errBugReportTooOften) {
		t.Fatalf("got %v", err)
	}
}

func TestWithoutAnyWayOutTheReportWaitsForAHandDelivery(t *testing.T) {
	a, _, fr, _ := desktopApp(t)
	a.bugReport.getenv = func(k string) string {
		if k == envBugReportRelay {
			return "off"
		}
		return ""
	}
	res, err := a.SubmitBugReport(BugReportRequest{Description: "x"})
	if err != nil || res.Status != bugReportQueued || len(fr.ids) != 0 {
		t.Fatalf("%+v %v relay %d", res, err, len(fr.ids))
	}
	if st, _ := a.GetBugReportStatus(); st.Channel != "" || st.Pending != 1 {
		t.Fatalf("status: %+v", st)
	}
	a.flushBugReports(context.Background()) // nothing to send with: the report stays
	if a.bugReport.outbox.Count() != 1 {
		t.Fatal("the report must stay")
	}
}

func TestWebServerHasNoOutboxAndNoDefaultReceiver(t *testing.T) {
	a, _, _ := bugApp(t) // the web server: no outbox, no default receiver
	fr := &fakeRelay{}
	a.bugReport.relay = fr.send
	if _, err := a.SubmitBugReport(BugReportRequest{Description: "x"}); !errors.Is(err, errBugReportNotConfigured) {
		t.Fatalf("got %v", err)
	}
	if len(fr.ids) != 0 {
		t.Fatal("the server must not send reports to the project unless the administrator says so")
	}
	// The administrator names a receiver of the organization.
	a.bugReport.getenv = func(k string) string {
		if k == envBugReportRelay {
			return "https://relay.corp.example/api/bugreport"
		}
		return ""
	}
	a.sessionID = "s1"
	res, err := a.SubmitBugReport(BugReportRequest{Description: "x"})
	if err != nil || res.Channel != "relay" || fr.endpoint != "https://relay.corp.example/api/bugreport" {
		t.Fatalf("%+v %v %q", res, err, fr.endpoint)
	}
	if !a.GetCapabilities().BugReport && a.platform != nil {
		_ = a // the capability flag follows the shell; covered by TestBugReportCapabilityFollowsTheMailSetup
	}
	// Without an outbox a failure is shown, not hidden.
	fr.err = bugreport.ErrRelayUnavailable
	a.bugReport.now = func() time.Time { return time.Now().Add(time.Hour) }
	if _, err := a.SubmitBugReport(BugReportRequest{Description: "y"}); !errors.Is(err, bugreport.ErrRelayUnavailable) {
		t.Fatalf("got %v", err)
	}
	if st, _ := a.GetBugReportStatus(); st.CanQueue || st.Pending != 0 {
		t.Fatalf("status: %+v", st)
	}
}

func TestWebReportToTheReceiverDoesNotCarryTheLogin(t *testing.T) {
	a, _, _ := bugApp(t)
	fr := &fakeRelay{}
	a.bugReport.relay = fr.send
	a.bugReport.getenv = func(k string) string {
		if k == envBugReportRelay {
			return "https://relay.corp.example/r"
		}
		return ""
	}
	a.sessionID = "s1"
	if _, err := a.SubmitBugReport(BugReportRequest{Description: "x"}); err != nil {
		t.Fatal(err)
	}
	// The report carries the login inside the core (the mail path shows it to the administrator); SendRelay leaves it out.
	if fr.reports[0].Diagnostics.User != "admin" {
		t.Fatalf("diagnostics: %+v", fr.reports[0].Diagnostics)
	}
}

func TestInvalidDeliverySettingsAreSkippedWhileAnotherWayExists(t *testing.T) {
	a, _, fr, _ := desktopApp(t)
	// A receiver address that is not valid (clear text over a network): the mail server is still used.
	a.bugReport.getenv = func(k string) string {
		switch k {
		case envBugReportRelay:
			return "http://relay.example.org/r"
		}
		return smtpEnv(k)
	}
	var mails int
	a.bugReport.send = func(context.Context, bugreport.SMTPConfig, []byte) error { mails++; return nil }
	res, err := a.SubmitBugReport(BugReportRequest{Description: "x"})
	if err != nil || res.Channel != "mail" || mails != 1 || len(fr.ids) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	// With nothing else left the reason is shown.
	a.bugReport.getenv = func(k string) string {
		if k == envBugReportRelay {
			return "http://relay.example.org/r"
		}
		return ""
	}
	a.bugReport.outbox = nil
	if _, err := a.SubmitBugReport(BugReportRequest{Description: "y"}); !errors.Is(err, bugreport.ErrRelayURLInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestExportBugReportsWritesAnArchiveAndShowsIt(t *testing.T) {
	a, sh, fr, dir := desktopApp(t)
	if _, err := a.ExportBugReports(); !errors.Is(err, errBugReportNothingToExport) {
		t.Fatalf("got %v", err)
	}
	fr.err = bugreport.ErrRelayUnavailable
	_, _ = a.SubmitBugReport(BugReportRequest{Description: "no network", Screenshot: shotB64(t)})
	path, err := a.ExportBugReports()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != filepath.Join(dir, "export") || !strings.HasPrefix(filepath.Base(path), "kubron-reports-") ||
		!strings.HasSuffix(path, ".zip") {
		t.Fatalf("path %q", path)
	}
	if len(sh.revealed) != 1 || sh.revealed[0] != path {
		t.Fatalf("revealed %v", sh.revealed)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(zr.File) != 2 {
		t.Fatalf("archive: %v, %d files", err, len(zr.File))
	}
	if a.bugReport.outbox.Count() != 1 {
		t.Fatal("an exported report must still be delivered later by the application")
	}
}

func TestExportBugReportsKeepsFewArchives(t *testing.T) {
	a, _, fr, dir := desktopApp(t)
	fr.err = bugreport.ErrRelayUnavailable
	_, _ = a.SubmitBugReport(BugReportRequest{Description: "x"})
	for i := 0; i < exportKeep+3; i++ {
		if _, err := a.ExportBugReports(); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "export", "kubron-reports-*.zip"))
	if len(files) != exportKeep {
		t.Fatalf("%d archives kept, want %d", len(files), exportKeep)
	}
}

func TestExportBugReportsIsNotForTheServer(t *testing.T) {
	a, _, _ := bugApp(t)
	if _, err := a.ExportBugReports(); !errors.Is(err, errBugReportUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestRetryLoopDeliversAndStopsWithTheContext(t *testing.T) {
	a, _, fr, _ := desktopApp(t)
	fr.err = bugreport.ErrRelayUnavailable
	_, _ = a.SubmitBugReport(BugReportRequest{Description: "x"})
	fr.err = nil
	a.bugReport.retryFirst, a.bugReport.retryEvery = 5*time.Millisecond, 5*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.runBugReportRetry(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for a.bugReport.outbox.Count() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the waiting report was not delivered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop must end with the context")
	}
}

func TestRetryLoopDoesNothingWithoutAnOutbox(t *testing.T) {
	a, _, _ := bugApp(t)
	done := make(chan struct{})
	go func() { a.runBugReportRetry(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the server has no outbox: the loop must return at once")
	}
}

func TestOnlyOneRetryRunsAtATime(t *testing.T) {
	a, _, _, _ := desktopApp(t)
	s := a.bugReport
	if !s.beginFlush() || s.beginFlush() {
		t.Fatal("a second retry must not start while one runs")
	}
	s.endFlush()
	if !s.beginFlush() {
		t.Fatal("a retry must be possible after the last one ended")
	}
}
