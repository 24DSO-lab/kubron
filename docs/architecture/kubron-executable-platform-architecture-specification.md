# Kubron Executable Platform Architecture Specification

- Статус: **сводная инженерная спецификация; проект реализации, не разрешение на немедленную реализацию write-actions**
- Дата: 2026-11-26
- Проверенная база кода: `refactor` — `2330388d8d2460ad72ddc57dd003a8d30fb9f1c7`
- Основа: merged PR #260 и принятые архитектурные решения, перенесённые в эту ветку
- Цель: единая реализационная модель Control Plane + Operations + Intelligence + Autonomy Level 3 для Desktop и Web

## 1. Назначение и нормативность

Документ сводит целевую архитектуру, принятые решения по Execution Identity, technical authorization, Scope/Target Groups, Cross-Team Delegation и расписаниям в единый набор инженерных контрактов.

Метки:
- **MUST / MUST NOT** — обязательный инвариант.
- **SHOULD** — сильная рекомендация; отклонение требует документированного обоснования.
- **OPEN** — вопрос, который должен быть закрыт отдельным ADR/implementation design до соответствующей реализации.

Этот документ не объявляет новые runtime capabilities реализованными. Существующий код остаётся источником истины для текущего поведения; спецификация определяет целевое поведение новых компонентов.

## 2. Продуктовая и системная модель

Kubron — одна Kubernetes Operations & Intelligence Platform, а не набор независимых продуктов или микросервисов. Логические capabilities:

1. **Control Plane** — Organization/Team/Cluster catalogue, identities, access, Target Groups, scope and coordination.
2. **Operations Platform** — Kubernetes, Helm, GitOps, typed actions, operational workflows.
3. **Intelligence Platform** — evidence, dependency graph, diagnosis, correlation, problems, recommendations and explainability.
4. **Autonomy Layer** — policies, profiles, plans, approvals, execution, verification, recovery and audit.
5. **Shared Platform Foundation** — authz, credentials, tenancy, persistence, audit, events, jobs, HA, secrets and observability.

Предпочтительная архитектура — модульный монолит на существующем Go core. Не вводить отдельные сервисы, брокер сообщений или агенты до доказанной потребности в масштабировании, изоляции отказов или security boundary.

Целевой цикл:
`Observe → Understand → Decide → Approve (if required) → Execute → Verify → Recover/Escalate → Audit`

Целевая автономность — Level 3, внедрение поэтапное. Никакая новая write capability не должна появляться только потому, что существует UI-кнопка, Kubernetes client или техническая identity.

## 3. Существующая архитектурная основа

Проверенная ветка `refactor` уже содержит следующие seams:

| Существующий компонент | Роль и правило повторного использования |
|---|---|
| `internal/app` | Application facade для обеих оболочек; не превращать в место хранения всей новой доменной логики |
| `pkg/ext.Platform`, `internal/platform/{wails,web}` | Go platform boundary; core не импортирует Wails |
| `frontend/src/platform/` | Frontend API/events/files adapters; UI не вызывает Wails напрямую |
| `internal/auth` | Текущие user/session/app-RBAC механизмы; не притворяться, что Team/Service principals — обычные users |
| `internal/tenant`, `internal/teams` | Текущая Organization/Team ownership model |
| `internal/credential` | Provider/Broker/Subject/Store для Kubernetes credentials |
| `internal/store`, `internal/pgstore` | File/PostgreSQL persistence implementations |
| `internal/audit` | Существующий audit subsystem; его best-effort API не считать автоматически достаточным для durable side-effect audit |
| `internal/jobs` | Периодические service jobs с leader lock; не durable user-operation queue |
| Event Hub / `replicaevents` | Уведомления и invalidation, не источник истины о Run state |
| `internal/opsplatform` | Безопасный сбор данных, не произвольный write executor |
| Intelligence/Lifecycle domains | Сохраняют собственные модели и жизненные циклы; не сводить к универсальной модели результата |

### 3.1. ADR boundaries

