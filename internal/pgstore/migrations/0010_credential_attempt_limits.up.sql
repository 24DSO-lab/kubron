-- Failed interactive credential answers are shared across replicas. Keys are SHA-256 digests;
-- no user identifiers or identity-provider URLs are stored in this table.
CREATE TABLE credential_attempt_limits (
    dimension     text        NOT NULL CHECK (dimension IN ('user', 'idp')),
    key_hash      text        NOT NULL CHECK (key_hash <> ''),
    failures      integer     NOT NULL CHECK (failures >= 0),
    window_started timestamptz NOT NULL,
    locked_until  timestamptz,
    updated_at    timestamptz NOT NULL,
    PRIMARY KEY (dimension, key_hash)
);
CREATE INDEX credential_attempt_limits_updated_idx ON credential_attempt_limits (updated_at);
