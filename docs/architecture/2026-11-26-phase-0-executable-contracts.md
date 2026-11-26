# Kubron Phase 0 — Executable Contracts and Package Boundaries

- Статус: **engineering design proposal; Phase 0 deliverable**
- Дата: 2026-11-26
- Проверенная база: `refactor` — `2330388d8d2460ad72ddc57dd003a8d30fb9f1c7`
- Связанные документы:
  - [Kubron Executable Platform Architecture Specification](kubron-executable-platform-architecture-specification.md)
  - [Execution Identity & Durable Operations Design](execution-identity-durable-operations-design.md)
  - ADR-0001…0034, особенно ADR-0002, 0003, 0005, 0006, 0007, 0008, 0009, 0010, 0012, 0016, 0018, 0022, 0026

## 1. Назначение и границы

Документ переводит целевую архитектуру в инженерные контракты Phase 0:
- ответственность доменных пакетов и допустимые зависимости;
- границы application orchestration и platform adapters;
- минимальные интерфейсы между доменами;
- требования к atomic persistence и durable audit;
- общий набор contract/security/failure tests для Desktop и Web;
- критерии готовности перед первой runtime-реализацией.

Это не описание уже существующих интерфейсов. Типы ниже — **целевые контракты**, псевдокод для обсуждения package ownership. Их нельзя копировать в код до отдельного ADR и анализа конкретных call sites.

Phase 0 не реализует Operation Runner, scheduler, новые principals, grants, delegation, Target Groups или Kubernetes write actions. Не меняет существующие permission checks и credential behavior. Не вводит микросервисы, message broker, отдельный Desktop daemon или универсальный workflow engine.

## 2. Проверенная текущая основа и важные уточнения

### 2.1. Реальные seams

| Path | Текущее назначение | Целевое использование |
|---|---|---|
| `internal/app` | Composition root / application facade, Wails bindings and RPC methods | Сохранить facade; orchestration передавать в domain/application services |
| `pkg/ext.Platform` | `Emit`, `PickFile`, `OpenURL`, `Capabilities` | Сохранять как единственный Go shell boundary |
| `internal/platform/wails`, `internal/platform/web` | Platform implementations | Реализуют только environment integration |
| `frontend/src/platform/` | API/events/files and other frontend adapters | Все UI platform differences остаются здесь |
| `internal/auth` | User sessions, permissions, Access, Directory | Не подменять User Session техническими principals |
| `internal/tenant` | Existing Org/Team/Cluster scope type | Источник scope из каталога, не из client input |
| `internal/teams` | Team administration use cases | Pattern для узких use-case services, но не шаблон для background identity |
| `internal/credential` | Broker, Provider, Subject, CredentialStore, token refresh | Единственный credential resolution path |
| `internal/k8s/configfor.go` | `PlanFor` / `ConfigFor`, единственное место создания stored-cluster `rest.Config` | Расширять connection-plan boundary, не обходить |
| `internal/store` | Composition of domain backends for File/PostgreSQL | Расширять композицию новыми узкими Backend interfaces |
| `internal/pgstore` | PostgreSQL implementations, migrations, advisory locks | Atomic SQL operations and HA coordination |
| `internal/audit` | Audit Event/Logger and append backend | Existing best-effort audit remains for legacy flows; critical audit needs explicit contract |
| `internal/jobs` | Periodic service jobs, optional leader lock | Never use as durable Run/Step queue |
| `internal/replicaevents` | Cross-replica notifications | Invalidation/progress only, not durable workflow state |

### 2.2. Corrections to avoid false assumptions

