# D1. Метрики: транспорт платформы, `signals`, доказательства в диагностике

Ты — разработчик **D1**. Реализуешь **ADR-0032, этап A**: метрики как необязательный источник доказательств для Intelligence поверх встроенного стека мониторинга Kubron.

## Прочитай сначала

- `docs/prompts/00-common.md`, `README.md`, `contracts.md` (разделы 2, 3).
- `docs/adr/0032-metrics-evidence.md` — целиком; `0026-shared-platform-foundation.md`; `0031` §7.
- Код: `internal/opsplatform/{contract,provider,reader,limiter,errors,allowlist}.go`, `internal/k8s/metrics_stack.go`, `metrics_queries.go`, `metrics_query.go`, `internal/k8s/manifests/metrics-stack.yaml` (откуда берутся метки и имена метрик), `internal/k8s/opsplatform_static_test.go`, `internal/diagnose/{engine,context,rules}.go`.

## Ты владеешь

- `internal/opsplatform/**` (новый `metrics.go` и тесты, правки `contract.go`, `provider.go`), `NOTES.md` (запись о дополнении контракта платформы).
- `internal/signals/**`.
- `internal/diagnose/metrics_evidence.go`, `metrics_missing.go`, `collect_signals.go` (+ тесты) — только через хуки D3.
- `internal/k8s/metrics_stack.go` (**только** экспорт констант имени сервиса Prometheus и namespace, если есть неэкспортируемые), новый `internal/k8s/metrics_signals_test.go`.
- `frontend/src/i18n/locales/{en,ru,cn}/incidentsMetrics.json` (заготовки создаёт D8; ты заполняешь).

**Не трогай:** `internal/diagnose/{types,engine,context,reader,collect,rules}.go` (D3), `internal/app/**` (D7), `internal/incident/**` (D5–D6), `internal/k8s/opsplatform_static_test.go` (D2).

## Фаза 0: скелет (первый PR, `[skeleton]`)

После PR D2 (создаёт `doc.go` в `internal/signals`). В скелет:

1. `internal/signals`: все типы, константы, `Set` (пустая реализация, `nil`-безопасная), `Provider`, `Querier`, `Sample`, ошибки, `NewPrometheus` (заглушка, возвращающая пустой `Set` с `StackReason() == ""`) — строго по `contracts.md` §2.
2. `internal/opsplatform`: `MetricSample`, `MetricsReader`, поле `Op.Metrics` (nil), `Config.Metrics` (`MetricsConfig`). Без реализации; всё компилируется и старые тесты зелёные.

## Задачи (Фаза 1)

### 1. `opsplatform.MetricsReader` (ADR-0032 §3)

- Реализация поверх `Clients.Core.CoreV1().RESTClient()`: запрос `.../services/<svc>:<port>/proxy/api/v1/query` (тот же механизм, что `k8s.Manager.promQuery`, но внутри платформы). `internal/k8s` не импортировать.
- Параметр `time` = момент операции. Только мгновенные запросы, только тип vector.
- Пределы: deadline запроса 10 с (в пределах deadline операции), ответ до 1 МиБ, до 500 серий; превышение — `ErrTooLarge`.
- Ошибки: 404 → `ErrNotFound`, 403 → `ErrForbidden`, таймаут/сеть/5xx/`status: error` → `ErrUnavailable`. Слот общего лимитера кластера, как у чтения `Reader`.
- `Provider.Begin` собирает `Op.Metrics`, если `!StoredOnly` и `Config.Metrics` заполнен. Аудит: только число запросов (ADR-0026 §3), никаких выражений и значений.
- Тесты: фейковый `httptest`/клиент — успех, все четыре ошибки, пределы размера и числа серий, лимитер, deadline, `StoredOnly` → `Op.Metrics == nil`.
- Запись в `NOTES.md`.

### 2. `internal/signals` (ADR-0032 §2, §4, §5)

