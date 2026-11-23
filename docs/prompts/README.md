# Промпты для параллельной реализации Kubron Intelligence (ADR-0031…0034)

Этот каталог — рабочие задания для 8 разработчиков. Они реализуют **первую итерацию** четырёх ADR так, чтобы работать параллельно и не мешать друг другу.

| ADR | Тема | Объём первой итерации |
|---|---|---|
| 0031 | Граф зависимостей и цепочечная диагностика | этап 1 (Pod → PVC → PV/StorageClass, Service, PVC и Node как цели) |
| 0032 | Метрики как необязательный источник доказательств | этап A (платформа, сигналы Pod и Node, интеграция в правила) |
| 0033 | Корреляция и проблемы | этапы A и B (снимок корреляции, `Problem`, автоприсоединение) |
| 0034 | Обратная связь | этапы A и B (сбор, статистика, повторение); выгрузка (этап C) входит |

Этапы B/C ADR-0031, B ADR-0032 и C ADR-0033 — **вторая итерация**, в эти промпты не входят.

## Состав команды

| № | Файл | Область | Главные пути |
|---|---|---|---|
| D1 | [dev1-metrics.md](dev1-metrics.md) | Метрики: транспорт платформы, `signals`, доказательства в диагностике | `internal/opsplatform`, `internal/signals`, `internal/diagnose/metrics_*.go` |
| D2 | [dev2-depgraph.md](dev2-depgraph.md) | Граф зависимостей | `internal/depgraph`, статический тест `opsPackages` |
| D3 | [dev3-diagnose.md](dev3-diagnose.md) | Ядро `diagnose` и цепочечные правила | `internal/diagnose` (кроме `metrics_*`) |
| D4 | [dev4-correlation.md](dev4-correlation.md) | Корреляция по живым данным | `internal/correlation`, `internal/incident/correlate.go` |
| D5 | [dev5-problems.md](dev5-problems.md) | Контракт `incident` и проблемы (хранилища, операции) | `internal/incident` (ядро), `problem*.go`, `internal/pgstore` |
| D6 | [dev6-feedback.md](dev6-feedback.md) | Обратная связь и статистика | `internal/feedback`, `outcome.go`, `signature.go`, `recurrence.go` |
| D7 | [dev7-app-wiring.md](dev7-app-wiring.md) | Слой связки, RPC, права, RBAC, документация | `internal/app`, `internal/server`, `deploy`, `docs`, `CHANGELOG.md` |
| D8 | [dev8-frontend.md](dev8-frontend.md) | Интерфейс и локализация | `frontend/**` |

Общее для всех: [00-common.md](00-common.md) (окружение, правила кода и коммитов). Единый источник контрактов: [contracts.md](contracts.md).

## Как мы избегаем пересечений

1. **Один файл — один владелец.** Таблица ниже. Чужой файл не правится; нужное изменение просят у владельца (issue или комментарий в PR).
2. **Скелеты контрактов в первый день (Фаза 0).** Каждый владелец пакета первым PR вносит только типы и сигнатуры (компилируемые заглушки) строго по `contracts.md`. После слияния скелетов остальные работают против настоящих импортов.
3. **Точки расширения вместо общих правок.** `diagnose` получает реестры (правила с эпохой, хуки сбора, пост-хуки, цели); `incident.Service` получает однострочные вызовы заглушек, лежащих в файлах разных владельцев. Поэтому `engine.go`, `service.go`, `model.go` правит один человек, а остальные работают в своих файлах.
4. **Локализация по файлам.** Коды бэкенда лежат в отдельных locale-файлах у своих владельцев; общий `incidents.json` — только у D8.
5. **`contracts.md` меняет только владелец контракта** отдельным PR и сразу сообщает затронутым (список в самом файле).

## Матрица владения файлами

