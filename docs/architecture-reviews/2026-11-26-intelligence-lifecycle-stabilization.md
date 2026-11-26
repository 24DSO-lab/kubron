# Закрытие LIFE-01 и INT-01: проверка стабилизации Intelligence и Lifecycle

- Дата проверки: 2026-11-26
- Проверенная ветка: `refactor`
- Проверенный HEAD: [`de4ddcbc0562b61cd65f531dabdfa61447350197`](https://github.com/24DSO-lab/kubron/commit/de4ddcbc0562b61cd65f531dabdfa61447350197)
- Итог по двум замечаниям: **исправления присутствуют в production-коде; регрессионное покрытие добавлено**
- CI для указанного HEAD: **не подтверждён этой проверкой** (у коммита не обнаружены связанные workflow runs или status checks). Не считать CI зелёным до отдельного успешного прогона.

Документ фиксирует закрытие двух замечаний финального архитектурного review. Он не означает, что все возможные задачи развития Kubron Intelligence или Kubron Lifecycle завершены.

## LIFE-01 — целостность Baseline Preview и подтверждения

**Статус: исправлено в коде.**

Проблема исходного review: confirmation token мог не отражать полный пользовательский scope и состояние покрытия. При неизменном наборе собранных объектов изменение namespace/selector, списка пропусков или состояния активной версии могло не инвалидировать предпросмотр.

### Реализация

- `internal/drift/engine.go` формирует fingerprint из канонизированного `Spec` (namespaces, selector, kinds, refs), версии правил, нормализованных объектов, пропусков с причинами, HPA targets и признака недоступности/неполноты HPA coverage.
- Namespace, kinds и refs приводятся к стабильному порядку; selector нормализуется, чтобы эквивалентные выражения не давали разные fingerprints.
- Preview token подписывает fingerprint, binding и срок действия через HMAC.
- `internal/drift/service.go` связывает preview существующего baseline с парой `BaselineID + ActiveVersionID`.
- При создании следующей версии хранилище дополнительно проверяет, что активная версия всё ещё та, относительно которой делался preview; конфликт возвращается как `ErrStalePreview`.

### Регрессионное покрытие

- `internal/drift/engine_test.go`: `TestPreviewTokenBindsSpecAndCoverage` проверяет изменение namespace, selector, причины пропуска, truncation и HPA coverage при неизменном наборе основных объектов.
- `internal/drift/engine_test.go`: `TestPreviewFingerprintCanonicalOrder` проверяет стабильность fingerprint при эквивалентном порядке элементов.
- `internal/drift/service_test.go`: `TestAddVersionRejectsChangedActiveVersion` проверяет отказ при смене активной версии между preview и подтверждением.

**Результат:** подтверждение относится к тому же scope, набору объектов и состоянию coverage, которые были показаны пользователю. Для существующего baseline подтверждение также относится к той же активной версии.

## INT-01 — конкурентное создание Problem

**Статус: исправлено в коде.**

Проблема исходного review: две серверные реплики могли одновременно не найти открытую Problem с одним correlation key и создать две независимые группы.

### Реализация

- `internal/incident/problem_backend.go` вводит операцию `FindOrCreateOpenProblem` как атомарную операцию backend-контракта.
- `internal/pgstore/incident_problems.go` выполняет lookup/create в одной PostgreSQL-транзакции и сериализует операции по полному tenant scope и correlation key с помощью transaction-scoped advisory lock. Поиск учитывает открытый статус, временное окно и список отсоединённых участников.
- `internal/incident/problem_filebackend.go` выполняет lookup/create под общей mutex-блокировкой файлового backend.
- `internal/incident/problem_hook.go` использует `FindOrCreateOpenProblem` вместо отдельной последовательности «найти → создать».
- Поиск выполняется по ключу и больше не ограничен сканированием первых `MaxProblemScan = 100` записей; константа оставлена для совместимости и помечена deprecated.

### Регрессионное покрытие

- `internal/incident/incidenttest/problems.go`: общий контракт содержит `problemFindOrCreateAtomic`, который одновременно запускает 12 вызовов с одинаковыми scope/key и проверяет, что все получают один Problem ID и в хранилище остаётся одна открытая проблема.
- `internal/incident/filebackend_test.go`: `TestFileProblemBackendContract` запускает общий контракт против файловой реализации.
- PostgreSQL implementation использует тот же `ProblemBackend` contract; фактическое выполнение интеграционного теста PostgreSQL зависит от соответствующего CI job и тестовой БД.

### Граница атомарности

Атомарность относится к поиску или созданию открытой Problem. Она не превращает всю последовательность сохранения Incident, добавления участника Problem и записи `Incident.ProblemID` в одну транзакцию между разными доменными записями. В `problem_hook.go` сохранение `ProblemID` остаётся отдельным best-effort шагом; частичный результат отражается через `Report.Partial`. Это существующая граница модели, а не незакрытая гонка создания дублирующих Problem из INT-01.

## Проверенные исходники

| Область | Файлы |
|---|---|
| Baseline Preview | `internal/drift/engine.go`, `internal/drift/service.go` |
| Baseline regression tests | `internal/drift/engine_test.go`, `internal/drift/service_test.go` |
| Problem contract | `internal/incident/problem_backend.go`, `internal/incident/incidenttest/problems.go` |
| File backend | `internal/incident/problem_filebackend.go`, `internal/incident/filebackend_test.go` |
| PostgreSQL backend | `internal/pgstore/incident_problems.go`, migration `0014_incident_problems.up.sql` |
| Incident integration | `internal/incident/problem_hook.go` |

## Вывод и оставшиеся условия

- LIFE-01 закрыт: preview token связывает полный scope/coverage и активную версию baseline.
- INT-01 закрыт: создание или повторное использование Problem сериализовано на уровне соответствующего backend.
- Эти два исправления не требуют переписывания архитектуры Intelligence или Lifecycle.
- Это не подтверждает отсутствие любых других дефектов и не является заявлением о завершении всех будущих функций продукта.
- Полный CI на HEAD `de4ddcbc0562b61cd65f531dabdfa61447350197` необходимо подтвердить отдельно. После успешного прогона этот документ можно дополнить ссылкой на конкретный workflow run и его SHA.
