# Kubron Phase 1 — Contract Freeze (Go domain contracts)

- Статус: **предлагаемый frozen contract для реализации Phase 1**
- Дата: 2026-11-27
- Parent handoff: [Phase 1 Engineering Package](2026-11-27-phase-1-engineering-package.md)
- Нормативные решения: technical identity/grants, Execution Identity rotation/revocation, Cross-Team Delegation and Target Group Governance, Phase 0 Executable Contracts.
- Это контракт между implementation lanes, а не утверждение, что runtime capability уже существует или что разрешены Kubernetes write actions.

## 1. Назначение

Четыре lanes должны использовать одни и те же типы и правила. Этот документ фиксирует shared value types, package dependency direction, минимальные Go interfaces и ошибки, credential subject compatibility, critical audit, file ownership и contract tests.

После принятия документа разработчики не меняют перечисленные shared types/interfaces самостоятельно. Любое изменение сначала проходит отдельный coordination change, после чего зависимые lanes обновляются согласованно.

**Сигнатуры ниже — нормативный проект контракта, а не утверждение, что эти типы уже есть в коде.** Допускаются механические адаптации под стиль репозитория, не меняющие семантику. До первой реализации integration owner проверяет точные package names, Go version и существующие error conventions.

## 2. Package graph и владельцы типов

Целевой package graph:

    internal/identity <---- internal/authorization
           ^                       ^
           |                       |
    internal/credential        internal/tenant
           ^
           |
       internal/k8s

    internal/audit <---- identity / authorization use cases
           ^
           |
      internal/app (composition root)

    internal/pgstore implements domain-owned Backend interfaces
    internal/store composes backend implementations

Правила:
1. internal/identity владеет canonical Principal/Actor/Owner/Identity/Binding types. Он не импортирует credential, authorization, k8s, pgstore, app или audit implementation.
2. internal/credential может импортировать internal/identity. Обратная зависимость запрещена.
3. internal/authorization импортирует identity и tenant; не импортирует credential, k8s, pgstore или app.
4. internal/audit независим от identity/credential/k8s. В audit передаются opaque identifiers, а не domain structs/secrets.
5. internal/k8s может импортировать identity и credential. Он не вычисляет Kubron grants/delegations.
6. internal/pgstore реализует интерфейсы доменов; domain packages не импортируют pgstore.
7. internal/store только собирает backend implementations.
8. internal/app — единственная точка composition/transport wiring. Domain policy algorithms остаются в доменах.
9. Не создавать internal/core, второй secrets subsystem, generic CRUD repository или универсальный transaction manager.

### 2.1. Shared type ownership

| Type | Единственный владелец |
|---|---|
| PrincipalKind, PrincipalRef, ActorKind, ActorRef, OwnerScope | internal/identity |
| Identity, IdentityState, CredentialBinding | internal/identity |
| TargetRef, TargetScope, Grant, Delegation, ConstraintSet, GrantDecision | internal/authorization |
| Subject, Key, Meta, Record | internal/credential |
| CriticalIntent, CriticalOutcome, CriticalRecorder | internal/audit |
| Existing tenant.Scope | internal/tenant; смысл не менять |

Не объявлять копии PrincipalRef/ActorRef в credential, authorization, app или transport DTO. На wire boundary допускаются DTO, преобразуемые в canonical domain types сервером.

## 3. Canonical identity types

Файл-владелец: internal/identity/types.go.

    type PrincipalKind string

    const (
        PrincipalUser    PrincipalKind = "user"
        PrincipalTeam    PrincipalKind = "team"
        PrincipalService PrincipalKind = "service"
    )

    type PrincipalRef struct {
        Kind   PrincipalKind
        ID     string
        OrgID  string
        TeamID string
    }

    type ActorKind string

    const (
        ActorUser      ActorKind = "user"
        ActorSystem    ActorKind = "system"
        ActorScheduler ActorKind = "scheduler"
    )

    type ActorRef struct {
        Kind ActorKind
        ID   string
    }

    type OwnerKind string

    const (
        OwnerOrganization OwnerKind = "organization"
        OwnerTeam         OwnerKind = "team"
    )

    type OwnerScope struct {
        Kind   OwnerKind
        OrgID  string
        TeamID string
    }

Validation:
- IDs opaque, non-empty, ограничены длиной/символами единым validator. Display name не является ID.
- User principal: Kind=user, ID обязателен; OrgID может отсутствовать в personal Desktop mode; TeamID пуст.
- Team principal: Kind=team, ID, OrgID, TeamID обязательны. ID — ID технической identity, TeamID — ID owner Team; это разные значения.
- Service principal: Kind=service, ID и OrgID обязательны; TeamID пуст.
- OwnerOrganization: OrgID обязателен, TeamID пуст. OwnerTeam: OrgID и TeamID обязательны.
- Неизвестный kind, пустая обязательная часть или противоречивая комбинация → typed validation error; никакого fallback.
- PrincipalRef не является credential, permission или доказательством доступа.
- Team/Service principal IDs не переиспользуются после revoke/delete. UserPrincipal — ссылка на существующего auth user, не новая учётная запись.

