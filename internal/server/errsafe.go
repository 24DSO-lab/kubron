// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"

	"github.com/24DSO-labs/kubron/internal/logging"
	"github.com/24DSO-labs/kubron/internal/publicerr"
	"github.com/jackc/pgx/v5/pgconn"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// PublicError помечает ошибку, текст которой можно показать пользователю как есть даже при статусе 500 (см. errsafe.go).
// Нужна, когда сообщение составлено поверх внутренней ошибки и всё равно безопасно. Сообщения Kubernetes API и ошибки
// проверки ввода без вложенных ошибок БД и сети маркера не требуют.
func PublicError(err error) error { return publicerr.Wrap(err) }

// internalErrorMessage — общий ответ вместо внутренней ошибки. Текст идёт через fmt.Errorf: i18n-check требует для него
// шаблон в backend.json (en, ru, cn), фронтенд переводит его по шаблону.
func internalErrorMessage(requestID string) string {
	return fmt.Errorf("internal error, request id %s", requestID).Error()
}

// leaksByType сообщает, что в цепочке ошибок есть тип с внутренними деталями: ошибка БД (pgx), сетевая ошибка, адрес
// или DNS, TLS/x509, путь файла, срок ожидания. Это решает по типам, а не по тексту: сообщение Kubernetes API (его
// текст может содержать «dial tcp») тип *apierrors.StatusError сохраняет.
func leaksByType(err error) bool {
	var (
		pgErr     *pgconn.PgError
		connErr   *pgconn.ConnectError
		parseErr  *pgconn.ParseConfigError
		opErr     *net.OpError
		dnsErr    *net.DNSError
		addrErr   *net.AddrError
		urlErr    *url.Error
		unknownCA x509.UnknownAuthorityError
		hostErr   x509.HostnameError
		certErr   x509.CertificateInvalidError
		tlsVerify *tls.CertificateVerificationError
		tlsRecord tls.RecordHeaderError
		pathErr   *fs.PathError
		sysErr    *os.SyscallError
		linkErr   *os.LinkError
		errno     syscall.Errno
		netErr    net.Error
	)
	return errors.As(err, &pgErr) || errors.As(err, &connErr) || errors.As(err, &parseErr) || errors.As(err, &opErr) ||
		errors.As(err, &dnsErr) || errors.As(err, &addrErr) || errors.As(err, &urlErr) || errors.As(err, &unknownCA) ||
		errors.As(err, &hostErr) || errors.As(err, &certErr) || errors.As(err, &tlsVerify) || errors.As(err, &tlsRecord) ||
		errors.As(err, &pathErr) || errors.As(err, &sysErr) || errors.As(err, &linkErr) || errors.As(err, &errno) ||
		errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded)
}

// leakyFragments — признаки внутренних деталей в тексте ошибки, потерявшей тип.
var leakyFragments = []string{
	"sqlstate", "pgx", "pgconn", "pq:", "failed to connect to `", "duplicate key value", "relation \"",
	"syntax error at or near", "connection string", "password authentication failed", "dial tcp", "dial udp",
	"no such host", "connection refused", "connection reset", "i/o timeout", "no route to host",
	"network is unreachable", "broken pipe", "x509:", "tls:", "certificate signed by unknown authority",
	"server misbehaving", "context deadline exceeded", "postgres://", "postgresql://",
}

func leakyText(msg string) bool {
	low := strings.ToLower(msg)
	for _, f := range leakyFragments {
		if strings.Contains(low, f) {
			return true
		}
	}
	return false
}

// exposeMessage возвращает текст ошибки для ответа клиента при статусе 500. Общее сообщение с идентификатором запроса
// получают ошибки с внутренними деталями (БД, сеть, TLS, пути файлов, в том числе потерявшие тип и опознанные по
// тексту); сообщения Kubernetes API, проверки ввода и помеченные PublicError остаются как есть. Первая итерация
// намеренно не скрывает неклассифицированные ошибки: сотни прикладных сообщений («user already exists», «password is
// too short») иначе превратились бы в «internal error», а e2e этой среды проверить регрессии не позволили.
func exposeMessage(err error, requestID string) (msg string, hidden bool) {
	switch {
	case publicerr.Is(err):
		return err.Error(), false
	case leaksByType(err):
		return internalErrorMessage(requestID), true
	}
	var st apierrors.APIStatus
	if errors.As(err, &st) {
		return err.Error(), false
	}
	if leakyText(err.Error()) {
		return internalErrorMessage(requestID), true
	}
	return err.Error(), false
}

// requestIDFor возвращает идентификатор запроса из контекста; если WithRequestID не стоит (тесты), создаёт его и ставит
// в заголовок ответа, чтобы идентификатор в теле и в журнале совпадал с тем, что видит клиент.
func requestIDFor(w http.ResponseWriter, r *http.Request) (string, *http.Request) {
	if id := logging.RequestID(r.Context()); id != "" {
		return id, r
	}
	id := logging.NewRequestID()
	w.Header().Set(requestIDHeader, id)
	return id, r.WithContext(logging.WithRequestID(r.Context(), id))
}

// logInternal пишет факт сбоя, класс ошибки и request_id. Текст ошибки и имя её типа не журналируются:
// ошибка может содержать ключи шифрования, токены и другие секреты. Для диагностики хватает константы из
// logging.ErrorKind (timeout, network, tls, filesystem, database, other).
func logInternal(r *http.Request, method string, err error, hidden bool) {
	slog.ErrorContext(r.Context(), "rpc call failed", "method", method, "error_type", logging.ErrorKind(err), "hidden_from_client", hidden)
}
