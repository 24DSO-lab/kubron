// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pfproxy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"regexp"
	"strings"
)

// idRe — допустимый идентификатор потока: 128 бит в base32 без паддинга, нижний регистр (DNS-метка до 63 символов).
// С несколькими репликами впереди стоит метка владельца из 6 символов (ownerTag): идентификатор должен остаться
// одной DNS-меткой, иначе wildcard-сертификат и запись *.pf.<домен> его не покроют.
var idRe = regexp.MustCompile(`^([a-z2-7]{6})?[a-z2-7]{26}$`)

const tagLen = 6

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewID возвращает неугадываемый идентификатор потока без владельца (одна реплика, десктоп).
func NewID() string { return NewIDFor("") }

// ownerTag — короткая метка реплики в идентификаторе потока: 30 бит от SHA-256 идентификатора реплики. Это не
// секрет и не защита: по метке реплика только понимает, что поток открыт не на ней (ADR-0017, раздел 7).
func ownerTag(replica string) string {
	sum := sha256.Sum256([]byte("kubron-pf/" + replica))
	return strings.ToLower(idEncoding.EncodeToString(sum[:4]))[:tagLen]
}

// NewIDFor возвращает идентификатор потока реплики replica: метка владельца и 128 случайных бит. Пустая replica
// даёт идентификатор без метки.
func NewIDFor(replica string) string {
	if replica == "" {
		return newRandomID()
	}
	return ownerTag(replica) + newRandomID()
}

// OwnedBy сообщает, что поток id открыт на реплике replica. Пустая replica (одна реплика, десктоп) принимает любой
// идентификатор, как и строка, не похожая на идентификатор потока (проброс на localhost).
func OwnedBy(replica, id string) bool {
	if replica == "" || !ValidID(id) {
		return true
	}
	return len(id) == tagLen+26 && id[:tagLen] == ownerTag(replica)
}

func newRandomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("pfproxy: crypto/rand is unavailable: " + err.Error())
	}
	return strings.ToLower(idEncoding.EncodeToString(b[:]))
}

// ValidID сообщает, похожа ли строка на идентификатор потока.
func ValidID(id string) bool { return idRe.MatchString(id) }