**Важно:** существующий tenant.Scope требует OrgID+TeamID+ClusterID и предназначен для cluster-scoped records. Нельзя использовать его для Organization-owned Service Identity. Для владельца identity используется identity.OwnerScope.

## 4. Identity domain contract

Файлы-владельцы: internal/identity/types.go, backend.go, service.go.

    type IdentityState string

    const (
        IdentityActive    IdentityState = "active"
        IdentitySuspended IdentityState = "suspended"
        IdentityRevoked   IdentityState = "revoked"
    )

    type Identity struct {
        Ref            PrincipalRef
        Owner          OwnerScope
        DisplayName    string
        State          IdentityState
        Revision       uint64
        CreatedBy      ActorRef
        CreatedAt      time.Time
        UpdatedAt      time.Time
        LastMutationID   string
        LastAuditEventID string
    }

    type BindingState string

    const (
        BindingPending BindingState = "pending"
        BindingActive  BindingState = "active"
        BindingRevoked BindingState = "revoked"
    )

    type CredentialBinding struct {
        ID             string
        Principal      PrincipalRef
        ClusterID      string
        State          BindingState
        Revision       uint64
        CreatedBy      ActorRef
        CreatedAt      time.Time
        UpdatedAt      time.Time
        LastMutationID   string
        LastAuditEventID string
    }

    type Backend interface {
        Create(ctx context.Context, in Identity) (Identity, error)
        Get(ctx context.Context, ref PrincipalRef) (Identity, error)
        List(ctx context.Context, owner OwnerScope) ([]Identity, error)
        SetState(ctx context.Context, ref PrincipalRef, expectedRevision uint64,
            next IdentityState, mutationID, auditEventID string) (Identity, error)

        PutBinding(ctx context.Context, in CredentialBinding,
            expectedRevision uint64, mutationID, auditEventID string) (CredentialBinding, error)
        GetBinding(ctx context.Context, principal PrincipalRef, clusterID string) (CredentialBinding, error)
        ListBindings(ctx context.Context, principal PrincipalRef) ([]CredentialBinding, error)
        ActivateBinding(ctx context.Context, id string, expectedRevision uint64,
            mutationID, auditEventID string) (CredentialBinding, error)
        RevokeBinding(ctx context.Context, id string, expectedRevision uint64,
            mutationID, auditEventID string) (CredentialBinding, error)
    }

    type AdminAction string

    const (
        AdminCreate   AdminAction = "create"
        AdminUpdate   AdminAction = "update"
        AdminSuspend  AdminAction = "suspend"
        AdminRevoke   AdminAction = "revoke"
        AdminAssignCredential AdminAction = "assign-credential"
    )

    type AdminAuthorizer interface {
        AuthorizeIdentityAdmin(ctx context.Context, actor ActorRef,
            action AdminAction, owner OwnerScope) error
    }

Rules:
- Backend checks structural invariants, owner immutability, revisions and state transitions. Service checks actor authority before mutation. Create accepts only Team/Service kinds; UserPrincipal remains the existing auth user and is not stored as a technical Identity record.
- Team Identity owner is exactly one Team. Service Identity owner is exactly one Organization. Owner cannot be changed in place.
- Creator is provenance only. It never becomes owner, execution authority or grant.
- TeamPrincipal cannot receive cross-Team delegation. ServicePrincipal can receive Team-issued delegation.
- Identity creation, binding assignment, credential mutation, grants, delegation and schedule/operation assignment are independent audited actions.
- State transitions: active ↔ suspended; active/suspended → revoked; revoked is terminal. Re-enable requires a new principal ID.
- CredentialBinding is one explicit assignment of one principal to one cluster. It contains no secret and does not imply a Kubron grant. Initial assignment creates BindingPending; it becomes BindingActive only after credential provisioning has durably completed. Pending/revoked bindings cannot be used for execution.
- Binding creation requires trusted server-side cluster ownership resolution. TeamPrincipal binding must target its owner Team. ServicePrincipal binding alone does not grant cross-Team access.
- One active binding per (principal, cluster) in Phase 1. Ambiguous binding resolution → deny. A pending binding reserves the principal/cluster tuple and cannot be used as an execution identity.
- Hard delete is not part of Phase 1; retain provenance and audit.

Errors (package-local sentinels, checked with errors.Is):
- ErrNotFound: absent or outside caller's visible scope.
- ErrConflict: revision/CAS mismatch.
- ErrInvalid: malformed reference, owner mismatch, invalid state transition.
- ErrForbidden: actor lacks administrative authority.
- ErrRevoked: terminal principal/binding.
- ErrUnavailable: ownership/authorization dependency unavailable; fail closed.

## 5. Credential contract — legacy compatibility and technical subjects

