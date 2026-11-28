# ADR-0056: Детерминированная оценка Autonomy Policy

- Статус: Accepted
- Дата: 2026-11-28
- Область: Phase 2 — Target & Policy Foundation
- Связано: ADR-0035, ADR-0037, ADR-0040, ADR-0042, ADR-0044, ADR-0053, ADR-0055

## Контекст

ADR-0042 задаёт Observe/Assisted/Autonomous, иерархию Organization → Cluster → Namespace → Workload → Object → Action, дополнительные Target Group bindings и правило: нижний уровень может только ограничивать верхний. Для реализации нужен детерминированный evaluator, который не смешивает policy decision с grants, credentials или Kubernetes RBAC.

## Решение

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

### Нормативная модель Policy

Policy — versioned declarative constraint set, привязанный к Organization, Cluster, Namespace, Workload, Object или Action scope. Каждая запись имеет opaque ID, OwnerScope, monotonic Revision, actor provenance и lifecycle. Update использует ExpectedRevision/CAS. Отсутствие Policy не означает allow: применяется Observe.

Канонический набор ограничений Phase 2:

- mode ceiling: Observe, Assisted или Autonomous;
- typed Action allowlist/denylist (пустой/неизвестный Action reference не даёт разрешения);
- maximum risk ceiling;
- approval requirements;
- execution budgets и frequency limits;
- allowed time windows;
- verification/recovery constraints;
- mandatory audit constraints.

Evaluator вычисляет пересечение ограничений без приоритетов «последняя запись побеждает»: mode выбирается наиболее ограничительный; deny объединяется; allow пересекается; risk ceiling и budgets берутся по наиболее строгой границе; approvals и mandatory verification/audit объединяются; time windows пересекаются. Пустое пересечение, неизвестное значение или недоступный обязательный input дают Observe/deny.

### Facts, revisions и explainability

- Organization/Team/Cluster ownership берётся из trusted resolver ADR-0053. Namespace/Workload/Object facts берутся только из доверенного Kubernetes read result, привязанного к target identity; клиентские labels/owner fields не являются авторитетными.
- Evaluation принимает immutable input и возвращает immutable decision с revision vector: Policy IDs/Revisions, Profile IDs/Revisions, Binding IDs/Revisions и Target Group ID/Revision, если применимо.
- Минимальные стабильные reason codes: `policy_missing`, `policy_unavailable`, `policy_stale_revision`, `binding_ambiguous`, `fact_unknown`, `mode_restricted`, `action_not_allowed`, `risk_exceeded`, `approval_required`, `budget_exceeded`, `time_window_closed`, `constraints_conflict`.
- Порядок входных записей не влияет на результат. Для одинаковых canonical inputs результат идентичен; property tests проверяют permutation invariance и monotonic restriction.
- Если Action Catalog ещё не доступен, evaluator может вычислить scope-level preview без Action. Любая mutating decision без точного typed Action ID+Version запрещена; Phase 2 не включает runtime Action Catalog.
- Revision vector фиксирует наблюдавшиеся версии, но не обещает атомарную междоменную фиксацию. Перед будущим side effect Plan/Commit обязан повторно проверить все версии и полномочия согласно ADR-0045.

Policy evaluation не заменяет Kubron authorization, delegation, credential validity или Kubernetes RBAC.

## Альтернативы

- Один глобальный Autonomous toggle.
- Приоритет дочерней policy над родительской.
- Считать policy engine источником authorization.
- При ошибке evaluation продолжать с последним известным allow.
- Использовать порядок хранения как неявный приоритет.

Отклонены как недетерминированные и небезопасные.

## Последствия и ограничения

- Canonical constraint schema, conflict model, reason codes и revision vector определены выше и являются нормативным контрактом Phase 2.
- Ownership facts берутся из trusted resolver; Kubernetes hierarchy facts — из доверенного read result, привязанного к target identity.
- Одновременно применимые policies и bindings всегда объединяются пересечением ограничений, независимо от порядка.
- Обязательные property-based tests: permutation invariance, monotonic restriction, fail-closed, missing facts, stale revisions и cross-tenant isolation.