1. **The Go core is not a new package named `core`.** It is the existing application/domain code, with `internal/app` as facade/composition boundary. Do not create a parallel `internal/core`.
2. **The current Store is a composition interface.** It already exposes several domain backends. New domain contracts should be separate narrow interfaces, not methods on one generic CRUD repository.
3. **Credential subject is not session itself.** Current `credential.Subject` has ClusterID, Scope, OwnerID, SessionID; current `Scope` only has `user` and `team`. SessionID is challenge/session context, while owner/scope identify the credential record. A technical principal needs an explicit new subject kind and lifecycle semantics.
4. **ConfigFor takes a ConnPlan, not an arbitrary user session directly.** `PlanFor(AuthSession, clusterID)` currently derives subject from session and cluster auth source. Background support must design a typed principal-to-ConnPlan path and prove every call site/pool key is correctly isolated.
5. **Current audit is persisted, but the public Logger API is best-effort.** File/PostgreSQL backends return append errors; `Logger.Record` intentionally swallows them and exposes last write error through query/UI. Therefore it is unsuitable as the only gate before an autonomous side effect.
6. **Current Store persistence semantics are mixed by domain.** Some interfaces are context-aware and versioned, others are synchronous. New durable Run contracts must define cancellation, timeout, conflict and atomicity explicitly; do not assume the existing Store gives a cross-domain transaction.
7. **Current Desktop and Web both share Go domain code, but runtime capabilities differ.** Capability flags describe platform availability, not authorization. A local file dialog or local kubeconfig does not grant extra domain permissions.

## 3. Package ownership and dependency graph

### 3.1. Target package map

Names are provisional. Keep package count low; split only when ownership or dependency direction requires it.

| Target package | Owns | Must not own |
|---|---|---|
| `internal/identity` | Technical principal IDs/types, identity lifecycle metadata, grants, delegation domain rules | Kubernetes secrets, auth sessions, HTTP/Wails |
| `internal/target` | Scope references, TargetGroup revision, selector validation, resolution result, immutable TargetSnapshot | Permission grants, K8s client construction, UI |
| `internal/autonomy` | Policy/Profile definitions, hierarchy evaluation, effective mode and constraints | Credential loading, direct side effects, UI decisions |
| `internal/operation` | Operation/Action definitions, Plan, Approval, Run/Step state machine, execution contracts | Transport DTOs, SQL, direct global Kubernetes manager |
| `internal/scheduler` | Schedule ownership, recurrence semantics, occurrence identity and dispatch contract | Long-lived daemon, Kubernetes credential secrets, direct K8s writes |
| `internal/app` | Composition, transport-facing methods, current-session extraction, use-case invocation | Domain policy algorithms, SQL, UI state as authority |
| Existing `internal/auth` | User authentication/session and existing app-RBAC | Team/Service principal impersonation |
| Existing `internal/credential` | Credential provider and subject resolution | Operation authorization decisions |
| Existing `internal/tenant` | Canonical tenant scope types/resolution adapters | User-defined group membership |
| Existing `internal/audit` | Legacy audit query/recording and backend adapters | Operation state machine |
| Existing `internal/store` / `internal/pgstore` | Backend composition and persistence implementations | Domain policy/authorization algorithms |

The first implementation slice may keep identity and grants together if that avoids premature package fragmentation. Do not introduce empty packages before a real domain service is ready.

### 3.2. Dependency direction

Allowed dependency shape:

```text
Wails / HTTP RPC / WebSocket adapters
                 |
             internal/app
                 |
        application use cases
          /      |       \
      identity target autonomy
          \      |       /
             operation
                 |
       domain-owned interfaces
                 |
 auth / tenant / credential / audit / store abstractions
                 |
  File / PostgreSQL / Kubernetes / Platform adapters
```

This is a conceptual ownership map, not an instruction to create a central package importing every domain. In code:
- domain packages may depend on small shared value types and interfaces;
- infrastructure implements interfaces defined by the consuming domain;
- application composition wires implementations;
- no domain package imports `internal/app`, `internal/platform/*`, Wails, HTTP handlers, Vue or `internal/pgstore`;
- `internal/pgstore` may import domain backend contracts to implement them, never the reverse;
- Kubernetes access must be injected through a narrow adapter that ultimately uses the existing manager/credential/config path.

### 3.3. App facade and session boundary

Every interactive call enters through the existing App/RPC boundary:
1. identify current user/session from the existing view;
2. resolve requested resource from trusted catalog;
3. run current app-RBAC/tenant check;
4. invoke a domain use case with an explicit immutable actor/context value;
5. map domain result to existing transport DTO.

Background execution does not retain an App pointer, session pointer, or Wails context. It loads a durable Run and resolves technical principal, identity and current policy from repositories at execution time.

Do not store `*auth.Service`, `*App`, `AuthSession` or request-scoped objects inside Run, Plan or scheduler records.

## 4. Minimal domain contracts

The following pseudocode establishes responsibilities, not final signatures.

