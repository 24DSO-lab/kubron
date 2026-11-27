# internal/identity — security review note (Phase 1, Lane A)

Scope: only `internal/identity/**`. No changes to store/pgstore/migrations/app/RPC/frontend/docs indexes.
No Kubernetes client, no write action, no scheduler/daemon, no secrets, no second secret store.

## Invariants and where they are enforced

| Invariant | Enforcement | Tests |
|---|---|---|
| Team identity has exactly one Team owner, Service exactly one Organization; owner immutable | `Identity.validate` (owner must equal owner derived from the ref); no API changes owner | contract "validation matrix", "CAS, idempotency and immutable fields" |
| Owner is never taken from the UI | `Service` derives owner from `PrincipalRef.Owner()`, authorizer sees that value, stored owner is re-checked | `TestService_TransitionTargetsMustBeVisibleToTheActor` |
| Creator is provenance only | no code path reads `CreatedBy` for authority | `TestService_CreatorDepartureChangesNothing` |
| No inherited admin rights; only a human `ActorUser` administers | `Service.authorize`; authorizer is injected, no role strings | `TestService_Authorization_*` |
| Revoked is terminal; IDs never reused | `ValidateIdentityTransition`; unique `(Kind, ID)` across tenants | contract "revoked is terminal" |
| One pending/active binding per (principal, cluster); pending/revoked unusable; no cross-principal binding reuse | `snapshot.putBinding`, `Service.ResolveBinding` | contract "binding: …" |
| Cluster ownership comes from a trusted catalog; Team principal → own Team only; Service → own Org only | `Service.checkClusterOwnership` via `ClusterOwnerResolver` | `TestService_Binding_ClusterOwnershipIsResolvedByTheServer` |
| Every mutation is CAS; zero is not a wildcard; no last-write-wins | `setState`/`transitionBinding` | contract + concurrent writers |
| Critical audit: intent before mutation; failed intent ⇒ no mutation; lost outcome ⇒ `audit.ErrOutcomeUnknown`, never blind retry | `Service.protect` | `TestService_IntentFailureBlocksEveryMutation`, `…OutcomeFailure…` |
| Use requires confirmed `CriticalSucceeded` for `LastAuditEventID` | `Service.Resolve`/`ResolveBinding` (missing/failed/unknown ⇒ deny; audit down ⇒ `ErrUnavailable`) | `TestService_Resolve_RequiresConfirmedSuccessfulAudit` |
| Outcome is recorded even if the caller canceled after the mutation | `context.WithoutCancel` for outcome appends | `…canceled after the mutation` |
| File backend: atomic replace, fsync, fail closed | `FileBackend.persist`, strict `decodeSnapshot`, poison on unclear dir-sync | `file_fault_test.go` |
| No secrets/free text in audit, logs, errors | audit gets opaque IDs + digest only (no display name); error texts carry field names only | `TestService_NoSecretsInAuditOrErrors` |

## Design decisions worth reviewing

1. **Server-derived IDs.** Principal and binding IDs are derived from owner + caller `MutationID` (sha256, prefix `tid-`/`sid-`/`bnd-`).
   Clients cannot pick IDs, and retries of the same request converge on the same record. Idempotency window for `Create`
   lasts until the next mutation of the record (only the last `LastMutationID` is stored, per contract).
2. **Replay reconciliation.** A repeated request whose mutation is already applied does not mutate again; if its outcome
   was lost it is completed *provably* (record carries the request's `LastMutationID` and the attempt's `LastAuditEventID`).
   There is no background reconciler (forbidden in Phase 1).
3. **Each attempt has its own `EventID`**, `MutationID` stays constant across retries; a crashed attempt leaves a pending intent that never gates use.
4. **`GetBinding` returns the single live binding;** when only revoked ones exist it returns the latest with `ErrRevoked`
   (safe default for consumers that forget a state check). Consumers should use `Service.ResolveBinding`.
5. **Revoked/suspended identity does not cascade-revoke bindings** (would multiply audited mutations); `Resolve*` fail closed on principal state.

## Known gaps / proposals for the integration owner

- **Additive interface:** `ClusterOwnerResolver` (§4 requires trusted cluster ownership but gives no signature). Needs an adapter over the cluster catalog.
- **No identity update use case** (`AdminUpdate` is used for resume): frozen `Backend` has no primitive to change `DisplayName`.
- **No `AdminRead` action** in the frozen contract: `Get/List/ListBindings` are trusted-server reads; RBAC for viewing must be enforced by the transport layer.
- **i18n:** CONTRIBUTING requires `backend.json` entries for new `errors.New` in `internal/`. The six sentinels in `types.go` need
  them (outside this lane's allowlist; `internal/audit` has the same pending 30 entries, so `i18n:check` already fails on `refactor`).
- **No general `ReconcilePending`:** needs a way to read an intent by `EventID` or a safe resolution policy; only same-request replay reconciles.
- **File backend is single-process** (no file lock), as the contract states. PostgreSQL adapter is integration-owned and must pass `identitytest.Run`.
- Actor/org/team/cluster IDs must match the audit token alphabet (`[A-Za-z0-9._:@+=-]`, no `/`, ≤128); IDs outside it cannot be audited and are rejected (fail closed).

## Commands

    go vet -tags webkit2_41 ./internal/identity/...
    GOOS=windows go vet ./internal/identity/...
    go test -race -count=1 -tags webkit2_41 ./internal/identity/... ./internal/audit/...
    python3 scripts/license-headers.py && gofmt -l internal/identity
