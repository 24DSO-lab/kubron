// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/24DSO-lab/kubron/internal/secretbox"
)

// Record layout (all integers big endian):
//
//	"KBE" 0x01 | len(kekID) u8 | kekID | len(wrapped) u16 | wrapped | data
//
// wrapped is nonce ‖ AES-GCM(KEK, DEK, aad); data is nonce ‖ AES-GCM(DEK, plaintext, aad).
const (
	envelopeVersion = 1
	dekSize         = secretbox.KeySize
	maxWrappedLen   = 1 << 10
)

var envelopeMagic = []byte{'K', 'B', 'E', envelopeVersion}

// AAD builds the additional authenticated data that binds a record to its cluster, scope and owner.
// The cluster id never contains a slash (it is restricted by the database), so the layout is unambiguous.
func AAD(clusterID, scope, ownerID string) []byte {
	return []byte("cred/" + clusterID + "/" + scope + "/" + ownerID)
}

// Envelope seals and opens records with a fresh data key per record.
type Envelope struct {
	kek *secretbox.Keyring
}

// NewEnvelope returns an Envelope whose data keys are wrapped with kek (current key) and unwrapped
// with any key of the ring (rotation).
func NewEnvelope(kek *secretbox.Keyring) (*Envelope, error) {
	if kek == nil {
		return nil, ErrNoMasterKey
	}
	return &Envelope{kek: kek}, nil
}

// CurrentKeyID is the identifier of the master key that protects new records.
func (e *Envelope) CurrentKeyID() string { return e.kek.CurrentID() }

// IsEnvelope reports whether blob carries the envelope header. A legacy secretbox value can collide with
// the header by chance (probability 2^-32); callers that accept both formats must fall back to the legacy
// reader when Open fails.
func IsEnvelope(blob []byte) bool { return bytes.HasPrefix(blob, envelopeMagic) }

// Seal encrypts plaintext and returns the record together with the identifier of the master key used.
func (e *Envelope) Seal(plaintext, aad []byte) (blob []byte, keyID string, err error) {
	dek := make([]byte, dekSize)
	defer wipe(dek)
	if _, err := rand.Read(dek); err != nil {
		return nil, "", fmt.Errorf("random source unavailable: %w", err)
	}
	wrapped, kekID, err := e.kek.Seal(dek, aad)
	if err != nil {
		return nil, "", err
	}
	aead, err := newGCM(dek)
	if err != nil {
		return nil, "", err
	}
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(plaintext)+aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, "", fmt.Errorf("random source unavailable: %w", err)
	}
	data := aead.Seal(nonce, nonce, plaintext, aad)

	out := make([]byte, 0, len(envelopeMagic)+1+len(kekID)+2+len(wrapped)+len(data))
	out = append(out, envelopeMagic...)
	out = append(out, byte(len(kekID)))
	out = append(out, kekID...)
	out = binary.BigEndian.AppendUint16(out, uint16(len(wrapped)))
	out = append(out, wrapped...)
	out = append(out, data...)
	return out, kekID, nil
}

type parsed struct {
	kekID   string
	wrapped []byte
	data    []byte
}

// parse splits a record. Anything malformed after the magic is secretbox.ErrDecrypt: the caller must not
// treat damaged data as "empty".
func parse(blob []byte) (parsed, error) {
	if !IsEnvelope(blob) {
		return parsed{}, ErrNotEnvelope
	}
	b := blob[len(envelopeMagic):]
	if len(b) < 1 {
		return parsed{}, secretbox.ErrDecrypt
	}
	idLen := int(b[0])
	b = b[1:]
	if idLen == 0 || len(b) < idLen+2 {
		return parsed{}, secretbox.ErrDecrypt
	}
	id := string(b[:idLen])
	b = b[idLen:]
	wlen := int(binary.BigEndian.Uint16(b))
	b = b[2:]
	if wlen == 0 || wlen > maxWrappedLen || len(b) < wlen {
		return parsed{}, secretbox.ErrDecrypt
	}
	return parsed{kekID: id, wrapped: b[:wlen], data: b[wlen:]}, nil
}

// KeyID returns the identifier of the master key that wraps the record's data key.
func KeyID(blob []byte) (string, error) {
	p, err := parse(blob)
	if err != nil {
		return "", err
	}
	return p.kekID, nil
}

// NeedsRotation reports whether the record is wrapped with a key other than the current one.
func (e *Envelope) NeedsRotation(blob []byte) bool {
	id, err := KeyID(blob)
	return err != nil || e.kek.NeedsRotation(id)
}

func (e *Envelope) unwrap(p parsed, aad []byte) ([]byte, error) {
	dek, err := e.kek.Open(p.wrapped, p.kekID, aad)
	if err != nil {
		return nil, err
	}
	if len(dek) != dekSize {
		wipe(dek)
		return nil, secretbox.ErrDecrypt
	}
	return dek, nil
}

// Open decrypts a record sealed with the same aad. Corrupted, truncated or foreign data returns
// secretbox.ErrDecrypt; an unknown master key returns secretbox.ErrUnknownKey; a non-envelope value
// returns ErrNotEnvelope.
func (e *Envelope) Open(blob, aad []byte) ([]byte, error) {
	p, err := parse(blob)
	if err != nil {
		return nil, err
	}
	dek, err := e.unwrap(p, aad)
	if err != nil {
		return nil, err
	}
	defer wipe(dek)
	aead, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	n := aead.NonceSize()
	if len(p.data) < n+aead.Overhead() {
		return nil, secretbox.ErrDecrypt
	}
	plain, err := aead.Open(nil, p.data[:n], p.data[n:], aad)
	if err != nil {
		return nil, secretbox.ErrDecrypt
	}
	return plain, nil
}

// OpenLegacy reads a value sealed by secretbox directly with a master key (task 3.7 oidc-password secrets).
func (e *Envelope) OpenLegacy(ciphertext []byte, keyID string, aad []byte) ([]byte, error) {
	return e.kek.Open(ciphertext, keyID, aad)
}

// Rewrap re-wraps the data key of the record with the current master key. Only the small wrapped key is
// re-encrypted; the data part is left as is. changed is false when the record already uses the current key.
func (e *Envelope) Rewrap(blob, aad []byte) (out []byte, keyID string, changed bool, err error) {
	p, err := parse(blob)
	if err != nil {
		return nil, "", false, err
	}
	if !e.kek.NeedsRotation(p.kekID) {
		// Still prove that the record belongs to aad: a rekey must not bless damaged data.
		if _, err := e.Open(blob, aad); err != nil {
			return nil, "", false, err
		}
		return blob, p.kekID, false, nil
	}
	dek, err := e.unwrap(p, aad)
	if err != nil {
		return nil, "", false, err
	}
	defer wipe(dek)
	wrapped, kekID, err := e.kek.Seal(dek, aad)
	if err != nil {
		return nil, "", false, err
	}
	out = make([]byte, 0, len(envelopeMagic)+1+len(kekID)+2+len(wrapped)+len(p.data))
	out = append(out, envelopeMagic...)
	out = append(out, byte(len(kekID)))
	out = append(out, kekID...)
	out = binary.BigEndian.AppendUint16(out, uint16(len(wrapped)))
	out = append(out, wrapped...)
	out = append(out, p.data...)
	if _, err := e.Open(out, aad); err != nil { // never write back something we cannot read
		return nil, "", false, err
	}
	return out, kekID, true, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func wipe(b []byte) { clear(b) }

// sanitizeID keeps log lines single-line.
func sanitizeID(s string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(s) }