### 4.1. Shared identifiers and request context

Use opaque typed IDs where useful; do not use usernames, cluster display names or client-provided strings as authorization identity.

```go
type ActorRef struct {
    Kind string // user | scheduler | system
    ID   string
}

type PrincipalRef struct {
    Kind string // user | team | service
    ID   string
}

type ExecutionIdentityRef struct {
    Kind    string // user | team | service
    ID      string
    Version string // opaque credential revision, never secret
}

type TenantScope struct {
    OrganizationID string
    TeamID         string
    ClusterID      string
}

type RequestProvenance struct {
    RequestID string
    Actor     ActorRef
    SessionID string // interactive only; empty for background
}
```

The existing `tenant.Scope` is currently fully populated Org/Team/Cluster and fails closed on empty parts. Do not silently weaken its meaning to represent Organization-only or Team-only scope. Introduce explicit owner/scope value types for those cases rather than using empty strings as wildcards.

### 4.2. Authorization evaluator

One domain-facing evaluator returns an explainable, versioned decision:

```go
type AuthorizationRequest struct {
    Actor       ActorRef
    Principal   PrincipalRef
    OperationID string
    ActionID    string
    Target      TargetRef
    Identity    ExecutionIdentityRef
    At          time.Time
}

type AuthorizationDecision struct {
    Allowed       bool
    ReasonCodes   []string
    PolicyRevision string
    GrantRevisions []string
    DelegationRevisions []string
}

type Authorizer interface {
    Evaluate(ctx context.Context, req AuthorizationRequest) (AuthorizationDecision, error)
}
```

Rules:
- this evaluator is for the future technical principal and operation model; do not replace `auth.Service.Check/CheckCluster/CheckTeam` in Phase 0;
- interactive administrative changes still use existing user app-RBAC;
- background decisions use explicit TeamPrincipal/ServicePrincipal, not initiator membership;
- unknown, stale, unavailable or ambiguous authorization data => denied;
- evaluator output is explainability metadata, not a capability token that bypasses revalidation;
- re-evaluate before every side effect.

### 4.3. Credential resolver

The resolver accepts a typed credential subject and returns a short-lived connection handle/configuration through the existing credential and Kubernetes connection path:

```go
type CredentialSubject interface {
    ClusterID() string
    Kind() string // user | team | service
    OwnerID() string
}

type CredentialResolver interface {
    Resolve(ctx context.Context, subject CredentialSubject) (ResolvedCredential, error)
}
```

`ResolvedCredential` must not expose raw refresh tokens or plaintext stored credentials to Operation/Run. The concrete design must preserve:
- current Provider/Broker refresh/challenge behavior;
- credential Store encryption, AAD, key IDs, rekey/keycheck;
- complete subject separation in cache/pool keys;
- rotation/revocation version checks;
- current cluster address/TLS/exec restrictions;
- the one `rest.Config` construction boundary in `internal/k8s/configfor.go`.

Do not implement this interface by wrapping the current `AuthSession` with a fake user. The exact subject-aware `ConnPlan` and `ConfigFor` changes are an ADR gate.

### 4.4. Target resolution

```go
type TargetResolver interface {
    Resolve(ctx context.Context, req ResolveTargetsRequest) (TargetResolution, error)
}

type TargetResolution struct {
    GroupID       string
    GroupRevision uint64
    ResolvedAt    time.Time
    Targets       []TargetSnapshotItem
    Coverage      Coverage
}
```

Target resolution does not authorize by itself. Each target has to pass authorization and cluster access. Resolution must pin one group revision and fail on revision race. Mutating operations default to all-or-nothing planning.

### 4.5. Policy evaluator

```go
type PolicyEvaluator interface {
    Evaluate(ctx context.Context, req PolicyRequest) (EffectivePolicy, error)
}
```

The result contains effective mode, allowed actions, constraints, matched revision IDs and reason codes. Child policies only narrow parent policy. No policy/ambiguous policy => Observe; write denied.

### 4.6. Operation persistence and orchestration

Domain-owned backend interfaces should expose semantic operations, not CRUD:

