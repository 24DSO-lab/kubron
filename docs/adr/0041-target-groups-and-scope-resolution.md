# ADR-0041: Target Groups и безопасное разрешение scope

- Статус: принято (ретроспективная фиксация ранее согласованной модели)
- Дата: 2026-11-28
- Связано: ADR-0012, ADR-0026, ADR-0037, ADR-0040; Executable Platform Architecture Specification; Cross-Team Delegation and Target Group Governance decision

## Контекст

Операции должны работать как с иерархией Kubernetes, так и с произвольными наборами объектов, включая объекты разных кластеров и Teams. При этом группировка целей не должна превращаться в механизм выдачи доступа. Динамическая выборка не может незаметно менять уже одобренный набор целей.

## Решение

1. Target Group — самостоятельный доменный объект, задающий логическую группу Kubernetes targets. Он может включать цели из нескольких кластеров и Teams, но сам по себе не предоставляет доступ, credentials, grants или delegation.
2. Владелец Target Group — ровно одна Organization или Team. Owner scope неизменяем после создания. Creator хранится только как provenance.
3. Sharing Target Group управляет видимостью/совместным использованием группы, но не изменяет tenant ownership, Kubernetes access или authorization grants.
4. Разрешение целей всегда выполняется относительно доверенного server-side catalog и текущих проверок tenant, cluster access, app-RBAC, principal grants, delegation и Kubernetes credentials/RBAC. Client-supplied ownership не является доказательством.
5. Поддерживаются:
   - явные ссылки на объекты;
   - декларативные Kubernetes label selectors;
   - ограниченный allowlist field selectors там, где он безопасно поддерживается.
   Произвольный код, скрипты, функции, внешние запросы и неограниченные expressions не допускаются.
6. Selector проходит canonical validation, ограничения размера/сложности, timeout и query budget. Селектор не должен создавать неограниченную нагрузку или скрытую динамическую логику.
7. Каждая принятая мутация Group увеличивает монотонную Group Revision. Revision изменяется при изменении selectors, explicit members, owner-visible metadata или sharing semantics, если это влияет на разрешение/использование группы.
8. Resolution выполняется по закреплённой Revision: прочитать revision, разрешить targets, повторно проверить revision и сохранить snapshot. При гонке — ограниченное число повторов; при повторной гонке или недоступности источника — отказ, а не приблизительный результат.
9. Результат resolution — immutable Target Snapshot с cluster ID, GVK, namespace/name, UID и нужными action-specific precondition markers. UID обязателен для существующего объекта. ResourceVersion сохраняется как наблюдаемая версия/precondition, но не подменяет UID.
10. Изменение Target Group после создания Plan не меняет snapshot старого Plan. Для нового состава требуется новый resolution, Plan и approval.
11. Для mutating Plan действует all-or-nothing по умолчанию: если хотя бы одна цель не проходит scope/authorization/precondition checks, весь Plan блокируется. Partial execution допустим только если контракт конкретной Operation доказывает независимость целей и это явно отражено и одобрено.
12. Cross-Team target требует отдельной явной delegation от Team-владельца конкретному ServicePrincipal согласно ADR-0037. Ни Organization ownership группы, ни sharing, ни Organization Admin не заменяют согласие Team-владельца.

## Альтернативы

- Считать Target Group grant или способом делегирования доступа.
- Использовать только иерархию Organization/Cluster/Namespace и запретить произвольные группы.
- Разрешать selectors непосредственно при каждом Step без immutable snapshot.
- Разрешать mutating операции по частично доступным наборам по умолчанию.

Отклонены из-за смешения discovery и authorization, нестабильного scope и риска непредсказуемого частичного изменения инфраструктуры.

## Последствия

- Нужен отдельный Target domain с owner, revision, bounded selector resolver и snapshot backend.
- Любая операция независимо проверяет доступ к каждому target.
- Plan и Approval связываются с конкретным snapshot; изменения группы не меняют уже утверждённую операцию.
- Target Group не может использоваться для обхода Team ownership и Cross-Team Delegation.
