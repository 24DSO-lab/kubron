// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"encoding/binary"

	"github.com/24DSO-lab/kubron/internal/secrets"
)

// LegacyTeamAAD is the associated data of the task 3.7 oidc-password secrets (clusters.secret_enc), which
// MigrateLegacyOIDC copies into the team scope without touching the ciphertext.
func LegacyTeamAAD(clusterID string) []byte { return []byte("cluster/" + clusterID + "/secret") }

// Sealer encrypts and decrypts records for the stores. The format of a stored record is stated explicitly
// (Meta.EncFormat) and never guessed from the bytes. Everything the Sealer writes is EncEnvelopeV1.
type Sealer struct {
	env *secrets.Envelope
}

// NewSealer returns a Sealer on top of env.
func NewSealer(env *secrets.Envelope) *Sealer { return &Sealer{env: env} }

// principalAADMagic starts the associated data of every technical record: the format marker and version. Legacy
// associated data starts with "cred/" (secrets.AAD), so the two encodings can never be equal.
const principalAADMagic = "kubron.cred.principal.v1\x00"

// aad returns the associated data of a record. Legacy keys keep the exact historical bytes (secrets.AAD). A technical
// key gets a distinct canonical encoding: the marker, then ClusterID, Scope, Principal.Kind, Principal.ID, OrgID,
// TeamID (empty for a Service principal) and BindingID, each as a 4-byte big-endian length followed by the bytes, so
// no concatenation of different values can give the same output.
func aad(k Key) []byte {
	if k.Scope != ScopePrincipal {
		return secrets.AAD(k.ClusterID, string(k.Scope), k.OwnerID)
	}
	b := []byte(principalAADMagic)
	for _, f := range [...]string{k.ClusterID, string(k.Scope), string(k.Principal.Kind), k.Principal.ID,
		k.Principal.OrgID, k.Principal.TeamID, k.BindingID} {
		b = binary.BigEndian.AppendUint32(b, uint32(len(f)))
		b = append(b, f...)
	}
	return b
}

// Seal encrypts the secret of a record in the envelope format.
func (s *Sealer) Seal(k Key, plaintext []byte) (blob []byte, keyID string, err error) {
	if err := k.ValidateAny(); err != nil {
		return nil, "", err
	}
	if len(plaintext) > MaxSecretSize {
		return nil, "", ErrSecretTooLarge
	}
	return s.env.Seal(plaintext, aad(k))
}

// Open decrypts a stored blob according to its stated format. The legacy format is accepted for the
// team scope only and is checked strictly against LegacyTeamAAD: there is no retry without it and no
// fallback to the envelope reader. A blob that cannot be read is an error, never an empty value.
func (s *Sealer) Open(k Key, blob []byte, keyID, format string) ([]byte, error) {
	if err := k.ValidateAny(); err != nil {
		return nil, err
	}
	switch format {
	case EncEnvelopeV1:
		return s.env.Open(blob, aad(k))
	case EncLegacyV1:
		if k.Scope != ScopeTeam {
			return nil, ErrLegacyNotAllowed
		}
		return s.env.OpenLegacy(blob, keyID, LegacyTeamAAD(k.ClusterID))
	default:
		return nil, ErrUnknownEncFormat
	}
}

// Rewrap brings a stored blob to the current master key and the envelope format. changed is false when
// the record is already current. A legacy record is opened strictly and sealed again.
func (s *Sealer) Rewrap(k Key, blob []byte, keyID, format string) (out []byte, outKeyID string, changed bool, err error) {
	if format == EncEnvelopeV1 {
		if err := k.ValidateAny(); err != nil {
			return nil, "", false, err
		}
		return s.env.Rewrap(blob, aad(k))
	}
	plain, err := s.Open(k, blob, keyID, format)
	if err != nil {
		return nil, "", false, err
	}
	out, outKeyID, err = s.Seal(k, plain)
	clear(plain)
	if err != nil {
		return nil, "", false, err
	}
	return out, outKeyID, true, nil
}
