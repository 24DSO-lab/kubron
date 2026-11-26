# Schedule Time Zones and DST — accepted architecture decision

- Дата: 2026-11-26
- Статус: принятое продуктовое/архитектурное решение пользователя
- Связано: [Schedule Ownership](2026-11-26-schedule-ownership-decision.md), [Missed Schedule Occurrences](2026-11-26-missed-schedule-occurrences-decision.md), [Execution Identity & Durable Operations design proposal](execution-identity-durable-operations-design.md), ADR-0016, ADR-0026
- Область: расписания Kubron Desktop и Web

## Решение

**Каждое расписание использует собственную IANA time zone.** Расписание задаёт wall-clock время в выбранной зоне, а не фиксированный UTC offset и не timezone текущего браузера/компьютера.

Пример: расписание «каждый день в 09:00» с timezone `Europe/Berlin` остаётся привязанным к 09:00 по правилам `Europe/Berlin`, даже если пользователь меняет системную timezone или сезонно меняется UTC offset.

## Хранение и вычисление времени

- Schedule definition хранит явный IANA timezone identifier, например `Europe/Berlin`, `Europe/Moscow` или `America/New_York`.
- Не использовать сокращения вроде CET, EST или произвольный числовой offset как идентификатор timezone: они не описывают надёжно исторические и будущие DST rules.
- Локальное расписание (calendar/cron expression) интерпретируется в timezone, сохранённой в Schedule.
- Каждое вычисленное occurrence разрешается в конкретный абсолютный instant.
- Фактические timestamps создания Run, начала/окончания выполнения, audit и scheduler processing хранятся в UTC.
- Для объяснимости и воспроизводимости occurrence сохраняет исходные локальные дату/время, IANA timezone, рассчитанный UTC instant, schedule revision и используемую версию timezone database (tzdb), если runtime позволяет её получить.
- Изменение timezone компьютера, браузера, профиля пользователя или server host не изменяет существующее расписание.
- Изменение timezone самого расписания — явное изменение Schedule, авторизуемое и аудируемое; оно влияет только на будущие occurrences. Уже зарегистрированные occurrences и Runs не переписываются.

## Переходы DST

### Несуществующее локальное время (spring-forward gap)

Если локальное время не существует из-за перевода часов вперёд, occurrence для этого локального времени **пропускается**. Kubron не должен молча сдвигать его на первый допустимый instant, поскольку это изменило бы смысл расписания.

Пример: если часы переходят с 01:59:59 на 03:00:00, расписание на 02:30 в этот день не создаёт Run.

Пропущенный DST occurrence фиксируется с явной причиной вроде `dst_nonexistent_local_time` и обрабатывается согласованной occurrence ledger/audit семантикой. Это специальная календарная причина пропуска, не Catch-up.

### Дважды встречающееся локальное время (fall-back fold)

Если локальное время встречается дважды при переводе часов назад, создаётся **ровно один occurrence**. Kubron выбирает первое наступление локального времени (ранний UTC instant / first fold) и не запускает повторно при втором наступлении.

Выбор должен быть детерминированным и одинаковым для Desktop и Web.

## Согласование с missed-occurrence policy

- DST gap не превращается автоматически в Catch-up.
- Coalesce/Skip/Catch-up применяются к occurrences, пропущенным из-за остановки/недоступности scheduler, согласно [Missed Schedule Occurrences decision](2026-11-26-missed-schedule-occurrences-decision.md).
- Дублирующийся local time при fall-back не является двумя occurrences: occurrence identity строится так, чтобы обе физические реализации одного локального расписания не породили два запуска.
- Durable occurrence identity должна учитывать schedule ID/revision и календарный локальный occurrence в его IANA timezone, а разрешённый UTC instant и fold-resolution сохраняются как атрибуты occurrence.

## Desktop и Web

- Desktop и Web используют одну и ту же библиотеку/контракт расчёта schedule occurrences и одинаковые DST правила.
- Различаться могут только источник scheduler ticks и persistence adapter.
- Web replicas должны получать одинаковое разрешение occurrence для одной schedule revision независимо от timezone host.
- Desktop restart восстанавливает календарные occurrences по timezone расписания, а не по текущей timezone ОС.

## Обязательные contract tests

- смена timezone браузера/ОС/server host не меняет расписание;
- расписание в `Europe/Berlin` остаётся на заданном wall-clock времени до и после сезонного изменения UTC offset;
- DST gap создаёт явный skipped outcome и не сдвигает запуск на другое время;
- DST fold создаёт ровно один occurrence по правилу first fold;
- повторный scheduler tick и две Web replicas не создают дубликат;
- Desktop и Web одинаково разрешают тестовые timezone transitions;
- изменение timezone schedule создаёт новую revision и не переписывает уже созданные occurrences/Runs;
- UTC timestamps и сохранённые local-time metadata позволяют объяснить фактическое исполнение.

## Границы решения

Это решение фиксирует IANA timezone, независимость от timezone среды, UTC storage и поведение DST gap/fold. Полная грамматика календарных выражений, пользовательский UI выбора timezone, политика обновления tzdb и точное представление occurrence key должны быть определены в scheduler contract до реализации.
