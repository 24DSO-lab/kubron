# ADR-0040: Operation Context и Resource Identity

- Статус: принято (ретроспективная фиксация согласованной модели)
- Дата: 2026-11-28
- Связано: ADR-0012, ADR-0026, ADR-0035, ADR-0037, ADR-0039; Phase 0 Executable Contracts; Executable Platform Architecture Specification

## Контекст

Для безопасного планирования и исполнения недостаточно передать ID пользователя, активный кластер или набор Kubernetes-объектов. Система должна однозначно различать инициатора, trigger, технического субъекта, полномочия, tenant scope, runtime owner и конкретную версию цели. Иначе фоновые операции могут случайно наследовать личные credentials или права, а повторно созданный Kubernetes-объект — быть принят за прежний.

## Решение

1. Каждая Operation/Run работает с явным типизированным Operation Context. Контекст содержит как минимум:
   - Actor/Initiator — кто запросил или создал действие;
   - Trigger — интерактивный запрос, расписание или системное событие;
   - Execution Identity — от имени какого principal выполняется действие;
   - Authorization Context — tenant scope, grants, delegation и прочие проверяемые полномочия;
   - Runtime Owner — Desktop process либо серверный execution runtime;
   - Operation/Action identity и версии;
   - target scope, policy binding и correlation identifiers.
2. Эти измерения независимы. Initiator является provenance/audit metadata и не становится автоматически Execution Identity. Runtime Owner не является principal и не выдаёт permissions. Trigger не выдаёт полномочия.
3. Для интерактивного пользовательского действия UserPrincipal допустим только при действующей сессии и актуальной app-RBAC. Для фонового/автоматического исполнения используются только явно назначенные TeamPrincipal или ServicePrincipal согласно ADR-0035.
4. Operation Context создаётся и проверяется backend. Client-supplied Actor, owner, tenant, permission, revision или execution identity не принимаются как доказательство полномочий.
5. Resource Identity Kubernetes-объекта включает устойчивую область и идентификатор: ClusterID, Group/Version/Kind, Namespace (если применимо), Name и UID.
6. Kubernetes ResourceVersion — не постоянная идентичность объекта. Она используется как наблюдаемая версия/precondition для конкретной операции. После удаления и повторного создания объекта новый UID означает другой Resource Identity, даже если namespace/name совпадают.
7. Target и Operation Context, использованные при планировании, фиксируются в immutable Plan. Изменение существенного контекста требует нового Plan и повторной применимой проверки/approval.
8. Контекст не содержит credentials, токены, kubeconfig или секретные provider payloads. Он хранит только безопасные references и версии.

## Альтернативы

- Использовать текущего вошедшего пользователя как универсальную identity для всех операций.
- Определять owner/tenant по данным, переданным клиентом.
- Считать namespace/name достаточной идентичностью Kubernetes-объекта.
- Смешать Execution Identity и Runtime Owner.

Варианты отклонены как небезопасные при background execution, multi-tenant и delete/recreate сценариях.

## Последствия

- Все новые application use cases получают явный Operation Context.
- Authorization и credential resolution проверяют его независимо и fail-closed.
- Audit связывает инициатора, trigger, execution principal, Operation/Run/Step и target без раскрытия секретов.
- Для target snapshots и preconditions используются разные поля: UID для identity, ResourceVersion и action-specific facts для актуальности.