Файлы-владельцы: internal/credential/contract.go, store.go, Broker/provider integration.

### 5.1. Keep legacy subject semantics intact

Today credential.Scope is user/team; Subject has ClusterID, Scope, OwnerID, SessionID; Key is ClusterID+Scope+OwnerID. These records are in production use and have encryption AAD implications.

Normative extension:

    const (
        ScopeUser      Scope = "user"       // legacy interactive User credentials
        ScopeTeam      Scope = "team"       // legacy shared Team-account credentials
        ScopePrincipal Scope = "principal" // new TeamPrincipal / ServicePrincipal credentials
    )

    type Subject struct {
        ClusterID string
        Scope     Scope
        OwnerID   string // legacy ScopeUser/ScopeTeam only
        SessionID string // legacy interactive challenge context only

        Principal identity.PrincipalRef // ScopePrincipal only
        BindingID string                // ScopePrincipal only
    }

    type ProvisioningContext struct {
        Actor     identity.ActorRef // authenticated UserPrincipal
        SessionID string            // active Kubron session for this setup challenge
        EventID   string            // durable CriticalIntent created before provisioning starts
    }

    type Key struct {
        ClusterID string
        Scope     Scope
        OwnerID   string // legacy key only

        Principal identity.PrincipalRef // technical key only
        BindingID string
    }

    type Meta struct {
        Key
        Kind             Kind
        Principal        string
        ExpiresAt        *time.Time
        Version          int64
        UpdatedAt        time.Time
        KeyID            string
        EncFormat        string
        LastAuditEventID string
    }

    type Record struct {
        Meta
        Secret []byte
    }

Validation is an exclusive tagged union:
- Legacy ScopeUser: OwnerID=user ID; Principal zero; BindingID empty. SessionID may be present for interactive Web challenge.
- Legacy ScopeTeam: OwnerID=Team ID; Principal zero; BindingID empty. This remains the old shared Team account, **not** TeamPrincipal. Existing positional Go struct literals for Subject/Key must be updated to keyed literals as part of the compile migration; persisted key/AAD semantics must not change.
- Technical ScopePrincipal: Principal.Kind is Team or Service; Principal.ID and OrgID required; TeamID required only for Team; OwnerID empty; BindingID non-empty; SessionID empty.
- No mixed legacy+technical fields.
- UserPrincipal continues using existing user credential path; Phase 1 does not create a technical User credential kind.
- Never infer principal kind from OwnerID or provider Kind.

The existing CredentialStore.List(ownerID) remains for legacy owners; add a typed ListPrincipal(ctx, PrincipalRef) path for technical principals. Extend RevokeFilter with typed PrincipalRef and BindingID fields. A normal principal revoke must supply an exact principal/binding scope; wildcard filters are not accepted from ordinary management calls.

### 5.2. Encryption and key compatibility

- Existing legacy records retain exact old AAD/key interpretation. Never reinterpret ScopeTeam records as TeamPrincipal. Legacy user/team Meta may have empty LastAuditEventID and retain legacy behavior; every technical ScopePrincipal record must carry LastAuditEventID.
- Technical records use a distinct canonical AAD encoding containing format/version marker, ClusterID, ScopePrincipal, Principal.Kind, Principal.ID, OrgID, TeamID (when applicable) and BindingID.
- Canonical encoding is length-delimited or otherwise unambiguous; never concatenate unescaped strings.
- Technical key uniqueness: (ClusterID, Principal.Kind, Principal.ID, BindingID). BindingID cannot be rebound to another principal or cluster.
- Existing user/team rows/files remain readable without rewrite. New technical records use envelope encryption only; no legacy encryption format for new records.
- Rekey, keycheck, backup/restore, delete/revoke/list and plaintext scanning include technical records.
- Migration failure is reported; unreadable records are never treated as absent/empty.

### 5.3. Broker and ConfigFor path

Existing credential.Broker remains the only provider orchestrator. Extend it to accept validated typed Subject for Acquire/Answer/Poll/Cancel/Token and revocation, and add typed principal listing rather than overloading legacy List(ownerID). The CredentialStore contract gains ListPrincipal(ctx, principal identity.PrincipalRef) ([]Meta, error). Technical Meta.LastAuditEventID is mandatory for ScopePrincipal; legacy user/team records may leave it empty. RevokeFilter gains Principal identity.PrincipalRef and BindingID string. No caller may invoke Provider directly.

Dedicated provisioning API (the Broker implementation must satisfy this interface; exact method names may follow existing Broker naming, semantics may not change):

    type ProvisioningBroker interface {
        BeginProvisioning(ctx context.Context, subject Subject, by ProvisioningContext, d Descriptor) (Result, error)
        AnswerProvisioning(ctx context.Context, subject Subject, by ProvisioningContext, challengeID string, answer Answer) (Result, error)
        CancelProvisioning(ctx context.Context, subject Subject, by ProvisioningContext, challengeID string) error
    }

