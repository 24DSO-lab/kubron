# Промпт 1. ADR-0059 и ядро SSO (`internal/sso`)

Сначала прочитай `docs/prompts/README.md` (общий контекст и правила) и выполни их.

## Задача

Напиши ADR и общее ядро OIDC-входа без привязки к HTTP-слою и к хранилищу пользователей.

### 1. ADR-0059

`docs/adr/0059-oidc-sign-in.md` (формат как у соседних ADR: статус, дата, связано, контекст, решение, рассмотренные
альтернативы, последствия). ADR-0011 не править: новый ADR реализует его и на него ссылается. Зафиксируй решения из
раздела «Принятые решения» README. Альтернативы: жёсткая привязка к Keycloak; SAML; автопривязка по email/имени;
хранение состояния входа в БД (отвергнуто: лишняя зависимость для режима без PostgreSQL).
Добавь строку в `docs/adr/README.md`.

### 2. Пакет `internal/sso`

- `Config` и `LoadConfig(getenv func(string) string) (Config, error)`:
  `KUBRON_OIDC_ISSUER`, `KUBRON_OIDC_CLIENT_ID`, `KUBRON_OIDC_CLIENT_SECRET` (только окружение), `KUBRON_OIDC_SCOPES`
  (по умолчанию `openid profile email`), `KUBRON_OIDC_USERNAME_CLAIM` (по умолчанию `preferred_username`, запасной `email`),
  `KUBRON_OIDC_GROUPS_CLAIM` (по умолчанию `groups`), `KUBRON_OIDC_ADMIN_GROUPS`, `KUBRON_OIDC_OPERATOR_GROUPS`,
  `KUBRON_OIDC_DEFAULT_ROLE`, `KUBRON_OIDC_AUTO_CREATE` (по умолчанию включено), `KUBRON_OIDC_LINK_EXISTING`,
  `KUBRON_OIDC_DISABLE_LOCAL_LOGIN`, `KUBRON_OIDC_BUTTON_LABEL`, `KUBRON_OIDC_CA_FILE` (PEM корневого CA провайдера).
  Пустой `ISSUER` означает «SSO выключен». Валидация: issuer — https URL без query и fragment (http допустим только
  для loopback и только в десктоп-режиме); `CLIENT_ID` обязателен при заданном issuer; роль по умолчанию — известная.
- `Provider`: ленивая инициализация discovery через `oidc.NewProvider` с HTTP-клиентом из `idpnet.NewClient`
  (`oidc.ClientContext`), с повтором при временной недоступности IdP, без блокировки старта сервера.
- `Begin(redirectURL string) (authURL string, flow FlowState, err error)`: генерирует `state`, `nonce`, PKCE verifier
  (S256), собирает URL авторизации через `oauth2.Config`.
- `Complete(ctx, flow FlowState, query url.Values) (Identity, error)`: сверяет `state` (постоянное время), параметр `iss`
  (RFC 9207), обрабатывает `error`/`error_description` IdP, обменивает code (с verifier), проверяет ID-токен
  (`oidc.Verifier` с `ClientID`), `nonce`, достаёт claims.
- `Identity{Issuer, Subject, Username, Email, EmailVerified, Name, Groups []string}`; имя пользователя нормализуется
  под правила `auth.ValidateUsername` (допустимые символы, длина 3–32); если из claim получить допустимое имя нельзя —
  типизированная ошибка.
- `FlowState`: структура + `Seal(key) (string, error)` / `Open(key, value) (FlowState, error)` на AES-GCM (ключ получить
  выводом из мастер-ключа `KUBRON_SECRET_KEY` через HKDF с отдельным контекстом; без ключа — случайный ключ процесса,
  допустим только для одной реплики, это нужно явно отразить в документации). Срок жизни 10 минут, проверяется при `Open`.
- `RoleFor(groups []string) (role string, mapped bool)` по настройкам групп.
- Ошибки типизированные (`ErrStateMismatch`, `ErrIssuerMismatch`, `ErrIDToken`, `ErrNoUsername`, `ErrDisabled` и т. п.),
  тексты на английском, шаблоны в `backend.json` всех языков. Секреты (client secret, токены, code) никогда не попадают
  в ошибки и логи.

### 3. Тесты

Поддельный IdP на `httptest` (discovery, JWKS с тестовым ключом RSA/ECDSA, token endpoint). Для тестов клиент
создаётся в режиме `Desktop: true` (допускает http и loopback). Проверить: успешный вход; неверный `state`; неверный
`nonce`; чужой `aud`; просроченный токен; несовпадение `iss`; ответ IdP с `error`; подмену PKCE verifier;
истёкшее и подделанное `FlowState`; нормализацию имён; `RoleFor`; валидацию конфига. Гонки: `go test -race ./internal/sso/...`.

## Критерии готовности

`go vet`, `gofmt`, тесты пакета зелёные; `npm run i18n:check` без ошибок; заголовки лицензии на месте; ADR добавлен;
коммит `feat(sso): add OIDC core and ADR-0059` с `Signed-off-by` запушен в `refactor`.
