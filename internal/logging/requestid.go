// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"
)

type ctxKey struct{}

// WithRequestID кладёт идентификатор запроса в контекст.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// RequestID возвращает идентификатор запроса из контекста ("" — не задан).
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// validRequestID — допустимый входящий идентификатор: короткий и без символов, которыми можно подделать
// запись журнала или заголовок.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// ValidRequestID сообщает, можно ли принять идентификатор, присланный клиентом или прокси.
func ValidRequestID(id string) bool { return validRequestID.MatchString(id) }

// NewRequestID создаёт идентификатор: 16 шестнадцатеричных символов (64 бита случайности).
func NewRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}
