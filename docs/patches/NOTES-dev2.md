# Dev 2 — Kubron Intelligence (ADR-0024)

Ветка `feat/adr-0024-intelligence` от `origin/refactor`. Патчи — рядом, `git am patches/feat-adr-0024-intelligence/*.patch`.
Контракты платформы (`opsplatform`, `tenant`, `logredact`, права) к моменту работы уже были в `refactor`; домены их **не импортируют** — только слой связки (`internal/app`).

## Что сделано

| Коммит | Содержание |
|---|---|
| `feat(diagnose)` | `internal/diagnose`: интерфейс `Reader` (get/list pods, pods/log, events, replicasets, deployments, namespaces, nodes; Secret нет), нормализованный контекст (Pod → ReplicaSet → Deployment, Events, Node), правила v1: Pending, CrashLoopBackOff, OOMKilled, ошибки запуска, rollout. Отчёт: `Fact`/`Hypothesis`/`Recommendation`/`Step` — разные типы с полем `kind`; уверенности «установлено» нет. Недостаток данных → `MissingContext`. 17 YAML-сценариев в `testdata/scenarios`, 3 fuzz-теста |
| `feat(incident)` | `internal/incident`: модель, `Backend`, файловая реализация (desktop), сервис (open/refresh/get/list/outcome/save/delete/read fragment/export/purge), лимиты логов константами, fail-closed редакция через `Redactor`, настройки и `KUBRON_INCIDENT_*`; контракты — `incident/incidenttest` |
| `feat(pgstore)` | PostgreSQL-реализация, миграция `0011_incidents` (up/down), шифрование текста фрагмента (secretbox, AAD привязан к строке), `RekeyIncidentFragments`, `CheckIncidentKeys`; тот же `incidenttest.Run` проходит на обеих реализациях |
| `test(diagnose)` | каждый код отчёта переведён в en/ru/cn |
| `feat(ui)` | `IncidentsView.vue` + `components/incidents/*`, `useIncidents.ts`, `incidentModel.ts`, `types/incident.ts`, `locales/*/incidents.json`, vitest, e2e |
| `docs` | `docs/intelligence.md` |
| `chore(wire): incidents` | всё общее, одним коммитом (см. ниже) |

Wire-коммит правит: `store/{store,file,ext}.go`, `store/pg.go`, `pgstore/{store,maintenance,keycheck}.go`, `server/methods.go`, `auth/policy_test.go` (в конец карты), `app/app.go` + `app_incident.go` + `incident_adapters.go`, `cmd/kubron-server/{main,jobs,admin}.go`, `main.go` + `incidents_desktop.go`, `Sidebar.vue`, `useViewRegistry.ts`, `locales/*/{backend,audit,nav}.json`, `CHANGELOG.md`, `docs/README.md`, docs-site (`features/intelligence.md` en/ru + навигация).

## Проверка

```
export PATH=$HOME/sdk/go/bin:$PATH GOFLAGS=-mod=vendor GOTOOLCHAIN=local
go test -race ./internal/diagnose/ ./internal/incident/...
KUBRON_TEST_DATABASE_URL=postgres://… go test -tags webkit2_41 ./internal/pgstore/ ./internal/app/ ./internal/store/ ./internal/auth/ ./internal/server/ ./internal/k8s/ ./cmd/...
go test ./internal/diagnose -run '^$' -fuzz FuzzAnalyzePod -fuzztime 30s    # также FuzzAnchorLine, FuzzNormalizeEvent
go test ./internal/incident -run '^$' -fuzz FuzzBuildFragment -fuzztime 30s
bash scripts/check-migrations.sh   # PG* переменные
cd frontend && npm ci && npm test && npm run i18n:check
```
Выполнено у меня: все Go-команды выше на PostgreSQL 16 (локально), `gofmt -l`, `go vet`, `license-headers.py`, vitest (708 тестов), `i18n:check` OK, fuzz по 10–20 с без находок. **Не выполнено:** Playwright e2e (`frontend/e2e/zz-incidents.spec.ts`: нет браузера/сервера; сценарий с кластером требует `E2E_KUBECONFIG` и `E2E_INCIDENT_WORKLOAD`), `vue-tsc` с настоящими биндингами Wails (`wailsjs` не в git и без Wails CLI не генерируется; вызовы `callApp` с объектами идут через `as never`, после генерации биндингов можно заменить на `models.*`), полная сборка десктопа. `frontend/dist` создавал пустым только для компиляции, в git его нет.

Новые зависимости: нет (vendor не менялся).

## Допущения (в ADR не сказано — решено по тексту)

