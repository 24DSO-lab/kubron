# ADR-0048: Cross-cluster Orchestration и независимые sub-runs

- Статус: принято (ретроспективная фиксация согласованной модели)
- Дата: 2026-11-28
- Связано: ADR-0035, ADR-0037, ADR-0040…0047; Execution Identity & Durable Operations Design

## Контекст

Одна Operation может затрагивать несколько кластеров и Teams. Общая identity родительского Run или единая транзакция для всех Kubernetes API не существуют. Попытка наследовать credentials между кластерами или считать multi-cluster исполнение атомарным создаёт риск cross-tenant доступа и ложного статуса успеха.

## Решение

1. Cross-cluster execution моделируется как parent Run с отдельными cluster-scoped child Runs/sub-runs.
2. Parent Run отвечает за orchestration, correlation, aggregate status и пользовательский lifecycle. Он не обязан иметь Kubernetes Execution Identity.
3. Каждый sub-run получает собственную явно назначенную TeamPrincipal или ServicePrincipal и соответствующий CredentialBinding до создания собственного Plan.
4. Execution Identity одного кластера не наследуется другим sub-run, инициатором или parent Run. Identity binding включается в immutable Plan и Approval конкретного sub-run.
5. Каждый sub-run независимо выполняет tenant/ownership checks, grants, Cross-Team Delegation, cluster access, credential resolution, Kubernetes RBAC, autonomy policy, target preconditions и critical audit checks.
6. Cross-Team target требует delegation от Team-владельца именно этого target конкретному ServicePrincipal той же Organization. Parent Run и Organization ownership не заменяют delegation.
7. Изменение identity, credential binding, delegation, target snapshot или policy для одного sub-run не переносится автоматически на соседние sub-runs. Требуется отдельный Plan/approval для затронутого sub-run.
8. Parent Run агрегирует результаты, сохраняя per-cluster outcome. Partial success является полноценным наблюдаемым состоянием, а не скрываемой ошибкой.
9. Distributed transaction между кластерами не имитируется. Успех в одном кластере не откатывается автоматически из-за отказа другого.
10. Execution order может использовать явно заданные waves/batches и blast-radius limits. Зависимости и порядок должны быть частью immutable Plan. Нельзя запускать следующую wave, если её gate conditions не выполнены.
11. Cancellation parent Run кооперативна: она запрещает запуск новых sub-runs/steps, но не гарантирует отмену уже отправленных Kubernetes requests. Все активные/unknown sub-runs требуют reconciliation.
12. Cross-cluster partial execution допустим только когда Operation contract явно определяет независимость целей, допустимые partial outcomes и способ aggregate verification.

## Альтернативы

- Одна общая Kubernetes identity для parent Run.
- Наследование credentials первого кластера на остальные.
- Считать весь multi-cluster Run атомарной транзакцией.
- Автоматически откатывать все успешные кластеры при ошибке одного.

Отклонены из-за нарушения least privilege и невозможности обеспечить distributed atomicity Kubernetes API.

## Последствия

- Plan/Approval/Execution Identity создаются на уровне каждого sub-run.
- Parent Run сохраняет только orchestration context и ссылки на child outcomes.
- Требуются contract tests на изоляцию identity, delegation и tenant scope между кластерами.
- UI и audit показывают per-cluster progress, partial success и unknown outcomes.
