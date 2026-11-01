-- Веб-сессии (ADR-0016, раздел 1): общие для всех реплик сервиса. Токен из cookie не хранится, только его SHA-256.
-- Вид ядра (подключения к кластерам, терминалы) в строке не лежит: он строится заново на той реплике, что приняла запрос.
CREATE TABLE web_sessions (
    token_hash         bytea       PRIMARY KEY,
    -- Прежний хэш после ротации токена: действует ещё prev_valid_until (параллельные запросы со старой cookie).
    prev_token_hash    bytea,
    prev_valid_until   timestamptz,
    -- Публичный идентификатор сессии (адресат событий); не меняется при ротации.
    id                 text        NOT NULL UNIQUE,
    user_id            text        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    csrf               text        NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    expires_at         timestamptz NOT NULL,
    last_active_at     timestamptz NOT NULL DEFAULT now(),
    source             text        NOT NULL DEFAULT '',
    active_cluster_id  text        REFERENCES clusters (id) ON DELETE SET NULL,
    replica_id         text        NOT NULL DEFAULT '',
    CONSTRAINT web_sessions_prev_pair CHECK ((prev_token_hash IS NULL) = (prev_valid_until IS NULL))
);
CREATE INDEX web_sessions_prev_idx ON web_sessions (prev_token_hash) WHERE prev_token_hash IS NOT NULL;
CREATE INDEX web_sessions_user_idx ON web_sessions (user_id);
CREATE INDEX web_sessions_expires_idx ON web_sessions (expires_at);
