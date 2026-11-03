// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package logging

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"syscall"
	"testing"
)

type fakePgError struct{}

func (fakePgError) Error() string    { return "ERROR: duplicate key (SQLSTATE 23505)" }
func (fakePgError) SQLState() string { return "23505" }

func TestErrorKind(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, KindNone},
		{"deadline", fmt.Errorf("read: %w", context.DeadlineExceeded), KindTimeout},
		{"canceled", context.Canceled, KindCanceled},
		{"net op", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, KindNetwork},
		{"dns", &net.DNSError{Err: "no such host", Name: "db"}, KindNetwork},
		{"url", &url.Error{Op: "Get", URL: "https://x", Err: errors.New("boom")}, KindNetwork},
		{"tls", x509.UnknownAuthorityError{}, KindTLS},
		{"path", &fs.PathError{Op: "open", Path: "/etc/kubron/key", Err: fs.ErrNotExist}, KindFilesystem},
		{"database", fmt.Errorf("query: %w", fakePgError{}), KindDatabase},
		{"other", errors.New("failed to load key super-secret-encryption-key"), KindOther},
	}
	for _, c := range cases {
		if got := ErrorKind(c.err); got != c.want {
			t.Errorf("%s: ErrorKind = %q, want %q", c.name, got, c.want)
		}
	}
}

// Результат — всегда одна из констант, а не часть текста ошибки.
func TestErrorKindNeverEchoesError(t *testing.T) {
	const secret = "k1:c3VwZXItc2VjcmV0LWtleS1tYXRlcmlhbA"
	errs := []error{
		errors.New(secret),
		fmt.Errorf("parse %s: %w", secret, &net.OpError{Op: "dial", Err: errors.New(secret)}),
		&fs.PathError{Op: "open", Path: "/run/" + secret, Err: errors.New(secret)},
		&url.Error{Op: "Get", URL: "postgres://u:" + secret + "@db/x", Err: errors.New(secret)},
	}
	known := map[string]bool{
		KindNone: true, KindTimeout: true, KindCanceled: true, KindNetwork: true,
		KindTLS: true, KindFilesystem: true, KindDatabase: true, KindOther: true,
	}
	for _, err := range errs {
		if got := ErrorKind(err); !known[got] {
			t.Errorf("ErrorKind returned %q, which is not one of the fixed kinds", got)
		}
	}
}
