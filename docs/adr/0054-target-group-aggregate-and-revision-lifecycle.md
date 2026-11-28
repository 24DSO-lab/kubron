# ADR-0054: Target Group aggregate и жизненный цикл Revision

- Статус: принято
- Дата: 2026-11-28
- Область: Phase 2 — Target & Policy Foundation
- Связано: ADR-0026, ADR-0037, ADR-0040, ADR-0041, ADR-0053

## Контекст

ADR-0041 закрепляет семантику Target Group: один неизменяемый owner scope, sharing не выдаёт доступ, изменения увеличивают revision. Для реализации требуется определить границы агрегата и правила конкурентных изменений, не превращая общий Store в универсальный CRUD и не связывая Target domain с конкретной БД.

## Решение

1. Ввести самостоятельный Target domain с собственными типами, use cases и узким backend-контрактом. Domain package не импортирует transport, Wails/Vue, internal/pgstore или конкретную файловую реализацию.
2. Target Group является агрегатом с opaque ID, immutable OwnerScope, Creator/CreatedAt provenance, declarative selector definition, sharing metadata, monotonic Revision и timestamps.
3. Создание требует доверенного actor context и проверки права создавать группу в выбранном owner scope. Клиент не может назначить произвольный Organization/Team owner.
4. Update принимает ExpectedRevision. Нулевая revision не является wildcard. Успешная мутация увеличивает revision ровно один раз; stale writer получает conflict. Last-write-wins запрещён.
5. Изменения состава, selectors, explicit members, owner-visible metadata или sharing semantics, влияющие на доступ/результат resolution, увеличивают revision. Поля, не влияющие на semantics, могут обновляться без изменения revision только если это явно доказано и задокументировано.
6. OwnerScope после создания неизменяем. Перенос между Organization/Team — не update; если такой сценарий когда-либо потребуется, он оформляется отдельным use case с отдельной авторизацией и аудитом.
7. Sharing является отдельной ACL/visibility metadata. Она не добавляет grants, delegation, Kubernetes RBAC или право менять группу.
8. Удаление группы — version-checked lifecycle transition, а не физическое удаление, пока существуют ссылки из snapshots, audit или будущих Plans. Retention и окончательное удаление определяются отдельной политикой.
9. Backend предоставляет атомарные операции агрегата, а не общий CRUD. File/Desktop реализация должна сериализовать read-modify-write и использовать атомарную durable replacement; PostgreSQL/Web — CAS/transaction/unique constraints.
10. Target domain не использует internal/tenant.Scope как универсальный owner тип без проверки его семантики: текущий Scope требует OrgID, TeamID и ClusterID, тогда как Target Group owner — ровно Organization или Team. Нужен отдельный явный OwnerScope или эквивалентный тип.

### Нормативный контракт агрегата и хранения

- `OwnerScope` имеет ровно два допустимых вида: `Organization(ID)` и `Team(ID)`. Пустые ID, неизвестный kind и implicit/default owner запрещены. В локальном Desktop используется локальный Organization scope, определённый ADR-0053.
- Backend API агрегата предоставляет атомарные Create, Get, Update(ExpectedRevision), Deactivate(ExpectedRevision) и List в пределах доверенного owner/visibility scope. Общий CRUD и last-write-wins запрещены.
- Конфликт ревизии возвращается как стабильная доменная ошибка conflict/stale_revision; повторное сохранение с тем же ExpectedRevision не может перезаписать более новую версию.
- Для File backend атомарность обеспечивается сериализацией read-modify-write внутри процесса и durable atomic file replacement. Для PostgreSQL — транзакцией и условным UPDATE по текущей revision. Обе реализации проходят один общий contract suite.
- Удаление в Phase 2 означает Deactivated tombstone. Повторная активация не входит в Phase 2. Физическое удаление не реализуется, пока существуют snapshots/audit references; retention не может удалять Critical Audit.
- Поля, влияющие на selectors, members, ownership, sharing/visibility или разрешение группы, всегда увеличивают revision. В Phase 2 нет исключений для «косметических» полей, чтобы избежать неоднозначного определения semantic change.

## Альтернативы

- Хранить Target Groups в общем Store как произвольные map/CRUD записи.
- Разрешить изменение owner scope при обычном Update.
- Использовать last-write-wins.
- Считать sharing эквивалентом delegation.

Отклонены из-за слабой конкурентной семантики, смешения ownership и visibility, а также риска обхода tenant boundaries.

## Последствия и ограничения

- До реализации integration owner должен подтвердить место композиции Target backend в internal/store.Store и соответствие существующим File/PostgreSQL conventions.
- Нужно определить tombstone/archival semantics и retention без удаления обязательного audit.
- Нужны одинаковые contract suites для File и PostgreSQL: create collision, immutable owner, stale update, revision increments, concurrent updates, sharing isolation, delete with references.
