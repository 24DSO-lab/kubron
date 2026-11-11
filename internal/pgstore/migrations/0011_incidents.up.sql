-- History of investigations of Kubron Intelligence (ADR-0024, ADR-0026 section 4).
-- Every record carries the tenant scope (organization, team, cluster) in its key and every query filters by it.
-- There are no foreign keys: the history must outlive the catalog entry of a cluster, and rows are removed by
-- the retention job. Organization/team/cluster ids are text, like everywhere else.

-- One document per investigation (changed consistently as a whole). summary keeps the history row, so a page of
-- history does not read the documents. version is the optimistic lock.
CREATE TABLE incidents (
    org_id     text        NOT NULL CHECK (org_id <> ''),
    team_id    text        NOT NULL CHECK (team_id <> ''),
    cluster_id text        NOT NULL CHECK (cluster_id <> ''),
    id         text        NOT NULL CHECK (id <> ''),
    status     text        NOT NULL CHECK (status IN ('open', 'closed')),
    opened_at  timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    version    bigint      NOT NULL CHECK (version >= 1),
    summary    jsonb       NOT NULL,
    doc        jsonb       NOT NULL,
    PRIMARY KEY (org_id, team_id, cluster_id, id)
);
CREATE INDEX incidents_history_idx ON incidents (org_id, team_id, cluster_id, opened_at DESC, (id COLLATE "C") DESC);
CREATE INDEX incidents_updated_idx ON incidents (org_id, updated_at);

-- Log fragments used as evidence. The text is redacted by the application and, in addition, encrypted with the
-- master key ring (ADR-0008): text_enc is a secretbox ciphertext bound to the row by its AAD, key_id is the key
-- that encrypted it (Rekey and the start-up key check read this table). meta holds everything except the text.
CREATE TABLE incident_fragments (
    org_id      text        NOT NULL CHECK (org_id <> ''),
    team_id     text        NOT NULL CHECK (team_id <> ''),
    cluster_id  text        NOT NULL CHECK (cluster_id <> ''),
    incident_id text        NOT NULL CHECK (incident_id <> ''),
    id          text        NOT NULL CHECK (id <> ''),
    meta        jsonb       NOT NULL,
    text_enc    bytea       NOT NULL,
    key_id      text        NOT NULL CHECK (key_id <> ''),
    created_at  timestamptz NOT NULL,
    PRIMARY KEY (org_id, team_id, cluster_id, incident_id, id)
);
CREATE INDEX incident_fragments_created_idx ON incident_fragments (org_id, created_at);
CREATE INDEX incident_fragments_key_idx ON incident_fragments (key_id);

-- Storage settings of an organization (log evidence switch, excluded namespaces, retention).
CREATE TABLE incident_settings (
    org_id     text        PRIMARY KEY CHECK (org_id <> ''),
    settings   jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
