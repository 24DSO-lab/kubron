// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package logging — настройка структурированного журнала процесса (log/slog).
//
// Формат по умолчанию — JSON, по строке на запись, для внешнего сборщика (Loki, Elastic, Splunk).
// Каждая запись проходит через редактор секретов: значения «чувствительных» ключей, токены, пароли в
// адресах, заголовки авторизации и зарегистрированные значения (ключ шифрования, пароль БД) заменяются
// на [REDACTED] в сообщении и во всех атрибутах, включая тексты ошибок. Это страховка, а не причина
// писать секреты в журнал: вызывающий код секреты в журнал не передаёт.
//
// slog.SetDefault с этим логгером перенаправляет и стандартный log.Printf, поэтому записи старого кода
// тоже выходят JSON-ом и тоже редактируются.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Redacted — чем заменяется скрытое значение.
const Redacted = "[REDACTED]"

// Options — настройки логгера.
type Options struct {
	Writer io.Writer  // куда писать; обязателен
	Level  slog.Level // минимальный уровень
	Text   bool       // true — читаемый текст для локальной разработки; по умолчанию JSON
}

// ParseLevel разбирает debug|info|warn|error (без учёта регистра); пустая строка — info.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q (expected debug, info, warn or error)", s)
}

// New создаёт логгер с редактором секретов и поддержкой идентификатора запроса из контекста
// (slog.InfoContext и др.: запись получает поле request_id).
func New(o Options) *slog.Logger {
	ho := &slog.HandlerOptions{Level: o.Level, ReplaceAttr: replaceAttr}
	var h slog.Handler
	if o.Text {
		h = slog.NewTextHandler(o.Writer, ho)
	} else {
		h = slog.NewJSONHandler(o.Writer, ho)
	}
	return slog.New(ctxHandler{h})
}

// ctxHandler добавляет в запись request_id из контекста.
type ctxHandler struct{ slog.Handler }

func (h ctxHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h ctxHandler) WithAttrs(a []slog.Attr) slog.Handler { return ctxHandler{h.Handler.WithAttrs(a)} }
func (h ctxHandler) WithGroup(n string) slog.Handler      { return ctxHandler{h.Handler.WithGroup(n)} }

// replaceAttr применяется ко всем атрибутам и к сообщению (ключ slog.MessageKey).
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey) {
		return a
	}
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		if v.String() == "" {
			return a
		}
		if sensitiveKey(a.Key) {
			return slog.String(a.Key, Redacted)
		}
		return slog.String(a.Key, Redact(v.String()))
	case slog.KindAny:
		if sensitiveKey(a.Key) {
			return slog.String(a.Key, Redacted)
		}
		switch x := v.Any().(type) {
		case nil:
			return a
		case error:
			return slog.String(a.Key, Redact(x.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, Redact(x.String()))
		default:
			// Произвольные структуры и карты: в строку и через редактор, чтобы секрет внутри не ушёл.
			return slog.String(a.Key, Redact(fmt.Sprintf("%+v", x)))
		}
	case slog.KindGroup:
		return a
	default: // числа, bool, время, длительность: секретом быть не могут
		if sensitiveKey(a.Key) {
			return slog.String(a.Key, Redacted)
		}
		return a
	}
}
