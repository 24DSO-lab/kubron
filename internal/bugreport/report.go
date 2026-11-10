// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package bugreport builds and sends the "report a problem" e-mail of the desktop: a description, an optional
// screenshot and a few non-sensitive facts about the application. It does not know about Wails or about the
// storage of the mail settings; the core (internal/app) wires those.
package bugreport

import (
	"bytes"
	"errors"
	"image/png"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DefaultRecipient receives the reports unless the settings say otherwise.
const DefaultRecipient = "224vadim224@gmail.com"

// Limits of a report.
const (
	MaxDescriptionRunes = 5000
	MaxScreenshotBytes  = 6 << 20
	maxScreenshotSide   = 16384
	maxFieldRunes       = 64
)

// Errors returned to the user. The texts are fixed and never hold a value that the user typed.
var (
	ErrDescriptionRequired = errors.New("describe the problem before sending the report")
	ErrDescriptionTooLong  = errors.New("the description is too long")
	ErrScreenshotInvalid   = errors.New("the screenshot is not a valid PNG image")
	ErrScreenshotTooLarge  = errors.New("the screenshot is too large")
	ErrContactInvalid      = errors.New("the contact address is not a valid e-mail address")
)

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// Diagnostics are the facts attached to a report. They never include cluster names, addresses or credentials.
type Diagnostics struct {
	AppVersion string
	Shell      string // "desktop" or "web"
	User       string // login of the signed-in user; web only
	OS, Arch   string
	Locale     string
	View       string // the section of the interface that was open
}

// Report is one problem report.
type Report struct {
	Description string
	Contact     string // optional e-mail address of the author, used as Reply-To
	Screenshot  []byte // optional PNG
	Diagnostics Diagnostics
}

// Normalize trims and checks the report. It must be called before Build.
func (r *Report) Normalize() error {
	r.Description = strings.TrimSpace(cleanText(r.Description))
	if r.Description == "" {
		return ErrDescriptionRequired
	}
	if utf8.RuneCountInString(r.Description) > MaxDescriptionRunes {
		return ErrDescriptionTooLong
	}
	if c := strings.TrimSpace(r.Contact); c != "" {
		addr, err := mail.ParseAddress(c)
		if err != nil || utf8.RuneCountInString(addr.Address) > 254 {
			return ErrContactInvalid
		}
		r.Contact = addr.Address
	} else {
		r.Contact = ""
	}
	if len(r.Screenshot) > 0 {
		if len(r.Screenshot) > MaxScreenshotBytes {
			return ErrScreenshotTooLarge
		}
		if !bytes.HasPrefix(r.Screenshot, pngSignature) {
			return ErrScreenshotInvalid
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(r.Screenshot))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxScreenshotSide || cfg.Height > maxScreenshotSide {
			return ErrScreenshotInvalid
		}
	}
	d := &r.Diagnostics
	for _, f := range []*string{&d.AppVersion, &d.Shell, &d.OS, &d.Arch, &d.Locale, &d.View, &d.User} {
		*f = oneLine(*f, maxFieldRunes)
	}
	return nil
}

// cleanText removes control characters except new lines and tabs, and invalid UTF-8.
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r == '\r' {
			return -1
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// oneLine makes a value that is safe for a header or a single line of the body.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(cleanText(s)), " ")
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}
