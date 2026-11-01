// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package streamid — идентификаторы потоков (терминал, логи) с владельцем внутри (ADR-0016, раздел 4).
//
// Поток привязан к процессу, в котором открыт сокет к кластеру. Идентификатор имеет вид
// «<replica_id>.<128 случайных бит в hex>»: по префиксу любая реплика понимает, чей это поток, без таблицы
// в БД. Запрос к потоку другой реплики не проксируется: клиент открывает поток заново.
package streamid

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
)

// ErrElsewhere — поток открыт на другой реплике (или такой реплики уже нет). Сервер отвечает 409 stream_elsewhere.
var ErrElsewhere = errors.New("stream is on another replica")

// New возвращает неугадываемый идентификатор потока реплики replica. Пустая replica — без префикса (одна реплика,
// десктоп): идентификатор тогда просто 128 случайных бит.
func New(replica string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("streamid: crypto/rand is unavailable: " + err.Error())
	}
	id := hex.EncodeToString(b)
	if replica == "" {
		return id
	}
	return replica + "." + id
}

// Owner возвращает реплику-владельца из идентификатора; false — префикса нет.
func Owner(id string) (string, bool) {
	i := strings.IndexByte(id, '.')
	if i <= 0 {
		return "", false
	}
	return id[:i], true
}

// Check проверяет, что поток принадлежит реплике self. Пустой self (одна реплика, десктоп) принимает любой
// идентификатор, чужой или без префикса даёт ErrElsewhere.
func Check(self, id string) error {
	if self == "" {
		return nil
	}
	if owner, ok := Owner(id); !ok || owner != self {
		return ErrElsewhere
	}
	return nil
}