ProvisioningContext is never part of Subject, CredentialStore.Key, encryption AAD or runtime pool key. Every challenge is bound to the exact technical Subject + BindingID + authenticated actor + active session + CriticalIntent EventID. Continuation rechecks the actor's current permission to manage that identity/binding; a lost role/session invalidates the challenge. The Broker stores the resulting token only under the technical Subject and persists LastAuditEventID in its Meta. Technical Token/Acquire refuses to return a credential unless CriticalRecorder.GetOutcome(orgID, Meta.LastAuditEventID) is CriticalSucceeded. This also gates a newly rotated credential version if its audit outcome is pending. A pending binding is activated only after the credential record is durably stored. Its LastAuditEventID is persisted with the state transition; it is usable only after GetOutcome confirms the matching CriticalSucceeded outcome. Missing, failed or unknown outcome keeps the binding unusable.

Required behavior:
- User challenge methods require valid User scope and session binding where Web semantics require it.
- Technical Team/Service subjects are non-interactive during execution: stored credential/refresh flow may run; if a human challenge is required, Acquire/Token returns ErrChallengeRequired and the background operation is blocked. Never attach a user session or fall back to another credential.
- Initial provisioning and explicit credential rotation are different from execution. An authorized human administrator may complete an IdP challenge on behalf of a Team/Service identity through dedicated provisioning methods. The resulting credential is stored under the technical Subject, never under the administrator's User scope. The administrator's session is authorization/provenance for setup only, not the Kubernetes Execution Identity.
- Revoke filters for technical subjects specify exact principal kind, principal ID, cluster/binding as required. Empty wildcard filters are reserved for explicitly authorized administrative maintenance, never normal principal revocation.
- Existing Broker methods and old RPC behavior remain compatible. Technical credential management APIs are not exposed through legacy user-facing RPC accidentally.
- Credential assignment, administrative credential replacement/rotation and revoke use CriticalRecorder intent/outcome. Provisioning intent is durable before starting an IdP challenge; success/failure/expiry/cancellation is durably recorded. Routine provider token refresh is not a new identity assignment, but secrets still never enter audit.

In internal/k8s:
- Keep ConfigFor(ConnPlan) as the **only** constructor of stored-cluster rest.Config.
- Refactor ConnPlan internally so broker transport can use either existing interactive AuthSession or typed technical Subject; do not fabricate AuthSession.
- Add an internal principal planning entry point accepting validated Team/Service PrincipalRef + CredentialBinding ID + cluster ID + Broker. It rejects UserPrincipal, missing binding, mismatched Team ownership and invalid scope.
- Principal plan has no interactive challenge callback. Missing/expired credential blocks the background operation; never fall back to user or legacy team credentials.
- Pool key for technical connection includes ClusterID + principal kind + principal ID + binding ID. Legacy Team account keeps existing cluster-key semantics. User pool isolation remains unchanged.
- Every Manager/client/informer/Helm/terminal consumer preserves subject isolation; no technical connection is served to another principal.
- Principal planning remains internal to trusted Go use cases in Phase 1; do not add a generic frontend RPC for arbitrary PrincipalRef.

## 6. Authorization contract

Package: internal/authorization. It owns Kubron grants/delegations, not Kubernetes credentials or Kubernetes RBAC.

### 6.1. Types

    type TargetRef struct {
        ClusterID string
        Group     string
        Version   string
        Resource  string
        Namespace string
        Name      string
        UID       string
    }

    type NamespaceRef struct {
        ClusterID string
        Namespace string
    }

    type ObjectRef struct {
        ClusterID string
        Group     string
        Version   string
        Resource  string
        Namespace string
        Name      string
        UID       string
    }

    type TargetScope struct {
        OrgID      string
        TeamIDs    []string
        ClusterIDs []string
        Namespaces []NamespaceRef
        Objects    []ObjectRef
    }

    type ConstraintSet struct {
        AllowedEnvironments []string
        DenyDestructive     bool
        NotBefore           *time.Time
    }

    type OperationAction struct {
        OperationID string
        ActionID    string
    }

    type GrantState string
    const (
        GrantActive  GrantState = "active"
        GrantRevoked GrantState = "revoked"
    )

    type Grant struct {
        ID             string
        Principal      identity.PrincipalRef
        Actions        []OperationAction
        Targets        TargetScope
        Constraints    ConstraintSet
        State          GrantState
        Revision       uint64
        ExpiresAt      time.Time
        CreatedBy        identity.ActorRef
        LastMutationID   string
        LastAuditEventID string
    }

    type DelegationState string
    const (
        DelegationActive    DelegationState = "active"
        DelegationSuspended DelegationState = "suspended"
        DelegationRevoked   DelegationState = "revoked"
    )

    type Delegation struct {
        ID               string
        OrgID            string
        OwnerTeamID      string
        ServicePrincipal identity.PrincipalRef
        Actions          []OperationAction
        Targets          TargetScope
        Constraints      ConstraintSet
        State            DelegationState
        Revision         uint64
        ExpiresAt        time.Time
        CreatedBy        identity.ActorRef
        LastMutationID   string
        LastAuditEventID string
    }

