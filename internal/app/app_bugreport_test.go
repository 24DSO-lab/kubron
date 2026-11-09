// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
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
	if err := a.SubmitBugReport(BugReportRequest{Description: "x"}); !errors.Is(err, errBugReportNotConfigured) {
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
	if err := a.SubmitBugReport(BugReportRequest{Description: "grid is empty", Screenshot: shotB64(t), View: "pods", Locale: "en"}); err != nil {
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
		if err := a.SubmitBugReport(c.req); !errors.Is(err, c.want) {
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
	if err := a.SubmitBugReport(req); err != nil {
		t.Fatal(err)
	}
	if err := a.SubmitBugReport(req); !errors.Is(err, errBugReportTooOften) {
		t.Fatalf("got %v", err)
	}
	now = now.Add(bugReportMinInterval)
	if err := a.SubmitBugReport(req); err != nil {
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
		if err := a.SubmitBugReport(BugReportRequest{Description: "x"}); !errors.Is(err, bugreport.ErrConnect) {
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
	if err := a.SubmitBugReport(BugReportRequest{Description: "grid is empty", View: "pods"}); err != nil {
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

// draftShell is a desktop platform that records what the core asks of it.
type draftShell struct {
	nopPlatform
	opened   []string
	revealed []string
}

func (d *draftShell) Capabilities() Capabilities { return Capabilities{BugReport: true} }
func (d *draftShell) OpenURL(u string)           { d.opened = append(d.opened, u) }
func (d *draftShell) RevealFile(p string) error  { d.revealed = append(d.revealed, p); return nil }

func draftApp(t *testing.T) (*App, *draftShell, string) {
	t.Helper()
	sh := &draftShell{}
	a := NewApp(sh)
	a.initAuth(t.TempDir())
	if _, err := a.SetupAdmin("admin", "correct-horse-battery-1"); err != nil {
		t.Fatal(err)
	}
	a.bugReport.getenv = func(string) string { return "" }
	dir := filepath.Join(t.TempDir(), "bugreports")
	SetBugReportDraftDir(a, dir)
	return a, sh, dir
}

func TestOpenBugReportDraftOpensTheMailProgramAndSavesTheScreenshot(t *testing.T) {
	a, sh, dir := draftApp(t)
	shot := shotB64(t)
	d, err := a.OpenBugReportDraft(BugReportRequest{Description: "grid is empty", Screenshot: shot, View: "pods", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Truncated || d.TextPath != "" || filepath.Dir(d.ScreenshotPath) != dir {
		t.Fatalf("draft: %+v", d)
	}
	got, err := os.ReadFile(d.ScreenshotPath)
	want, _ := base64.StdEncoding.DecodeString(shot)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("the saved file differs: %v", err)
	}
	if fi, _ := os.Stat(d.ScreenshotPath); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	if len(sh.opened) != 1 || !strings.HasPrefix(sh.opened[0], "mailto:"+bugreport.DefaultRecipient+"?subject=") ||
		!strings.Contains(sh.opened[0], "grid%20is%20empty") {
		t.Fatalf("opened %q", sh.opened)
	}
}

func TestOpenBugReportDraftLongTextGoesToAFile(t *testing.T) {
	a, sh, _ := draftApp(t)
	long := strings.Repeat("Ошибка в таблице. ", 200)
	d, err := a.OpenBugReportDraft(BugReportRequest{Description: long})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Truncated || d.ScreenshotPath != "" {
		t.Fatalf("draft: %+v", d)
	}
	txt, err := os.ReadFile(d.TextPath)
	if err != nil || !strings.Contains(string(txt), strings.TrimSpace(long)) {
		t.Fatalf("the file must hold the full text: %v", err)
	}
	if len(sh.opened[0]) > bugreport.MaxMailtoLen {
		t.Fatalf("link is %d characters", len(sh.opened[0]))
	}
}

func TestOpenBugReportDraftRefusesWhatIsNotReady(t *testing.T) {
	a, sh, dir := draftApp(t)
	if _, err := a.OpenBugReportDraft(BugReportRequest{}); !errors.Is(err, bugreport.ErrDescriptionRequired) {
		t.Fatalf("got %v", err)
	}
	if _, err := a.OpenBugReportDraft(BugReportRequest{Description: "x", Screenshot: "***"}); !errors.Is(err, bugreport.ErrScreenshotInvalid) {
		t.Fatalf("got %v", err)
	}
	if len(sh.opened) != 0 {
		t.Fatal("nothing may be opened for an invalid report")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("nothing may be saved for an invalid report")
	}
	SetBugReportDraftDir(a, "") // the server
	if _, err := a.OpenBugReportDraft(BugReportRequest{Description: "x"}); !errors.Is(err, errBugReportDraftUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestRevealBugReportFileOnlyShowsItsOwnFiles(t *testing.T) {
	a, sh, dir := draftApp(t)
	d, err := a.OpenBugReportDraft(BugReportRequest{Description: "x", Screenshot: shotB64(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RevealBugReportFile(d.ScreenshotPath); err != nil || len(sh.revealed) != 1 {
		t.Fatalf("got %v, revealed %v", err, sh.revealed)
	}
	outside := filepath.Join(t.TempDir(), draftPrefix+"x.png")
	_ = os.WriteFile(outside, []byte("x"), 0o600)
	bad := []string{
		outside,                                    // another folder
		filepath.Join(dir, "..", "secrets.json"),   // traversal
		filepath.Join(dir, draftPrefix+"x.exe"),    // wrong type
		filepath.Join(dir, "other.png"),            // not ours
		filepath.Join(dir, draftPrefix+"gone.png"), // not there
		dir,
	}
	for _, p := range bad {
		if err := a.RevealBugReportFile(p); !errors.Is(err, errBugReportFileUnknown) {
			t.Errorf("%q: got %v", p, err)
		}
	}
	if len(sh.revealed) != 1 {
		t.Fatalf("revealed %v", sh.revealed)
	}
}

func TestDraftFolderStaysSmall(t *testing.T) {
	a, _, dir := draftApp(t)
	_ = os.MkdirAll(dir, 0o700)
	old := time.Now().Add(-8 * 24 * time.Hour)
	for i := 0; i < draftMaxFiles+5; i++ {
		p := filepath.Join(dir, draftPrefix+"old"+string(rune('a'+i))+".png")
		_ = os.WriteFile(p, []byte("x"), 0o600)
		_ = os.Chtimes(p, old, old)
	}
	keep := filepath.Join(dir, "notes.txt") // not a file of a report: never touched
	_ = os.WriteFile(keep, []byte("x"), 0o600)
	if _, err := a.OpenBugReportDraft(BugReportRequest{Description: "x", Screenshot: shotB64(t)}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 { // the new screenshot and notes.txt
		t.Fatalf("%d files left", len(entries))
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("a foreign file was removed")
	}
}

func TestDraftWayShowsTheButtonWithoutAMailServer(t *testing.T) {
	a, _, _ := draftApp(t)
	if !a.GetCapabilities().BugReport {
		t.Fatal("the desktop must show the button before any mail server is set")
	}
	if st, _ := a.GetBugReportStatus(); !st.CanDraft || st.Configured {
		t.Fatalf("status: %+v", st)
	}
}
