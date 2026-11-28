# Промпты для разработки SSO (OpenID Connect)

Набор последовательных промптов для реализации входа в Kubron через универсальный OpenID Connect
(Keycloak, Authentik, Azure AD и другие). Каждый промпт самодостаточен: его можно отдать отдельной сессии.
Выполнять строго по порядку: каждый следующий опирается на результат предыдущего.

| № | Файл | Что делает |
|---|------|-----------|
| 1 | [01-adr-and-sso-core.md](01-adr-and-sso-core.md) | ADR-0059, пакет `internal/sso`, конфиг `KUBRON_OIDC_*` |
| 2 | [02-auth-service-storage.md](02-auth-service-storage.md) | `auth.Service.LoginExternal`, хранение связи с IdP, миграция |
| 3 | [03-web-server.md](03-web-server.md) | Маршруты `/auth/sso/*`, сессия, сборка в `kubron-server` |
| 4 | [04-frontend-i18n.md](04-frontend-i18n.md) | Кнопка SSO, состояние, локали `en`/`ru`/`cn` |
| 5 | [05-desktop.md](05-desktop.md) | Вход через SSO в десктопе (loopback, RFC 8252) |
| 6 | [06-docs-deploy-release.md](06-docs-deploy-release.md) | Документация, Helm, CHANGELOG, финальные проверки |

## Общий контекст (повторяется в каждом промпте)

Проект: Wails + Vue 3 + Go, репозиторий `24DSO-lab/kubron`, рабочая ветка `refactor`. Придерживаться современных
подходов (идиоматичный Go, `context` везде, `log/slog`, `errors.Is/As`, Vue 3 Composition API с `<script setup>` и TypeScript).

### Правила работы

1. Перед началом прочитать `CONTRIBUTING.md`, `docs/adr/0011-sso-extension-point.md`, `docs/adr/0018-cluster-auth-providers.md`
   (§7 «Безопасность входа»), `docs/adr/0016-ha.md`.
2. Окружение: Go 1.27.3 и каталог `vendor` лежат архивами в `.test/` (`go1.27.3.linux-amd64.tar.gz`, `vendor.tar.gz`).
   Распаковать вне репозитория, `vendor` в коммит **не добавлять**. Если `vendor/modules.txt` расходится с `go.mod`
   (например, `k8s.io/streaming`), использовать `-modfile` с локальной копией `go.mod`, сам `go.mod` под это не менять.
   Для проверок без фронтенда: `mkdir -p frontend/dist && touch frontend/dist/.keep`.
3. Заголовок в каждом `.go`, `.ts`, `.vue`:
   `// Copyright 2026 The Kubron Authors` и `// SPDX-License-Identifier: Apache-2.0` (в `.vue` — HTML-комментарии).
   Проверка: `python3 scripts/license-headers.py --fix`.
4. Локализация: никаких жёстко прописанных текстов в интерфейсе; ключи во всех языках (`en`, `ru`, `cn`) с одинаковыми
   плейсхолдерами; каждая новая `errors.New`/`fmt.Errorf` в `internal/` и `pkg/` получает шаблон в `backend.json`
   каждого языка. Go возвращает только английский текст. Кириллица вне `locales/` допустима только в комментариях.
5. Новые зависимости не добавлять: `github.com/coreos/go-oidc/v3` и `golang.org/x/oauth2` уже есть в `go.mod`.
6. Любое обращение к IdP — только через `internal/idpnet` (SSRF-политика, https в вебе, лимиты размера и времени).
7. Проверки перед пушем:
   ```
   gofmt -l .
   go vet ./... && go test -race ./internal/...
   cd frontend && npm test && npm run i18n:check
   ```
8. Коммит и пуш: в ветку `refactor` через GitHub-коннектор, **без PR** (PR откроет владелец). В сообщении коммита
   обязательна строка `Signed-off-by: Vadim <vmilyash@mts.ru>`; строки `Co-Authored-By:` и `Claude-Session:` не добавлять.
   Сообщения коммитов в стиле Conventional Commits (`feat(sso): ...`).
9. Видимые пользователю изменения — в `CHANGELOG.md` в разделе `[Unreleased]`.

### Принятые решения (не пересматривать без согласования)

- Протокол: Authorization Code + PKCE (S256), `state`, `nonce`, проверка подписи, `iss`, `aud`, `exp` ID-токена,
  защита от mix-up (RFC 9207: параметр `iss` в ответе, если он есть, обязан совпасть с издателем).
- IdP один на установку; провайдер заранее неизвестен, поэтому только стандартный OIDC (discovery), без привязки к вендору.
- Пользователь связывается с IdP по паре `(issuer, sub)`. **Автопривязки к существующим локальным пользователям по имени нет**;
  включается только явной опцией `KUBRON_OIDC_LINK_EXISTING=1` (по умолчанию выключена, в документации — предупреждение).
- Пока в системе нет ни одного пользователя, вход через SSO отклоняется: первого администратора создаёт обычная первичная настройка.
- Роль определяется группами из claim (`KUBRON_OIDC_ADMIN_GROUPS`, `KUBRON_OIDC_OPERATOR_GROUPS`), иначе роль по умолчанию
  (`KUBRON_OIDC_DEFAULT_ROLE`, по умолчанию `user`). Если сопоставление групп задано, IdP — источник истины для роли на каждом входе.
  Понижение последнего активного администратора не выполняется.
- Отключение парольного входа — `KUBRON_OIDC_DISABLE_LOCAL_LOGIN=1` (серверный конфиг; для аварийного входа достаточно
  убрать переменную и перезапустить сервер).
- Состояние входа (state, nonce, PKCE verifier) хранится в подписанной и зашифрованной cookie, без БД: callback может
  прийти на любую реплику (ADR-0016).