Canonicalization:
- ID lists are sorted, deduplicated and validated before persistence/digesting.
- Grant requires explicit OrgID, principal, at least one explicit OperationAction pair, finite expiry, and at least one explicit target selector (TeamIDs, ClusterIDs, Namespaces or Objects). OrgID alone is not a wildcard.
- Delegation requires explicit OrgID, owner Team, one same-Organization ServicePrincipal, finite expiry and explicit operations/actions/targets restricted to owner Team.
- TeamPrincipal grant TargetScope may include only its own Team; evaluator rejects any other Team even if malformed data exists.
- ServicePrincipal has no implicit Team/Cluster membership or target rights.
- Phase 1 target scopes use explicit organization/team/cluster/namespace/object references only. No scripts, executable selectors, external queries or user-defined functions. Label-selector/TargetGroup resolution belongs to later Target phase.
- Target selectors combine by union: a matching explicit Team subtree, Cluster subtree, exact NamespaceRef or exact ObjectRef may satisfy the target portion. Lists within one selector are OR; OrgID remains an outer boundary.
- Existing-object ObjectRef and object-specific TargetRef require UID. Name alone is not object identity.
- Empty child lists mean all descendants only within explicitly supplied parent scope; empty OrgID or entirely empty TargetScope is invalid, never “all tenants”.
- Constraints are typed fields, not arbitrary map/expressions. Phase 1 supports AllowedEnvironments (matched against trusted cluster metadata), DenyDestructive (matched against trusted action catalog facts) and NotBefore (inclusive UTC instant). Grant/Delegation ExpiresAt is exclusive. MaxConcurrency, MaxBudget and other execution-time constraints are deferred until an Operation runtime can enforce them; Phase 1 must reject, not silently accept, unsupported constraints. Unknown future constraint kinds/versions fail closed.

### 6.2. Backend and evaluator boundaries

    type GrantBackend interface {
        CreateGrant(ctx context.Context, in Grant) (Grant, error)
        GetGrant(ctx context.Context, id string) (Grant, error)
        ListGrants(ctx context.Context, principal identity.PrincipalRef) ([]Grant, error)
        UpdateGrant(ctx context.Context, in Grant, expectedRevision uint64) (Grant, error)
        RevokeGrant(ctx context.Context, id string, expectedRevision uint64, mutationID, auditEventID string) (Grant, error)
    }

    type DelegationBackend interface {
        CreateDelegation(ctx context.Context, in Delegation) (Delegation, error)
        GetDelegation(ctx context.Context, id string) (Delegation, error)
        ListDelegations(ctx context.Context, service identity.PrincipalRef,
            ownerTeamID string) ([]Delegation, error)
        UpdateDelegation(ctx context.Context, in Delegation, expectedRevision uint64) (Delegation, error)
        RevokeDelegation(ctx context.Context, id string, expectedRevision uint64, mutationID, auditEventID string) (Delegation, error)
    }

    type TargetFacts struct {
        OrgID       string
        TeamID      string
        Environment string
    }

    type OwnershipResolver interface {
        ResolveTargetFacts(ctx context.Context, target TargetRef) (TargetFacts, error)
    }

    type IdentityStateReader interface {
        GetPrincipalState(ctx context.Context, principal identity.PrincipalRef) (identity.IdentityState, error)
    }

    type ActionFacts struct {
        Destructive bool
    }

    type ActionFactsResolver interface {
        ResolveActionFacts(ctx context.Context, operationID, actionID string) (ActionFacts, error)
    }

    type Clock interface {
        Now() time.Time
    }

    type AdminAction string

    const (
        AdminGrantCreate AdminAction = "grant-create"
        AdminGrantUpdate AdminAction = "grant-update"
        AdminGrantRevoke AdminAction = "grant-revoke"
        AdminDelegationCreate AdminAction = "delegation-create"
        AdminDelegationUpdate AdminAction = "delegation-update"
        AdminDelegationSuspend AdminAction = "delegation-suspend"
        AdminDelegationRevoke AdminAction = "delegation-revoke"
    )

    type AdminAuthorizer interface {
        AuthorizeGrantAdmin(ctx context.Context, actor identity.ActorRef,
            principal identity.PrincipalRef, action AdminAction) error
        AuthorizeDelegationAdmin(ctx context.Context, actor identity.ActorRef,
            ownerTeamID string, action AdminAction) error
    }

    type GrantRequest struct {
        Principal   identity.PrincipalRef
        OperationID string
        ActionID    string
        Target      TargetRef
    }

    type GrantDecision struct {
        Granted        bool
        ReasonCode     string
        GrantIDs       []string
        DelegationID   string
        GrantRevisions []uint64
    }

    type Evaluator interface {
        EvaluateKubronGrant(ctx context.Context, req GrantRequest) (GrantDecision, error)
    }

