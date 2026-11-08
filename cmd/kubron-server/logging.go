// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/logging"
	"github.com/24DSO-lab/kubron/internal/server"
)

// setupLogging настраивает журнал процесса по окружению и делает его логгером по умолчанию (slog.SetDefault
// перенаправляет и стандартный log.Printf, поэтому весь вывод — JSON с редакцией секретов):
//
//	KUBRON_LOG_LEVEL   debug | info (по умолчанию) | warn | error
//	KUBRON_LOG_FORMAT  json (по умолчанию) | text (читаемый вывод для разработки)
//
// Журнал идёт в stderr; stdout остаётся для аудита (KUBRON_AUDIT_STDOUT) и вывода команд.
func setupLogging(getenv func(string) string, w io.Writer) error {
	level, err := logging.ParseLevel(getenv("KUBRON_LOG_LEVEL"))
	if err != nil {
		return fmt.Errorf("KUBRON_LOG_LEVEL: %w", err)
	}
	var text bool
	switch f := strings.ToLower(strings.TrimSpace(getenv("KUBRON_LOG_FORMAT"))); f {
	case "", "json":
	case "text":
		text = true
	default:
		return fmt.Errorf("KUBRON_LOG_FORMAT %q: expected json or text", f)
	}
	slog.SetDefault(logging.New(logging.Options{Writer: w, Level: level, Text: text}))
	return nil
}

// registerSecrets сообщает журналу значения, которых в нём быть не должно: ключи шифрования и пароли
// из адресов БД. Они заменяются на [REDACTED] даже внутри чужих сообщений об ошибках.
func registerSecrets(cfg server.Config) {
	logging.AddKeySecret(cfg.SecretKey)
	for _, k := range cfg.PreviousSecretKeys {
		logging.AddKeySecret(k)
	}
	logging.AddSecret(cfg.SetupToken)
	logging.AddURLSecrets(cfg.DatabaseURL)
	logging.AddURLSecrets(cfg.DBListenURL)
}

// enableAuditStdout дублирует журнал аудита строками JSON в stdout (KUBRON_AUDIT_STDOUT=1): внешний
// сборщик собирает их независимо от БД. Строки отличает "type":"audit". Включается только для сервера: у
// административных команд stdout — их результат.
func enableAuditStdout(getenv func(string) string, w io.Writer) bool {
	switch strings.ToLower(strings.TrimSpace(getenv("KUBRON_AUDIT_STDOUT"))) {
	case "1", "true", "yes", "on":
		audit.SetMirror(w)
		return true
	}
	return false
}

// logFatal пишет безопасную метаинформацию об ошибке. Только keyMismatchError имеет фиксированный
// безопасный текст. Остальные ошибки могут содержать конфигурационные секреты, поэтому в журнал идёт
// лишь класс ошибки — константа из logging.ErrorKind, а не её текст и не имя типа.
func logFatal(what string, err error) {
	if _, ok := err.(keyMismatchError); ok {
		slog.Error(what, "error", keyMismatchError{}.Error())
		return
	}
	slog.Error(what, "error_type", logging.ErrorKind(err))
}

// fatal журналирует ошибку (logFatal) и завершает процесс с кодом 1 (замена log.Fatalf).
func fatal(what string, err error) {
	logFatal(what, err)
	os.Exit(1)
}

// logf — адаптер для кода, принимающего функцию в стиле Printf (notifier, диспетчер событий реплик).
func logf(format string, args ...any) { slog.Info(fmt.Sprintf(format, args...)) }
