// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package logging

import (
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// sensitiveFragments — фрагменты имён ключей (без учёта регистра, без «-», «_», «.»), чьё значение
// скрывается целиком.
var sensitiveFragments = []string{
	"password", "passwd", "secret", "token", "authorization", "cookie", "kubeconfig",
	"privatekey", "apikey", "accesskey", "databaseurl", "dsn", "credential", "csrf",
}

func sensitiveKey(k string) bool {
	k = strings.ToLower(k)
	k = strings.NewReplacer("-", "", "_", "", ".", "").Replace(k)
	for _, f := range sensitiveFragments {
		if strings.Contains(k, f) {
			return true
		}
	}
	return false
}

var (
	reBearer = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=\-]{6,}`)
	// scheme://user:password@host — пароль в адресе (PostgreSQL, OCI-реестры, прокси)
	reURLUserinfo = regexp.MustCompile(`(?i)([a-z][a-z0-9+.\-]*://)([^/\s:@]*):([^/\s@]+)@`)
	// password=..., token: ..., "secret":"..." и т. п.
	rePair = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|client[_-]?secret|authorization|set-cookie|cookie|kubron_secret_key|kubron_session)\b(["']?\s*[:=]\s*["']?)([^\s"',;&}]+)`)
	reJWT  = regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]*`)
	rePEM  = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
)

// minSecretLen — короче зарегистрированное значение не ищется: иначе короткая строка исказит обычный текст.
const minSecretLen = 6

var (
	secretsMu sync.RWMutex
	secrets   []string
)

// AddSecret регистрирует значения, которые нельзя выводить в журнал: они заменяются везде, где встретятся.
// Вызывается при старте; безопасно вызывать из нескольких горутин.
func AddSecret(values ...string) {
	secretsMu.Lock()
	defer secretsMu.Unlock()
	for _, v := range values {
		v = strings.TrimSpace(v)
		if len(v) >= minSecretLen && !contains(secrets, v) {
			secrets = append(secrets, v)
		}
	}
}

// AddKeySecret регистрирует ключ шифрования вида «id:base64»: и всю строку, и часть после двоеточия
// (в журнал может попасть любая из них).
func AddKeySecret(key string) {
	AddSecret(key)
	if i := strings.Index(key, ":"); i >= 0 {
		AddSecret(key[i+1:])
	}
}

// AddURLSecrets регистрирует пароль из адреса подключения (postgres://user:pass@host/db). Не адрес целиком:
// хост и база нужны в диагностике; скрывает пароль и общий редактор адресов.
func AddURLSecrets(raw string) {
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		if p, ok := u.User.Password(); ok {
			AddSecret(p)
		}
	}
}

// ResetSecrets забывает все зарегистрированные значения (тесты).
func ResetSecrets() {
	secretsMu.Lock()
	secrets = nil
	secretsMu.Unlock()
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Redact скрывает секреты в строке: зарегистрированные значения, заголовки авторизации, пароли в адресах,
// пары «ключ=значение» с чувствительными ключами, JWT и закрытые ключи PEM.
func Redact(s string) string {
	if s == "" {
		return s
	}
	secretsMu.RLock()
	for _, v := range secrets {
		s = strings.ReplaceAll(s, v, Redacted)
	}
	secretsMu.RUnlock()
	s = rePEM.ReplaceAllString(s, Redacted)
	s = reURLUserinfo.ReplaceAllString(s, "${1}${2}:"+Redacted+"@")
	s = reBearer.ReplaceAllString(s, "${1} "+Redacted)
	s = rePair.ReplaceAllString(s, "${1}${2}"+Redacted)
	s = reJWT.ReplaceAllString(s, Redacted)
	return s
}