```go
type PlanBackend interface {
    CreateImmutablePlan(ctx context.Context, plan Plan) error
    GetPlan(ctx context.Context, id PlanID) (Plan, error)
}

type RunBackend interface {
    CreateRunForOccurrence(ctx context.Context, run Run, occurrence OccurrenceKey) (RunID, error)
    ClaimStep(ctx context.Context, runID RunID, worker WorkerID, lease LeaseRequest) (Claim, error)
    TransitionStep(ctx context.Context, claim Claim, expected StepState, next StepState, outcome Outcome) error
    RequestCancel(ctx context.Context, runID RunID, actor ActorRef) error
    ReconcileInterrupted(ctx context.Context, runID RunID, observed Reconciliation) error
}
```

Required properties:
- atomicity and concurrency semantics are documented per method;
- compare-and-set on expected state/version;
- claims and transitions validate current fencing token;
- occurrence uniqueness prevents duplicate Runs;
- no caller can set arbitrary Run/Step state;
- reads enforce tenant scope in the repository query, not only after loading a row.

Do not add `Save(any)`, generic `Update(map[string]any)` or a universal transaction interface.

## 5. Persistence boundaries and atomicity

### 5.1. Backend ownership

Each domain defines its backend interface in its own package. Implementations:
- File backend for supported local/Desktop mode;
- PostgreSQL backend for Web/server mode;
- adapters composed in `internal/store` and `internal/store/pg.go`.

A domain service must not import concrete pgstore or file paths. Backend contract tests run against both implementations where the capability is supported.

### 5.2. Required atomic operations

| Operation | Atomic guarantee | PostgreSQL mechanism | Desktop mechanism |
|---|---|---|---|
| Create Group revision | One monotonic revision per accepted mutation | transaction + row lock/conditional update | serialized access + atomic replacement |
| Pin resolution snapshot | One group revision and deterministic target set | transaction/conditional revision check | serialized resolution/persist |
| Create Plan | immutable Plan and exact snapshot bindings persisted together | transaction | one aggregate file or atomic multi-record journal |
| Approve Plan | approval only for current digest, expiry and revision | conditional update/transaction | serialized compare-and-set |
| Create scheduled Run | unique occurrence -> at most one Run | unique constraint + transaction | serialized occurrence index |
| Claim Step | one current lease/fencing token | conditional update + DB time | single process mutex |
| Transition Step | expected state + current fencing token | conditional update | serialized state transition |
| Persist audit intent | durable before external side effect | transaction/commit | append + fsync durable journal |
| Persist outcome | durable reconciliation evidence | transaction/append | append + fsync durable journal |

No distributed transaction is claimed across PostgreSQL/File Store and Kubernetes API.

### 5.3. Desktop storage contract

Desktop execution is one process. It does not promise multi-process locking or server HA. For operation-critical aggregates:
- use a versioned append-only journal or one aggregate file with atomic replacement;
- write a complete record, sync it to durable storage, then acknowledge;
- use monotonic sequence/version and checksum to detect truncation/corruption;
- recover only complete records; partial tail is quarantined/reported, never interpreted as success;
- keep a backup/compaction protocol that preserves audit and active Run state;
- on corruption or uncertain persistence, block new side effects and surface a recovery-required state.

The final format (WAL vs aggregate snapshots) is an implementation ADR; the guarantee is not optional.

### 5.4. PostgreSQL contract

- All new tables are tenant-scoped and indexed for the actual query paths.
- Every lookup/update includes tenant scope or joins through a verified owner relationship.
- State transitions use conditional SQL, not read-then-unconditional-write.
- DB time is authoritative for leases/expiry and scheduler occurrence processing.
- Unique constraints enforce idempotency at the database boundary.
- Migrations are numbered, reversible where safe, and validated by `scripts/check-migrations.sh`.
- Secrets use existing envelope encryption/keyring and must be included in Rekey, keycheck, backup/restore and plaintext checks.
- No migration is introduced in Phase 0.

### 5.5. Atomicity limits

The following cannot be one transaction and must use durable intent + reconciliation:
- Plan/Run store and Kubernetes API side effects;
- cross-cluster sub-runs;
- remote identity provider refresh and local credential persistence;
- external evidence collection and local evidence metadata.

A durable intent records what the system is about to attempt. A later result record records what it observed. If the process dies between them, the action is unknown until reconciled.

## 6. Critical audit contract

### 6.1. Existing behavior

