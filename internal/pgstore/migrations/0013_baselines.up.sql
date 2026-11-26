-- Baseline конфигурации и результаты проверок дрейфа (ADR-0027). Область (организация, команда, кластер) входит в
-- первичный ключ каждой таблицы: запрос другой области не видит строк. Идентификаторы — текст, как в файловом
-- хранилище десктопа.
CREATE TABLE baselines (
    org_id            text        NOT NULL CHECK (org_id <> ''),
    team_id           text        NOT NULL CHECK (team_id <> ''),
    cluster_id        text        NOT NULL CHECK (cluster_id <> ''),
    id                text        NOT NULL CHECK (id <> ''),
    name              text        NOT NULL CHECK (name <> ''),
    namespaces        jsonb       NOT NULL DEFAULT '[]',
    selector          text        NOT NULL DEFAULT '',
    kinds             jsonb       NOT NULL DEFAULT '[]',
    refs              jsonb       NOT NULL DEFAULT '[]',
    active_version_id text        NOT NULL DEFAULT '',
    created_by        text        NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL,
    PRIMARY KEY (org_id, team_id, cluster_id, id)
);
CREATE INDEX baselines_cluster_idx ON baselines (org_id, team_id, cluster_id, created_at, id);

-- Версия неизменяема. Содержимое (соль HMAC и нормализованные объекты) хранится только зашифрованным (secretbox):
-- content_enc, key_id; AAD привязывает шифротекст к области, baseline и версии.
CREATE TABLE baseline_versions (
    org_id        text        NOT NULL,
    team_id       text        NOT NULL,
    cluster_id    text        NOT NULL,
    baseline_id   text        NOT NULL,
    id            text        NOT NULL CHECK (id <> ''),
    seq           integer     NOT NULL CHECK (seq >= 1),
    rules_version integer     NOT NULL,
    source        text        NOT NULL,
    author        text        NOT NULL DEFAULT '',
    comment       text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL,
    object_count  integer     NOT NULL CHECK (object_count >= 0),
    content_enc   bytea       NOT NULL,
    key_id        text        NOT NULL CHECK (key_id <> ''),
    PRIMARY KEY (org_id, team_id, cluster_id, baseline_id, id),
    UNIQUE (org_id, team_id, cluster_id, baseline_id, seq),
    FOREIGN KEY (org_id, team_id, cluster_id, baseline_id)
        REFERENCES baselines (org_id, team_id, cluster_id, id) ON DELETE CASCADE
);
CREATE INDEX baseline_versions_key_idx ON baseline_versions (key_id);

-- Исключения редактируются, поэтому живут отдельно от неизменяемой версии: один список на версию.
CREATE TABLE baseline_exceptions (
    org_id      text  NOT NULL,
    team_id     text  NOT NULL,
    cluster_id  text  NOT NULL,
    baseline_id text  NOT NULL,
    version_id  text  NOT NULL,
    items       jsonb NOT NULL DEFAULT '[]',
    PRIMARY KEY (org_id, team_id, cluster_id, baseline_id, version_id),
    FOREIGN KEY (org_id, team_id, cluster_id, baseline_id, version_id)
        REFERENCES baseline_versions (org_id, team_id, cluster_id, baseline_id, id) ON DELETE CASCADE
);

-- Результаты проверок. Значений чувствительных полей в них нет (только факт изменения и имя ключа).
CREATE TABLE baseline_results (
    org_id        text        NOT NULL,
    team_id       text        NOT NULL,
    cluster_id    text        NOT NULL,
    baseline_id   text        NOT NULL,
    id            text        NOT NULL CHECK (id <> ''),
    checked_at    timestamptz NOT NULL,
    version_id    text        NOT NULL,
    initiator     text        NOT NULL DEFAULT '',
    rules_version integer     NOT NULL,
    summary       jsonb       NOT NULL,
    body          jsonb       NOT NULL,
    PRIMARY KEY (org_id, team_id, cluster_id, baseline_id, id),
    FOREIGN KEY (org_id, team_id, cluster_id, baseline_id)
        REFERENCES baselines (org_id, team_id, cluster_id, id) ON DELETE CASCADE
);
CREATE INDEX baseline_results_time_idx ON baseline_results (org_id, team_id, cluster_id, baseline_id, checked_at DESC, id DESC);
CREATE INDEX baseline_results_retention_idx ON baseline_results (checked_at);
