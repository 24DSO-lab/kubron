// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
)

// Идентичность реплики (ADR-0016, раздел 4). replica_id кодируется в идентификаторах потоков
// («<replica_id>.<случайные 128 бит>»), поэтому не может содержать точку; он же попадает в сообщения
// между репликами (поле origin) и в журнал. Допустимы буквы, цифры, «-» и «_», длина 1–63.

// MaxReplicaIDLen — предел длины replica_id (как у метки DNS: имя пода всегда укладывается).
const MaxReplicaIDLen = 63

// replicaSuffixBytes — длина случайного суффикса автоматического replica_id (в байтах, 8 hex-символов).
const replicaSuffixBytes = 4

// ValidateReplicaID проверяет явно заданный идентификатор реплики (KUBRON_REPLICA_ID).
func ValidateReplicaID(id string) error {
	if id == "" || len(id) > MaxReplicaIDLen {
		return errors.New("replica id must be 1 to 63 characters long")
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return errors.New("replica id may contain only letters, digits, '-' and '_'")
		}
	}
	return nil
}

// ResolveReplicaID возвращает идентификатор реплики: явно заданный (проверенный) либо построенный
// автоматически из имени узла и случайного суффикса процесса. Перезапущенный под с тем же именем получает
// другой идентификатор: потоки прежнего процесса исчезли вместе с ним, и «владелец» не должен совпасть.
func ResolveReplicaID(explicit, hostname string) (string, error) {
	if explicit != "" {
		if err := ValidateReplicaID(explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}
	var b [replicaSuffixBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return autoReplicaID(hostname, hex.EncodeToString(b[:])), nil
}

// autoReplicaID строит «<имя узла>-<суффикс>»: символы вне допустимого набора (точки в FQDN) заменяются
// на «-», имя укорачивается так, чтобы с суффиксом результат уместился в MaxReplicaIDLen.
func autoReplicaID(hostname, suffix string) string {
	var sb strings.Builder
	for i := 0; i < len(hostname); i++ {
		c := hostname[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			sb.WriteByte(c)
		default:
			sb.WriteByte('-')
		}
	}
	base := strings.Trim(sb.String(), "-_")
	if base == "" {
		base = "kubron"
	}
	if limit := MaxReplicaIDLen - 1 - len(suffix); len(base) > limit {
		base = strings.TrimRight(base[:limit], "-_")
		if base == "" {
			base = "kubron"
		}
	}
	return base + "-" + suffix
}
