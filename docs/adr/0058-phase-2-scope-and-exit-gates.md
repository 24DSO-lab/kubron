# ADR-0058: Границы Phase 2 и критерии завершения Target & Policy Foundation

- Статус: предложено
- Дата: 2026-11-28
- Область: roadmap / delivery governance
- Связано: ADR-0039, ADR-0041, ADR-0042, ADR-0052, ADR-0053…0057

## Контекст

После Phase 1 в refactor интегрирован фундамент Identity, Credential Resolution, Authorization и Critical Audit. Следующий этап должен добавить Target и Policy Foundation, не перескакивая к Plans, Run Engine и Kubernetes write actions. Зелёный CI сам по себе не доказывает корректность security semantics.

## Предлагаемое решение

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
5. До реализации должны быть приняты Phase 2 Contract Freeze: точные types/interfaces, dependency direction, trusted ownership adapter, persistence ownership, selector limits, reason codes, shared contract test suites и file allowlists.
6. Desktop/File и Web/PostgreSQL обязаны иметь одинаковые доменные инварианты. Platform capability влияет на доступность preview/runtime, но не на authorization result.
7. Exit review включает: code review, security review, concurrency/race tests, tenant isolation tests, persistence parity, i18n/error surface review, полный CI и проверку отсутствия write path.
8. Завершение Phase 2 открывает подготовку Phase 3 (Assisted Planning) только после отдельного architecture review. Оно не означает разрешение на Kubernetes write actions.

## Альтернативы

- Сразу реализовать Plans и write actions вместе с Target/Policy.
- Считать preview равнозначным approval.
- Начать параллельные implementation lanes до фиксации общих contracts.
- Принимать зелёный CI как единственный exit criterion.

Отклонены из-за нарушения фазовых gates и риска неодинаковой интерпретации security contracts.

## Последствия и открытые вопросы

- После принятия этого ADR нужно подготовить отдельный Phase 2 Contract Freeze, сверенный с актуальным refactor.
- Только после Contract Freeze готовится Engineering Package и назначаются независимые lanes.
- Если в ходе проектирования понадобится изменение ранее принятого решения ADR-0041/0042, это оформляется новым ADR; старые ADR не переписываются.