Реализация обязана сохранять:
- ADR-0001/0002: core в `internal/app`, shell через Platform interface, никакого Wails import в core.
- ADR-0003/0009/0010: существующий RPC allowlist, WebSocket events, frontend platform adapters.
- ADR-0005/0006/0012: общий cluster manager, действующая модель app-RBAC ∩ Kubernetes RBAC, один cluster — одна Team, без универсального ClusterGrant.
- ADR-0007/0008: File/PostgreSQL Store, envelope encryption и key lifecycle.
- ADR-0016: durable shared state в PostgreSQL для HA; process-local state можно восстановить.
- ADR-0018: credential providers/Broker остаются единственным путём получения токенов и Kubernetes config.
- ADR-0022: аудит, redaction и наблюдаемость.
- ADR-0024…0034: доменные контракты Intelligence/Lifecycle не дублируются и не ломаются.

ADR-0026 прямо ограничивает текущую платформу: короткие синхронные операции — предпочтительный первый шаг; `internal/jobs` не предоставляет durable Run/Step lifecycle. Новая модель расширяет эти границы отдельными узкими интерфейсами, не заменяя существующие механизмы.

## 4. Архитектурные слои и зависимости

Целевые зависимости направлены внутрь:

```text
Transport adapters (Wails bindings / HTTP RPC / WebSocket)
                    |
              App facade
                    |
     Application use cases / orchestration
       |          |           |          |
   Control     Operations  Intelligence  Autonomy
       |          |           |          |
       +------ shared domain contracts --+
                    |
    AuthZ / Tenant / Credential / Store / Audit
                    |
       Platform adapters and infrastructure
```

Правила:
- Domain packages MUST NOT import Wails, HTTP transport, Vue DTOs or concrete PostgreSQL implementations.
- Application orchestration composes domain services; it does not duplicate their algorithms.
- Infrastructure implements narrow interfaces owned by the consuming domain.
- Cross-domain communication uses typed interfaces/events, not direct access to another domain's tables or internal structs.
- `internal/app` exposes transport-facing methods and composes dependencies; business rules live in domain/use-case packages.
- Avoid a universal CRUD Store, universal transaction abstraction, generic workflow engine or untyped action dispatcher.

Предварительные logical modules (имена provisional до package-boundary ADR):
- `identity`: UserPrincipal/TeamPrincipal/ServicePrincipal, grants, delegations.
- `target`: Scope, TargetGroup, revision, selector validation, resolution, immutable snapshot.
- `autonomy`: policy evaluation, profiles, effective mode and constraints.
- `operation`: Operation/Action catalog, Plan, Approval, Run, Step, execution and verification.
- `scheduler`: schedule ownership, calendar calculation, occurrence registration and dispatch.
- Existing `auth`, `tenant`, `teams`, `credential`, `store`, `audit` remain authoritative foundations and are extended through reviewed contracts.

## 5. Principal, identity and authorization contracts

### 5.1. Four independent identity dimensions

Every operation records and keeps separate:
1. **Actor / Initiator** — who requested or triggered it (user, scheduler, system trigger).
2. **Kubron Authorization Principal** — whose Kubron grants authorize the operation.
3. **Kubernetes Execution Identity** — whose Kubernetes credentials are used.
4. **Runtime Owner** — Desktop process or server replica currently holding the execution lease.

None is inferred from another. Creator/initiator is provenance, not background execution principal.

### 5.2. Principal types

- `UserPrincipal`: interactive user, current session and current app-RBAC.
- `TeamPrincipal`: technical principal belonging to one Team, limited to that Team.
- `ServicePrincipal`: technical principal owned/managed at Organization level, without automatic access to any Team or cluster.

Technical principals MUST NOT inherit creator, Organization Admin, Team Admin or broad Team member permissions. They do not receive generic Admin/Editor/Viewer roles.

### 5.3. Grants

A technical grant is explicit and bounded by:
- Operation IDs and Action IDs;
- Target Scope;
- constraints (environment, namespaces, GVK, time, budgets, concurrency, destructive-action limits);
- validity interval and revision;
- grant owner, actor and audit provenance.

