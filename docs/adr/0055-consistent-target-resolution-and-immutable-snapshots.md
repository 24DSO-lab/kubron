# ADR-0055: Согласованное разрешение целей и immutable Target Snapshots

- Статус: предложено
- Дата: 2026-11-28
- Область: Phase 2 — Target & Policy Foundation
- Связано: ADR-0040, ADR-0041, ADR-0044, ADR-0045, ADR-0053, ADR-0054

## Контекст

Target Group может быть динамической, но результат её разрешения не должен изменяться во время последующей работы с ним. ADR-0041 требует закреплённой Group Revision и immutable snapshot. Phase 2 должна реализовать безопасный read/preview path, не создавая пока Plans или Run Engine.

## Предлагаемое решение

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

## Альтернативы

- Разрешать selector заново на каждом шаге будущей операции.
- Сохранять только selector без результата resolution.
- Считать частичный результат полным scope.
- Использовать ResourceVersion вместо UID.
- Автоматически повторять resolution без ограничений.

Отклонены из-за нестабильного состава целей, риска исполнения на другом объекте и неконтролируемой нагрузки.

## Последствия и открытые вопросы

- Contract Freeze должен определить формат partial/complete resolution outcome и какие причины допустимо раскрывать без утечки сведений о чужих tenant targets.
- Нужно определить минимальный Kubernetes read capability, не обходя существующий Broker/Provider/ConfigFor путь.
- Нужны tests на Group revision race, delete/recreate UID change, partial cluster outage, timeout, selector budget, empty result и cross-tenant metadata leakage.
- Atomic Plan Commit из ADR-0045 остаётся отдельной будущей задачей; snapshot creation не обещает атомарность с будущим Plan.