| Путь | Владелец | Остальным |
|---|---|---|
| `internal/opsplatform/**`, `NOTES.md` (раздел платформы) | D1 | только читать |
| `internal/signals/**` | D1 | импорт |
| `internal/diagnose/metrics_*.go`, `internal/diagnose/collect_signals.go` | D1 | — |
| `internal/k8s/metrics_stack.go` (только экспорт констант), `internal/k8s/metrics_signals_test.go` | D1 | — |
| `internal/depgraph/**` (включая `graphtest`) | D2 | импорт |
| `internal/k8s/opsplatform_static_test.go` | D2 | — |
| `internal/diagnose/*.go`, кроме файлов D1 (`types`, `engine`, `context`, `reader`, `collect`, `rules*`, `hooks`, `targets`, `chain_*`) | D3 | — |
| `internal/correlation/**`, `internal/incident/correlate.go` | D4 | — |
| `internal/incident/{model,service,backend,filebackend,settings}.go` | D5 | только D5 |
| `internal/incident/problem*.go`, `internal/incident/incidenttest/problems.go` | D5 | — |
| `internal/pgstore/incident_problems.go`, `internal/pgstore/migrations/0014_*` (номер уточнить) | D5 | — |
| `internal/feedback/**` | D6 | импорт |
| `internal/incident/{outcome,signature,recurrence,summarize_feedback}.go` | D6 | — |
| `internal/app/**`, `internal/server/**`, `incidents_desktop.go`, `main.go` | D7 | — |
| `deploy/**`, `docs/**` (кроме `docs/prompts/`, `docs/adr/`), `docs-site/**`, `CHANGELOG.md` | D7 | пишут текст в описание PR |
| `frontend/**` | D8 | — |
| `frontend/src/i18n/locales/*/incidentsMetrics.json` | D1 (заполняет) | D8 создаёт заготовку |
| `frontend/src/i18n/locales/*/incidentsChain.json` | D3 (заполняет) | D8 создаёт заготовку |
| `frontend/src/i18n/locales/*/incidentsCorrelation.json` | D4 (заполняет) | D8 создаёт заготовку |
| `internal/diagnose/**/*_test.go`, `testdata/` | владелец соответствующего файла | — |

Если нужного файла нет в таблице, владелец — тот, кто его создаёт; существующий файл вне таблицы правит только D7 (слой связки) после согласования.

## Фазы и порядок слияния

### Фаза 0. Скелеты (день 1, целиться в первые часы)

PR должны быть небольшими, без логики, только типы, интерфейсы, константы и заглушки. Порядок слияния (кто от кого зависит):

1. **D2** — `internal/depgraph` (типы чтения, модель графа, конструкторы `New/AddNode/AddEdge`, `graphtest.FakeReader`), каталоги с `doc.go` для `signals`, `correlation`, `feedback`, регистрация четырёх пакетов в `opsPackages`.
2. **D1** — `internal/signals` (типы, `Provider`, `Querier`) и `opsplatform.MetricsReader` + поле `Op.Metrics`.
3. **D4** — типы `internal/correlation` (`Result`, `Candidate`, `Run` как заглушка).
4. **D6** — типы `internal/feedback` (`Record`, `Stats`, константы, заглушки `Compute`, `Signature`, `Export`).
5. **D3** — ядро `diagnose` (псевдонимы типов чтения на `depgraph`, поля контракта, реестры, `CollectWith`, `RuleSetVersion = 2`). Требует 1 и 2.
6. **D5** — контракт `incident` (поля `Incident`, `Outcome`, `Summary`, заглушки-хуки, `Caller.Signals`). Требует 3 и 5.
7. **D7** — DTO и заглушки RPC (возвращают «не реализовано»), запись имён RPC в `contracts.md`.
8. **D8** — `types/incident.ts`, заготовки locale-файлов, регистрация в `registry.ts`, фикстуры JSON, заготовки компонентов.

Пока скелет зависимости не слился, разработчик пишет логику своего пакета на локальных фейках (это разрешено и ожидаемо), а в импортах опирается на `contracts.md`.

### Фаза 1. Параллельная реализация