Evaluation:
- matching allow grants are unioned;
- constraints inside each grant are conjunctive (AND);
- identity scope, tenant ownership, delegation, Kubernetes RBAC and Autonomy Policy intersect and only narrow;
- no matching grant, ambiguity, stale data or evaluation failure => deny;
- no last-grant-wins and no user-defined Deny grants; mandatory upper-level restrictions always override allows.

### 5.4. Cross-Team Delegation

A ServicePrincipal can operate on another Team's resources only through explicit Cross-Team Delegation:
- issued by Team Admin of the owning Team;
- addressed to one ServicePrincipal in the same Organization;
- bounded by Operation, Action, Target Scope, constraints and finite expiry;
- independently revocable by the granting Team;
- does not make the principal a Team member and does not create ClusterGrant.

Organization Admin manages Service Identity but cannot bypass Team consent. TeamPrincipal remains within its Team. Cluster transfer, Team deletion, expiry, revocation or inability to evaluate delegation => deny. A delegation change is revalidated before every side effect and never expands an existing Plan/Approval.

### 5.5. Effective authorization

For each target/action:
`Effective = PrincipalGrants ∩ TenantOwnership ∩ CrossTeamDelegation(if needed) ∩ KubernetesRBAC ∩ AutonomyPolicy ∩ PlanApprovalBinding`

Current app-RBAC is additionally required for interactive user actions and administrative changes to identities, grants, groups and schedules. Background execution does not inherit the initiator's current membership/role; it uses the assigned technical principal plus current grants, ownership, delegation and policy.

Authorization is evaluated at Plan creation and immediately before every side effect. A UI capability flag is never authorization.

## 6. Kubernetes credential resolution

### 6.1. Existing contract

ADR-0018 and `internal/credential` establish Provider/Broker/Subject/Store as the credential path. The current credential scopes are `user` and `team`; current `ConfigFor` is based on a session-oriented connection plan.

### 6.2. Required extension

Background execution requires a typed, session-independent credential subject:
- User subject for interactive use;
- Team subject for Team Identity;
- Service subject for Service Identity.

The exact API/signature is OPEN for a dedicated ADR after tracing `ConfigFor`, `ConnPlan`, provider Subject, credential key/AAD, refresh, challenge, revoke, pool keying and all call sites.

MUST NOT:
- fabricate a user Session for a technical identity;
- read decrypted credentials directly from Store;
- construct `rest.Config` outside the existing manager/provider path;
- bypass provider refresh, subject isolation, key lifecycle or security validation;
- silently fall back from revoked/invalid Service/Team credentials to user credentials.

The credential key, encryption associated data, provider subject and client/pool cache key must all include the complete subject identity and scope, so credentials cannot cross subjects or be confused between Team and Service. Service Identity adds a logical type to the existing protected credential subsystem; it does not create a second secrets system.

### 6.3. Rotation and revocation

- Each identity has a logical stable ID and immutable credential version/revision.
- New Runs bind to the currently verified version.
- Existing Runs re-resolve the current permitted version before the next side effect, subject to the accepted rotation policy.
- Revocation blocks new Runs and new side effects; an already submitted Kubernetes request may still complete.
- Identity replacement is explicit; it does not silently rewrite an in-flight Run's binding and requires a new Plan and applicable approval.
- Store only opaque version references in Plan/Run; never secrets or plaintext tokens.

## 7. Tenant, Scope, Target Groups and resolution

### 7.1. Ownership

ADR-0012 remains unchanged: each Cluster belongs to exactly one Team. No generic ClusterGrant.

Target Group is a target description, never an access grant. Owner scope is exactly one of Organization or Team; creator is audit provenance. Organization Groups may describe cross-Team targets; Team Groups are bounded to their Team; Custom Groups have one explicit immutable owner scope.

Group permissions are separate:
- manage group and sharing;
- use group in Plan creation;
- view definition;
- view resolved target metadata.

Sharing a group does not grant target visibility, app permission, Kubernetes RBAC or execution rights. Avoid leaking metadata for unauthorized targets.

### 7.2. Selectors and bounded resolution

