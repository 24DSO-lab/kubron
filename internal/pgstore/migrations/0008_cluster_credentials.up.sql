-- Sign-in data of clusters (ADR-0018, A.1): one encrypted record per cluster and owner.
-- owner_kind 'user' keeps personal data (owner_id = user id), 'team' keeps shared data (owner_id = team id).
-- enc_format states how secret_enc is encrypted: 'envelope-v1' (internal/secrets) for everything written by
-- the application, 'legacy-v1' (secretbox, AAD cluster/<id>/secret) only for rows copied from
-- clusters.secret_enc by MigrateLegacyOIDC. The copy is done by the application, not by this migration, so
-- that it can be repeated until the cut-over; clusters.secret_enc is never modified or removed here.
CREATE TABLE cluster_credentials (
    cluster_id    text        NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    owner_kind    text        NOT NULL CHECK (owner_kind IN ('user', 'team')),
    owner_id      text        NOT NULL CHECK (owner_id <> ''),
    kind          text        NOT NULL CHECK (kind <> ''),
    secret_enc    bytea       NOT NULL,
    secret_key_id text        NOT NULL CHECK (secret_key_id <> ''),
    enc_format    text        NOT NULL DEFAULT 'envelope-v1' CHECK (enc_format IN ('envelope-v1', 'legacy-v1')),
    principal     text        NOT NULL DEFAULT '',
    expires_at    timestamptz,
    version       bigint      NOT NULL DEFAULT 1 CHECK (version >= 1),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, owner_kind, owner_id),
    CONSTRAINT cluster_credentials_legacy_team CHECK (enc_format <> 'legacy-v1' OR owner_kind = 'team')
);
CREATE INDEX cluster_credentials_owner_idx ON cluster_credentials (owner_kind, owner_id);
CREATE INDEX cluster_credentials_key_idx ON cluster_credentials (secret_key_id);
