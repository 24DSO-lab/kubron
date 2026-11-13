# NOTES — Dev 3: жизненный цикл Kubron, готовность к обновлению (ADR-0025)

Ветка: `feat/adr-0025-lifecycle` (от `origin/refactor`). ADR и их статус не менялись. Отдельный файл (а не правка `NOTES.md` Dev 1) — чтобы заметки четырёх веток не конфликтовали при интеграции.

## Что сделано
- `internal/readiness`: модель отчёта с обязательным `coverage` (валидатор, «не проверено» — часть структуры), сравнение с предыдущим, сервис (Reader со стороны потребителя: `ServerVersion/ServerResources/List`), экспорт JSON/Markdown (оба с разделом coverage), хранение (`Backend`), retention (`KUBRON_READINESS_KEEP_PER_CLUSTER`=50, `KUBRON_READINESS_MAX_AGE`=180d).
- `apimatrix` (встроенная матрица, окно 1.35–1.37, сгенерирована 2026-11-11) и `scripts/gen-apimatrix`; `catalog` (4 статуса, ссылка и дата проверки); `kver`.
- Хранилища: файловое и PostgreSQL (миграция `0012_readiness_reports`), один контрактный набор `readinesstest.Run` на обе реализации (проверено на реальном PG16 и `scripts/check-migrations.sh`).
- Связка (последний коммит `chore(wire): readiness`): делегаты `Readiness*` в `internal/app/app_readiness.go` (право `read`, `beginOp`, аудит `readiness.run`/`readiness.export`), поле в `app.go`, `methods.go`, `policy_test.go`, `store.*`, задача retention (`cmd/kubron-server`, `main.go`), `backend.json` ×3, навигация и реестр view.
- UI: `ReadinessView` и подкомпоненты, `useReadiness`, locales en/ru/cn, vitest (19 тестов), e2e `frontend/e2e/readiness.spec.ts`.
- Документация: `docs/lifecycle-readiness.md` (порядок обновления матрицы), страницы docs-site en/ru, строка CHANGELOG.

## Что НЕ сделано / ограничения
- Только живые объекты кластера; GitOps и локальные файлы — «не проверено» (по ADR). Drift/baseline — Dev 4.
- ~~allowlist платформы содержит мало видов с deprecated API…~~ **Закрыто (Dev 1, `integrate/adr-0026`):** allowlist расширен видами матрицы, страж — `internal/app/allowlist_matrix_test.go`.
- ~~`beginOp` требует подключённого кластера даже для истории и экспорта.~~ **Закрыто (Dev 1):** `OpRequest.StoredOnly`, History/Get/Compare/Export переведены на него.
- Markdown-экспорт только на английском (JSON — коды сообщений).
- e2e и тесты pgstore в этой среде не гонялись целиком: e2e нужен kind (`E2E_READINESS=1`, `E2E_PF_URL`, `E2E_KUBECONFIG`); pgstore — PG16 запускался ранее, к концу работы был остановлен.

## Допущения
1. Миграция 0012 вне ADR: таблица `readiness_reports` (jsonb отчёта + колонки сводки + индекс истории). Миграция 0011 — у Dev 2.
2. Вид источника в данных — `gitops_manifests` (не `gitops`); режим — `generic-kubernetes`.
3. Сообщения — `{code, params}`; перевод в UI по `readiness.codes.<code>` (вложенные ключи, ни один код не префикс другого). Английский текст нужен экспорту и как запасной.
4. Раздел размещён в группе «Конфигурация» бокового меню (`nav.views.readiness`).
5. Неизвестная версия/сочетание компонента — `insufficient_data`, никогда `confirmed`; нездоровый компонент — важность не ниже `warning`.
6. Окно целей — три последние минорные версии; цель вне окна или старше текущей — ошибка запроса.
7. Десктоп копирует экспорт в буфер обмена (скачивание файла только в вебе).

## Просьбы к другим
- ✅ Выполнено (Dev 1) — Dev 1: allowlist `Reader` — виды из `internal/readiness/apimatrix/matrix.json`; опционально — `beginOp` без подключения для чтения истории.
- Интеграция: удалить локальную заглушку `internal/pgstore/migrations/0011_incidents.{up,down}.sql` (в коммиты она не входит) перед применением патчей Dev 2; `vendor/` и `frontend/dist` не коммитить.

## Новые строки ошибок Go
Все добавлены в `locales/{en,ru,cn}/backend.json` (≈110 шаблонов: `internal/readiness/**`, `internal/pgstore/readiness.go`, `internal/app/app_readiness.go`); `node scripts/i18n-check.mjs` без ошибок.

## Строка CHANGELOG
См. `[Unreleased] → Added → Upgrade readiness`.

## Команды проверки
```
export PATH=$HOME/sdk/go/bin:$PATH GOFLAGS=-mod=vendor GOTOOLCHAIN=local
gofmt -l . ; python3 scripts/license-headers.py --fix
go vet -tags webkit2_41 ./internal/{readiness,app,store,pgstore,auth,server}/... ./cmd/kubron-server/... .
go test -tags webkit2_41 ./internal/{readiness,app,auth,server,store,k8s}/...
KUBRON_TEST_DATABASE_URL=… go test -tags webkit2_41 ./internal/pgstore/ ; scripts/check-migrations.sh
cd frontend && npm ci && npm test && npm run i18n:check
git log --format=full origin/refactor..HEAD
```