Only declarative Kubernetes label selectors and explicit object references are allowed initially. Field selectors require a documented allowlist. No arbitrary scripts, expressions with code execution, user functions or external lookups.

Resolution must:
1. validate and canonicalize selectors in shared Go domain code;
2. constrain them to the group's declared scope and caller/principal's authorized scope;
3. apply time, object-count, request-count and response-size limits;
4. sort deterministically by Cluster ID, GVK, namespace, name, UID;
5. bind to exactly one immutable Group revision;
6. re-check that revision after resolution; revision race => bounded retry then conflict, never mixed-revision snapshot.

No silent truncation on limit exhaustion.

### 7.3. Target Snapshot and object identity

Each resolved target records Cluster ID, GVK, namespace/name, UID, observed ResourceVersion, group ID/revision, selector provenance and action-specific preconditions.

UID is mandatory for existing objects. Name is not identity. ResourceVersion is an observed state marker, not stable identity. A delete/recreate with same name has a different UID and MUST NOT satisfy an old Plan.

Before each side effect, fresh-read and validate UID, ownership, selectors where applicable, and action-specific preconditions. Where Kubernetes API supports UID/resourceVersion preconditions on the actual write, use them to close read/write races. If the operation cannot enforce a required precondition safely, block it rather than claim atomic protection.

**Revision binding precedence:** any Group revision change after Plan creation requires a fresh resolution, new Plan and new applicable Approval, as required by the accepted Scope/Target Groups decision. A stored snapshot is never mutated. The older Plan cannot be reinterpreted against a new Group revision.

### 7.4. Partial target resolution

- Mutating Plan: all-or-nothing by default. Any unresolved, unauthorized, ambiguous or precondition-invalid target blocks the Plan.
- No silent skipping, substitution, narrowing or automatic approval of a subset.
- Partial mutating execution is allowed only when a versioned Operation contract explicitly declares target independence and partial semantics; accepted/rejected targets, reasons and exact accepted snapshot are visible before approval.
- Runtime can still end partially succeeded after execution begins; report per-target outcomes and never hide failures.
- Read-only Observe may return partial results, but missing targets are marked unknown/unavailable; incomplete coverage is never evidence of health.

## 8. Autonomy policy and profiles

### 8.1. Modes

- `Observe`: inspect, collect evidence, diagnose and recommend; no mutating side effects.
- `Assisted`: prepare Plan and require configured human approval before protected actions.
- `Autonomous`: execute only explicitly permitted actions under all configured guardrails.

Default when no applicable policy exists, policy is ambiguous, stale or unavailable: `Observe`; for an attempted write this means deny.

### 8.2. Policy hierarchy

Policies can apply across Organization → Team → Cluster → Namespace → Workload/Object → Action and to arbitrary user-defined target groups spanning clusters. A child policy can only narrow the parent's allowed actions/mode/limits, never expand them. Group membership does not alter ownership or permissions.

Effective policy is the restrictive intersection of all applicable policy layers and explicit constraints. Conflicting or unresolvable policy => Observe/deny. Policy evaluation returns a versioned decision with matched policies, effective mode, allowed actions, constraints and reason codes.

### 8.3. Autonomy Profiles

Reusable profiles (e.g. `production-strict`, `development-full`, `database-protected`) are versioned templates, not hidden grants. They may define mode, action allow/deny lists, approval rules, risk thresholds, time windows, rate/concurrency budgets, verification and recovery requirements. A profile cannot expand grants, tenant access, delegation or Kubernetes RBAC.

### 8.4. Action risk contract

Every executable Action has a stable ID/version, target kinds, required permissions, risk class, parameters schema, preconditions, expected postconditions, timeout/budget, idempotency/retry classification, verification and recovery strategy.

No generic arbitrary command/shell/exec action. Actions are implemented by typed adapters with narrow Kubernetes operations. High-risk/destructive actions require stronger approval/constraints and may remain permanently unavailable to Autonomous mode.

## 9. Operation, Plan, Approval, Run and Step

### 9.1. Domain records