The existing `audit.Logger.Record` intentionally does not return append errors. The backend can fail; Logger stores the last error for display. In PostgreSQL, `audit_events` append is durable at the database layer, but caller does not receive failure. File audit append currently closes the file but does not explicitly `fsync`. Stdout mirror is best-effort and must not be treated as authoritative.

Do not change all legacy audit behavior as a side effect of adding autonomous operations.

### 6.2. Required critical path

Add a separate critical audit contract for protected Operations:

```go
type CriticalAudit interface {
    AppendIntent(ctx context.Context, event CriticalAuditEvent) (AuditReceipt, error)
    AppendOutcome(ctx context.Context, receipt AuditReceipt, outcome CriticalAuditOutcome) error
}
```

Required sequence:
1. Revalidate authorization, identity, Plan, policy, target preconditions and lease.
2. Persist durable Step intent and corresponding audit intent before sending the Kubernetes request.
3. Only after durable acknowledgement, send the side effect.
4. Persist the observed result and audit outcome.
5. If result persistence fails, mark/recover as unknown and reconcile; never report success solely from the HTTP response.

For PostgreSQL, append intent and transition Step to `executing` in one DB transaction (or a domain-owned atomic backend operation). For Desktop, use one durable append journal record containing both intent and state transition; sync before the request. Audit outcome is a subsequent durable record.

A transactional outbox is only needed if downstream delivery is introduced. It is not a substitute for the pre-side-effect durable intent. External stdout/metrics/events are mirrors and never part of the authorization proof.

### 6.3. Critical audit fields

At minimum: event ID, timestamp, actor/initiator, authorization principal, Execution Identity ID and credential version (not secret), operation/action/version, Run/Step IDs, target reference (privacy-safe), decision/reason, Plan digest, policy/grant/delegation revisions, attempt, outcome/unknown marker, request/correlation ID.

Never include kubeconfig, token, plaintext credentials, private provider state, secret values or unrestricted Kubernetes object payloads.

### 6.4. Failure semantics

If the system cannot durably persist intent/audit, do not start a new protected side effect. If a Kubernetes request may have been sent but result/audit cannot be persisted, state is unknown and requires reconciliation. Never convert an audit storage error into a successful Run.

## 7. Desktop/Web parity and platform contract tests

### 7.1. Same semantics

Both clients call the same application use cases and domain services. Platform adapters provide local environment functions only. The backend is authoritative for identity, authorization, policy, approval, Run state and persistence.

Allowed differences:
- local kubeconfig/file dialog availability;
- Desktop local process runner vs Web server runner;
- File Store vs PostgreSQL;
- Desktop local shell capabilities vs Web restrictions.

Forbidden differences:
- different meaning of permissions, policies, approvals, Run states or terminal outcomes;
- UI-side authorization;
- WebSocket as source of truth;
- Desktop local environment granting additional domain permissions.

### 7.2. Test layers

| Layer | Tests | Expected result |
|---|---|---|
| Domain unit | pure policy, selector canonicalization, state transition rules, digest | same result independent of platform |
| Backend contract | Plan/Run/Step/Group/Approval/Audit semantics | same domain guarantees File and PostgreSQL |
| Authorization security | no implicit grants, tenant boundary, delegation, fail-closed | deny in both clients |
| Credential contract | subject isolation, refresh, rotation, revocation, key lifecycle | no cross-subject token reuse |
| Application use-case | same request/result and error categories | shared Go use case |
| Transport | Wails bindings and HTTP RPC allowlist/DTO | parity of exposed domain semantics |
| Frontend adapter | API/events/files | UI does not call shell directly |
| Failure injection | crash, DB outage, WebSocket loss, stale lease, unknown K8s response | reconcile; no blind retry |
| E2E | Desktop process restart and Web multi-replica reconnect | durable state recovered |

### 7.3. Required test cases before first write action

