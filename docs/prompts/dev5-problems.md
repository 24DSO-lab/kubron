# D5. Контракт `incident` и проблемы (хранилища, операции)

Ты — разработчик **D5**, владелец ядра `internal/incident`. Делаешь две вещи: (1) **контракт-PR** с всеми новыми полями и хуками (от него зависят D4, D6, D7), (2) **проблемы** — **ADR-0033 §6–§7, этап B**.

## Прочитай сначала

- `docs/prompts/00-common.md`, `README.md`, `contracts.md` (разделы 4, 5, 6, 7).
- `docs/adr/0033-correlation-problems.md` — целиком; `0034` §2, §5 (поля `Outcome`/`Summary`); `0026` §4 (хранилище, версии, сироты, retention); `0024` §5; `0007` (Store, файлы и PostgreSQL).
- Код целиком: `internal/incident/*.go` (`model`, `backend`, `filebackend`, `service`, `settings`, `evidence`, `sanitize`, `incidenttest/`), `internal/pgstore/incidents.go` и `migrations/`, `internal/tenant`.

## Ты владеешь

- `internal/incident/{model,service,backend,filebackend,settings}.go` — **единственный**, кто их правит.
- `internal/incident/problem*.go` (`problem.go`, `problem_backend.go`, `problem_filebackend.go`, `problem_service.go`, `problem_hook.go` + тесты), `internal/incident/incidenttest/problems.go`.
- `internal/pgstore/incident_problems.go`, `internal/pgstore/migrations/0014_incident_problems.{up,down}.sql` (**проверь следующий свободный номер** в `refactor` перед PR) и необходимые правки подключения в `internal/pgstore` (только в новых файлах и минимальной строкой в `Store`).

**Не трогай после контракт-PR:** `correlate.go` (D4), `outcome.go`, `signature.go`, `recurrence.go`, `summarize_feedback.go` (D6). Сам файлы-заглушки создаёшь ты (один раз), дальше они принадлежат их владельцам. Не трогай `internal/diagnose/**`, `internal/app/**`.

## Фаза 0: контракт-PR (`[skeleton]`; после скелета D4 и ядра D3)

**Это критичный PR для трёх разработчиков.** Делай его первым и маленьким. Без логики; всё компилируется, старые тесты зелёные.

1. `model.go`: типы `RuleRef`, `CorrelationFeedback`, `CorrelationVerdict`; новые поля `Incident`, `Outcome`, `Summary` — **точно** по `contracts.md` §5 (всё `omitempty`; имена JSON — camelCase). `Incident.Correlation` имеет тип `*correlation.Result` (импорт из скелета D4). `Summarize()` копирует простые поля (`ProblemID`, `Signature`, `RecurrenceOf`, `RuleSetVersion`, `Symptoms`, `ResolvedAt`) и вызывает `in.summarizeFeedback(&sum)`.
2. Файлы-заглушки с пустыми телами — **ровно те сигнатуры и имён файлов** из `contracts.md` §5: `correlate.go`, `signature.go`, `recurrence.go`, `outcome.go`, `summarize_feedback.go`, `problem_hook.go`.
3. `service.go`: вызовы заглушек в `Open` (`correlate` → `signatureOf` → `detectRecurrence` → сохранение → `autoAttach`), `MarkOutcome` (`prepareOutcome` → сохранение → `onOutcomeSaved`), `Purge` (`purgeProblems`). Имена частичных источников, возвращаемые хуками, сливаются в тот же механизм `Partial`, что уже есть. `Open` вызывает `diagnose.CollectWith(..., CollectOptions{Signals: caller.Signals})`; `Caller` получает поле `Signals signals.Provider`.
4. `service.go`: `WithProblems(pb ProblemBackend) *Service` (поле `s.problems`; при `nil` хуки проблем — no-op).
5. `problem.go`/`problem_backend.go`: типы и интерфейс `ProblemBackend` (`contracts.md` §5). Тип `Version` — тот же, что у `Incident`.
6. Тест обратной совместимости: старый JSON `Incident` без новых полей читается и пишется без ошибок (файловый бэкенд и контрактные тесты `incidenttest`).

**Важно:** `internal/pgstore/incidents.go` считает колонку `summary` из `Summarize()` при записи, поэтому суммари старых строк новых полей не имеет — и это замечание для D6 (статистика их пропускает). Миграция для суммари не нужна.

## Задачи (Фаза 1)

### 1. `ProblemBackend`: файловая и PostgreSQL-реализации