- **Operation** — versioned definition/workflow and its contract, not one execution.
- **Action** — one typed executable capability in the catalog.
- **Plan** — immutable proposed work: actions, exact targets, identity binding, policy revision, preconditions, expected outcomes, budgets and digest.
- **Approval** — actor's explicit approval bound to Plan digest, exact target snapshot, Operation/Action versions, policy revision, Execution Identity binding and expiry.
- **Run** — one durable execution of an approved Plan; records initiator, technical principal, identity, scope, schedule occurrence if any and lifecycle.
- **Step** — one typed unit of work with target, attempt, state, pre/postconditions, outputs/evidence references and retry/recovery metadata.
- **EvidenceReference** — pointer/provenance to domain-owned evidence; Run does not copy/own all evidence payloads.

### 9.2. Plan immutability

Plan digest covers all safety-relevant inputs: Operation/Action versions, parameters, target snapshot and Group revision, identity and credential-version binding, principal/grants/delegation revisions, policy revision, preconditions, approval requirements, budgets and expiry.

Any material change creates a new Plan and invalidates prior Approval. No approval transfer across targets, identity, revisions, actions or changed parameters.

### 9.3. Multi-cluster execution

A parent Run coordinates cluster-scoped sub-runs. Each sub-run has its own Execution Identity, credential subject, principal authorization context, Target Snapshot, Plan/Approval binding, lease and results. No identity or authorization is inherited from sibling sub-runs. There is no distributed transaction illusion across clusters; parent outcome reflects partial success and reconciliation.

### 9.4. State machines

Run states include:
`draft, planned, awaiting_approval, approved, queued, running, verifying, succeeded, partially_succeeded, failed, blocked, cancel_requested, cancelled, interrupted, unknown, expired`.

Step states include:
`pending, claimed, precondition_check, executing, verification_pending, succeeded, failed, blocked, retry_wait, cancel_requested, interrupted, unknown, compensating, compensated, compensation_failed`.

Transitions are explicit and validated by domain methods. No caller may set arbitrary status fields. Cancellation is cooperative. Unknown side effect outcome requires fresh reconciliation before retry.

## 10. Durable execution, concurrency and recovery

### 10.1. Execution pipeline

`Load durable Run → Claim Step → Revalidate authorization/identity/policy/approval → Verify lease/fencing → Fresh-read target/preconditions → Execute one typed side effect → Persist outcome → Verify postconditions → Audit`.

Repeat authorization and safety checks before every side effect, not only at Plan creation or Run start.

### 10.2. Lease and fencing

Web workers claim durable work with a lease containing owner, expiry, heartbeat and monotonically increasing fencing token. PostgreSQL time is authoritative for server lease expiry. Every durable state transition checks the current fencing token.

A stale worker must not be able to persist state or begin a subsequent Step after losing its lease. Kubernetes API does not understand Kubron fencing tokens; fencing cannot retract an already submitted request. Therefore exactly-once execution is not promised.

### 10.3. Idempotency and unknown outcomes

Use durable idempotency keys for schedule occurrence, Run creation and step attempt as appropriate. At-least-once attempts plus action-specific idempotency and reconciliation are the realistic guarantee.

If process/network fails after a request may have reached Kubernetes but before its outcome is durable:
- mark outcome unknown/interrupted;
- perform fresh reads and compare postconditions;
- determine whether action already took effect;
- retry only if action contract proves it safe;
- otherwise block/escalate for human review.

Never blindly replay an uncertain write.

### 10.4. Desktop and Web runtime ownership

- Desktop runner runs inside the existing Kubron Desktop process, independent of UI/window lifecycle. No separate daemon/service.
- Desktop process exit, OS logout, shutdown/reboot or crash stops local execution. Sleep/hibernation pauses physical execution. Restart performs reconciliation before resuming.
- Web runner is server-side and independent of browser, WebSocket or request lifetime. Any healthy replica may recover work through durable state and leases.
- File Store Desktop is single-process serialized; PostgreSQL Web supports cross-replica atomic claims/transitions.
- Both platforms use the same domain state machine and contract tests.

## 11. Persistence contracts

Do not turn current `Store` into one universal CRUD API. Each domain owns a narrow backend interface and specialized atomic operations.