GrantDecision.Granted means **only** that Kubron technical grants + required Team delegation + principal/tenant target boundaries permit the tuple. It is not final permission to call Kubernetes. It does not evaluate Kubernetes RBAC, credentials, Autonomy Policy, Approval, Plan preconditions or action-specific guardrails. No Phase 1 API may present it as a final “operation authorized” result.

### 6.3. Normative evaluation algorithm

1. Validate principal kind/state and resolve target ownership from trusted server-side catalog. Never trust client-supplied OrgID/TeamID as ownership proof.
2. Require target Organization to equal principal Organization. Require target Cluster to belong to exactly one current Team according to ADR-0012. TargetRef carries no client-supplied owner or environment fields.
3. For TeamPrincipal, require target Team == principal.TeamID. No delegation can expand it.
4. For ServicePrincipal, require an active, unexpired delegation issued by target Team for this exact ServicePrincipal. ServicePrincipal is Organization-owned, not Team-owned; therefore every Team-owned cluster target requires explicit consent from that target Team. Organization Admin cannot substitute for Team Admin.
5. Find active, unexpired grants for the principal matching exact OperationAction pair and target.
6. Grants combine by union: at least one applicable grant may cover the tuple. All constraints within the qualifying grant are ANDed.
7. Resolve ActionFacts from the trusted versioned Operation/Action catalogue and use injected Clock for time checks. Evaluate every supported ConstraintSet field using trusted target/action facts. If a stored constraint cannot be evaluated by this Phase 1 evaluator, deny; never skip it.
8. Intersect with identity scope, tenant ownership and required delegation. No grant/delegation, ambiguity, stale revision, missing ownership or unavailable dependency → deny.
9. Return stable safe reason code and IDs/revisions needed for audit. Do not reveal metadata of foreign targets.

The evaluator also verifies that the principal, each qualifying grant and the required delegation have a durable successful CriticalOutcome for their LastAuditEventID. Missing, failed, unknown or unavailable outcome → deny. No explicit user Deny grants, precedence ordering or last-grant-wins. OperationAction pairs are exact pairs; separate Operation and Action lists must not be interpreted as an unintended Cartesian product. Kubernetes RBAC and Autonomy Policy remain mandatory independent gates, implemented outside this package. Evaluator must not fetch credentials, construct a Kubernetes client or call Kubernetes API.

Admin write authorization:
- Team Admin may create/update/suspend/revoke delegation only for their own Team and its clusters.
- Organization Admin may manage Service Identity but cannot issue or alter a Team's delegation on its behalf.
- Grant management is scoped to identity owner and current app-RBAC. Service receives injected admin-authorizer; it does not trust a role string supplied by UI.
- Changes take effect at next evaluation. No stale positive authorization cache. Any future cache requires explicit revision invalidation and fail-closed behavior.

## 7. Critical audit contract

Package: internal/audit. Do not change legacy Logger.Record(Event) best-effort contract in this phase.

### 7.1. Types and interface

    type CriticalResult string

    const (
        CriticalSucceeded CriticalResult = "succeeded"
        CriticalFailed    CriticalResult = "failed"
        CriticalUnknown   CriticalResult = "unknown"
    )

    type CriticalIntent struct {
        EventID          string
        MutationID       string
        RequestDigest    string
        ActorKind        string
        ActorID          string
        PrincipalKind    string
        PrincipalID      string
        OrgID            string
        TeamID           string
        ClusterID        string
        Action           string
        ResourceType     string
        ResourceID       string
        ExpectedRevision uint64
        OccurredAt       time.Time
    }

    type CriticalOutcome struct {
        EventID          string
        Result           CriticalResult
        ResourceRevision uint64
        ReasonCode       string
        CompletedAt      time.Time
    }

    type CriticalRecorder interface {
        AppendIntent(ctx context.Context, in CriticalIntent) error
        AppendOutcome(ctx context.Context, out CriticalOutcome) error
        GetOutcome(ctx context.Context, orgID, eventID string) (CriticalOutcome, error)
        ListPending(ctx context.Context, orgID string, limit int) ([]CriticalIntent, error)
    }

EventID identifies one immutable intent/outcome pair; MutationID identifies requested domain mutation. The resource mutation persists LastAuditEventID=EventID. A technical identity, binding, grant or delegation is usable only when GetOutcome(orgID, LastAuditEventID) returns CriticalSucceeded; missing, failed or unknown outcome means deny. GetOutcome and ListPending are organization-scoped. Same EventID + same canonical payload is idempotent. Same EventID + different payload is a conflict. Outcome requires a known intent and is unique; conflicting second outcome is an error. Pending means durable intent exists but no durable outcome exists.

### 7.2. Required call protocol

