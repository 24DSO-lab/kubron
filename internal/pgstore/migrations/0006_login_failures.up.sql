-- Блокировки входа по логину (ADR-0016, раздел 5): общие для всех реплик, иначе перебор пароля можно вести
-- через разные реплики. Ключ — SHA-256 нормализованного логина (в том числе несуществующего): записи не выдают,
-- есть ли такой пользователь, и логин не хранится открыто. Счётчики по адресу клиента остаются в памяти реплики.
CREATE TABLE login_failures (
    key          bytea       PRIMARY KEY,
    count        integer     NOT NULL,
    locked_until timestamptz,
    last_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_failures_last_idx ON login_failures (last_at);
