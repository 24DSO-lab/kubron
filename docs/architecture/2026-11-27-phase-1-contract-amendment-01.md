# Kubron Phase 1 — Contract Amendment 01

- Status: **accepted normative amendment; effective** (merged via PR #273 at `9a2998f83147d1c79c4a4aef3b5c9504e1aa416b`).
- Date: 2026-11-27
- Parent contract: [Phase 1 Contract Freeze](2026-11-27-phase-1-contract-freeze.md)
- Scope: identity cluster ownership, identity display-name mutation, pending-intent reconciliation ownership, and cross-package implementation clarifications.

This amendment is additive. It does not reopen unrelated Phase 1 contracts, authorize Kubernetes write actions, or change the four-lane ownership model. This document and the parent Contract Freeze must be read together. Where this amendment is more specific, it takes precedence.

## 1. Trusted cluster ownership resolution

The Identity domain requires trusted cluster ownership for credential binding, but the parent contract did not define the adapter contract. Add the following to the Identity domain contract.

```go
type ClusterOwner struct {
    OrgID  string
    TeamID string
}

type ClusterOwnerResolver interface {
    ResolveClusterOwner(ctx context.Context, clusterID string) (ClusterOwner, error)
}
```

Normative rules:

1. The resolver is an injected port owned by `internal/identity`; the concrete adapter is owned by the Integration Owner and must read the existing trusted server-side cluster catalog.
2. Do not create a second cluster catalog, duplicate ownership records, or accept OrgID/TeamID from a client as ownership proof.
3. A successful result must identify exactly one current Organization and one current Team for the cluster. Empty, malformed, missing, or ambiguous ownership is a deny condition.
4. Missing cluster maps to `identity.ErrNotFound`. Ambiguous ownership or unavailable catalog maps to `identity.ErrUnavailable` (or a package-local error that preserves fail-closed `errors.Is` semantics). No fallback is permitted.
5. TeamPrincipal binding is allowed only when the resolved TeamID equals the principal's TeamID and OrgID matches. ServicePrincipal binding is allowed only within its Organization; binding does not grant authorization to act on the cluster.
6. The resolver is used only for current ownership facts. It does not evaluate Kubron grants, delegation, Kubernetes RBAC, credentials, or autonomy policy.

The resolver's contract tests must cover one valid owner, missing cluster, unavailable catalog, malformed result, and ambiguous/multiple ownership. The production adapter and its integration tests are Integration Owner work, not Lane A.

## 2. Audited DisplayName update use case

The parent contract lists `AdminUpdate` but the Backend has no operation capable of changing `Identity.DisplayName`. Add this narrow mutation; do not introduce a generic Update/Patch API.

Add to `identity.Backend`:

```go
UpdateDisplayName(
    ctx context.Context,
    ref PrincipalRef,
    expectedRevision uint64,
    displayName string,
    mutationID string,
    auditEventID string,
) (Identity, error)
```

Add the corresponding domain service use case:

```go
UpdateDisplayName(
    ctx context.Context,
    actor ActorRef,
    ref PrincipalRef,
    expectedRevision uint64,
    displayName string,
    mutationID string,
) (Identity, error)
```

Normative rules:

1. The service derives OwnerScope from the canonical PrincipalRef and calls the existing AdminAuthorizer with `AdminUpdate`. No owner or role is accepted from the client.
2. The operation changes only DisplayName, UpdatedAt, Revision, LastMutationID and LastAuditEventID. PrincipalRef, Owner, CreatedBy, CreatedAt and lifecycle State are immutable in this operation.
3. Validate and normalize DisplayName according to the existing identity validation policy. Reject empty/invalid values; do not use DisplayName as an identifier.
4. Use the existing CriticalIntent → CAS mutation → CriticalOutcome protocol. The request digest includes the canonical new DisplayName and target identity; audit contains opaque identifiers and digest only, never the display-name text.
5. Require a non-zero expected revision. A stale revision returns ErrConflict. Same MutationID + same canonical request is idempotent; same MutationID with a different request conflicts. A lost outcome is ErrOutcomeUnknown and must not trigger an automatic second mutation.
6. This operation does not change principal grants, credential binding, credentials, identity state, ownership, or execution authority.
7. File and PostgreSQL implementations must pass the same backend contract tests. PostgreSQL implementation remains Integration Owner work.

No `AdminRead` action is added. Backend Get/List/GetBinding/ListBindings are trusted internal server-side reads. User-visible read permission is enforced at the application/transport boundary; absence of AdminRead is not permission to expose these methods to unauthenticated callers.

## 3. Pending CriticalIntent reconciliation

The parent contract §7.2 requires pending-intent reconciliation, while Lane A implemented only same-request replay. These are distinct mechanisms.

### 3.1 Same-request replay

Same-request replay is the domain service's idempotency behavior for a retry carrying the same MutationID and the same canonical request. It may return or complete a result only when the resource's persisted mutation/audit metadata proves the exact operation.

### 3.2 Pending-intent reconciliation

Pending-intent reconciliation is an explicit recovery workflow over durable intents returned by `CriticalRecorder.ListPending(orgID, limit)`. It is not a request replay and is not owned by Lane A.

The Integration Owner owns the Phase 1 reconciliation orchestration and its application-level entry point. The workflow must:

1. Enumerate pending intents by Organization using ListPending; never scan or expose another Organization's intents.
2. Resolve each intent to its typed domain resource using the trusted resource catalog/adapter. Read the resource; do not repeat the mutation as a probe.
3. Compare the intent's EventID, MutationID, RequestDigest, ExpectedRevision and opaque resource identity with the resource's persisted LastAuditEventID, LastMutationID and Revision, using the resource-specific contract.
4. Append a CriticalOutcome only when the result is provable under the resource's reconciliation rules. A proven applied mutation may be completed as succeeded. A proven not-applied mutation may be completed as failed only when the resource's CAS/revision history proves no side effect occurred.
5. If the resource is missing unexpectedly, has advanced beyond a provable state, has mismatched IDs/digest, or cannot be read, retain the pending/unknown condition and surface a safe unresolved result. Never infer success or failure from timestamps, logs, caller retries, or absence of a matching latest mutation marker alone.
6. Reconciliation never automatically retries the protected mutation. A new attempt requires a fresh authorization and a new CriticalIntent after the previous state has been safely resolved.
7. Phase 1 does not add a daemon, background worker, scheduler, periodic polling loop, or automatic retry service. Reconciliation is an explicit trusted application/administrative workflow and can be invoked after restart or on demand.
8. The reconciler must be idempotent and safe under concurrent invocations. Conflicting outcomes remain errors; do not overwrite an existing outcome.

The existing `CriticalRecorder.ListPending` interface is sufficient for discovery. Do not add `AdminRead`, a generic audit mutation API, or an audit-to-domain dependency. If an implementation discovers that a typed read adapter is missing, the Integration Owner proposes the narrowest domain-owned port rather than adding a generic repository.

## 4. Audit identifier compatibility

Identity, actor, organization, team, cluster, mutation, and event identifiers written into CriticalIntent must satisfy the existing audit token constraints. For all identifiers passed into CriticalRecorder, enforce the canonical audit-safe alphabet:

`[A-Za-z0-9._:@+=-]`, maximum 128 bytes, non-empty where required.

Validation must occur at the owning domain boundary before an intent is appended. Do not silently rewrite IDs, replace unsupported characters, or defer failure until after partial work. DisplayName and other human-readable free text are not identifiers and must not be copied into audit token fields.

## 5. Lane ownership and implementation sequence

- Lane A's already-merged implementation is not reopened by this amendment. Its current `ClusterOwnerResolver` implementation is the intended narrow port; confirm its exact signature against §1 and report any mechanical mismatch. The Integration Owner supplies the production catalog adapter.
- The DisplayName Backend/service use case is a focused Identity follow-up, separate from Lanes B and C. It was implemented against this amendment and merged in PR #276 (`fix(identity): implement audited DisplayName updates`). PostgreSQL parity remains Integration Owner work.
- Lane B consumes the canonical identity types and binding-resolution service. It must not create its own cluster ownership catalog or duplicate the `ClusterOwnerResolver` adapter.
- Lane C's `OwnershipResolver` remains a separate authorization-domain port for resolving target ownership facts. It must not be conflated with `identity.ClusterOwnerResolver`; both adapters may read the same trusted catalog.
- Integration Owner owns PostgreSQL parity, migration, composition/wiring, explicit pending-intent reconciliation workflow, cross-platform integration and audit-safe identifier validation across boundaries.
- No Lane may start from a different semantic contract. After this amendment merges, B and C must use the same current `refactor` base SHA, and the Integration Owner records that SHA in their task headers.

## 6. Required acceptance checks

- Identity resolver contract tests cover valid, missing, ambiguous and unavailable cluster ownership.
- DisplayName update tests cover authorization, immutable fields, CAS conflict, idempotency, audit intent failure, unknown outcome, and File/PostgreSQL contract parity.
- Reconciliation tests cover proven-applied, proven-not-applied, ambiguous/advanced resource state, missing resource, unavailable resource read, duplicate invocation and conflicting outcome.
- Audit identifier tests reject invalid characters, empty required IDs and values longer than 128 bytes before any CriticalIntent is appended.
- No daemon, scheduler, automatic retry, Kubernetes write action, new secrets store, or client-trusted ownership input is introduced.
