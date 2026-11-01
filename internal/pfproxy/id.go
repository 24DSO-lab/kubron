// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pfproxy

import (
	"crypto/rand"
	"encoding/base32"
	"regexp"
	"strings"
)

// idRe — допустимый идентификатор потока: 128 бит в base32 без паддинга, нижний регистр (DNS-метка до 63 символов).
var idRe = regexp.MustCompile(`^[a-z2-7]{26}$`)

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewID возвращает неугадываемый идентификатор потока.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("pfproxy: crypto/rand is unavailable: " + err.Error())
	}
	return strings.ToLower(idEncoding.EncodeToString(b[:]))
}

// ValidID сообщает, похожа ли строка на идентификатор потока.
func ValidID(id string) bool { return idRe.MatchString(id) }
