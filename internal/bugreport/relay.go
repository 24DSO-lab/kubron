// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package bugreport

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultRelayURL is the address of the receiver of the project: a Cloudflare Pages Function (docs-site/functions)
// that stores the report in a private repository. It holds no secret on the client side.
const DefaultRelayURL = "https://kubron.pages.dev/api/bugreport"

// RelayOff is the value of KUBRON_BUGREPORT_RELAY_URL that turns the relay off.
const RelayOff = "off"

// RelayTimeout bounds one delivery to the relay.
const RelayTimeout = 20 * time.Second

// maxRelayReply is how much of the reply is read: the relay answers with a few bytes.
const maxRelayReply = 4 << 10

// Errors of the relay. ErrRelayUnavailable is temporary (no network, a busy or failing receiver): the report is worth
// keeping and sending again. ErrRelayRejected is final: the receiver looked at the report and refused it.
var (
	ErrRelayUnavailable = errors.New("the report receiver is not available")
	ErrRelayRejected    = errors.New("the report receiver did not accept the report")
	ErrRelayURLInvalid  = errors.New("the report receiver address is not valid")
)

var idRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// NewID returns the identifier of a report: 128 random bits in hex. The receiver stores a report under it, so a
// repeated delivery of the same report does not create a second one.
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// ValidID reports whether id has the form that NewID returns.
func ValidID(id string) bool { return idRe.MatchString(id) }

// ResolveRelayURL turns the setting into the address to use. An empty value means def (the default of the shell, which
// may be empty: the web server has no relay unless the administrator names one). RelayOff means none. The address must
// be HTTPS; plain HTTP is accepted only for this machine (tests and a local receiver).
func ResolveRelayURL(raw, def string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = def
	}
	if raw == "" || strings.EqualFold(raw, RelayOff) {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", ErrRelayURLInvalid
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return "", ErrRelayURLInvalid
		}
	default:
		return "", ErrRelayURLInvalid
	}
	return u.String(), nil
}

// relayDiagnostics is what leaves the machine besides the text and the screenshot. The login of a web user is not
// among them: the relay is outside the organization.
type relayDiagnostics struct {
	AppVersion string `json:"appVersion"`
	Shell      string `json:"shell"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	Locale     string `json:"locale"`
	View       string `json:"view"`
}

type relayPayload struct {
	ID          string           `json:"id"`
	Description string           `json:"description"`
	Contact     string           `json:"contact,omitempty"`
	Screenshot  string           `json:"screenshot,omitempty"` // PNG as plain base64
	Diagnostics relayDiagnostics `json:"diagnostics"`
}

// relayClient never follows a redirect (the report must reach the address that was set, not another one) and keeps
// TLS at 1.2 or newer. The proxy of the environment is used, as for the rest of the application.
func relayClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	return &http.Client{
		Transport: tr, Timeout: RelayTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// SendRelay delivers a normalized report to the relay at endpoint (see ResolveRelayURL). A nil client means the default.
func SendRelay(ctx context.Context, client *http.Client, endpoint, id string, r Report) error {
	if !ValidID(id) {
		return ErrRelayRejected
	}
	if client == nil {
		client = relayClient()
	}
	p := relayPayload{
		ID: id, Description: r.Description, Contact: r.Contact,
		Diagnostics: relayDiagnostics{
			AppVersion: r.Diagnostics.AppVersion, Shell: r.Diagnostics.Shell, OS: r.Diagnostics.OS,
			Arch: r.Diagnostics.Arch, Locale: r.Diagnostics.Locale, View: r.Diagnostics.View,
		},
	}
	if len(r.Screenshot) > 0 {
		p.Screenshot = base64.StdEncoding.EncodeToString(r.Screenshot)
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, RelayTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ErrRelayURLInvalid
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Kubron/"+oneLine(r.Diagnostics.AppVersion, 32))
	resp, err := client.Do(req)
	if err != nil {
		return relayFail(ErrRelayUnavailable, err)
	}
	defer resp.Body.Close()
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, maxRelayReply))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusRequestTimeout, resp.StatusCode >= 500:
		return relayFail(ErrRelayUnavailable, fmt.Sprintf("status %d: %s", resp.StatusCode, reply))
	default:
		return relayFail(ErrRelayRejected, fmt.Sprintf("status %d: %s", resp.StatusCode, reply))
	}
}

// relayFail logs the cause and returns the public error: a reply of the receiver must not reach the screen.
func relayFail(public error, cause any) error {
	slog.Warn("bug report: relay delivery failed", "reason", public.Error(), "cause", fmt.Sprint(cause))
	return public
}
