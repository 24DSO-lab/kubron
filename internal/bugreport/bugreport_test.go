// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package bugreport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestNormalize(t *testing.T) {
	big := strings.Repeat("a", MaxDescriptionRunes+1)
	cases := []struct {
		name string
		r    Report
		want error
	}{
		{"ok", Report{Description: " broken table "}, nil},
		{"empty", Report{Description: "  \n\t"}, ErrDescriptionRequired},
		{"only control chars", Report{Description: "\x00\x01"}, ErrDescriptionRequired},
		{"too long", Report{Description: big}, ErrDescriptionTooLong},
		{"bad contact", Report{Description: "x", Contact: "not an address"}, ErrContactInvalid},
		{"header injection in contact", Report{Description: "x", Contact: "a@b.c\r\nBcc: e@f.g"}, ErrContactInvalid},
		{"not a png", Report{Description: "x", Screenshot: []byte("GIF89a....")}, ErrScreenshotInvalid},
		{"truncated png", Report{Description: "x", Screenshot: append([]byte{}, pngSignature...)}, ErrScreenshotInvalid},
		{"too large", Report{Description: "x", Screenshot: append(append([]byte{}, pngSignature...), make([]byte, MaxScreenshotBytes)...)}, ErrScreenshotTooLarge},
		{"good png", Report{Description: "x", Contact: "Ann <ann@example.org>", Screenshot: pngBytes(t, 4, 4)}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := c.r
			if err := r.Normalize(); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
	r := Report{Description: "x", Contact: "Ann <ann@example.org>"}
	_ = r.Normalize()
	if r.Contact != "ann@example.org" {
		t.Fatalf("contact = %q", r.Contact)
	}
}

func TestNormalizeCleansDiagnostics(t *testing.T) {
	r := Report{Description: "x", Diagnostics: Diagnostics{View: "pods\r\nBcc: x@y.z", Locale: strings.Repeat("é", 200)}}
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(r.Diagnostics.View, "\r\n") || len([]rune(r.Diagnostics.Locale)) != maxFieldRunes {
		t.Fatalf("diagnostics not cleaned: %+v", r.Diagnostics)
	}
}

func TestBuildMessage(t *testing.T) {
	shot := pngBytes(t, 300, 200)
	r := Report{
		Description: "Таблица подов пустая\nпосле смены namespace",
		Contact:     "ann@example.org",
		Screenshot:  shot,
		Diagnostics: Diagnostics{AppVersion: "1.2.0", Shell: "desktop", OS: "linux", Arch: "amd64", Locale: "ru", View: "pods"},
	}
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	raw, err := Build("sender@example.com", "224vadim224@gmail.com", r, time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := msg.Header.Get("To"); !strings.Contains(got, "224vadim224@gmail.com") {
		t.Fatalf("To = %q", got)
	}
	if got := msg.Header.Get("Reply-To"); !strings.Contains(got, "ann@example.org") {
		t.Fatalf("Reply-To = %q", got)
	}
	subj, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subj != "[Kubron bug report] Таблица подов пустая" {
		t.Fatalf("Subject = %q (%v)", subj, err)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("content type %q %v", mt, err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	p1, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	text, _ := io.ReadAll(quotedprintable.NewReader(p1))
	for _, want := range []string{"Таблица подов пустая\nпосле смены namespace", "Contact: ann@example.org", "Open section: pods", "System: linux/amd64", "attached (screenshot.png"} {
		if !strings.Contains(strings.ReplaceAll(string(text), "\r\n", "\n"), want) {
			t.Errorf("body lacks %q:\n%s", want, text)
		}
	}
	p2, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if p2.FileName() != "screenshot.png" {
		t.Fatalf("file name %q", p2.FileName())
	}
	enc, _ := io.ReadAll(p2)
	got, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\r", "", "\n", "").Replace(string(enc)))
	if err != nil || !bytes.Equal(got, shot) {
		t.Fatalf("attachment differs: %v", err)
	}
}

func TestBuildWithoutScreenshotHasOnePart(t *testing.T) {
	r := Report{Description: "x"}
	_ = r.Normalize()
	raw, err := Build("a@b.c", "d@e.f", r, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("image/png")) {
		t.Fatal("unexpected attachment")
	}
}

func TestSubjectIsOneLine(t *testing.T) {
	s := subject("first\r\nBcc: evil@x.y\nsecond")
	if strings.ContainsAny(s, "\r\n") {
		t.Fatalf("subject = %q", s)
	}
	if got := subject(strings.Repeat("я", 500)); len([]rune(got)) != len([]rune(subjectPrefix))+70 {
		t.Fatalf("subject is not capped: %d", len([]rune(got)))
	}
}

func TestNormalizeConfig(t *testing.T) {
	c, err := SMTPConfig{Host: "smtp.gmail.com", Username: "me@gmail.com", Password: "p"}.Normalize()
	if err != nil || c.Port != 587 || c.Security != SecurityStartTLS || c.From != "me@gmail.com" || c.To != DefaultRecipient {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	if c, _ := (SMTPConfig{Host: "h.example", Port: 465, From: "a@b.c"}).Normalize(); c.Security != SecurityTLS {
		t.Fatalf("465 must default to TLS: %+v", c)
	}
	bad := []SMTPConfig{
		{},
		{Host: "smtp.example.com", From: "a@b.c", Security: SecurityNone}, // clear text to a remote host
		{Host: "bad host", From: "a@b.c"},
		{Host: "smtp.example.com", Port: 70000, From: "a@b.c"},
		{Host: "smtp.example.com", Username: "u", From: "a@b.c"}, // user without password
		{Host: "smtp.example.com", From: "nope"},
		{Host: "smtp.example.com", From: "a@b.c", Security: "weird"},
	}
	for i, b := range bad {
		if _, err := b.Normalize(); !errors.Is(err, ErrSettingsInvalid) {
			t.Errorf("case %d: got %v", i, err)
		}
	}
}

// fakeSMTP is a minimal server: EHLO, AUTH PLAIN, MAIL, RCPT, DATA, QUIT.
type fakeSMTP struct {
	ln       net.Listener
	mu       sync.Mutex
	data     string
	authLine string
	rejectTo bool
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		rd := bufio.NewReader(conn)
		say := func(s string) { conn.Write([]byte(s + "\r\n")) }
		say("220 fake ESMTP")
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(line, "EHLO"):
				say("250-fake\r\n250 AUTH PLAIN")
			case strings.HasPrefix(line, "AUTH PLAIN"):
				f.mu.Lock()
				f.authLine = line
				f.mu.Unlock()
				say("235 ok")
			case strings.HasPrefix(line, "MAIL FROM"):
				say("250 ok")
			case strings.HasPrefix(line, "RCPT TO"):
				if f.rejectTo {
					say("550 no such user")
				} else {
					say("250 ok")
				}
			case line == "DATA":
				say("354 go")
				var b strings.Builder
				for {
					l, err := rd.ReadString('\n')
					if err != nil || l == ".\r\n" {
						break
					}
					b.WriteString(l)
				}
				f.mu.Lock()
				f.data = b.String()
				f.mu.Unlock()
				say("250 queued")
			case line == "QUIT":
				say("221 bye")
				return
			default:
				say("500 what")
			}
		}
	}()
	return f
}

func (f *fakeSMTP) config() SMTPConfig {
	addr := f.ln.Addr().(*net.TCPAddr)
	return SMTPConfig{Host: "127.0.0.1", Port: addr.Port, Username: "u", Password: "secret", From: "sender@example.com", Security: SecurityNone}
}

func TestSendDeliversMessage(t *testing.T) {
	f := newFakeSMTP(t)
	r := Report{Description: "it broke", Screenshot: pngBytes(t, 8, 8)}
	_ = r.Normalize()
	raw, err := Build("sender@example.com", DefaultRecipient, r, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := Send(context.Background(), f.config(), raw); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.Contains(f.data, "Subject: [Kubron bug report] it broke") || !strings.Contains(f.data, "image/png") {
		t.Fatalf("server got:\n%s", f.data)
	}
	if f.authLine == "" {
		t.Fatal("no sign-in was made")
	}
}

func TestSendMapsErrors(t *testing.T) {
	f := newFakeSMTP(t)
	f.rejectTo = true
	if err := Send(context.Background(), f.config(), []byte("x")); !errors.Is(err, ErrRejected) {
		t.Fatalf("got %v", err)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cfg := SMTPConfig{Host: "127.0.0.1", Port: port, From: "a@b.c", Security: SecurityNone}
	if err := Send(context.Background(), cfg, []byte("x")); !errors.Is(err, ErrConnect) {
		t.Fatalf("got %v", err)
	}
	if err := Send(context.Background(), SMTPConfig{}, []byte("x")); !errors.Is(err, ErrSettingsInvalid) {
		t.Fatalf("got %v", err)
	}
}