- Правила те же, что у `Backend`: область (`tenant.Scope`) входит в ключ записи; чужая область → `ErrNotFound`; ошибка версии → `ErrConflict`; время в UTC с микросекундами; `Version==0` создаёт (при существующем — `ErrConflict`), иначе CAS.
- **Файловая**: в том же корне хранилища, отдельный подкаталог проблем в том же формате и с той же атомарной записью, что инциденты (посмотри `filebackend.go`). Пустой каталог — не ошибка.
- **PostgreSQL**: таблица `incident_problems` (область как у `incidents`, документ JSONB, колонки `status`, `key`, `updated_at`, `version`, индекс на (область, `status`, `updated_at DESC`)); миграция up/down; без изменения таблиц инцидентов. `PurgeProblems` учитывает `OrgIDs`, если он собирает организации из нескольких таблиц (проверь, как он устроен в `incidents.go`).
- **Контрактные тесты** `incidenttest.RunProblems(t, factory)`: создание/чтение/обновление, `ErrConflict` на устаревшей версии и на повторном создании, изоляция областей, порядок и пагинация `ListProblems` (новые первыми, фильтр по статусу), `DeleteProblem`, `PurgeProblems`, документ с участниками на максимуме. Вызывается из файлового и pg-тестов (pg-тесты — по тому же паттерну окружения, что у `incidents`).

### 2. Сервис проблем (ADR-0033 §6–§7)

- **`autoAttach`** (`problem_hook.go`): если `in.Correlation.Best()` имеет уверенность ≥ `medium` — найти открытую проблему той же области с тем же `Key`, обновлённую в пределах 6 ч, без этого расследования в `Detached`; поиск — среди не более 100 открытых проблем кластера (`ListProblems`, **без индекса по расследованиям**); присоединить (CAS с повтором при `ErrConflict`, как `Service.mutate`) или создать новую; проставить `ProblemID` в расследовании.
- **Порядок записи**: сначала расследование, потом проблема, потом `ProblemID` в расследовании. Сбой после первого шага **не делает `Open` неуспешным**: расследование без группы, источник `problems` в частичных. Ссылка на несуществующую проблему — «без группы»; участник с удалённым расследованием пропускается при чтении и очищается при следующей записи.
- **Ручные операции** (`problem_service.go`): `AttachToProblem`, `DetachFromProblem` (добавляет в `Detached`), `MergeProblems` (участники переносятся, исходная → `closed` с `MergedInto`), `CreateProblem` (из выбранных расследований), `GetProblem`, `ListProblems`. Лимит 100 участников, `ErrProblemFull`. Присоединение к закрытой проблеме вручную открывает её. **Расследования никогда не удаляются, не скрываются и не сливаются.**
- `onOutcomeSaved`: когда закрыт последний открытый участник, проблема → `closed` (best-effort).
- `purgeProblems`: проблемы, обновлённые раньше границы retention расследований.
- **События и аудит** в слое связки (D7); твои методы возвращают изменённую `Problem`, чтобы D7 мог отправить `problem.changed`.

### 3. Тесты

- Сервис проблем на фейковом `ProblemBackend` и фейковом `Backend`: автоприсоединение от `medium` (и нет ниже), создание новой проблемы, окно 6 ч, работа `Detached`, отказы записи (случай «расследование сохранено, проблема нет»), конфликт версий (два одновременных присоединения), дубль проблемы при гонке (известное ограничение, зафиксируй тестом), слияние, закрытие, purge.
- Проверка инварианта: ни одна операция не меняет документ расследования, кроме `ProblemID`.
- Старые тесты `incident`, `pgstore` зелёные.

## Чего не делаешь

Корреляцию (D4), статистику/повторение (D6), RPC и аудит (D7), UI; новые методы `Backend`; индекс расследований; фоновое обнаружение; «пересчитать корреляцию».

## Стыки

- **D4:** типы `correlation.Result/Candidate` и `Key()`/`Best()`; тело `correlate` пишет он.
- **D6:** тела `signatureOf`, `detectRecurrence`, `prepareOutcome`, `summarizeFeedback`.
- **D7:** проводка `WithProblems`, `Caller.Signals`, события; отдай ему список экспортируемых ошибок для маппинга (`ErrProblemFull`, `ErrProblemNotFound`).

## Приёмка

- В `refactor` в первый день лежит контракт-PR; остальные не правят ни `model.go`, ни `service.go`.
- E2E-2: второе расследование по той же Node присоединяется к той же проблеме; ручное отсоединение запоминается.
- `go test ./internal/incident/... ./internal/pgstore/...` зелёный.
- В PR — строка для CHANGELOG и список известных ограничений (дубль проблемы при гонке).