- Каталог сигналов этапа A (таблица §4 ADR). Каждый сигнал: семейство, окно, единица, построитель PromQL. Только `Subject` из валидных имён (DNS-1123); невалидное → `Gap` с `invalid_ref`, запрос не строится. Значения в литералах PromQL экранируются; сопоставление строгое (`=`); для нескольких Pod одной Node — альтернатива из `regexp.QuoteMeta`, не более 50 имён. Запросов «по всему кластеру» нет.
- **Метки бери из `metrics-stack.yaml` и `metrics_queries.go`, не из памяти:** cAdvisor (`namespace`, `pod`, `container`), node-exporter (`kubernetes_node`), kube-state-metrics v2.11 (имена метрик и метки — проверь по документации версии).
- Проверка возможностей: ленивая, только для семейств, нужных `Request`, в той же операции, без кэша. Покрытие по времени — `prometheus_tsdb_lowest_timestamp` (**проверь точное имя и что сам Prometheus его собирает**; если нет — покрытие неизвестно, без `history_short`).
- Бюджет: до 12 запросов сигналов + до 5 проверок; параллельно под лимитером; ошибка одного сигнала не влияет на остальные.
- Причины и семантика: `not_installed` (404), `forbidden`, `unavailable` (стек есть, сбой), `family_missing`, `no_series`, `history_short`, `invalid_ref`. «Нет данных» никогда не превращается в значение 0.
- `Set.PartialSource()` возвращает `"metrics"` только при `unavailable` у существующего стека. Отсутствие стека и `forbidden` операцию «частичной» не делают.
- Тесты: фабрика запросов (золотые строки), **fuzz-тест экранирования** меток, число Pod ≤ 50, все причины, бюджеты, невалидные имена, детерминированный порядок `Values()`/`Gaps()`; пакет не импортирует `opsplatform` и `diagnose` (проверяет статический тест D2).
- Тест согласованности каталога с манифестом (`internal/k8s/metrics_signals_test.go`): виды ресурсов kube-state-metrics и задачи сбора, нужные сигналам, есть в `metrics-stack.yaml`.

### 3. Доказательства в `diagnose` (ADR-0032 §6, §7) — новые файлы через хуки D3

Выполняется после слияния ядра D3 (`RegisterCollectHook`, `registerPostHook`).

- **Сбор (`collect_signals.go`)**: `RegisterCollectHook(200, "signals", ...)`. После базового сбора и графа определи субъекты (контейнеры Pod'ов цели, Node) из `Context`. Стартует только если `CollectOptions.Signals != nil` и до deadline осталось ≥ 5 с; иначе — пропуск с причиной `unavailable`. Запрашивай только сигналы, нужные правилам найденного симптома: реестр `signalWants` (`func(c *Context) []signals.Want` на правило).
- **Пост-хук (`metrics_evidence.go`)**: `registerPostHook(200, "metrics", ...)`. Добавляет факты `metric.<signal>` (`SourceMetric`, `Signal`, параметры строками); подтверждающий факт поднимает уверенность существующей гипотезы не более чем на один уровень, противоречащий — опускает на один; **`high` метриками не достигается**. Новые гипотезы — только при уже найденном симптоме и не выше `medium` (например «CPU ограничен лимитом», «память у лимита», «рестарты растут», «Node была под давлением»). `RuleID` этих гипотез — `metrics.<имя>`, эпоха 1. Пороги — константы с комментарием обоснования.
- Шаги «для человека» `step.check_memory_metrics` и `step.check_node_capacity` снимаются, когда соответствующий сигнал получен (пост-хуком, без правки `rules.go`).
- **Подсказка (`metrics_missing.go`)**: `MissingContext` `metrics.unavailable` / `metrics.signal_unavailable` с `Params.reason` — **только** для сигналов, нужных правилам найденного симптома. Отчёт без симптома подсказок не получает.
- **Инвариант:** отчёт без метрик (`Signals == nil` или пустой `Set`) побайтно равен отчёту без вашего кода, кроме `MissingContext` подсказок. Докажи тестом.
- Тесты: фикстуры `Context` + подставленный `signals.Set` (значение, `no_series`, `family_missing`, `forbidden`, `unavailable`, `history_short`); потолок `medium` для новых гипотез; «один уровень вверх/вниз»; детерминизм.

### 4. Локализация

`incidentsMetrics.json` в en/ru/cn: коды `metric.*` (факты, гипотезы, рекомендации), `metrics.unavailable`, `metrics.signal_unavailable` и все семь причин. Текст подсказки зависит от причины: `not_installed` — как включить мониторинг Kubron; `forbidden` — администратору кластера нужно привязать роль `prometheus-proxy-access`; остальные — нейтральное пояснение. Тон — спокойный, без рекламы.

## Чего не делаешь

Внешний Prometheus и защита от SSRF; диапазонные запросы и графики; сигналы этапа B (PVC, HPA, Ingress); свой Prometheus с другими метками; новое право доступа; кэш между операциями; правки `types.go`/`rules.go`.

## Зависимости и как работать до их готовности

- Задачи 1 и 2 не зависят ни от кого, кроме каталога-заглушки от D2.
- Задача 3 ждёт ядро D3. Пока его нет, пиши логику как чистые функции над локальными типами и перенеси в `diagnose` после слияния.
- D7 подключает `MetricsReader` к `signals.Querier` адаптером в слое связки; дай ему пример в описании PR.

## Приёмка

- `go test ./internal/opsplatform/... ./internal/signals/... ./internal/diagnose/... ./internal/k8s/...` зелёный; статический тест платформы проходит с `signals` в `opsPackages`.
- Сценарий E2E-3 из README выполняется на тестах фикстур (без реального Prometheus).
- В PR — строка для CHANGELOG и пример адаптера для D7.
