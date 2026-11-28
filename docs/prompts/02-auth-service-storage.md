# Промпт 2. Вход внешнего пользователя в `auth.Service` и хранение связи с IdP

Сначала прочитай `docs/prompts/README.md`. Промпт 1 должен быть выполнен (есть `internal/sso`).

## Задача

Научить сервис аутентификации открывать сессию пользователю, подтверждённому IdP, и сохранить связь с IdP во всех
хранилищах пользователей.

### 1. Модель

- В `auth.UserRecord` добавить `ExternalIssuer` и `ExternalSubject` (JSON `omitempty`, обратная совместимость файла
  `users.json`). Пользователь с внешней идентичностью и пустым `PasswordHash` не может войти по паролю: проверить, что
  `Login` для такой записи всегда отказывает с `ErrInvalidCredentials` (и тратит то же время, что и обычная ошибка).
- Те же поля добавить в `pkg/ext.UserRecord` (отдельный модуль, изменение только аддитивное) и в `internal/auth/ext_adapter.go`.
- Файловое хранилище: поля сохраняются автоматически; проверить round-trip и чтение старого файла без полей.
- PostgreSQL: новая миграция `internal/pgstore/migrations/` со следующим свободным номером (на момент написания — `0016`,
  пара `.up.sql`/`.down.sql`): столбцы `external_issuer`, `external_subject` в `users` и уникальный частичный индекс
  `(org_id, external_issuer, external_subject) WHERE external_subject IS NOT NULL`. Обновить `Load`, `upsertUser`,
  `InsertFirstUser`, `UpsertUser` в `internal/pgstore/users.go`. Тесты миграций и хранилища (они пропускаются без
  `KUBRON_TEST_DATABASE_URL`, но должны компилироваться и быть корректны).

### 2. `Service.LoginExternal`

Сигнатура примерно `LoginExternal(ctx, id ExternalIdentity, pol ExternalPolicy) (*UserInfo, error)`, где
`ExternalIdentity{Issuer, Subject, Username, Email, Role string, RoleMapped bool}` — уже проверенные данные из `sso.Identity`
(пакет `auth` не импортирует `internal/sso`). `ExternalPolicy{AutoCreate, LinkExisting bool, DefaultRole Role}`.

Поведение:

1. Нет ни одного пользователя → `ErrSetupRequired`.
2. Поиск по `(issuer, subject)`; при промахе — перечитать пользователей из общего хранилища один раз (как `refreshOnMiss`).
3. Не найден: если `LinkExisting` и есть локальный пользователь с таким именем **без** внешней идентичности — привязать
   (аудит: `LinkExternal`); иначе при `AutoCreate` создать пользователя (`MustChangePassword=false`, пустой хэш, роль из
   `Role` либо `DefaultRole`); иначе `ErrExternalNotProvisioned`. Конфликт имени с другим пользователем → понятная
   типизированная ошибка, без раскрытия лишнего.
4. Найден, но `Disabled` → `ErrAccountDisabled` (с записью в аудит).
5. Если `RoleMapped` и роль изменилась — обновить роль через ту же логику, что `UpdateRole`: завершить остальные
   сессии пользователя (`endUserSessionsLocked`), не понижать последнего активного администратора (`ErrLastAdmin` →
   оставить прежнюю роль и записать предупреждение в аудит).
6. Обновить `LastLoginAt` (`saveLogin`), открыть сессию (`sessionID`, `touchLocked`), аудит `Login` с пометкой `sso`;
   вернуть `toInfo`. Блокировки перебора по логину здесь не применяются (пароля нет), но защита по адресу клиента
   остаётся на уровне сервера.
7. Вся работа под `s.mu` по тем же правилам, что у `Login` (хранилище — вне блокировки там, где это уже сделано).

Добавить пакетную функцию в `internal/app` (не метод `App`, чтобы она не попала в Wails и RPC), например
`LoginSSO(v *App, id auth.ExternalIdentity, pol auth.ExternalPolicy) (*auth.UserInfo, error)`; она вызывает
`v.auth.LoginExternal` и затем `bindAuthSession`/отзыв личных данных по аналогии с `Login`. Убедиться, что
`internal/auth/policy_test.go` и `allowlist_matrix_test.go` проходят.

### 3. Тесты

Таблицы для всех веток выше: создание, повторный вход, привязка по имени (включена/выключена), коллизия имени,
отключённый пользователь, смена роли, последний администратор, пустая система, пароль внешнего пользователя, round-trip
в файловом хранилище, гонка двух одновременных первых входов одного пользователя (`-race`; ровно одна запись).

## Критерии готовности

`gofmt`, `go vet`, `go test -race ./internal/auth/... ./internal/app/... ./internal/pgstore/...` зелёные;
`i18n:check` без ошибок (новые `errors.New` есть в `backend.json` всех языков); коммит
`feat(auth): external identity sign-in and storage` с `Signed-off-by` запушен в `refactor`.
