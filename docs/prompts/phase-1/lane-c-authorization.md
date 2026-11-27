# Developer C — Authorization Grants and Cross-Team Delegation

You implement Lane C from the Phase 1 Contract Freeze. Read the common instructions in [README](README.md) and the complete normative contract before editing code.

## Goal

Implement Kubron authorization grants and Team-issued Cross-Team Delegation for technical principals. This package evaluates Kubron eligibility only; it does not authorize Kubernetes API calls by itself.

## Allowed files

- `internal/authorization/**`: domain types, narrow Backend interfaces, File backend, evaluator, unit/property/contract tests.
- No other files without explicit integration-owner assignment.

## Required implementation

1. Consume canonical `identity.PrincipalRef`, `ActorRef`, identity state and `tenant` scope types; do not redeclare shared principal types.
2. Define exact OperationID + ActionID pairs. Never interpret separate operation/action arrays as an unintended Cartesian product.
3. Define typed TargetRef/TargetScope. Resolve current owner/team/environment facts through an injected trusted OwnershipResolver; client-supplied owner facts are never trusted.
4. TeamPrincipal is restricted to its own Team. It cannot receive delegation and cannot access another Team's targets.
5. ServicePrincipal requires an active, unexpired delegation issued by the Team owner of each Team-owned target, addressed to that exact ServicePrincipal in the same Organization. Organization Admin cannot issue delegation on behalf of Team.
6. Grant semantics: applicable allows combine by union; constraints within one grant combine by AND; identity scope, tenancy, ownership and delegation intersect and only narrow.
7. No explicit Deny grants, precedence ordering or last-grant-wins. No grant/delegation, revoked/expired state, stale revision, unknown owner, malformed scope or unavailable dependency => deny.
8. Inject ActionFactsResolver and Clock. Evaluate only typed, versioned constraints; unknown/un-evaluable constraints fail closed.
9. Return stable safe reason codes and grant/delegation IDs/revisions. Do not leak foreign target metadata.
10. GrantDecision means only Kubron grant/delegation eligibility. It is not final Kubernetes authorization and must not be used as a bearer token.
11. Re-evaluate on every protected mutation; no stale positive authorization cache. Use CAS revisions and idempotent mutation IDs.
12. Critical audit is mandatory for grant/delegation mutations. Use frozen CriticalRecorder; audit failure blocks mutation and unknown outcome blocks use.

## Explicit non-goals

- No credential loading, Kubernetes clients/RBAC, ConfigFor, Autonomy policy, Plan/Approval or operation execution.
- No Target Groups or selector engine in this phase.
- No PostgreSQL adapters/migrations, Store composition, app/RPC/Wails or frontend.
- No Kubernetes write actions.

## Required tests

- Grant union is order-independent; constraints are ANDed.
- External identity/tenant/delegation boundaries narrow grant union.
- Team principal cross-team denial; Service principal per-team explicit delegation.
- Org Admin cannot issue Team delegation.
- Expiry/revocation/revision race and missing ownership/dependency fail closed.
- Target Group sharing never grants access.
- Critical audit failure blocks mutation; unknown outcome denies.
- No metadata leakage for inaccessible targets.

## Deliverable

A focused PR limited to `internal/authorization/**`, with evaluator property tests, reusable backend contract tests, reason-code documentation and no integration files. Do not merge.
