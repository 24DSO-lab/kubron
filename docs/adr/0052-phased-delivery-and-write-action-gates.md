# ADR-0052: Поэтапная реализация и gates для Kubernetes write actions

- Статус: принято (ретроспективная фиксация согласованной последовательности)
- Дата: 2026-11-28
- Связано: ADR-0035…0051; Phase 0 Executable Contracts; Phase 1 Contract Freeze; Phase 1 Engineering Package; Executable Platform Architecture Specification

## Контекст

После v1.3.0 целевая архитектура включает техническую identity, authorization, target/policy foundation, planning, approvals, durable execution, recovery и постепенную автономность. Нельзя безопасно реализовывать Kubernetes write actions или Autonomous Mode до готовности всех необходимых security и execution foundations.

## Решение

Реализация идёт через последовательные фазы с отдельными contract/ADR gates:

1. **Architecture baseline / Phase 0:** границы доменов, package dependency graph, operation context, persistence/atomicity requirements, Desktop/Web parity, failure/threat model.
2. **Identity & Authorization / Phase 1:** User/Team/Service principals, subject-aware credentials, grants/delegation, durable critical audit. Phase 1 не разрешает новые Kubernetes write actions.
3. **Target & Policy Foundation / Phase 2:** Target Groups, ownership/revisions, bounded selector resolution, immutable target snapshots, Autonomy Policies и Profiles. Необходимо доказать restrictive inheritance и fail-closed evaluation.
4. **Assisted Planning / Phase 3:** typed Action Catalog, read-only/diagnostic Operations, immutable Plans, canonical digest, revision-bound approvals и atomic Plan Commit. До завершения revision consistency нельзя создавать исполнимые mutating Plans.
5. **Durable Run Engine / Phase 4:** Run/Step persistence, state machines, leases/fencing, idempotency, cancellation, recovery/reconciliation. Сначала read-only и side-effect-free действия.
6. **Restricted Assisted writes / Phase 5:** только ограниченный allowlist mutating Actions с action-specific preconditions, critical audit intent, verification, retry/recovery и security review.
7. **Policy-based Autonomy / Phase 6:** только ограниченные low-risk Actions, scopes, budgets, time windows и explicit policy enablement; staged rollout и kill switch.
8. **Cross-cluster and Goal-based / Phase 7:** parent/sub-runs, per-cluster identities, waves/blast-radius, partial outcomes и Goal decomposition в typed Actions.
9. Фазы могут иметь параллельные implementation lanes только после заморозки shared contracts и явного file ownership. Параллельная работа не может менять frozen contracts независимо.
10. Переход между фазами требует acceptance evidence: unit/contract/integration/E2E tests, failure injection, security review, Desktop/Web parity и подтверждённый CI. Зелёный CI сам по себе не доказывает архитектурную полноту, но является обязательным gate.
11. Новая mutating Action не включается в production allowlist только потому, что её adapter реализован. Она должна пройти все применимые gates ADR-0043…0047 и иметь отдельный documented risk/verification/recovery contract.
12. Никакой phase completion не означает автоматического включения Autonomous mode. Observe остаётся default; Assisted/Autonomous включаются только явной политикой и в поддерживаемом scope.

## Альтернативы

- Реализовать полный execution engine до identity/authorization foundations.
- Разрешить write actions сразу после успешного Plan creation.
- Параллельно менять shared contracts в нескольких lanes.
- Включить глобальный Autonomous mode после завершения одной фазы.

Отклонены как создающие security gaps и неуправляемые интеграционные риски.

## Последствия

- Каждая фаза имеет собственные ADR/contract freeze, implementation handoff и exit criteria.
- Следующий engineering package после интеграции Phase 1 — Target & Policy Foundation.
- Phase 3 включает нашу задачу Atomic Plan Commit, но не подменяет Phase 2 prerequisites.
- Write actions и Autonomous Mode остаются заблокированными до прохождения всех соответствующих gates.