Required logical aggregates:
- identity, credential metadata, grants and delegations;
- Target Groups/revisions and resolution snapshots;
- policies/profiles and immutable policy revisions;
- Operation definitions, Plans, Approvals;
- Runs, Steps, leases, idempotency records and occurrence records;
- audit events and evidence references.

Atomicity requirements:
- unique occurrence → at most one Run;
- Plan + immutable target/identity/policy bindings persisted consistently;
- approval transition checks current Plan digest/expiry;
- claim/lease acquisition is atomic;
- each state transition checks expected state/version and fencing token;
- cancellation and terminal completion are compare-and-set transitions.

PostgreSQL uses transactions, unique constraints and conditional updates. Desktop uses serialized domain access and atomic file replacement. Same domain contract suite validates both; concurrency tests include PostgreSQL multi-replica behavior. No cross-system transaction spanning Kubron DB and Kubernetes API.

Migrations must preserve existing encrypted records, key IDs, AAD, rekey/keycheck, backup/restore and plaintext checks. Service scope additions require explicit migration and backward compatibility.

## 12. Audit, evidence and retention

Audit is a separate durable security record, not stdout, process logs, Run history or Event Hub. Audit retention is independent of Run/Evidence retention.

**Important existing-code gap:** current `internal/audit.Logger.Record` is best-effort and does not return persistence errors to the caller. It cannot alone guarantee durable audit for autonomous side effects. Before write execution, design a durable audit contract/outbox or transactional append mechanism. If required audit intent cannot be durably committed, the system must not start a new protected side effect. Do not silently change semantics of all existing interactive audit events without a dedicated compatibility decision.

Audit must capture actor/initiator, authorization principal, Execution Identity ID/version (not secret), operation/run/step, target reference, decision/reason, policy/grant/delegation revisions, outcome, timestamps and correlation IDs. Never store kubeconfig, tokens, plaintext credentials, secret values or unrestricted logs.

Run/Step/Evidence retention is configurable at Organization level. Audit has separate mandatory retention rules. Retention cannot delete active Runs, approvals, leases or data required for reconciliation. Evidence payload lifecycle remains owned by its domain; Run stores references and provenance.

## 13. Scheduler contracts

- Schedule owner is Organization or Team; creator is provenance only.
- Authorized Team Admin manages Team schedules; Organization Admin manages Organization schedules under app-RBAC.
- Desktop schedules are local to the running Desktop process; Web schedules are server-side and survive browser closure.
- No automatic Desktop/Web schedule synchronization.
- Each schedule stores IANA timezone; event timestamps are UTC. DST gap is skipped; DST fold produces one occurrence using the first fold.
- Missed occurrence defaults: Observe = Coalesce; Assisted/Autonomous = Skip. Catch-up only with explicit bounded opt-in.
- Occurrence identity is deterministic from Schedule ID/revision and local calendar occurrence semantics; exact key format is an implementation ADR.
- Web occurrence registration and Run creation must be atomic/HA-safe; duplicate ticks cannot create duplicate Runs.
- Schedule edits affect future occurrences only. Existing Runs keep immutable Plan/identity/target/approval bindings.
- Pause/revoke stops new occurrences; in-flight Run cancellation is separate cooperative operation.
- Every occurrence has an auditable disposition (executed, coalesced, skipped, expired, blocked, etc.).
- Every scheduled Run uses an explicitly assigned Team/Service Execution Identity; no user credential inheritance.

## 14. API, events and frontend contracts

- Extend existing RPC allowlist and generated Wails bindings; do not expose arbitrary reflection methods.
- Define transport-neutral domain request/response contracts and map to versioned DTOs at application boundary.
- Web and Desktop expose identical domain semantics. Platform differences remain in `pkg/ext.Platform`, shell adapters and `frontend/src/platform/`.
- Event Hub sends invalidation/progress notifications with IDs and minimal metadata; client reloads authoritative state through RPC with fresh authorization.
- WebSocket reconnect must recover from durable Run state; event delivery is not guaranteed to be the state log.
- UI cannot make policy/approval/identity decisions or persist authoritative Run state.
- API must support pagination, bounded payloads, safe errors and idempotent submission semantics.