Каждый работает только в своих файлах. Интеграция идёт через заглушки и реестры из Фазы 0. Ежедневно `git rebase origin/refactor`.

### Фаза 2. Интеграция (парами)

| Пара | Что сшиваем |
|---|---|
| D3 + D2 | `diagnose.Collect` строит граф `depgraph.Build`, цепочечные правила читают граф |
| D3 + D1 | сбор сигналов (хук 200) и метрические пост-правила работают поверх ядра |
| D4 + D5 | `Service.correlate` пишет снимок, `autoAttach` создаёт и находит проблемы |
| D5 + D6 | `Summarize` видит поля обратной связи, `MarkOutcome` закрывает проблему |
| D7 + все бэкенды | RPC, права, аудит, события, адаптеры, RBAC |
| D8 + D7 | реальные RPC вместо фикстур |

### Фаза 3. Приёмка (E2E)

Сценарии (ожидаемое поведение — в ADR, подробности в промптах):

1. **PVC без StorageClass.** Pod в `Pending`, PVC ссылается на несуществующий класс. Отчёт показывает цепочку Pod → PVC → StorageClass (`Missing`), кандидат первопричины — на PVC, `step.check_pvc` заменён фактами. Отчёт без графа (старый инцидент) открывается без ошибок.
2. **12 Pod'ов на Node под давлением памяти.** Открываем расследование по одному Pod'у. Снимок корреляции показывает `shared_node` с уверенностью не ниже `medium`, число затронутых workloads, область влияния. Второе расследование по другому Pod'у той же Node автоматически присоединяется к той же проблеме; ручное отсоединение запоминается.
3. **Метрик нет / метрики есть.** Без стека мониторинга: отчёт полный, подсказка «с мониторингом точнее» только для сигналов найденного симптома, операция не «частичная». Со стеком: факты `metric.*`, уверенность меняется не больше чем на один уровень, `high` метриками не достигается. Нет RBAC на прокси: причина `forbidden` с подсказкой про `prometheus-proxy-access`.
4. **Обратная связь.** Закрываем расследование с подтверждённой гипотезой, отвергнутой, помогшей и непомогшей рекомендацией. Статистика кластера показывает счётчики; при выборке меньше 5 процента нет. Выгрузка не содержит имён ресурсов, namespace, идентификаторов и комментариев.
5. **Повторение.** Новое расследование с той же сигнатурой в течение 30 дней после `resolved` получает `RecurrenceOf`; закрытое не изменяется.

Детерминизм: один и тот же снимок кластера даёт побайтно одинаковый отчёт (кроме полей времени наблюдения).

## Ветки и PR

- Ветка разработчика: `intel/dN-<кратко>` (например, `intel/d3-diagnose`), создаётся от `refactor`.
- Слияние в `refactor` через PR; принимает тимлид. Один PR — одна законченная задача; PR скелета — отдельный.
- Перед PR: `git rebase origin/refactor`, полный прогон тестов своих пакетов и `go build ./...`.
- Коммиты по правилам из `00-common.md` (подпись `Signed-off-by`, без `Co-Authored-By`).

## Открытые проверки (сверить по коду при реализации)

- Как PostgreSQL-реализация строит `Summary`: сейчас колонка `summary` вычисляется `Summarize()` при записи (проверено), поэтому у старых строк нет новых полей: статистика их пропускает (D5, D6).
- Какое право сегодня требует `MarkOutcome` (D5, D7): изменение состава проблемы требует `operate`; выровнять, если нужно.
- Как ограничение по namespace влияет на `Reader` (D4, D7): список затронутых нагрузок не должен показывать больше, чем доступно сессии.
- Точное имя метрики покрытия Prometheus `prometheus_tsdb_lowest_timestamp` (D1).
- ~~Номер следующей миграции PostgreSQL~~ — решено: `0014_incident_problems` (D5).
- ~~Точное имя права для выгрузки статистики~~ — решено: `users.manage` (плюс `read` на кластер) (D7).
