# Промпт 3. Веб-сервер: маршруты `/auth/sso/*`, сессия, сборка

Сначала прочитай `docs/prompts/README.md`. Промпты 1 и 2 выполнены.

## Задача

Подключить SSO к `kubron-server`.

### 1. Конфиг

В `internal/server/config.go` добавить поле `SSO sso.Config`, заполняемое `sso.LoadConfig(getenv)` в `LoadConfig`;
ошибки конфигурации оборачивать префиксом `KUBRON_OIDC`. `Validate`: при включённом SSO обязателен `KUBRON_PUBLIC_URL`
(из него строится redirect URI `<PUBLIC_URL>/auth/sso/callback`); при `DISABLE_LOCAL_LOGIN` SSO обязан быть включён.
Секреты не логировать.

### 2. HTTP-слой (`internal/server`)

По образцу `web_oidc_callback.go` и `WithWebOIDCCallback`: новая опция `WithSSO(...)` с функциональными зависимостями
(без импорта `internal/app` в пакет `server`).

- `GET /auth/sso/login`: ограничение частоты по адресу клиента (`g.limitAnonymous`), `sso.Begin`, запись `FlowState` в cookie
  `__Host-kubron_sso` (по HTTP/loopback — `kubron_sso`): `HttpOnly`, `SameSite=Lax` (нужно для возврата с IdP), `Path=/`,
  `Max-Age=600`, `Secure` по `g.secure(r)`; ответ 302 на IdP с `Cache-Control: no-store`. Параметр `return` (если нужен)
  принимать только как относительный путь, начинающийся с одного `/`; иначе игнорировать (защита от open redirect).
- `GET /auth/sso/callback`: лимит длины query, ровно одно значение `state`/`code`, чтение и сразу сброс cookie,
  `sso.Complete`, затем `NewView` (новый вид), вход через `app.LoginSSO`, `Sessions.Open`, `Bind`, `setCookie` сессии,
  302 на `/`. При любой ошибке — освободить вид (`dropView`), вернуть 302 на `/?sso_error=<код>` с коротким машинным кодом
  (`denied`, `expired`, `not_provisioned`, `disabled`, `unavailable`), без текстов ошибок IdP. Ошибки хранилища сессий —
  `503` + `Retry-After`, как в RPC.
- Заголовки: `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`.
- Публичный RPC `Login` при `DISABLE_LOCAL_LOGIN` отклоняется кодом `403 local_login_disabled` до диспетчеризации
  (`SetupAdmin` остаётся). Добавить код в `MapError`/документацию контракта при необходимости.
- `AuthStatus` в `afterCall` дополнять полем `sso` (`{enabled, label, loginUrl, localLoginDisabled}`), как уже делается
  для `SetupTokenRequired`: поле добавить в `auth.Status` как необязательное (`omitempty`), заполняет только серверный слой.
  Сессия Kubron после SSO-входа — обычная: CSRF, простой, ротация, завершение — без изменений.
- Аудит: событие входа через SSO записывается ядром (см. промпт 2); отказы на уровне HTTP — в технический журнал
  (`slog`, без секретов и без `code`/`id_token`).

### 3. Сборка в `cmd/kubron-server/main.go`

Создать `sso.Provider` при включённой настройке (ленивый discovery не блокирует старт), связать с `app.LoginSSO`,
ключом `FlowState` и `Guard`. Для нескольких реплик ключ выводится из `KUBRON_SECRET_KEY`; без него — предупреждение
в журнале (одна реплика).

### 4. Тесты

Сквозной тест на `httptest` с поддельным IdP: login → redirect → callback → cookie сессии → `AuthStatus` с пользователем.
Негативные: нет/чужая cookie, повтор callback (одноразовость), подмена `state`, истёкший flow, отключённый пользователь,
пустая система, `return` с внешним адресом, `Login` при `DISABLE_LOCAL_LOGIN`, лимит частоты. Тест, что в ответах и журнале
нет `code`, токенов и секрета клиента (контрольная строка).

## Критерии готовности

`gofmt`, `go vet`, `go test -race ./internal/server/... ./cmd/...` зелёные; ручная проверка на локальном IdP
(например, Keycloak в контейнере) описана в PR-описании; коммит `feat(server): OIDC sign-in routes` с `Signed-off-by`
запушен в `refactor`.
