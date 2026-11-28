# ADR-0055: Согласованное разрешение целей и immutable Target Snapshots

- Статус: Accepted
- Дата: 2026-11-28
- Область: Phase 2 — Target & Policy Foundation
- Связано: ADR-0040, ADR-0041, ADR-0044, ADR-0045, ADR-0053, ADR-0054

## Контекст

Target Group может быть динамической, но результат её разрешения не должен изменяться во время последующей работы с ним. ADR-0041 требует закреплённой Group Revision и immutable snapshot. Phase 2 должна реализовать безопасный read/preview path, не создавая пока Plans или Run Engine.

## Решение

1. Resolution является отдельным доменным use case с явным входом: Target Group ID + Expected Group Revision + trusted requester context + bounded resolution options.
2. Resolver сначала читает группу и фиксирует revision, затем разрешает selector/explicit members через доверенный cluster catalog и Kubernetes read path, после чего повторно проверяет Group Revision перед сохранением результата.
3. Если revision изменилась во время resolution, результат не публикуется. Допускается ограниченное число повторов; исчерпание бюджета, timeout, неполные данные или недоступность обязательного кластера приводят к явной ошибке.
4. Snapshot неизменяем и содержит как минимум snapshot ID, source Group ID/Revision, время resolution, список target identities (ClusterID, GVK, namespace/name, UID), безопасные source/provenance references и outcome/coverage по каждому cluster.
5. ResourceVersion хранится отдельно как наблюдаемая версия и возможная action precondition. Он не заменяет UID и не является долговременной идентичностью объекта.
6. Если объект отсутствует, UID не получен или target identity неоднозначна, объект не включается как пригодная цель. Resolution report обязан показать безопасный reason code; нельзя молча подменять UID комбинацией namespace/name.
7. Результат resolution не является authorization grant. Проверка visibility/read access для preview и проверка прав на будущую операцию — разные решения. Snapshot может быть сохранён даже с недоступными/неразрешёнными элементами только как diagnostic report, но не может считаться complete executable scope.
8. Для Phase 2 UI/API разрешается показывать preview и coverage. Это не создаёт Plan, approval, Run, lease или разрешение на Kubernetes mutation.
9. Сохранённый snapshot не обновляется при изменении Target Group. Новая revision требует нового resolution и нового snapshot.
10. Размер группы, число кластеров, selector complexity, timeout и общий query budget ограничиваются серверной конфигурацией. Клиент не может повысить лимиты выше установленных backend.

### Нормативное разделение Report и Snapshot

- `ResolutionReport` — диагностический результат, который может быть partial. Он содержит coverage и безопасные reason codes по кластерам/элементам и никогда не используется как executable scope.
- `TargetSnapshot` создаётся только при полном успешном resolution всех обязательных источников и targets, когда у каждого объекта есть UID. Partial report не преобразуется в snapshot автоматически.
- Snapshot публикуется атомарно только если Group всё ещё имеет ExpectedRevision. File backend выполняет проверку revision и запись под одной блокировкой; PostgreSQL — в одной транзакции. Повторное чтение revision без атомарной публикации недостаточно.
- Snapshot хранит Group ID/Revision, время, канонические owner observations, ClusterID, GVK, namespace/name, UID и отдельно ResourceVersion как observed precondition. Snapshot immutable.
- Изменение ownership кластера во время resolution обнаруживается повторным чтением trusted catalog; при изменении resolution прекращается. Snapshot не является доказательством доступа: ownership, grants, delegation, credentials и Kubernetes RBAC повторно проверяются в будущих Plan/side-effect слоях.
- Kubernetes read path использует существующие credential/connection seams (`PlanFor` и `ConfigFor` либо их штатный subject-aware эквивалент). Запрещено читать kubeconfig или строить `rest.Config` из Target domain.
- Preview может показывать partial ResolutionReport. Только complete TargetSnapshot считается стабильным результатом resolution; Phase 2 не создаёт Plan/Approval/Run и не выполняет mutation.

### Outcome и безопасные reason codes

Outcome имеет значения `complete` и `partial`; ошибки, при которых нельзя сформировать безопасный report (например, invalid request или storage failure), возвращаются как доменные ошибки. Reason codes — стабильные машинные коды, а не тексты Kubernetes API. Минимальный набор: `cluster_unavailable`, `cluster_not_visible`, `selector_invalid`, `selector_limit_exceeded`, `target_not_found`, `target_identity_incomplete`, `ownership_unresolved`, `group_revision_changed`, `resolution_timeout`. Report не раскрывает имена/метаданные чужих tenant targets.

## Альтернативы

- Разрешать selector заново на каждом шаге будущей операции.
- Сохранять только selector без результата resolution.
- Считать частичный результат полным scope.
- Использовать ResourceVersion вместо UID.
- Автоматически повторять resolution без ограничений.

Отклонены из-за нестабильного состава целей, риска исполнения на другом объекте и неконтролируемой нагрузки.

## Последствия и ограничения

- `ResolutionReport` и `TargetSnapshot` имеют разные типы и разные гарантии: partial report — только диагностика; snapshot — только полный успешный результат с UID для каждого target.
- Kubernetes read path использует существующие Broker/Provider/`ConfigFor` seams. Никакой прямой доступ к kubeconfig или создание `rest.Config` из Target domain не допускаются.
- Обязательные tests: Group revision race, atomic publish, delete/recreate UID change, partial cluster outage, timeout, selector budget, empty result и cross-tenant metadata leakage.
- Atomic Plan Commit из ADR-0045 остаётся отдельной будущей задачей; snapshot creation не обещает атомарность с будущим Plan.
