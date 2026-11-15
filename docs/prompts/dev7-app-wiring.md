# D7. Слой связки, RPC, права, RBAC, документация

Ты — разработчик **D7**. Ты владеешь всем, что соединяет доменные пакеты с приложением: адаптеры платформы, RPC, права и аудит, события, подключение хранилищ, манифесты развёртывания и RBAC, документацию и CHANGELOG.

## Прочитай сначала

- `docs/prompts/00-common.md`, `README.md`, `contracts.md` (всё, особенно раздел 7).
- ADR-0031…0034 целиком (у тебя по части каждого); `0026` (платформа, аудит, права, лимитер), `0009` (события), `0006`, `0018`, `0020`.
- Код: `internal/app/{app_incident,incident_adapters,incidents_desktop,ops_env,opctx,audit_names_test,capabilities_test,allowlist_matrix_test,app_metrics}.go`, `internal/server/**`, `internal/opsplatform/**`, `internal/k8s/metrics_stack.go`, `deploy/**`, корневой `CHANGELOG.md`, `docs/`.

## Ты владеешь

- `internal/app/**`, `internal/server/**`, точки входа (`main.go`, `incidents_desktop.go`, серверные `cmd/`).
- `deploy/**` (RBAC, чарт), `docs/**` (кроме `docs/adr/` и `docs/prompts/`), `docs-site/**`, `CHANGELOG.md`.
- `frontend/src/platform/**` — **не** твоё: всё в `frontend/**` у D8; ты даёшь ему DTO и имена RPC.

**Не трогай:** доменные пакеты (`diagnose`, `depgraph`, `signals`, `correlation`, `feedback`, `incident`, `opsplatform`) — только читать. Если нужна правка в домене — запрос владельцу.

## Фаза 0: скелет (PR `[skeleton]`)

После контракт-PR D5. Цель — дать D8 стабильные имена.

1. Типы DTO (`ProblemSummaryDTO`, `ProblemDetailDTO`, `OutcomeInput` с новыми полями, `RuleStatsDTO` = `feedback.Stats`, `ExportFileDTO`) в отдельном файле `internal/app/app_incident_dto.go` — по `contracts.md` §7, JSON camelCase.
2. RPC-заглушки `IncidentProblemList/Get/Attach/Detach/Merge/Create`, `IncidentRuleStats`, `IncidentRuleStatsExport` в новых файлах `app_incident_problems.go`, `app_incident_feedback.go`; возвращают ошибку «не реализовано». **Если имена не консистентны с тем, как уже названы RPC в `app_incident.go`, переименуй их в скелете и исправь `contracts.md` §7 в том же PR.** После слияния сообщи D8.
3. Регистрация в матрицах прав и тестах (`capabilities_test.go`, `allowlist_matrix_test.go`, `audit_names_test.go`): новые имена операций и права, чтобы тесты оставались зелёными.

## Задачи (Фаза 1–2)

### 1. Метрики в слое связки (ADR-0032 §3)

- `internal/k8s/metrics_stack.go` экспортирует константы (это делает D1) — ты их используешь в `Config.Metrics`, **не дублируя** строки в платформе.
- Адаптер `opsplatform.MetricsReader → signals.Querier` (преобразование `MetricSample → signals.Sample`, ошибок → `signals.Err*`) в `incident_adapters.go` (рядом с `diagReader`); `Caller.Signals = signals.NewPrometheus(adapter)` только если `op.Metrics != nil`.
- Статус операции: отсутствие стека и `forbidden` **не** делают операцию `partial`; частичной её делает `Set.PartialSource() == "metrics"`.
- Аудит: только число запросов и признак доступности; никаких выражений и значений.
- Тесты: адаптер (все четыре ошибки, nil-случай), `StoredOnly` → нет метрик.

### 2. Хранилища и проблемы (ADR-0033 §6)

