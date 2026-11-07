// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package gke

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"strings"
)

const (
	// MaxKeyBytes bounds the key file: a real service account key is about 2.4 KiB.
	MaxKeyBytes = 16 << 10

	maxEmailLen = 256
	maxKeyIDLen = 128

	typeServiceAccount = "service_account"
)

// Kinds of Google credential files that are valid but that this provider does not handle. The Broker
// falls back to another way of signing in for them.
var unsupportedTypes = map[string]bool{
	"authorized_user":                  true,
	"external_account":                 true, // workload identity federation
	"external_account_authorized_user": true,
	"impersonated_service_account":     true,
	"gdch_service_account":             true,
}

// key is a parsed service account key. Only the fields that the provider uses are kept: token_uri, the
// project and every other field of the file are dropped on purpose.
type key struct {
	email      string
	keyID      string
	privateKey string
}

func (key) String() string   { return "[redacted service account key]" }
func (key) GoString() string { return "[redacted service account key]" }

// keyFile is the part of a service account key file that is read.
type keyFile struct {
	Type         string `json:"type"`
	ClientEmail  string `json:"client_email"`
	PrivateKeyID string `json:"private_key_id,omitempty"`
	PrivateKey   string `json:"private_key"`
}

// parseKey reads a service account key. Anything that is not such a key is credential.ErrInvalidAnswer,
// a Google credential of another kind is credential.ErrUnsupported. No error carries a part of the input.
func parseKey(raw []byte) (*key, error) {
	if len(raw) > MaxKeyBytes {
		return nil, invalid(errKeyTooLarge)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, invalid(errKeyJSON)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var f keyFile
	if err := dec.Decode(&f); err != nil {
		return nil, invalid(errKeyJSON) // err is dropped: its text can quote the input
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) { // data after the document
		return nil, invalid(errKeyJSON)
	}
	switch {
	case f.Type == typeServiceAccount:
	case unsupportedTypes[f.Type]:
		return nil, unsupported(errKeyUnsupported)
	default:
		return nil, invalid(errKeyType)
	}
	if !validEmail(f.ClientEmail) || !validKeyID(f.PrivateKeyID) {
		return nil, invalid(errKeyEmail)
	}
	if !validPrivateKey(f.PrivateKey) {
		return nil, invalid(errKeyPrivate)
	}
	return &key{email: f.ClientEmail, keyID: f.PrivateKeyID, privateKey: f.PrivateKey}, nil
}

// normalized is the data that is saved: the key without token_uri and the other fields. It is itself a
// valid service account key file, so parseKey reads it back. The Broker encrypts it.
func (k *key) normalized() []byte {
	b, err := json.Marshal(keyFile{Type: typeServiceAccount, ClientEmail: k.email, PrivateKeyID: k.keyID, PrivateKey: k.privateKey})
	if err != nil { // cannot happen: only strings are marshalled
		return []byte{}
	}
	return b
}

// validEmail accepts the shape of a service account address: printable ASCII, one "@", no spaces. The value
// becomes the iss claim and the audit name, so control characters must not get through.
func validEmail(s string) bool {
	if s == "" || len(s) > maxEmailLen || strings.Count(s, "@") != 1 || s[0] == '@' || s[len(s)-1] == '@' {
		return false
	}
	return printableASCII(s)
}

func validKeyID(s string) bool { return len(s) <= maxKeyIDLen && printableASCII(s) }

func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= 0x20 || s[i] >= 0x7f {
			return false
		}
	}
	return true
}

// validPrivateKey checks that the value is an unencrypted RSA key in a PEM block (PKCS#8 or PKCS#1), which
// is what Google issues. Checking it here turns a damaged key into a local error, without a request.
func validPrivateKey(s string) bool {
	block, _ := pem.Decode([]byte(s))
	if block == nil {
		return false
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		_, ok := k.(*rsa.PrivateKey)
		return ok
	}
	_, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	return err == nil
}
