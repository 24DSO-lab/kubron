// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package bugreport

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/smtp"
	"regexp"
	"strconv"
	"time"
)

// SendTimeout bounds the whole delivery: connecting, signing in and transferring the message.
const SendTimeout = 30 * time.Second

// Security is how the connection to the mail server is protected.
type Security string

const (
	SecurityStartTLS Security = "starttls" // plain connection upgraded with STARTTLS (port 587)
	SecurityTLS      Security = "tls"      // TLS from the first byte (port 465)
	SecurityNone     Security = "none"     // no TLS; accepted only for a server on this machine
)

// Errors of the delivery. The cause is logged, not returned: a server reply must not reach the screen.
var (
	ErrSettingsInvalid = errors.New("the mail server settings are not valid")
	ErrConnect         = errors.New("could not connect to the mail server")
	ErrAuth            = errors.New("the mail server rejected the sign-in or does not support it")
	ErrRejected        = errors.New("the mail server did not accept the message")
)

// SMTPConfig is the mail server and the addresses of the report.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	To       string
	Security Security
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.\-]*[A-Za-z0-9])?$|^[0-9A-Fa-f:]+$`)

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Normalize fills the defaults and checks the settings.
func (c SMTPConfig) Normalize() (SMTPConfig, error) {
	if c.Security == "" {
		if c.Port == 465 {
			c.Security = SecurityTLS
		} else {
			c.Security = SecurityStartTLS
		}
	}
	if c.Port == 0 {
		switch c.Security {
		case SecurityTLS:
			c.Port = 465
		case SecurityNone:
			c.Port = 25
		default:
			c.Port = 587
		}
	}
	switch c.Security {
	case SecurityStartTLS, SecurityTLS:
	case SecurityNone:
		if !isLoopback(c.Host) {
			return c, ErrSettingsInvalid // never send a screenshot or a password in clear text over a network
		}
	default:
		return c, ErrSettingsInvalid
	}
	if c.Host == "" || len(c.Host) > 253 || !hostRe.MatchString(c.Host) || c.Port < 1 || c.Port > 65535 {
		return c, ErrSettingsInvalid
	}
	if c.Username != "" && c.Password == "" {
		return c, ErrSettingsInvalid
	}
	if c.From == "" {
		c.From = c.Username
	}
	if c.To == "" {
		c.To = DefaultRecipient
	}
	for _, p := range []*string{&c.From, &c.To} {
		a, err := mail.ParseAddress(*p)
		if err != nil {
			return c, ErrSettingsInvalid
		}
		*p = a.Address
	}
	return c, nil
}

// Send delivers msg. It refuses to continue without encryption unless the server is on this machine.
func Send(ctx context.Context, c SMTPConfig, msg []byte) error {
	c, err := c.Normalize()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(c.Host, strconv.Itoa(c.Port)))
	if err != nil {
		return fail(ErrConnect, err)
	}
	defer context.AfterFunc(ctx, func() { _ = conn.Close() })()
	dl, _ := ctx.Deadline()
	_ = conn.SetDeadline(dl)

	tlsCfg := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	if c.Security == SecurityTLS {
		tc := tls.Client(conn, tlsCfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return fail(ErrConnect, err)
		}
		conn = tc
	}
	cl, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		_ = conn.Close()
		return fail(ErrConnect, err)
	}
	defer cl.Close()

	if c.Security == SecurityStartTLS {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return fail(ErrConnect, "server does not offer STARTTLS")
		}
		if err := cl.StartTLS(tlsCfg); err != nil {
			return fail(ErrConnect, err)
		}
	}
	if c.Username != "" {
		if ok, _ := cl.Extension("AUTH"); !ok {
			return fail(ErrAuth, "server does not offer AUTH")
		}
		if err := cl.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
			return fail(ErrAuth, err)
		}
	}
	if err := cl.Mail(c.From); err != nil {
		return fail(ErrRejected, err)
	}
	if err := cl.Rcpt(c.To); err != nil {
		return fail(ErrRejected, err)
	}
	w, err := cl.Data()
	if err != nil {
		return fail(ErrRejected, err)
	}
	if _, err := w.Write(msg); err != nil {
		return fail(ErrRejected, err)
	}
	if err := w.Close(); err != nil {
		return fail(ErrRejected, err)
	}
	_ = cl.Quit()
	return nil
}

func fail(public error, cause any) error {
	slog.Warn("bug report: delivery failed", "reason", public.Error(), "cause", fmt.Sprint(cause))
	return public
}
