-- Problems group investigations that share a source (ADR-0033 section 6). A problem stores only the key and the
-- member list, never the data of investigations. Every record carries the tenant scope (organization, team,
-- cluster) in its key and every query filters by it. There are no foreign keys: members are referenced by id inside
-- the document, a vanished investigation is skipped on read, and rows are removed by the retention job.

-- One document per problem (changed consistently as a whole). status, key and updated_at are kept in columns for
-- the list query and retention; version is the optimistic lock.
CREATE TABLE incident_problems (
    org_id     text        NOT NULL CHECK (org_id <> ''),
    team_id    text        NOT NULL CHECK (team_id <> ''),
    cluster_id text        NOT NULL CHECK (cluster_id <> ''),
    id         text        NOT NULL CHECK (id <> ''),
    status     text        NOT NULL CHECK (status IN ('open', 'closed')),
    key        text        NOT NULL,
    updated_at timestamptz NOT NULL,
    version    bigint      NOT NULL CHECK (version >= 1),
    doc        jsonb       NOT NULL,
    PRIMARY KEY (org_id, team_id, cluster_id, id)
);
CREATE INDEX incident_problems_status_idx ON incident_problems (org_id, team_id, cluster_id, status, updated_at DESC, (id COLLATE "C") DESC);
CREATE INDEX incident_problems_list_idx ON incident_problems (org_id, team_id, cluster_id, updated_at DESC, (id COLLATE "C") DESC);
CREATE INDEX incident_problems_updated_idx ON incident_problems (org_id, updated_at);
