# ADR-0049: Ownership и runtime semantics расписаний

- Статус: принято (ретроспективная фиксация явно согласованных решений)
- Дата: 2026-11-28
- Связано: ADR-0012, ADR-0016, ADR-0026, ADR-0035…0038, ADR-0040, ADR-0046…0048; Execution Identity & Durable Operations Design

## Контекст

Расписания должны переживать уход создателя и потерю браузерной сессии, но Desktop не должен превращаться в постоянно работающий daemon. Семантика повторов, timezone, DST и пропущенных запусков должна быть детерминированной и одинаково понятной в Desktop и Web.

## Решение

1. Владелец Schedule — Organization или Team, а не пользователь-создатель. Creator сохраняется только как initiator/provenance в audit metadata.
2. Уход создателя из Team, блокировка его учётной записи или потеря им роли не останавливают Schedule и не передают его ownership другому пользователю.
3. Team Admin управляет расписаниями своей Team; Organization Admin — расписаниями Organization. Эти административные полномочия не заменяют Execution Identity и authorization checks.
4. Schedule содержит явную Team/Service Execution Identity assignment для каждого cluster scope. Автоматический Run не использует личные credentials creator/initiator.
5. **Desktop schedule** локален и исполняется внутри процесса Kubron Desktop:
   - отдельный Desktop daemon/service не создаётся;
   - сворачивание окна, неактивность UI или блокировка ОС сами по себе не останавливают execution;
   - shutdown, reboot, logout ОС или crash процесса прекращают локальное исполнение;
   - sleep/hibernation физически приостанавливает его;
   - после следующего запуска Kubron восстанавливает durable state, выполняет reconciliation незавершённых occurrences и не повторяет неизвестные side effects вслепую.
6. **Web schedule** исполняется серверной платформой независимо от браузера, HTTP request, WebSocket и пользовательской вкладки. В HA один occurrence может создать не более одного Run.
7. Синхронизация Schedule между Desktop и Web не входит в эту модель и требует отдельного решения.
8. Расписание хранит IANA timezone. Wall-clock recurrence интерпретируется в указанной timezone, timestamps событий сохраняются в UTC.
9. DST semantics:
   - локальное время, попавшее в весенний несуществующий интервал, пропускается;
   - повторяющееся осеннее локальное время создаёт один occurrence — по первому наступлению;
   - tzdb update не должен задним числом создавать дубли уже обработанных occurrences.
10. Пропущенные occurrences:
    - Observe: допускается Coalesce до одного актуального observation run;
    - Assisted и Autonomous: по умолчанию Skip, без автоматического накопления и массового replay;
    - Catch-up допускается только как явный opt-in с лимитами количества, возраста, blast radius и повторной policy/authorization evaluation.
11. Каждому логическому occurrence соответствует стабильный occurrence key. Создание Run для occurrence атомарно и уникально в backend; повторный scheduler tick не создаёт дубликат.
12. Schedule trigger не является authorization grant. Перед каждым Run и каждым side effect заново проверяются identity, credential binding, grants, delegation, ownership, policy и target scope.
13. Изменение Execution Identity требует нового Plan для следующего Run; уже созданный Plan не перепривязывается к новой identity.

## Альтернативы

- Владение расписанием пользователем-создателем.
- Desktop daemon, который исполняет задания после закрытия приложения.
- Web execution, привязанный к WebSocket/browser session.
- Неограниченный catch-up всех пропущенных запусков.
- Использование локального времени без IANA timezone/DST semantics.

Отклонены из-за хрупкого ownership, скрытого always-on runtime и риска массовых повторных изменений.

## Последствия

- Schedule storage должен различать owner scope и creator provenance.
- Web backend требует HA-safe occurrence uniqueness; Desktop обеспечивает single-process durable semantics.
- Scheduler использует тот же Plan/Run/Step pipeline и все security gates.
- Неопределённые side effects после рестарта reconciled до retry.