Every protected Identity/Grant/Delegation/Binding mutation uses this order:
1. Validate request and authorize human actor.
2. Construct immutable CriticalIntent with unique EventID, MutationID, canonical request digest, expected resource revision and opaque resource identifiers. No secret values.
3. Durably AppendIntent. If it fails, do not call domain Backend and do not mutate credentials.
4. Perform one CAS/domain mutation. No retry loop may silently reuse the same event with changed payload.
5. Append CriticalOutcome (succeeded or failed). If outcome append fails after mutation attempt, return typed ErrOutcomeUnknown; do not report success and do not blindly repeat mutation.
6. Pending intents are reconciled by reading resource and comparing mutation ID/revision/request digest. If result cannot be proven, retain unknown and require explicit safe resolution.

A helper may centralize this protocol, but must not hide errors or automatically retry mutation after unknown outcome. The same protocol applies to credential assignment/replacement/revoke operations; Lane B must inject CriticalRecorder at the trusted credential-management use-case boundary (credential may import audit; audit must not import credential). The technical credential Meta stores LastAuditEventID, and Broker.Token verifies its successful outcome before returning the token. If outcome persistence fails, the new version is unusable until reconciliation. Provider token refresh itself is not a new identity assignment. If outcome persistence fails, the binding/resource remains unusable until GetOutcome confirms success or reconciliation resolves the pending intent.

### 7.3. Durability and persistence

- PostgreSQL: append intent/outcome in committed transaction; unique constraints enforce EventID/phase idempotency. Integration owner supplies pgstore adapter and migration. A domain mutation and its audit intent are not claimed to be one cross-domain transaction in Phase 1; durable intent-before-mutation is the guarantee. A later aggregate may strengthen this with atomic mutation+audit in one transaction.
- Desktop File: dedicated critical journal, separate from rotating legacy audit.log; serialized single-process append, complete-write check, file Sync, directory Sync when creating/rotating. If durability cannot be confirmed, fail closed. Startup detects incomplete/corrupt tail; it must not silently discard or reinterpret a partial record.
- Critical journal is append-only. Retention/archival is separate and cannot silently delete pending intents or mandatory audit. Pending-intent queries are organization-scoped and must not expose another tenant's audit metadata.
- stdout mirror is not durable audit storage.
- Never store plaintext kubeconfig, tokens, passwords, refresh tokens, provider payloads, private keys or secret fields. Audit contains IDs, action, revision, safe reason code, digest and timestamps only.
- Legacy best-effort audit remains behavior-compatible. Only new protected operations use CriticalRecorder.

## 8. Error and concurrency contract

Shared domain error semantics (package-local sentinels, errors.Is):
- ErrNotFound: absent or outside caller's visible scope.
- ErrInvalid: malformed input / invalid enum / invalid owner or scope.
- ErrForbidden: actor or principal is not authorized.
- ErrConflict: revision/CAS conflict or conflicting idempotency/audit payload.
- ErrRevoked: identity, binding, grant or delegation is revoked/expired where distinct result is safe.
- ErrUnavailable: required dependency cannot be evaluated; fail closed.
- ErrOutcomeUnknown: mutation may have happened but durable outcome could not be recorded; reconcile before retry.

Use package-local sentinel errors and stable transport reason codes. Never use string matching. Never return secrets in wrapped errors.

Concurrency:
- Every mutable aggregate has monotonic Revision.
- Update/revoke uses expected revision (CAS); zero is not a wildcard.
- Create requires nonexistence.
- Stale writer receives ErrConflict; no last-write-wins.
- Web/PostgreSQL uses transaction/CAS/unique constraints and database time for expiry decisions.
- Desktop File backend serializes read-modify-write in one process and uses atomic replacement + durable flush. No multi-process concurrency claim.
- Authorization is re-evaluated for every protected mutation; stale cached allow is forbidden.

## 9. Storage and integration ownership

Lane-owned files:

