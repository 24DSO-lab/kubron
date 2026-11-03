// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/logging"
	"github.com/jackc/pgx/v5/pgconn"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// errTarget — «ядро», методы которого возвращают заданные ошибки.
type errTarget struct{ errs map[string]error }

func (e *errTarget) DB() error        { return e.errs["DB"] }
func (e *errTarget) Net() error       { return e.errs["Net"] }
func (e *errTarget) Flat() error      { return e.errs["Flat"] }
func (e *errTarget) TLS() error       { return e.errs["TLS"] }
func (e *errTarget) File() error      { return e.errs["File"] }
func (e *errTarget) Kube() error      { return e.errs["Kube"] }
func (e *errTarget) KubeDial() error  { return e.errs["KubeDial"] }
func (e *errTarget) Invalid() error   { return e.errs["Invalid"] }
func (e *errTarget) Public() error    { return e.errs["Public"] }
func (e *errTarget) Forbidden() error { return e.errs["Forbidden"] }
func (e *errTarget) Panic() error     { panic("boom: postgres://kubron:s3cret@db.internal:5432/kubron") }

const secretDSN = "postgres://kubron:s3cret@db.internal:5432/kubron"

func newErrHandler(t *testing.T, errs map[string]error) http.Handler {
	t.Helper()
	d, err := NewDispatcher(&errTarget{errs}, []string{"DB", "Net", "Flat", "TLS", "File", "Kube", "KubeDial", "Invalid", "Public", "Forbidden", "Panic"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return WithRequestID(NewHandler(d, nil, nil))
}

func callErr(h http.Handler, method string) (*httptest.ResponseRecorder, rpcError) {
	req := httptest.NewRequest(http.MethodPost, "/api/rpc/"+method, strings.NewReader("[]"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var rep struct {
		Error *rpcError `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &rep)
	if rep.Error == nil {
		return rec, rpcError{}
	}
	return rec, *rep.Error
}

type synchronizedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func captureLog(t *testing.T) *synchronizedBuffer {
	t.Helper()
	buf := &synchronizedBuffer{}
	prev := slog.Default()
	slog.SetDefault(logging.New(logging.Options{Writer: buf, Level: slog.LevelDebug}))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func TestInternalErrorsAreHiddenWithRequestID(t *testing.T) {
	_, cfgErr := pgconn.ParseConfig(secretDSN + "?sslmode=bogus")
	if cfgErr == nil {
		t.Fatal("the sample DSN must fail to parse")
	}
	dial := &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("10.20.30.40"), Port: 5432}, Err: syscall.ECONNREFUSED}
	secrets := []string{"10.20.30.40", "10.1.2.3", "db.internal", "s3cret", "kubron_users", "users_pkey", "/home/kubron/.kubron", "5432", "6443", "postgres://"}
	cases := map[string]error{
		"DB": fmt.Errorf("failed to save user: %w", &pgconn.PgError{Severity: "ERROR", Code: "23505",
			Message: `duplicate key value violates unique constraint "users_pkey"`, TableName: "kubron_users", ConstraintName: "users_pkey"}),
		"Net": fmt.Errorf("failed to read cluster: %w", &url.Error{Op: "Get", URL: "https://10.1.2.3:6443/version", Err: dial}),
		// Ошибка, потерявшая тип (%v вместо %w), опознаётся по тексту
		"Flat": fmt.Errorf("connect: %v", "dial tcp 10.1.2.3:6443: connect: connection refused"),
		"TLS":  fmt.Errorf("handshake with db.internal: %w", x509.UnknownAuthorityError{}),
		"File": fmt.Errorf("failed to read users: %w", &fs.PathError{Op: "open", Path: "/home/kubron/.kubron/users.json", Err: syscall.EACCES}),
	}
	// строка подключения из ошибки разбора конфигурации
	cases["DB"] = errors.Join(cases["DB"], cfgErr)

	for method, internal := range cases {
		buf := captureLog(t)
		h := newErrHandler(t, map[string]error{method: internal})
		rec, e := callErr(h, method)
		if rec.Code != http.StatusInternalServerError || e.Code != "error" {
			t.Errorf("%s: %d %q", method, rec.Code, e.Code)
		}
		body := rec.Body.String()
		for _, s := range secrets {
			if strings.Contains(body, s) {
				t.Errorf("%s: the response leaks %q: %s", method, s, body)
			}
		}
		id := rec.Header().Get("X-Request-Id")
		if id == "" || e.RequestID != id || !strings.Contains(e.Message, id) {
			t.Errorf("%s: the request id must be in the header, the body and the message: %q %q %q", method, id, e.RequestID, e.Message)
		}
		if e.Message != "internal error, request id "+id {
			t.Errorf("%s: unexpected generic message %q", method, e.Message)
		}
		// Подробности и тот же request id — в журнале
		logged := buf.String()
		if !strings.Contains(logged, id) || !strings.Contains(logged, "rpc call failed") {
			t.Errorf("%s: the log must carry the request id and the failure: %s", method, logged)
		}
		if strings.Contains(logged, "users_pkey") || strings.Contains(logged, "s3cret") {
			t.Errorf("%s: sensitive error details must not reach the log: %s", method, logged)
		}
		if !strings.Contains(logged, "error_type") {
			t.Errorf("%s: the log must retain the error type for diagnostics: %s", method, logged)
		}
	}
}

// Сообщения Kubernetes API и проверки ввода полезны пользователю и сохраняются; метка делает публичной любую ошибку.
func TestUsefulMessagesAreKept(t *testing.T) {
	gr := schema.GroupResource{Resource: "pods"}
	cases := map[string]struct {
		err  error
		want string
	}{
		"Kube":      {apierrors.NewNotFound(gr, "web-1"), `pods "web-1" not found`},
		"KubeDial":  {apierrors.NewServiceUnavailable("error trying to reach service: dial tcp 10.0.0.9:443: connect: connection refused"), "error trying to reach service"},
		"Invalid":   {errors.New("password must not match the username"), "password must not match the username"},
		"Public":    {PublicError(fmt.Errorf("the repository %q is unreachable: %w", "stable", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED})), `the repository "stable" is unreachable`},
		"Forbidden": {auth.ErrForbidden, auth.ErrForbidden.Error()},
	}
	for method, c := range cases {
		h := newErrHandler(t, map[string]error{method: c.err})
		rec, e := callErr(h, method)
		if !strings.Contains(e.Message, c.want) {
			t.Errorf("%s: %q must contain %q (status %d)", method, e.Message, c.want, rec.Code)
		}
		if strings.HasPrefix(e.Message, "internal error, request id") {
			t.Errorf("%s: a useful message must not be replaced", method)
		}
	}
	if rec, e := callErr(newErrHandler(t, map[string]error{"Forbidden": auth.ErrForbidden}), "Forbidden"); rec.Code != http.StatusForbidden || e.RequestID != "" {
		t.Errorf("non-500 answers are untouched: %d %+v", rec.Code, e)
	}
}

// Паника в методе: клиент получает общий текст без значения паники.
func TestPanicDetailsDoNotReachTheClient(t *testing.T) {
	rec, e := callErr(newErrHandler(t, nil), "Panic")
	if strings.Contains(rec.Body.String(), "s3cret") || strings.Contains(rec.Body.String(), "postgres://") {
		t.Fatalf("panic details leaked: %s", rec.Body.String())
	}
	if rec.Code != http.StatusInternalServerError || e.Message == "" {
		t.Fatalf("%d %+v", rec.Code, e)
	}
}

// Без WithRequestID (тесты, встраивание) идентификатор создаётся на месте и совпадает в заголовке и теле.
func TestRequestIDCreatedWhenMiddlewareIsAbsent(t *testing.T) {
	d, _ := NewDispatcher(&errTarget{map[string]error{"Net": &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}}}, []string{"Net"}, nil)
	rec, e := callErr(NewHandler(d, nil, nil), "Net")
	if id := rec.Header().Get("X-Request-Id"); id == "" || id != e.RequestID {
		t.Fatalf("header %q, body %q", id, e.RequestID)
	}
}

func TestLeaksInternalsClassification(t *testing.T) {
	hidden := []error{
		&pgconn.PgError{Message: "relation \"x\" does not exist"},
		&net.DNSError{Err: "no such host", Name: "db.internal"},
		fmt.Errorf("wrap: %w", syscall.ECONNRESET),
		errors.New("failed to connect to `host=db.internal user=kubron`: dial error"),
		errors.New("x509: certificate signed by unknown authority"),
	}
	for _, err := range hidden {
		if msg, h := exposeMessage(err, "rid"); !h || msg != "internal error, request id rid" {
			t.Errorf("%v must be hidden: %q", err, msg)
		}
	}
	kept := []error{
		errors.New("a user with this username already exists"),
		errors.New("cluster name must not be empty"),
		apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "s", errors.New("denied")),
		apierrors.NewBadRequest("invalid manifest"),
	}
	for _, err := range kept {
		if msg, h := exposeMessage(err, "rid"); h || msg != err.Error() {
			t.Errorf("%v must be kept: %q", err, msg)
		}
	}
}
