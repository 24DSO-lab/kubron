# Missed Schedule Occurrences — accepted architecture decision

- Дата: 2026-11-26
- Статус: принятое продуктовое/архитектурное решение пользователя
- Связано: [Schedule Ownership](2026-11-26-schedule-ownership-decision.md), [Execution Identity & Durable Operations design proposal](execution-identity-durable-operations-design.md), ADR-0016, ADR-0026
- Область: Desktop-local и Web-server schedules; Observe, Assisted и Autonomous execution

## Решение

Пропущенные occurrences обрабатываются по стратегии, соответствующей режиму автономности:

| Режим | Стратегия по умолчанию | Семантика |
|---|---|---|
| Observe | `Coalesce` | Все пропущенные occurrences в допустимом окне сводятся к одному актуальному запуску наблюдения |
| Assisted | `Skip` | Пропущенные occurrences пропускаются; очередь устаревших approval requests не создаётся |
| Autonomous | `Skip` | Пропущенные автоматические операции не воспроизводятся задним числом |

`Catch-up` допускается только как явная настройка конкретного расписания и при соблюдении ограничений, перечисленных ниже. Это не стратегия по умолчанию ни для одного режима.

## Семантика стратегий

### Coalesce

- Несколько пропущенных occurrences объединяются в один актуальный запуск.
- Coalesced Run должен отражать фактическое время обработки и диапазон/набор occurrences, которые он покрывает, чтобы аудит не создавал впечатление, будто исторические проверки выполнялись вовремя.
- Для действий Observe результат должен быть актуальным на момент исполнения; нельзя выдавать устаревший снимок за результат каждого пропущенного occurrence.
- Coalesce не должен объединять разные schedule definitions, разные owner scopes или несовместимые execution bindings.

### Skip

- Пропущенные occurrences помечаются как пропущенные/закрытые согласно durable occurrence ledger и не создают Run.
- Пропуск не должен оставлять неоднозначное состояние, из-за которого тот же occurrence будет бесконечно повторно обнаруживаться scheduler-ом.
- Skip не отменяет следующий регулярный occurrence.

### Catch-up

Catch-up разрешается только при явном opt-in на уровне расписания. Конфигурация должна задавать как минимум:
- максимальное количество occurrences для воспроизведения за один recovery cycle;
- максимальный возраст occurrence;
- допустимые Operation/Action types и режимы автономности;
- concurrency/rate limits и общий execution budget;
- поведение при частичном отказе или превышении лимита.

Перед каждым воспроизводимым Run обязательны свежие проверки tenant scope, Kubron authorization, Execution Identity и её grants, Kubernetes RBAC, effective autonomy policy, target identity/preconditions и approval requirements. Catch-up не является approval и не переиспользует approval, привязанный к другому Plan, target snapshot, policy revision или identity binding.

Если ограничение превышено, оставшиеся occurrences должны быть явно пропущены или переведены в требующее административного решения состояние согласно конфигурации; бесконечная очередь не допускается.

## Общие инварианты

1. **Durable occurrence identity:** каждый запланированный occurrence имеет стабильный уникальный ключ, связанный с schedule ID и его revision/definition. Повторный scheduler tick, retry или конкурентная обработка не создают второй Run для одного occurrence.
2. **Atomic creation:** регистрация occurrence и создание/связывание Run выполняются атомарно на уровне соответствующего persistence backend. Для Web это должно быть безопасно при нескольких server replicas.
3. **No inherited approval:** пропущенный occurrence не считается одобренным. Approval применим только к точному immutable Plan и его digest, target snapshot, policy revision, Execution Identity binding и сроку действия.
4. **Fresh authorization:** актуальные права, owner scope, grants, credentials, policy и доступ к каждому target проверяются заново при обработке occurrence и непосредственно перед каждым side effect.
5. **No blind replay:** неизвестный результат предыдущего side effect сначала reconciled через свежие reads; он не повторяется слепо.
6. **Auditable disposition:** каждый occurrence получает однозначный outcome: executed/coalesced, skipped, expired, blocked или equivalent explicit terminal state. Audit хранит причину и ссылку на связанный Run без секретов.
7. **Immutable existing Runs:** изменение расписания влияет на будущие occurrences. Уже созданные Runs сохраняют immutable Plan и собственные bindings; удаление/пауза расписания не переписывает их задним числом.
8. **Platform parity:** Desktop и Web используют одинаковую доменную семантику и contract tests. Различается только источник scheduler ticks и persistence/execution adapter.

## Desktop и Web recovery

- Web scheduler восстанавливает occurrences из durable schedule state/occurrence ledger после рестарта или потери реплики; дедупликация не зависит от памяти конкретного процесса.
- Desktop scheduler обрабатывает период, когда процесс Kubron Desktop был остановлен, при следующем запуске приложения. Применяется та же стратегия Observe/Assisted/Autonomous и те же ограничения Catch-up.
- Desktop не исполняет задания, пока процесс приложения завершён; отдельный daemon/service не вводится.
- В обоих случаях период sleep/hibernation может создать пропуск; после возобновления применяется та же occurrence policy.

## Обязательные contract tests

- повторная обработка одного occurrence не создаёт дубликат Run;
- два Web replicas одновременно обрабатывают один occurrence — создаётся не более одного Run;
- Observe coalesces несколько пропущенных occurrences в один актуальный Run с корректным audit coverage;
- Assisted и Autonomous пропускают накопившиеся occurrences по умолчанию;
- Catch-up работает только при явном opt-in и соблюдает max count, max age, action allowlist и budgets;
- approval от старого Plan не переносится на новый catch-up Run;
- изменение/отзыв Execution Identity или policy до replay блокирует side effects;
- creator departure не влияет на schedule ownership и occurrence processing;
- Desktop restart и Web replica restart применяют одинаковую доменную семантику;
- изменение schedule revision не меняет уже созданные Runs и occurrence outcomes.

## Границы решения

Это решение определяет default missed-occurrence policy и обязательные safety invariants. Оно не задаёт точные значения max age/count/budgets, полный формат occurrence ledger, cron/time-zone/DST semantics, административный UI или синхронизацию расписаний между Desktop и Web. Эти детали требуют отдельного проектирования до реализации scheduler.