- Подключи `incident.WithProblems(...)` в режимах «файлы» (десктоп, `incidents_desktop.go`) и «PostgreSQL» (сервер): как `Backend` берётся из `Store`, так и `ProblemBackend` — та же привязка (аксессор в `Store`, добавленный D5).
- RPC `IncidentProblem*` (`StoredOnly`: подключение к кластеру не строится): просмотр — `read`; изменение состава — `operate`. Аудит операций `ProblemAttach/Detach/Merge/Create` с идентификаторами, без содержимого отчётов. После успешной операции — адресное событие `problem.changed` с только `problemID` (ADR-0009): узнай, как устроен механизм событий для инцидентов (`incidents.changed` или аналог) и повтори. Клиент загружает подробности RPC с повторной проверкой прав.
- Маппинг ошибок домена (`ErrProblemFull`, `ErrProblemNotFound`, `ErrConflict`) в коды ответа по существующему образцу, включая словарь `backendErrors` для фронтенда (текст переводов — у D8).
- Тесты на права (без права — отказ), аудит, отсутствие обращения к кластеру в `StoredOnly`.

### 3. Обратная связь (ADR-0034 §2, §7, §8)

- `IncidentMarkOutcome`: расширенный `OutcomeInput` прокидывается в `incident.Outcome` (новые поля); валидация — в домене (D6).
- `IncidentRuleStats` (`read`, `StoredOnly`) и `IncidentRuleStatsExport` (право, которым защищена смена настроек хранения расследований, `SetSettings`; **проверь точное имя**). Выгрузка пишет аудит **без содержимого файла**; возвращает `{filename, mime, dataBase64}` (имя файла без имён кластера и организации).

### 4. Развёртывание и RBAC (ADR-0031 §6, ADR-0032 §8)

- `deploy/**`: в роль/ClusterRole технической учётной записи Intelligence добавь **только `get` и `list`** для: `services`, `endpoints`, `discovery.k8s.io/endpointslices`, `persistentvolumeclaims`, `persistentvolumes`, `storage.k8s.io/storageclasses` (этап 1). **Никакого `secrets`, никакой записи.** Сверься с тем, как там уже описаны права для pods, events, nodes. Если чарт и манифесты в разных местах — обнови все и добавь тест/проверку согласованности, если она есть в проекте.
- Документация для администратора: какие права нужны по этапам; доступ к метрикам выдаётся отдельно (роль `prometheus-proxy-access`); ограничения встроенного стека (хранение 2 дня, `emptyDir`); что происходит при нехватке прав (`Unknown/forbidden`, `MissingContext`); как работает корреляция и группировка; выгрузка статистики (что входит и что не входит).
- Документ для мантейнеров «Как улучшать правила по реальным результатам» (ADR-0034 §9): тезисы берёшь из PR D6.
- `CHANGELOG.md`: собери строки из описаний PR D1–D8 в одну запись в формате файла.

### 5. Связка и тесты стыков

- Сквозной тест на фейках в `internal/app` (без кластера): открытие расследования с метриками/без, присоединение к проблеме, отметка результата, статистика, выгрузка.
- Проверь, что статические тесты (`opsplatform_static_test.go`, `imports_test`-подобные) зелёные на итоговой ветке.

## Чего не делаешь

Новые права доступа (`diagnostics.metrics`, право на группировку); внешний Prometheus; телеметрию; чтение Secret; правки доменных пакетов; фронтенд.

## Стыки

- **D1:** пример адаптера в описании его PR; экспорт констант стека.
- **D5:** `WithProblems`, `Caller.Signals`, ошибки домена.
- **D6:** `RuleStats`, `ExportRuleStats`, правила валидации `Outcome`.
- **D8:** имена RPC и DTO (скелет — первый день); граница: ты — Go, он — Vue.

## Приёмка

- E2E-1…E2E-5 из README проходят на сквозном тесте слоя связки (на фейках), каждый RPC есть в матрице прав и тестах аудита.
- `go build ./...`, `go vet ./...`, `go test ./internal/app/... ./internal/server/...` зелёные.
- Никакой вариант работы не читает Secret и не пишет в кластер.
- Документация и CHANGELOG готовы; в PR — список изменённых манифестов RBAC.
