# Промпт 4. Фронтенд: кнопка SSO, состояние, локализация

Сначала прочитай `docs/prompts/sso/README.md`, затем `docs/i18n.md` и ADR-0014. Промпты 1–3 выполнены.

## Задача

Добавить вход через SSO на экран входа веб-версии.

- `frontend/src/types/auth.ts`: тип `SsoInfo` (`enabled`, `label`, `loginUrl`, `localLoginDisabled`); `useAuth.ts` читает его
  из `AuthStatus` (поле необязательное: в десктопе его нет) и отдаёт `sso` как `readonly` ref.
- `AuthScreen.vue` (режим `login`):
  - если SSO включён — кнопка «Войти через SSO» (подпись из `label`, иначе из локали), по клику
    `window.location.assign(loginUrl)`; разделитель «или» над парольной формой;
  - при `localLoginDisabled` парольная форма не показывается;
  - состояние `busy` после клика, чтобы не было двойного перехода;
  - параметр `sso_error` из `location.search`: показать понятное сообщение по коду (`denied`, `expired`, `not_provisioned`,
    `disabled`, `unavailable`) и убрать параметр из адресной строки (`history.replaceState`), код не доверять и не
    выводить как есть — только сопоставление с ключами локали;
  - доступность: кнопка — настоящий `<button>`/`<a>` с понятным текстом, `role="alert"` для ошибки, фокус на первом
    интерактивном элементе.
- Если SSO включён, а пользователь в режиме `setup` (первая настройка) — SSO не показывать.
- Локали: новые ключи в `en/auth.json` и такие же в `ru/auth.json` и `cn/auth.json` (одинаковые плейсхолдеры); `t()` не
  вызывать на верхнем уровне модуля; `v-html` не использовать; никаких текстов в коде.
- `PRE_LOGIN_METHODS`/`PUBLIC_METHODS` не менять без необходимости: новых RPC нет.
- Тесты (`vitest`): `useAuth` разбирает `sso` из статуса; `AuthScreen` с SSO / без SSO / с `localLoginDisabled` / с
  `sso_error`; проверка по ролям и `data-testid`, не по русским строкам. Если в проекте есть e2e для входа
  (`frontend/e2e`), добавить минимальный сценарий с замоканным `AuthStatus`.

## Критерии готовности

`cd frontend && npm test && npm run i18n:check && npm run build:web` зелёные; `python3 scripts/license-headers.py --fix`
ничего не меняет; коммит `feat(frontend): SSO button on the sign-in screen` с `Signed-off-by` запушен в `refactor`.