1. **Кто что может.** Сохранить фрагмент, отметить результат, открыть, обновить Events, отключить сохранение для расследования — `read`. Прочитать содержимое, включить фрагменты в экспорт, **удалить фрагмент**, удалить расследование и удалить уже сохранённые фрагменты — `diagnostics.logs`. Настройки хранения — `users.manage`. (Подтвердить.)
2. **Область.** История — по полному `tenant.Scope` (организация, команда, кластер): «фильтр по кластеру» = кластер области запроса; общей истории по всем кластерам команды нет.
3. **Один документ.** Инцидент — один JSON-документ с версией (оптимистичная блокировка, `ErrConflict`, 3 повтора в сервисе). Фрагмент — отдельная запись; порядок «сначала фрагмент, потом документ» оставляет в худшем случае сирот, их убирает retention фрагментов (≤15 дней).
4. **Retention.** Инциденты считаются от последней активности (`UpdatedAt`), фрагменты — от `CreatedAt`. Задача ходит по организациям, у которых есть данные или настройки, с настройками каждой. Пересоздание/дубли: `Open` всегда создаёт новый инцидент.
5. **Refresh** обновляет только снимок Events (слияние с прежним: исчезнувшие из кластера события остаются; до 200 новых). Отчёт и фрагменты не пересчитываются — это состояние на момент открытия.
6. **Закрытие.** Отметка результата закрывает расследование; закрытое не обновляется. Подтвердить гипотезу может только человек и только из отчёта; в отчёте тип гипотезы не меняется.
7. **Редакция отчёта.** Через `Redactor` проходят значения параметров всех записей отчёта, сообщения Events и комментарий к результату. При сбое вместо текста пишется `[unavailable]`. Таблица инцидентов (метаданные, отчёт) в PostgreSQL **не шифруется** — шифруется только текст фрагментов, как сказано в ТЗ.
8. **Якорь** — последняя строка с признаком ошибки (`panic`, `fatal`, `error`, `oom`, …), иначе последняя непустая. Окно растёт от якоря в обе стороны до 200 строк/32 КиБ. Читается хвост 400 строк / до 512 КиБ (при `ErrTooLarge` — 100 строк). Интервал времени — по RFC 3339-меткам строк, если они есть; иначе только `capturedAt` (у `Reader` нет `Timestamps`).
9. **Env.** `KUBRON_INCIDENT_*` задают значения по умолчанию для организаций без сохранённых настроек; сохранённые настройки приоритетнее, верхние границы (15/365) действуют всегда. Добавлена `KUBRON_INCIDENT_PURGE_INTERVAL` (по умолчанию 1h). Неверное значение: server не стартует, desktop отключает расследования.
10. **События** собираются одним `List` по namespace (до 1000) и фильтруются по объектам контекста; усечение → `events.truncated` + `Partial`.
11. **Допущение про `extStore`:** внешнее хранилище (`pkg/ext`) может не иметь `Incidents()`; тогда используется файловое хранилище в каталоге данных (необязательная возможность, как `WebOIDC`).
12. **Нет** кнопки «расследовать» в таблицах ресурсов (общие компоненты) — вход через форму в разделе Incidents (вид/namespace/имя).
13. **Аудит.** Действия: `IncidentOpen`, `IncidentMarkOutcome`, `IncidentSaveFragment`, `IncidentDeleteFragment`, `IncidentSetLogCapture`, `IncidentReadFragment`, `IncidentExport`, `IncidentDelete`, `IncidentSaveSettings` — без содержимого. Метки добавлены в `audit.json` (3 языка).
14. **Событие** `incident:updated` (данные — ID) шлётся после `IncidentRefresh`; UI перечитывает открытое расследование.

## Просьбы к другим

- **Dev 1:** при применении патчей возможны механические конфликты в `policy_test.go` (мои записи в конце карты), `methods.go` (в начале списка), `locales/*/backend.json`, `nav.json`, `audit.json`, `CHANGELOG.md`, `docs-site/.vitepress/config.ts`, `Sidebar.vue`, `useViewRegistry.ts`, `cmd/kubron-server/{main,jobs}.go`, `main.go`. `maintenanceJobs` получил variadic-параметр `extra ...*jobs.Job` (им воспользуются Dev 3–4 для своих задач). Подтвердить допущение 1 и то, что `PermUsersManage` годится для настроек организации.
  - **Ответ Dev 1:** слияние механических конфликтов выполнено, `maintenanceJobs` с `extra ...*jobs.Job` принят. Допущение 1 и `PermUsersManage` — рекомендация «подходит», решение за ревьюером (см. `NOTES.md` → «Интеграция»).
- **Dev 3/4:** `SetupIncidents` вызывается в server до первых видов сессий; свои сервисы подключайте так же (виды копируют указатель). `store.Store` — добавляйте метод в интерфейс, `file.go`, `pg.go`, `ext.go` по образцу `Incidents()`.

## Новые строки ошибок Go (добавлены в `backend.json` en/ru/cn в wire-коммите)

`incident not found`, `incident was changed concurrently`, `tenant scope is not defined`, `identifier contains unsupported characters`, `investigation is closed`, `investigation outcome is invalid`, `incident settings are invalid`, `saving log fragments is disabled`, `pod is not an affected resource of the investigation`, `diagnostics are not supported for this resource kind`, `incident storage is not available`, `log access check is required`, `reading the log: %w`, `reading %s %s/%s: %w`, `incident fragment with key %q cannot be read by the key ring: %w` и составные (`corrupted …`, `%s: expected …`). Ошибки `diagnose.Err*` повторяют тексты `opsplatform.Err*` (шаблоны уже были).

## Строки для CHANGELOG

Уже в wire-коммите, раздел `[Unreleased] → Added` (инциденты; фрагменты логов; **право `diagnostics.logs` у `admin` и `operator`**; настройки `KUBRON_INCIDENT_*`; миграция `0011_incidents`).

## Документы

`docs/intelligence.md`; docs-site: `features/intelligence.md` и `ru/features/intelligence.md` (+ пункты навигации); индекс `docs/README.md`.
