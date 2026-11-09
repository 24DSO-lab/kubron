// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package bugreport

import (
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"unicode/utf8"
)

// MaxMailtoLen is the longest mailto link that is opened. Classic Windows mail clients and ShellExecute stop near
// 2000 characters, so the link is kept shorter and the rest of the text goes to a file. Every Cyrillic letter takes
// six characters of a link, so about 250 of them fit.
const MaxMailtoLen = 1900

// Mailto builds the link that opens the user's mail program with the report prepared. A link cannot carry an
// attachment, so the screenshot is handled apart from it. If the description does not fit into maxLen characters it
// is shortened on a letter boundary and truncated is true; the caller then keeps the full text (PlainBody) in a file.
func Mailto(to string, r Report, maxLen int) (link string, truncated bool, err error) {
	addr, err := mail.ParseAddress(to)
	if err != nil {
		return "", false, ErrSettingsInvalid
	}
	prefix := "mailto:" + url.PathEscape(addr.Address) + "?subject=" + escape(subject(r.Description)) + "&body="
	footer := escape("\r\n\r\n-- \r\n" + mailtoFacts(r))

	budget := maxLen - len(prefix) - len(footer)
	if budget < 0 {
		budget = 0
	}
	desc := r.Description
	if len(escape(desc)) > budget {
		truncated = true
		const ellipsis = "…"
		budget -= len(escape(ellipsis))
		var b strings.Builder
		used := 0
		for _, ch := range desc {
			n := len(escape(string(ch)))
			if used+n > budget {
				break
			}
			b.WriteRune(ch)
			used += n
		}
		desc = strings.TrimRight(b.String(), " \t\r\n") + ellipsis
	}
	return prefix + escape(desc) + footer, truncated, nil
}

// mailtoFacts is the short list of facts below the description (no time and no attachment note: they add length).
func mailtoFacts(r Report) string {
	d := r.Diagnostics
	return fmt.Sprintf("Kubron %s (%s), %s/%s, %s, %s", orDash(d.AppVersion), orDash(d.Shell), orDash(d.OS), orDash(d.Arch),
		orDash(d.Locale), orDash(d.View))
}

// escape percent-encodes s for a mailto link: spaces as %20 (a plus sign would stay a plus in a mail program) and
// line breaks as CRLF.
func escape(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}
