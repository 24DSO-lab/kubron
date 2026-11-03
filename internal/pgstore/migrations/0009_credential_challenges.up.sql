-- Pending sign-in requests (ADR-0018, A.3). They live in PostgreSQL so that a request started on one replica
-- can be completed on another (the return from an identity provider may reach any replica). state_enc holds the
-- private state (descriptor with client secret, provider state) encrypted with the envelope key; the other
-- columns are routing data. The one-time completion is a compare-and-swap on version.
CREATE TABLE credential_challenges (
    id           text        PRIMARY KEY,
    cluster_id   text        NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    owner_kind   text        NOT NULL CHECK (owner_kind IN ('user', 'team')),
    owner_id     text        NOT NULL CHECK (owner_id <> ''),
    session_id   text        NOT NULL DEFAULT '',
    status       text        NOT NULL CHECK (status IN ('active', 'busy')),
    attempts     integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    locked_until timestamptz,
    expires_at   timestamptz NOT NULL,
    created_at   timestamptz NOT NULL,
    version      bigint      NOT NULL DEFAULT 1 CHECK (version >= 1),
    state_key_id text        NOT NULL CHECK (state_key_id <> ''),
    state_enc    bytea       NOT NULL
);
CREATE INDEX credential_challenges_expires_idx ON credential_challenges (expires_at);
CREATE INDEX credential_challenges_owner_idx ON credential_challenges (owner_kind, owner_id);
CREATE INDEX credential_challenges_cluster_idx ON credential_challenges (cluster_id);
