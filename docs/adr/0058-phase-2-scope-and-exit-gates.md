# ADR-0058: Границы Phase 2 и критерии завершения Target & Policy Foundation

- Статус: Accepted
- Дата: 2026-11-28
- Область: roadmap / delivery governance
- Связано: ADR-0039, ADR-0041, ADR-0042, ADR-0052, ADR-0053…0057

## Контекст

После Phase 1 в refactor интегрирован фундамент Identity, Credential Resolution, Authorization и Critical Audit. Следующий этап должен добавить Target и Policy Foundation, не перескакивая к Plans, Run Engine и Kubernetes write actions. Зелёный CI сам по себе не доказывает корректность security semantics.

## Решение

1. Phase 2 включает только:
   - Target Group domain: owner scope, lifecycle, sharing metadata и revisioned mutations;
   - trusted target ownership resolution через существующий cluster catalog;
   - bounded selector validation и target resolution;
   - immutable Target Snapshots и безопасный preview/coverage;
   - Policy domain и deterministic fail-closed evaluator;
   - versioned Autonomy Profiles и scope bindings;
   - audit/security tests, persistence parity и cross-platform integration.
2. Phase 2 не включает:
   - Operation/Action Catalog runtime;
   - Plan creation/commit, approval workflows;
   - Run/Step engine, leases, scheduler;
   - Kubernetes mutating actions;
   - Autonomous execution;
   - AI-generated execution plans or commands;
   - новый secrets subsystem, отдельный daemon, обязательный cluster agent/operator.
3. Target preview и Policy preview — read-only capability. Ни один endpoint, RPC, UI action или background job Phase 2 не должен создавать Kubernetes side effect.
4. Все мутации Target/Policy/Profile как Kubron configuration требуют actor authorization, revision/CAS, durable critical audit согласно применимому security contract и tenant-scoped storage.
5. Контракт Phase 2 содержится в ADR-0053…0058 и связанных принятых ADR. Эти ADR задают обязательные domain invariants, security semantics, revision rules, fail-closed behavior и exit gates. Внутренние Go types/interfaces и file allowlists фиксируются во временных developer prompts без создания параллельной постоянной спецификации.
6. Desktop/File и Web/PostgreSQL обязаны иметь одинаковые доменные инварианты. Platform capability влияет на доступность preview/runtime, но не на authorization result.
7. Exit review включает: code review, security review, concurrency/race tests, tenant isolation tests, persistence parity, i18n/error surface review, полный CI и проверку отсутствия write path.
8. Завершение Phase 2 открывает подготовку Phase 3 (Assisted Planning) только после отдельного architecture review. Оно не означает разрешение на Kubernetes write actions.

### Правило фиксации контракта и временных заданий

Для Phase 2 не создаются отдельные Contract Freeze, Engineering Package, Architecture Review или постоянные architecture documents. Контрактом реализации являются ADR-0053…0058 вместе с уже принятыми ADR-0035…0042. Решение считается замороженным для реализации, когда соответствующий ADR имеет статус `Accepted`.

Все изменения контракта оформляются непосредственно в соответствующем ADR до начала или в рамках явно согласованного изменения scope. Если обнаружено противоречие с ранее принятым ADR, реализация останавливается; сначала согласуется изменение ADR, а не добавляется параллельная спецификация.

Developer prompts создаются только для активных задач, содержат ссылки на применимые ADR и file allowlist. После review, зелёного CI и интеграции реализации связанные ADR переводятся в `Implemented`, а завершённые prompts удаляются.

### Exit gates и границы

Phase 2 считается завершённой только после:

- security review ownership, tenant isolation, policy fail-closed и audit;
- concurrency/race tests для CAS, resolution publish и Profile/Binding revisions;
- одинакового доменного contract suite для File и PostgreSQL;
- проверки credential path и отсутствия обхода `ConfigFor`;
- i18n/error-surface review;
- полного CI;
- доказательства отсутствия Kubernetes write path, Plan/Approval/Run и autonomous execution.

После Phase 2 можно проектировать Phase 3 (Assisted Planning) отдельным набором ADR. Реализация Phase 2 сама по себе не разрешает Kubernetes write actions.

## Альтернативы

- Сразу реализовать Plans и write actions вместе с Target/Policy.
- Считать preview равнозначным approval.
- Начать параллельные implementation lanes до фиксации общих contracts.
- Принимать зелёный CI как единственный exit criterion.

Отклонены из-за нарушения фазовых gates и риска неодинаковой интерпретации security contracts.

## Последствия и ограничения

- Реализация начинается после принятия ADR-0053…0058. Developer prompts — временные задания; после review, зелёного CI и интеграции связанные ADR переводятся в `Implemented`, а prompts удаляются.
- Если реализация требует изменения принятого решения, сначала согласуется изменение соответствующего ADR; параллельная спецификация не создаётся.
