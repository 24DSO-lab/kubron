# ADR-0037: Grants технических principals и Cross-Team Delegation

- Статус: Implemented (2026-11-28)
- Дата: 2026-11-27
- Связано: ADR-0012, ADR-0026, ADR-0035; Phase 1 Contract Freeze; Cross-Team Delegation decision

## Контекст

Kubernetes RBAC не заменяет авторизацию внутри Kubron. Технической identity нужны собственные ограниченные полномочия. Organization-owned ServicePrincipal может работать с Team-owned target только при явном согласии команды-владельца.

## Решение

1. TeamPrincipal и ServicePrincipal получают только явно назначенные Kubron grants. Права создателя, администратора и всей команды не наследуются.
2. Grant ограничивает точные пары OperationID + ActionID, target scope и типизированные constraints.
3. Разрешающие grants складываются по union. Все constraints внутри одного grant применяются совместно (AND).
4. Tenant ownership, identity scope, обязательная delegation и другие внешние ограничения сужают результат (intersection).
5. Отдельные пользовательские Deny grants и модель last-grant-wins не вводятся.
6. TeamPrincipal ограничен своей Team и её кластерами; delegation не может расширить его scope.
7. ServicePrincipal принадлежит Organization, но не имеет доступа ко всем Team-owned targets автоматически. Для каждого target Team Admin соответствующей команды выдаёт явную Cross-Team Delegation конкретному ServicePrincipal той же Organization.
8. Organization Admin не может выдать или изменить delegation от имени Team.
9. Sharing Target Group не является grant, delegation или Kubernetes access.
10. Ownership, grant, delegation, identity state и текущие target facts проверяются из доверенных серверных источников. Client-supplied ownership не является доказательством доступа.
11. Нет grant/delegation, неоднозначность, expired/revoked state или невозможность вычислить constraint означает deny.
12. GrantDecision сообщает только результат проверки Kubron grants/delegation. Он не заменяет Kubernetes RBAC, credential validity, Autonomy Policy, Approval, Plan preconditions или action guardrails.

## Последствия

- Evaluator не получает credentials и не строит Kubernetes clients.
- Авторизация повторно вычисляется при каждой защищённой операции; stale positive cache запрещён.
- Изменение grants/delegation влияет на последующие проверки; оно не расширяет старый immutable Plan.
- Все изменения отдельно аудируются.

Реализация Phase 1 foundation интегрирована в `refactor`; по состоянию на 2026-11-28 владелец проекта подтвердил зелёный CI для завершённых lanes. Этот статус не разрешает Kubernetes write actions и не означает реализации Target/Policy, Plans или Run Engine.