1. Same use-case tests run without platform-specific branches.
2. User loses Team membership after schedule creation; scheduled Run still uses explicitly assigned Team/Service identity, subject to current grants and ownership.
3. Initiator is disabled/deleted; creator provenance remains but does not authorize or block technical execution.
4. Technical principal has Kubernetes credentials but no Kubron grant: denied.
5. ServicePrincipal has grant but no owning-Team delegation for a cross-team target: denied.
6. Child autonomy policy attempts to expand parent: denied/reduced to parent effective set.
7. Target Group changes after Plan: old Plan cannot be executed against new revision.
8. Object UID changes after delete/recreate: old Plan blocked.
9. One target in mutating Plan is unauthorized: whole Plan blocked by default.
10. Audit intent append fails: no Kubernetes request sent.
11. Kubernetes request succeeds but result persistence fails: Run becomes unknown and reconciles.
12. Worker loses lease before next Step: no new side effect.
13. WebSocket disconnect: Run continues server-side; reconnect reloads durable state.
14. Desktop process exits during request: restart reconciles before retry.
15. Duplicate scheduler tick across two replicas: one occurrence/one Run.
16. Credential rotation between Steps: next Step uses current verified version or blocks.
17. No secret appears in Plan, Run, events, audit, logs or error payloads.

## 8. Phase 0 deliverables

Before Phase 1 implementation, engineering must produce:

| Deliverable | Owner boundary | Acceptance |
|---|---|---|
| Package dependency graph | Architecture | No forbidden imports/cycles; domain packages independent of transport/SQL |
| Principal and authorization model | Identity/Auth | User vs Team vs Service distinct; grants/delegation intersections tested |
| Credential subject design | Credential/K8s | Exact subject-aware ConnPlan and ConfigFor path; cache/AAD isolation |
| Operation persistence contract | Operation/Store | Domain-owned interfaces and atomic semantics for File/Postgres |
| Critical audit design | Audit/Operation | Pre-side-effect intent durable; failure blocks action |
| Target resolution contract | Target/K8s | revision pinning, selector bounds, UID and partial semantics |
| Platform contract suite | Platform/App | same domain semantics Desktop/Web |
| Failure/threat model | Security/Architecture | crash, revocation, HA and unknown-outcome paths covered |
| Migration plan | Persistence | compatibility, rollback/forward-only constraints, key lifecycle |

Phase 0 exits only when each deliverable has a focused design/ADR, tests are specified and no security-critical ambiguity remains. The Phase 0 document itself does not imply these deliverables have already been implemented.

## 9. Explicit implementation order after Phase 0

1. **Identity foundations:** typed principals and owner-scoped records; no Kubernetes write actions.
2. **Credential extension:** Service subject and subject-aware resolution via existing provider path; no fake session.
3. **Authorization grants/delegation:** evaluate grants, tenancy, delegation and Kubernetes RBAC intersection.
4. **Critical audit:** durable intent/outcome contract and failure injection.
5. **Target foundation:** groups, revisions, bounded selector resolution and snapshots.
6. **Autonomy policies:** profiles and restrictive evaluation.
7. **Read-only Plans:** operation catalog, plan digest, approval binding, explainability.
8. **Durable Runs:** Run/Step backend, claims, leases, fencing, reconciliation.
9. **Scheduler:** occurrence deduplication, ownership, timezone and missed-occurrence semantics.
10. **Restricted Assisted writes:** only after action-specific preconditions, critical audit, verification and recovery pass.
11. **Autonomous mode:** limited low-risk action allowlist and staged rollout.

Credential, authorization and audit are cross-cutting prerequisites; do not build a Run Engine first and retrofit them later.

## 10. Phase 0 exit checklist

- [ ] Every new package has one domain owner and an approved dependency direction.
- [ ] No domain imports `internal/app`, Wails, HTTP, Vue or `internal/pgstore`.
- [ ] Existing `auth.Service`, `tenant.Scope`, `credential.Broker` and `ConfigFor` boundaries are preserved or explicitly extended by ADR.
- [ ] User, Team and Service principals are distinct; initiator is not execution principal.
- [ ] Every background cluster sub-run has its own Execution Identity and authorization context.
- [ ] Store APIs are domain-owned and semantic; no universal CRUD/transaction API.
- [ ] File and PostgreSQL backends have matching contract tests for supported operations.
- [ ] Critical audit intent is durably committed before protected side effects.
- [ ] Run/Step transitions use CAS and fencing; events are notifications only.
- [ ] Desktop restart and Web replica failure recovery are specified and tested.
- [ ] Every failure path defaults to deny/block/unknown rather than success or blind replay.
- [ ] No runtime write action begins before the above gates are satisfied.
