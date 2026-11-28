# ADR-0056: Детерминированная оценка Autonomy Policy

- Статус: предложено
- Дата: 2026-11-28
- Область: Phase 2 — Target & Policy Foundation
- Связано: ADR-0035, ADR-0037, ADR-0040, ADR-0042, ADR-0044, ADR-0053, ADR-0055

## Контекст

ADR-0042 задаёт Observe/Assisted/Autonomous, иерархию Organization → Cluster → Namespace → Workload → Object → Action, дополнительные Target Group bindings и правило: нижний уровень может только ограничивать верхний. Для реализации нужен детерминированный evaluator, который не смешивает policy decision с grants, credentials или Kubernetes RBAC.

## Предлагаемое решение

1. Policy evaluation — чистый доменный use case над явным immutable evaluation input. Он не обращается к UI, не строит Kubernetes client и не выполняет side effects.
2. Evaluation input включает доверенный principal/tenant context, target identity, typed operation/action reference (если задан), применимые policy/profile references и их revisions, а также достоверные facts, необходимые для scope matching.
3. Effective Policy вычисляется как пересечение всех применимых ограничений: Organization, Cluster, Namespace, Workload, Object, Action и дополнительных Target Group bindings. Ни одна более узкая policy не может расширить разрешения более широкой.
4. Если policy отсутствует, источник недоступен, binding неоднозначен, revision устарела или evaluator не может доказать применимость ограничения, итоговый режим — Observe и mutating action запрещён.
5. Evaluation возвращает не только mode, но и структурированный explain result: matched policy/profile IDs и revisions, ограничения, стабильные reason codes и список неизвестных/недоступных inputs. Не возвращать чувствительные сведения о чужих targets.
6. Policy не выдаёт grants, credentials, delegation или Kubernetes permissions. Положительное policy decision не заменяет authorization evaluation, Kubernetes RBAC, credential validity, approval или action-specific preconditions.
7. Approval requirements, risk ceilings, budgets, time windows, verification/recovery constraints и audit requirements являются ограничениями. Конфликт разрешается наиболее строгим результатом; отсутствие достоверного результата блокирует mutation.
8. Evaluation должен быть детерминированным для одинаковых input revisions и одинаковых facts. Не допускается скрытый приоритет «последняя запись побеждает», порядок обхода БД или зависимость от порядка массивов.
9. Решение evaluation фиксирует revisions, на которых оно основано. До будущего side effect policy подлежит повторной проверке; изменение несовместимой policy блокирует продолжение по правилам Plan/Run ADR.
10. Phase 2 может реализовать evaluation и preview, но не разрешает выполнение mutating action. Enforcement перед side effect остаётся обязательным в будущих Operation/Run layers.

## Альтернативы

- Один глобальный Autonomous toggle.
- Приоритет дочерней policy над родительской.
- Считать policy engine источником authorization.
- При ошибке evaluation продолжать с последним известным allow.
- Использовать порядок хранения как неявный приоритет.

Отклонены как недетерминированные и небезопасные.

## Последствия и открытые вопросы

- Contract Freeze должен зафиксировать policy rule schema, conflict model, reason code registry и revision vector.
- Нужно определить, какие Kubernetes hierarchy facts берутся из trusted catalog и какие требуют read query.
- Нужно определить поведение при нескольких одновременно применимых Target Group profiles/policies; базовое правило — пересечение ограничений без расширяющего приоритета.
- Нужны property-based tests: permutation invariance, monotonic restriction, fail-closed, missing facts, stale revisions, cross-tenant isolation.
