# D4 — Job и CronJob (ADR-0031, этап 3)

Перед началом прочитай `00-common.md`, `README.md` и `contracts.md` (§1 про Job и CronJob, §2–3). Ветка: `intel2/d4-batch` от `refactor`. Ты **ни от кого не зависишь** и никого не ждёшь: все точки расширения уже в `refactor`, чужие результаты берёшь из `contracts.md` и подменяешь фейками.

## Цель

Job и CronJob — виды графа и цели: причины неудач заданий и пропусков расписания объясняются по графу и conditions. Правила `chain.job`, `chain.cronjob`; гипотезы `job.backoff_limit_exceeded`, `job.deadline_exceeded`, `cronjob.suspended`, `cronjob.missed_schedule`, `cronjob.concurrency_blocked`.

## Твои файлы (создаёшь и правишь только их)

- `internal/depgraph/kind_job.go`, `kind_cronjob.go` (+ `registerExpander`: `owns` Job → Pod, CronJob → Job; ограничь число читаемых Job'ов последними N по времени, лимит константой «ADR-0031 §3»); `internal/depgraph/batch_test.go`.
- `internal/diagnose/targets_job.go`, `targets_cronjob.go`, `rules_chain_job.go` (`chain.job`), `rules_chain_cronjob.go` (`chain.cronjob`), `job_chain_test.go`, `cronjob_chain_test.go`.
- `internal/diagnose/testdata/chain/job_*`, `testdata/chain/cronjob_*`.
- `frontend/src/i18n/locales/{en,ru,cn}/incidentsChainBatch.json` — оба вида.

## Не трогай

- Общие файлы из списка «Заморожено» в `README.md`, в частности `kind_registry.go`, `kinds.go`, `build.go`, `engine.go`, `hooks.go`, `reader.go`, `types.go` (в т. ч. `RuleSetVersion`).
- Файлы других разработчиков (`kind_*.go`, `rules_chain_*.go`, `targets_*.go`, `testdata/chain/*`, `incidentsChain*.json` других видов), общие тесты первой итерации.
- `go.mod`, `go.sum`, `vendor/`.

## Задачи

1. **Job.** Conditions `Failed` с reason `BackoffLimitExceeded` → `job.backoff_limit_exceeded`; `DeadlineExceeded` → `job.deadline_exceeded`; причина падения Pod'а **не пересобирается** — она выдаётся существующими правилами Pod'ов Job'а (OOM, crash loop, image pull): объединяй цепочкой `Job > Pod` (`Explains`). Приостановленный Job (`spec.suspend`) — не проблема, факт.
2. **CronJob.** `spec.suspend=true` и ожидаемые запуски → `cronjob.suspended`; пропущенные запуски — по `status.lastScheduleTime`, расписанию (cron-выражение разбирается стандартным парсером из зависимостей проекта; **новых зависимостей нет** — если парсера нет, оцени пропуск по `lastScheduleTime` + `startingDeadlineSeconds` и по событиям `FailedNeedsStart`/`TooManyMissedTimes`) → `cronjob.missed_schedule`; `concurrencyPolicy: Forbid` и активный Job дольше интервала → `cronjob.concurrency_blocked`.
3. **Границы.** История Job'ов ограничена (последние N); срок хранения Pod'ов может быть истёк — это `MissingContext`, не `Missing`.
4. **Цели.** `RegisterTarget` для `Job` и `CronJob`, namespace обязателен. CronJob → последний неудавшийся Job → его Pod'ы.
5. **Время.** Только `Context.ObservedAt`; никаких `time.Now()`.
6. **Переводы** en/ru/cn.

## Тесты и фикстуры

`job_healthy`, `job_backoff_exceeded`, `job_deadline_exceeded`, `job_suspended`, `job_pod_oom` (цепочка Job > Pod с причиной от правил Pod), `job_forbidden`, `job_truncated`; `cronjob_healthy`, `cronjob_suspended`, `cronjob_missed`, `cronjob_concurrency_blocked`, `cronjob_no_jobs`, `cronjob_forbidden`, `cronjob_truncated`. Детерминизм: два прогона дают одинаковый отчёт.

## Не входит

Метрики, корреляция, карта, права RBAC (D9; `jobs` и `cronjobs` уже в allowlist и чарте — D9 сверит).

## Готово, когда

- Все пункты «Задач» выполнены и покрыты тестами; `gofmt`, `go build ./...`, `go vet`, тесты своих пакетов, `go test ./internal/app/... ./internal/k8s/...` зелёные, `cd frontend && npm test && npm run build`.
- `git diff --stat origin/refactor` содержит только файлы из раздела «Твои файлы».
- Переводы en/ru/cn с паритетом ключей; нет сырых значений Kubernetes в переводах.
- Описание PR по `00-common.md` (в том числе строка для CHANGELOG).
