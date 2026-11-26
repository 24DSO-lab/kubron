-- Upgrade Readiness history (ADR-0025 §6, ADR-0026 §4). One row is one self-contained report document:
-- the tenant scope (org, team, cluster) is part of the key and of every query, so a foreign scope sees nothing.
-- The report holds no Secret values and nothing that needs the envelope key (ADR-0008): it is plain JSON.
-- Summary columns are derived from the document on write and serve the history list without reading the JSON.
-- No foreign keys: a report of a removed cluster is dropped by the retention job like any other old report.
CREATE TABLE readiness_reports (
    org_id          text        NOT NULL CHECK (org_id <> ''),
    team_id         text        NOT NULL CHECK (team_id <> ''),
    cluster_id      text        NOT NULL CHECK (cluster_id <> ''),
    id              text        NOT NULL CHECK (id ~ '^[A-Za-z0-9_-]{8,64}$'),
    created_at      timestamptz NOT NULL,
    initiator       text        NOT NULL DEFAULT '',
    current_version text        NOT NULL,
    target_version  text        NOT NULL DEFAULT '',
    blockers        integer     NOT NULL CHECK (blockers >= 0),
    warnings        integer     NOT NULL CHECK (warnings >= 0),
    infos           integer     NOT NULL CHECK (infos >= 0),
    not_checked     integer     NOT NULL CHECK (not_checked >= 0),
    schema_version  integer     NOT NULL CHECK (schema_version >= 1),
    report          jsonb       NOT NULL,
    PRIMARY KEY (org_id, team_id, cluster_id, id)
);
CREATE INDEX readiness_reports_history_idx ON readiness_reports (org_id, team_id, cluster_id, created_at DESC, id DESC);
CREATE INDEX readiness_reports_created_idx ON readiness_reports (created_at);
