// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package bugreport

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
	"unicode/utf8"
)

const subjectPrefix = "[Kubron bug report] "

// Build returns the RFC 5322 message for a normalized report. from and to are plain addresses.
func Build(from, to string, r Report, now time.Time) ([]byte, error) {
	fromAddr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, ErrSettingsInvalid
	}
	toAddr, err := mail.ParseAddress(to)
	if err != nil {
		return nil, ErrSettingsInvalid
	}
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	domain := "kubron.local"
	if i := strings.LastIndexByte(fromAddr.Address, '@'); i >= 0 && i+1 < len(fromAddr.Address) {
		domain = fromAddr.Address[i+1:]
	}

	var buf bytes.Buffer
	mp := multipart.NewWriter(&buf)
	// The boundary is random and made of safe characters; the headers are written before the body.
	var head bytes.Buffer
	h := func(k, v string) { head.WriteString(k + ": " + v + "\r\n") }
	h("From", fromAddr.String())
	h("To", toAddr.String())
	if r.Contact != "" {
		h("Reply-To", (&mail.Address{Address: r.Contact}).String())
	}
	h("Subject", mime.QEncoding.Encode("utf-8", subject(r.Description)))
	h("Date", now.Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id[:])+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("X-Mailer", "Kubron")
	h("Content-Type", "multipart/mixed; boundary="+mp.Boundary())
	head.WriteString("\r\n")

	text, err := mp.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=utf-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return nil, err
	}
	qp := quotedprintable.NewWriter(text)
	if _, err := qp.Write([]byte(PlainBody(r, now))); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}

	if len(r.Screenshot) > 0 {
		att, err := mp.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {`image/png; name="screenshot.png"`},
			"Content-Transfer-Encoding": {"base64"},
			"Content-Disposition":       {`attachment; filename="screenshot.png"`},
		})
		if err != nil {
			return nil, err
		}
		enc := base64.StdEncoding.EncodeToString(r.Screenshot)
		for len(enc) > 76 {
			if _, err := att.Write([]byte(enc[:76] + "\r\n")); err != nil {
				return nil, err
			}
			enc = enc[76:]
		}
		if _, err := att.Write([]byte(enc + "\r\n")); err != nil {
			return nil, err
		}
	}
	if err := mp.Close(); err != nil {
		return nil, err
	}
	return append(head.Bytes(), buf.Bytes()...), nil
}

// subject takes the first line of the description; the result is a single line.
func subject(description string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(description), "\n")
	line = oneLine(line, 70)
	if line == "" {
		line = "(no title)"
	}
	return subjectPrefix + line
}

// PlainBody is the full text of the report: the description and the facts. It is the body of the e-mail and the
// content of the text file saved when the description does not fit into a mailto link.
func PlainBody(r Report, now time.Time) string {
	d := r.Diagnostics
	shot := "not attached"
	if len(r.Screenshot) > 0 {
		shot = fmt.Sprintf("attached (screenshot.png, %d KB)", (len(r.Screenshot)+1023)/1024)
	}
	contact := r.Contact
	if contact == "" {
		contact = "not given"
	}
	var b strings.Builder
	b.WriteString(r.Description)
	b.WriteString("\n\n-- \n")
	fmt.Fprintf(&b, "Contact: %s\n", contact)
	if d.User != "" {
		fmt.Fprintf(&b, "Signed-in user: %s\n", d.User)
	}
	fmt.Fprintf(&b, "Application: Kubron %s (%s)\n", orDash(d.AppVersion), orDash(d.Shell))
	fmt.Fprintf(&b, "System: %s/%s\n", orDash(d.OS), orDash(d.Arch))
	fmt.Fprintf(&b, "Language: %s\n", orDash(d.Locale))
	fmt.Fprintf(&b, "Open section: %s\n", orDash(d.View))
	fmt.Fprintf(&b, "Screenshot: %s\n", shot)
	fmt.Fprintf(&b, "Created: %s\n", now.UTC().Format(time.RFC3339))
	return b.String()
}

func orDash(s string) string {
	if s == "" || !utf8.ValidString(s) {
		return "-"
	}
	return s
}
