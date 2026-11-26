-- Address automatic correlation lookup by full tenant scope and key.
-- Advisory transaction locks serialize find-or-create across replicas.
CREATE INDEX incident_problems_open_key_idx
    ON incident_problems (org_id, team_id, cluster_id, key, updated_at DESC, (id COLLATE "C") DESC)
    WHERE status = 'open';
