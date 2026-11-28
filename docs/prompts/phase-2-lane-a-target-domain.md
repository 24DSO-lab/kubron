# Phase 2 — Lane A: Target Domain

## Назначение

Реализовать независимый доменный пакет Target Foundation по принятым ADR. Это временное задание на реализацию; постоянная архитектурная спецификация находится в ADR.

## Обязательные ADR

- ADR-0053 — Trusted Target Catalog and Ownership Resolution
- ADR-0054 — Target Group Aggregate and Revision Lifecycle
- ADR-0055 — Consistent Target Resolution and Immutable Snapshots
- Также соблюдать ADR-0037 (authorization/delegation), ADR-0038 (Critical Audit), ADR-0040/0041 (Operation Context и Target Groups), ADR-0058 (Phase 2 scope).

Все ADR находятся в `docs/adr/`. Не вводить альтернативную архитектурную модель. Если ADR противоречат существующему коду или друг другу, остановиться и сообщить конкретное противоречие.

## File ownership

Разрешено изменять:
- `internal/target/**`
- unit/property tests для `internal/target` (внутри пакета или соответствующих test-файлов этого пакета).

Не изменять:
- `internal/store/**`, `internal/pgstore/**`, migrations;
- `internal/autonomy/**`;
- app composition, transport/UI, существующий cluster catalog и credential infrastructure;
- ADR и другие постоянные документы.

Не расширять allowlist без согласования.

## Реализовать

1. Явный `OwnerScope` ровно двух видов: Organization(ID) и Team(ID). Пустой ID и implicit owner запрещены. Не переиспользовать `internal/tenant.Scope` как owner scope.
2. Target Group aggregate: opaque ID, immutable OwnerScope, creator provenance, selector definition, sharing metadata, monotonic Revision, timestamps и lifecycle.
3. CAS semantics: Update/Deactivate принимают ExpectedRevision; stale writer получает стабильный conflict. Каждая semantic mutation увеличивает revision ровно один раз. Last-write-wins запрещён.
4. Deactivation — tombstone. В Phase 2 не реализовывать physical delete или reactivation.
5. Узкий backend interface для атомарных aggregate operations (Create/Get/Update/Deactivate/List). Домен не импортирует конкретное хранилище, transport, Wails/Vue или pgstore.
6. Trusted ownership resolver interface. Он возвращает только доверенные owner facts; он не является authorization и не принимает клиентские OrgID/TeamID как доказательство владения.
7. Selector validation с server-side bounds. Клиент не может увеличить лимиты. Не выполнять неограниченные запросы.
8. Отдельные типы `ResolutionReport` и `TargetSnapshot`. Partial report — только диагностика. Snapshot допустим только после полного успешного resolution; каждый target обязан иметь UID. ResourceVersion хранить отдельно от identity.
9. Узкий `ClusterReader`/resolution port, не создающий Kubernetes clients и не читающий kubeconfig. Интеграция будет подключена Lane C через существующие Broker/PlanFor/ConfigFor seams.
10. Контракт атомарной публикации Snapshot: Group revision проверяется и snapshot публикуется в одной consistency boundary. Повторного чтения revision до/после записи недостаточно.
11. Безопасные reason codes и отсутствие утечки foreign-tenant metadata.

## Важное разделение ответственности

- Ты владеешь только доменными типами, use cases и интерфейсами пакета `internal/target`.
- Lane C реализует persistence/adapters за интерфейсами, которые ты объявишь.
- В первом рабочем коммите (или сразу после него) опубликуй C краткий список exported interfaces, сигнатуры и семантику атомарности. Не жди завершения всего домена.
- Не создавай временные интерфейсы в чужих пакетах и не меняй C-owned файлы.

## Запрещено в Phase 2

- Plans, Approvals, Run/Step, leases, scheduler;
- Kubernetes mutations / write actions;
- Autonomous execution или AI-generated commands/plans;
- обход Broker, `PlanFor`, `ConfigFor`, cluster catalog;
- новый secrets subsystem или обязательный cluster agent/operator.

## Обязательные тесты

- OwnerScope validation и spoofed owner rejection;
- immutable owner;
- stale revision, concurrent updates, monotonic revision;
- deactivation/tombstone;
- selector validation и server-side limits;
- partial report никогда не становится snapshot;
- missing UID / duplicate or ambiguous identity;
- ResourceVersion не используется как identity;
- Group revision race и atomic snapshot publication;
- ownership change during resolution;
- cross-tenant metadata leakage;
- deterministic/stable reason codes.

## Definition of Done

- Пакет компилируется и имеет unit/property tests.
- Нет зависимостей на конкретные persistence/transport реализации.
- Интерфейсы для Lane C опубликованы.
- Нет изменений вне allowlist.
- CI по затронутому коду зелёный.
- В PR указаны проверенные ADR и ограничения, которые остались за рамками.

Не переводить ADR в `Implemented`: это делается после интеграции всей Phase 2, review и зелёного CI.
