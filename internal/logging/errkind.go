// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package logging

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io/fs"
	"net"
	"net/url"
	"os"
)

// Классы ошибок, возвращаемые ErrorKind. Это единственные значения, которые попадают в журнал вместо
// самой ошибки.
const (
	KindNone       = "none"
	KindTimeout    = "timeout"
	KindCanceled   = "canceled"
	KindNetwork    = "network"
	KindTLS        = "tls"
	KindFilesystem = "filesystem"
	KindDatabase   = "database"
	KindOther      = "other"
)

// sqlStater — ошибка СУБД с кодом SQLSTATE (в том числе *pgconn.PgError). Интерфейс позволяет не тянуть
// драйвер в пакет журнала.
type sqlStater interface{ SQLState() string }

// ErrorKind относит ошибку к одному из классов Kind* и возвращает только константу этого класса.
//
// Текст ошибки и её динамический тип не возвращаются намеренно: ошибка может вобрать в себя ключ
// шифрования, токен или пароль из адреса БД, а значение, выведенное из самой ошибки (в том числе через
// fmt.Sprintf("%T", err)), анализаторы потоков данных (CodeQL) считают зависящим от неё и помечают как
// утечку секрета. Здесь результат всегда берётся из набора констант, поэтому в журнал попадает только
// класс сбоя, достаточный для диагностики, и никогда — содержимое ошибки.
func ErrorKind(err error) string {
	var (
		sqlErr     sqlStater
		opErr      *net.OpError
		dnsErr     *net.DNSError
		addrErr    *net.AddrError
		urlErr     *url.Error
		netErr     net.Error
		unknownCA  x509.UnknownAuthorityError
		hostErr    x509.HostnameError
		certErr    x509.CertificateInvalidError
		tlsVerify  *tls.CertificateVerificationError
		tlsRecord  tls.RecordHeaderError
		pathErr    *fs.PathError
		linkErr    *os.LinkError
		syscallErr *os.SyscallError
	)
	switch {
	case err == nil:
		return KindNone
	case errors.Is(err, context.DeadlineExceeded):
		return KindTimeout
	case errors.Is(err, context.Canceled):
		return KindCanceled
	case errors.As(err, &unknownCA), errors.As(err, &hostErr), errors.As(err, &certErr),
		errors.As(err, &tlsVerify), errors.As(err, &tlsRecord):
		return KindTLS
	case errors.As(err, &sqlErr):
		return KindDatabase
	case errors.As(err, &opErr), errors.As(err, &dnsErr), errors.As(err, &addrErr),
		errors.As(err, &urlErr), errors.As(err, &netErr):
		return KindNetwork
	case errors.As(err, &pathErr), errors.As(err, &linkErr), errors.As(err, &syscallErr):
		return KindFilesystem
	default:
		return KindOther
	}
}
