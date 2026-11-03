# Этап 1: веб-оболочка (итоги)

**Итог этапа: ЗАВЕРШЁН.** HTTP-сервер, веб-транспорт, сессии, WebSocket, терминал и smoke-e2e реализованы. Gate 1 пройден; веб-режим одного пользователя принят, регрессия десктопа подтверждена.

| Задача | Состояние | Где |
|---|---|---|
| 1.1 `cmd/kubron-server`, конфиг | сделано | `cmd/kubron-server`, `internal/server/config.go` |
| 1.2 RPC-диспетчер + allowlist + контрактный тест | сделано | `internal/server/rpc.go`, `methods.go`, `contract_test.go` |
| 1.3 HTTP: ошибки, Origin, лимиты, SPA, healthz | сделано | `internal/server/http.go`, `server.go` |
| 1.4 Сессии: cookie, CSRF, Fetch Metadata | сделано; сессии позже переехали в PostgreSQL и стали многопользовательскими (этап 2, версия 0.5.0, `docs/ha-sessions.md`) | `internal/server/session.go`, `internal/websession` |
| 1.5 Event hub + `/api/ws` | сделано (без адресации) | `internal/server/hub.go`, `ws.go` |
| 1.6 Терминал по binary WebSocket | сделано: `OpenTerminal` (id выдаёт сервер) + `/api/terminal/{id}`, ввод/вывод бинарные кадры, resize управляющим кадром; буфер вывода до подключения, сборка забытых сессий | `internal/termbuf`, `internal/server/terminal_ws.go`, `frontend/src/platform/terminal*.ts` |
| 1.7 Веб-слой фронтенда | сделано | `frontend/src/platform/*.web.ts`, `.env.web` |
| 1.8 Smoke-e2e Playwright + kind | сделано; job `e2e` в CI блокирующий | `frontend/e2e`, `docs/e2e.md` |

## Отклонения от ADR
- ~~ADR-0004: сессии в памяти процесса, не в БД~~ — устарело: активный кластер лежит в `Session` с этапа 2, веб-сессии в PostgreSQL с версии 0.5.0 (`docs/ha-sessions.md`).
- ~~ADR-0009: хаб без адресации, ID потоков логов выбирает клиент~~ — устарело: адресные события, серверные ID и владелец логов и терминалов сделаны в этапе 2 (тикеты 2.4, 2.5).

## Известные пробелы веб-режима
- ~~Проброс порта и `OpenArgoUI` закрыты~~ — устарело: HTTP port-forward через отдельный origin сделан в версии 0.5.0 (ADR-0017), `OpenArgoUI` в вебе разрешён и возвращает адрес потока прокси или опубликованный адрес Argo CD.
- ~~`SetupAdmin` публичен, пока нет пользователей~~ — устарело: в сервисном режиме первый администратор создаётся с токеном первичной настройки (`KUBRON_SETUP_TOKEN`) или командой `kubron-server create-admin`; чарт генерирует токен (`docs/deploy.md`, находка 1 в `docs/security-review.md`).
- ~~Helm: SSRF и общий каталог~~ — устарело: политика адресов `KUBRON_HELM_ADDR_POLICY` и состояние Helm в PostgreSQL по командам сделаны в версии 0.5.0 (ADR-0015).
- TLS сервер не терминирует: для сети нужен прокси (`--trust-proxy`), иначе cookie уходит открытым текстом.
- ~~Нет ограничения попыток входа по IP~~ — устарело: с этапа 3 (3.5) есть блокировка по адресу клиента (30 попыток за 15 минут, экспоненциальная до 5 минут), `SetupAdmin` и `ChangePassword` покрыты, анонимные RPC ограничены token-bucket по адресу (`docs/http-security.md`). Блокировки по логину теперь общие между репликами (PostgreSQL); счётчики по адресу и token bucket остаются в памяти реплики (`docs/http-security.md`, раздел «Несколько реплик»).

## Проверено при ревизии (2026-10-27)
- В `internal` нет импортов Wails вне `internal/platform/wails`; во фронтенде нет обращений к Wails вне `src/platform`.
- Все вызовы `callApp` из фронтенда классифицированы в `allowedMethods`/`deniedMethods`; закрытые методы вызываются только в режимах интерфейса, скрытых в вебе (кроме порт-форварда и OpenArgoUI).
- Код multi-namespace (`scope*.go`) работает через `Manager.GetActiveConnection()` — совместим с моделью «Manager = вид сессии» (ADR-0005).
- Новые Helm-методы проходят `authz` и классифицированы.
