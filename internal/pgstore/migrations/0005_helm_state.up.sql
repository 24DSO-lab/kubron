-- Состояние Helm в сервисном режиме (ADR-0015): репозитории, OCI-реестры и журнал операций, область — команда.
-- Файлы helm/ (repositories.yaml, oci.yaml, registry-config.json, operations.json) в сервисе не используются.

-- Репозитории обоих видов в одной таблице: имя уникально в команде независимо от вида. Пароль репозитория или
-- реестра хранится только зашифрованным (secretbox, AAD «helm/<team>/<name>»); secret_key_id указывает ключ,
-- которым он зашифрован, как у секретов кластеров (0003).
CREATE TABLE helm_repositories (
    team_id       text        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    name          text        NOT NULL CHECK (name ~ '^[a-z0-9]([a-z0-9_.-]{0,61}[a-z0-9])?$'),
    kind          text        NOT NULL CHECK (kind IN ('http', 'oci')),
    url           text        NOT NULL,
    username      text        NOT NULL DEFAULT '',
    -- insecureTLS (http), plainHTTP и charts (oci)
    options       jsonb       NOT NULL DEFAULT '{}',
    secret_enc    bytea,
    secret_key_id text        NOT NULL DEFAULT '',
    created_by    text        REFERENCES users (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, name),
    CONSTRAINT helm_repositories_secret_key CHECK (secret_enc IS NULL OR secret_key_id <> '')
);

-- Журнал операций Helm: последние 200 на команду. Запись целиком лежит в record (json), в колонки вынесено то,
-- по чему выбирают. Журнал переживает кластер: cluster_id обнуляется при его удалении, имя остаётся в записи.
CREATE TABLE helm_operations (
    team_id     text        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    id          text        NOT NULL,
    cluster_id  text        REFERENCES clusters (id) ON DELETE SET NULL,
    finished_at timestamptz NOT NULL,
    record      jsonb       NOT NULL,
    PRIMARY KEY (team_id, id)
);
CREATE INDEX helm_operations_recent_idx ON helm_operations (team_id, finished_at DESC);
