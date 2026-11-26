# Schedule Ownership — accepted architecture decision

- Дата: 2026-11-26
- Статус: принятое продуктовое/архитектурное решение пользователя
- Связано: [Execution Identity & Durable Operations design proposal](execution-identity-durable-operations-design.md), [Missed Schedule Occurrences](2026-11-26-missed-schedule-occurrences-decision.md), [Schedule Time Zones and DST](2026-11-26-schedule-timezones-dst-decision.md), ADR-0012, ADR-0016, ADR-0018, ADR-0022, ADR-0026
- Область: расписания Desktop и Web, автоматические и policy-triggered операции

## Решение

**Schedule принадлежит Organization или Team, а не пользователю, который его создал.**

Создатель расписания сохраняется как initiator/provenance для аудита и расследования, но не является владельцем расписания, execution principal или источником полномочий для последующих запусков.

### Владение и управление

- Organization-scoped schedule принадлежит Organization; Team-scoped schedule принадлежит конкретной Team.
- Team Admin может просматривать и управлять расписаниями своей Team в пределах app-RBAC.
- Organization Admin может управлять организационными расписаниями и выполнять административные действия в пределах app-RBAC.
- Управление включает pause/resume, изменение конфигурации и revoke/delete в соответствии с правами и audit requirements.
- Создание расписания не даёт создателю исключительных прав владения им.
- Уход создателя из Team, деактивация его пользователя или потеря им административной роли сами по себе не останавливают и не удаляют расписание.
- Изменение или отзыв расписания уполномоченным администратором должны быть авторизованы и записаны в Audit.

### Разделение полномочий

Владение расписанием не является разрешением на выполнение Kubernetes-действий.

Каждый occurrence и каждый Run по-прежнему обязан:
1. разрешить schedule owner и его актуальный tenant scope;
2. разрешить явно назначенную Team/Service Execution Identity;
3. проверить Kubron authorization principal/grants этой identity отдельно от Kubernetes RBAC;
4. проверить актуальный доступ к каждому target/cluster и effective autonomy policy;
5. повторить необходимые проверки непосредственно перед каждым side effect.

Инициатор расписания остаётся provenance metadata. Его текущие личные права не используются как execution authorization для автоматического Run. Отзыв Execution Identity, её grants, tenant access или policy блокирует дальнейшее выполнение независимо от того, кто создал расписание.

### Desktop и Web

- **Desktop:** расписание принадлежит локально выбранной Team/Organization scope и хранится в локальном состоянии Desktop. Оно исполняется только пока запущен процесс Kubron Desktop; блокировка ОС и свёрнутое окно не останавливают runner. Отдельный Desktop daemon не вводится.
- **Web:** расписание принадлежит Team/Organization в серверном хранилище и исполняется серверной платформой независимо от браузера, UI connection и WebSocket.
- Это решение не подразумевает автоматическую синхронизацию Desktop schedules и Web schedules. Такая синхронизация, перенос владения между средами и конфликт-резолюция требуют отдельного дизайна.
- В обеих средах сохраняются одинаковые доменные правила ownership, authorization, identity binding и audit; различается только platform execution/persistence adapter.

### Изменение расписания и уже созданные Runs

- Изменение расписания влияет на будущие occurrences.
- Уже созданный Run сохраняет собственный immutable Plan, Execution Identity binding, target snapshot и approval binding.
- Изменение или удаление расписания не должно молча переписывать уже созданные Runs.
- Если изменение конфигурации расписания меняет существенные параметры/targets/identity будущего запуска, новый occurrence строит новый Plan и проходит полную актуальную policy/authz validation.
- Pause/revoke предотвращает создание новых Runs. Отмена уже выполняющегося Run — отдельная операция с cooperative cancellation и reconciliation неизвестных side effects; она не подразумевается автоматически удалением расписания.

## Обязательные следствия для реализации

1. Schedule schema должна иметь явный owner scope/type и стабильный Organization/Team ID; поле creator/created_by хранится отдельно.
2. Авторизация управления schedule проверяет текущую роль вызывающего пользователя относительно owner scope, а не совпадение с creator ID.
3. Scheduler не использует creator credentials и не зависит от активной сессии создателя.
4. Каждый автоматический Run использует заранее назначенную Team/Service Execution Identity; для cross-cluster Run назначение выполняется отдельно на каждый cluster sub-run.
5. Web scheduler обязан дедуплицировать occurrence атомарно и безопасно работать при нескольких server replicas.
6. Audit фиксирует создание, изменение, pause/resume, revoke, actor, owner scope и изменения execution binding без записи секретов.
7. Contract tests должны покрывать оба сценария: создатель покинул Team, но schedule продолжает работать; пользователь без роли создателя не может управлять schedule, пока не имеет актуальных административных прав.
8. Desktop и Web должны иметь одинаковую доменную семантику, хотя сроки и поведение исполнения при остановке процесса различаются.
9. Политика пропущенных occurrences задаётся отдельно и должна соответствовать [принятому решению](2026-11-26-missed-schedule-occurrences-decision.md): Observe — Coalesce, Assisted/Autonomous — Skip по умолчанию; Catch-up — только явный opt-in с ограничениями.
10. Расписание использует собственную IANA timezone; правила DST gap/fold определены в [принятом решении](2026-11-26-schedule-timezones-dst-decision.md).

## Границы решения

Это решение определяет ownership и административное управление расписаниями. Стратегия пропущенных occurrences и timezone/DST semantics определены отдельными принятыми решениями. Синхронизация расписаний между Desktop и Web, перенос владения между средами и конфликт-резолюция по-прежнему требуют отдельного дизайна. Решение не разрешает write actions без принятых policy, identity, approval и recovery controls.