| Lane | Allowed implementation files |
|---|---|
| A — Identity | internal/identity/** (types/backend/service/file backend/tests); no shared composition |
| B — Credentials | internal/credential/**, narrowly scoped internal/k8s/configfor.go and connection pool files/tests after freeze |
| C — Authorization | internal/authorization/** (types/backend/evaluator/file backend/tests) |
| D — Critical Audit | internal/audit/critical*.go and dedicated critical file backend/tests; legacy Logger changes only if required for compile and explicitly reviewed |

Integration owner only:
- internal/store/** composition and interfaces.
- internal/pgstore/**, SQL migrations, migration manifest and schema tests.
- internal/app/**, transport DTOs, RPC allowlists, Wails bindings/generated files, HTTP routes.
- Cross-domain wiring, PostgreSQL adapters for identity/authorization/critical audit, shared frontend changes, docs index.
- Any file touched by more than one lane.

Lane A/C/D submit backend interfaces and schema requirements; they do not edit migration registry or pgstore. Lane B documents credential record/AAD migration requirements; actual PG schema changes remain integration-owned.

**No shared file edits in parallel.** If a task needs a file outside its lane allowlist, submit a small interface proposal to integration owner and wait for an assigned integration change. Do not force-push or silently broaden scope.

## 10. Required contract test suites

Each domain publishes reusable test functions/fixtures so File and PostgreSQL adapters run the same cases.

### Identity
- PrincipalRef/OwnerScope validation matrix; Team vs Service owner invariants.
- Owner scope immutable; creator is provenance only.
- Team principal cannot target another Team.
- Service principal has no implicit Team/cluster rights.
- State transition matrix, revoked terminal, CAS conflict, duplicate create.
- Binding uniqueness per principal+cluster; no cross-principal binding reuse.
- Identity/credential assignment do not implicitly create grants.

### Credential
- Legacy ScopeUser/ScopeTeam records round-trip with unchanged AAD.
- ScopeTeam legacy record is never resolved for TeamPrincipal.
- ScopePrincipal requires kind+principal ID+binding ID; all mixed/partial combinations reject.
- Principal key separation for same cluster and same textual ID but different kind.
- Pool isolation for same cluster across TeamPrincipal/ServicePrincipal/bindings.
- No fake AuthSession, no challenge fallback, no User credential fallback.
- Broker/provider remains the only token path; ConfigFor remains sole rest.Config constructor.
- Rekey/keycheck/backup/restore/plaintext scans include technical records.
- Concurrent rotation/revoke and stale version cannot overwrite/revive newer credential.

### Authorization
- Grant union is order-independent.
- All constraints in one grant are ANDed.
- Outer scope/ownership/delegation restricts the union.
- TeamPrincipal is strictly one-Team.
- ServicePrincipal requires explicit owner-Team delegation for every Team-owned target.
- Organization Admin cannot issue delegation on behalf of Team.
- No grant, no delegation, expired/revoked delegation, unknown ownership, malformed target or unavailable dependency → deny.
- Grant revocation/change is visible on next evaluation; no stale allow.
- Target Group sharing is never a grant.

### Critical audit
- AppendIntent failure prevents domain mutation.
- AppendOutcome failure returns ErrOutcomeUnknown.
- Duplicate same EventID/payload is idempotent; different payload conflicts.
- Pending intents are discoverable after restart.
- File short write, disk full, permission failure, Sync failure, corrupt tail and process interruption are tested.
- PostgreSQL unique constraints, transaction rollback, tenant-safe query and concurrent append tests.
- Secrets never appear in JSON, logs, errors or stdout mirror.

### Cross-platform
- Same domain contract suite for File/Desktop and PostgreSQL/Web.
- Same validation, revision, state transition and authorization reason semantics.
- Platform capability does not alter authorization.
- Web request/session loss cannot convert User credentials into technical credentials.
- Desktop shutdown does not introduce a daemon.
- No new UI/API endpoint is exposed before backend enforcement and integration tests exist.

## 11. ADR and implementation sequence

ADR numbers 0035–0038 are proposed by the Phase 1 handoff. Confirm the repository ADR list immediately before adding files; do not renumber or overwrite an existing ADR.

Required formal ADRs:
- ADR-0035 Technical Principals and Identity Lifecycle.
- ADR-0036 Subject-Aware Credential Resolution and ConfigFor.
- ADR-0037 Technical Authorization Grants and Cross-Team Delegation.
- ADR-0038 Critical Audit Durability.

They formalize already accepted product/security decisions; they must not reopen them. If an engineering choice changes semantics, stop and request a new explicit decision before runtime code.

Recommended sequence:
1. Integration owner commits this frozen type/interface contract and assigns exact lane branches/file allowlists.
2. Lane D implements CriticalRecorder File contract and failure tests.
3. Lane A implements identity types/backend/service/File contract.
4. Lane C implements grant/delegation backend/evaluator against frozen identity types.
5. Lane B implements technical Subject/Broker/ConnPlan/ConfigFor and isolation tests against frozen identity types.
6. Integration owner implements PG migrations/adapters, Store composition, app wiring and cross-platform contract runs.
7. Independent security review and Phase 1 exit review.

No Kubernetes mutating action, scheduler execution or Autonomous mode is included. This freeze does not authorize them.

## 12. Acceptance of the freeze

The contract is ready for parallel implementation only when:
- [ ] Integration owner confirms type names and signatures against repository Go conventions.
- [ ] No dependency cycle exists.
- [ ] Lane file allowlists are accepted by all four developers.
- [ ] Legacy credential AAD and key compatibility plan is reviewed.
- [ ] Critical audit intent/outcome and unknown-result semantics are accepted.
- [ ] PostgreSQL schema/migration owner is named.
- [ ] Shared test command list is copied from current CI workflows.
- [ ] ADR numbers are confirmed unused.
- [ ] Freeze commit SHA is copied verbatim into each developer task.

A compile-only PR is insufficient. Every lane must include the security and persistence tests listed above.