## 15. Threat model and failure rules

| Threat/failure | Required behavior |
|---|---|
| Initiator leaves Team | Existing schedule remains; execution uses assigned technical identity and current grants/delegation |
| Team revokes delegation | Block new Runs and next side effects; submitted Kubernetes request may finish |
| Credential rotation/revocation | Resolve current permitted version; no fallback to revoked/user credential |
| Policy/grant changes | Re-evaluate before next side effect; old Plan never expands |
| Group revision changes | New resolution, Plan and approval required |
| Object deleted/recreated | UID mismatch blocks old Plan |
| Selector too broad/slow | Explicit limit error; no silent truncation |
| Partial unauthorized targets | Mutating Plan blocked by default |
| Browser/WebSocket disconnect | Server Run continues; UI reconnect reads durable state |
| Desktop process crash | Mark/reconcile uncertain work; never blind replay |
| Server replica crash | Lease expiry/reclaim with fencing; reconcile uncertain Kubernetes side effects |
| Lease lost during request | Request may complete; stale worker cannot advance/persist; reconciliation required |
| Audit persistence unavailable | Protected new side effect fails closed |
| Store unavailable or authorization indeterminate | Deny/block, never assume permission |
| Cross-team target appears in shared group | Group membership does not grant visibility or execution |

## 16. Test and acceptance matrix

### Domain contract tests (shared)
- principal/grant union and AND constraints;
- tenancy/delegation intersection and fail-closed ambiguity;
- policy hierarchy cannot expand at child scope;
- immutable Plan digest and approval binding;
- group revision race and deterministic target resolution;
- UID mismatch, precondition drift and no blind retry;
- partial target planning and per-target terminal outcomes;
- state machine legality, cancellation, idempotency and unknown outcome;
- rotation/revocation between Steps;
- schedule occurrence deduplication, timezone and DST semantics.

### Persistence contract tests
- same semantics for File and PostgreSQL backends;
- atomic occurrence/Run uniqueness;
- expected-version and fencing checks;
- crash/restart recovery;
- retention exclusions for active Runs/approvals/reconciliation;
- encryption/rekey/backup/restore compatibility for new identity types.

### Cross-platform tests
- same use case and domain response semantics in Wails Desktop and HTTP Web;
- RPC allowlist and generated binding parity;
- frontend adapter tests for API/events/files;
- WebSocket loss/reconnect and multi-replica state reload;
- Desktop window hide/OS lock continues; process exit stops and restart reconciles.

### Security tests
- no user credential inheritance by scheduler;
- TeamPrincipal cannot cross Team boundary;
- ServicePrincipal has no implicit cluster access;
- Organization Admin cannot issue Team delegation on behalf of Team;
- sharing Target Group leaks no unauthorized object metadata;
- revoked grants/delegations/credentials block next side effect;
- no secrets in Plan, Run, audit, logs, events or errors;
- no Wails imports in domain/core and no direct frontend Wails imports outside platform layer.

## 17. Implementation sequence and gates

No write-action runtime implementation begins until Phase 0 deliverables are reviewed.

### Phase 0 — Architecture and contract baseline
1. Confirm this specification against current ADRs and code.
2. Resolve remaining OPEN decisions below.
3. Define package dependency graph, domain DTOs and backend interfaces.
4. Produce threat model and migration plan.
5. Establish cross-platform and persistence contract test harness.

Exit: reviewed contracts, no unresolved security-critical ambiguity, CI contract skeleton agreed.

### Phase 1 — Identity and authorization foundation
1. Typed technical principals.
2. Team/Service credential subject extension through existing Broker/ConfigFor path.
3. Grants and Cross-Team Delegation.
4. Audit durability for security-critical mutations.

Exit: authorization tests prove no implicit grants, no fake sessions and no cross-tenant escalation.

### Phase 2 — Target and policy foundation
1. Target Group ownership/sharing and revision.
2. Selector validation and bounded resolution.
3. Immutable target snapshots and preconditions.
4. Autonomy policy evaluator and profiles.

