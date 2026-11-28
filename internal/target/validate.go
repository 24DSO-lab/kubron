// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package target

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxIDLen          = 128
	maxDisplayNameLen = 128 // в рунах
	maxNameLen        = 253
	maxUIDLen         = 128
	maxResourceVerLen = 256
	maxFieldValueLen  = 253
	maxLabelNameLen   = 63
	maxLabelValueLen  = 63
)

var (
	reDNSLabel     = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	reDNSSubdomain = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)
	reKind         = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
	reLabelName    = regexp.MustCompile(`^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`)
	reLabelValue   = regexp.MustCompile(`^([A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?)?$`)
)

// validID — единый валидатор непрозрачных идентификаторов (совпадает с identity.ValidID): непустой, ≤128 байт,
// первый символ буква/цифра, далее [A-Za-z0-9._:@+=-]. Безопасен как часть имени файла/ключа.
func validID(s string) bool {
	if s == "" || len(s) > maxIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case i > 0 && strings.IndexByte("._:@+=-", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// validSegment — безопасный сегмент пути Kubernetes (name, uid): непустой, ≤max байт, ASCII без '/', '%',
// управляющих символов и пробелов, не "." и не "..".
func validSegment(s string, max int) bool {
	if s == "" || len(s) > max || s == "." || s == ".." {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c == 0x7f || c == '/' || c == '%' || c >= 0x80 {
			return false
		}
	}
	return true
}

func validDNSLabel(s string) bool { return len(s) <= 63 && reDNSLabel.MatchString(s) }

func validDNSSubdomain(s string) bool { return len(s) <= 253 && reDNSSubdomain.MatchString(s) }

// validAPIGroup: пустая (core) либо DNS-субдомен.
func validAPIGroup(s string) bool { return s == "" || validDNSSubdomain(s) }

func validAPIVersion(s string) bool { return validDNSLabel(s) }

func validKindName(s string) bool { return len(s) <= 63 && reKind.MatchString(s) }

// validLabelKey: [prefix/]name; prefix — DNS-субдомен ≤253, name ≤63.
func validLabelKey(s string) bool {
	prefix, name := "", s
	if i := strings.IndexByte(s, '/'); i >= 0 {
		prefix, name = s[:i], s[i+1:]
		if !validDNSSubdomain(prefix) {
			return false
		}
	}
	return len(name) > 0 && len(name) <= maxLabelNameLen && reLabelName.MatchString(name)
}

func validLabelValue(s string) bool { return len(s) <= maxLabelValueLen && reLabelValue.MatchString(s) }

// validDisplayName: 1..128 рун, валидный UTF-8, без управляющих символов и краевых пробелов.
func validDisplayName(s string) bool {
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxDisplayNameLen || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validResourceVersion(s string) bool {
	if len(s) > maxResourceVerLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] == 0x7f || s[i] >= 0x80 {
			return false
		}
	}
	return true
}