Exit: group/selector/policy contracts deterministic and fail-closed.

### Phase 3 — Assisted read/plan workflows
1. Operation/Action catalog with read-only actions first.
2. Plan generation, digest and approval binding.
3. UI/API visibility, coverage and explanation.
4. No automatic mutation yet.

Exit: identical Desktop/Web semantics and auditable plans.

### Phase 4 — Durable Run engine (read-only and no-side-effect actions first)
1. Run/Step state machine and domain backend.
2. Desktop process runner and Web HA lease/fencing.
3. Idempotency, interruption and reconciliation.
4. Scheduler occurrence registration and ownership.

Exit: crash/restart and multi-replica tests pass; no blind replay.

### Phase 5 — Restricted Assisted write actions
1. Small typed action allowlist.
2. Explicit Plan/Approval.
3. Precondition enforcement at API call where supported.
4. Durable audit before side effects, postcondition verification and safe failure behavior.

Exit: threat-model review and cross-platform tests pass for each action type.

### Phase 6 — Policy-based Autonomous execution
1. Enable only low-risk actions under explicit profiles.
2. Budgets, rate limits, blast-radius limits and progressive rollout.
3. Kill switch, audit and operator visibility.
4. Expand action catalog only with evidence and action-specific recovery.

Exit: independent security review and production-readiness evidence.

### Phase 7 — Cross-cluster and goal-based operations
1. Parent/sub-run orchestration and independent per-cluster identity.
2. Partial success and reconciliation.
3. Goal contracts, convergence checks and bounded planning.
4. No distributed transaction or exactly-once claims.

Exit: cross-cluster failure injection and recovery evidence.

## 18. OPEN engineering decisions before implementation

These are not product-level questions for routine re-approval; engineering owners should produce proposals and tests:
1. Exact typed API for subject-aware credential resolution and how `ConfigFor` evolves.
2. Technical principal/grant/delegation storage schema and revision semantics.
3. Selector allowlist, canonical serialization, query limits and timeout budgets.
4. Operation/Action schema, risk taxonomy and action adapter interface.
5. Plan digest canonicalization and cryptographic/hash versioning.
6. Approval quorum/expiry/revocation semantics and separation of proposer/approver for high-risk actions.
7. Durable audit outbox/append design and consistency guarantees.
8. Narrow backend interfaces and atomic transaction boundaries per aggregate.
9. Lease duration, heartbeat, fencing token generation and reclaim policy.
10. Action-specific idempotency and recovery classifications.
11. Schedule calendar grammar, occurrence key format, tzdb update policy and Catch-up limits.
12. Organization retention/legal hold/export and evidence deletion semantics.
13. Desktop local-store corruption/recovery and supported durability guarantees.
14. Versioning/compatibility policy for Operation/Action definitions and in-flight Plans.
15. Cross-cluster parent Run cancellation and compensation policy.

Each item must be resolved by a focused ADR/design note before the corresponding runtime slice; do not block unrelated read-only contract work.

## 19. Architectural invariants

1. One Go core and one domain semantics for Web/Desktop.
2. Backend is the only authority for permissions, policies, approvals and Run state.
3. No automatic operation inherits the initiator's personal Kubernetes identity.
4. Team/Service identity is explicit, independently authorized and revocable.
5. Team ownership remains authoritative; no universal ClusterGrant.
6. Target Group and sharing never grant target access.
7. Scope and policy only narrow; no ambiguity expands access.
8. Plan, target snapshot and approval are immutable and digest-bound.
9. UID identifies an object instance; name alone is insufficient.
10. Revalidate every side effect; unknown outcome means reconcile, not blind retry.
11. No exactly-once guarantee for Kubernetes side effects.
12. Durable state, not events or process memory, is the source of truth.
13. `internal/jobs` is not a user-operation executor.
14. Audit is distinct from logs and Run history; protected side effects require durable audit.
15. Retention of Run/Evidence never silently removes mandatory audit.
16. No runtime write-action implementation until its contract, tests, audit and recovery are reviewed.
