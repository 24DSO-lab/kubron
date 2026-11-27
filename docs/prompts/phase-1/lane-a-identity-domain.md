# Developer A — Identity Domain

You implement Lane A from the Phase 1 Contract Freeze. Read the common instructions in [README](README.md) and the complete normative contract before editing code.

## Goal

Implement canonical principal/identity metadata and lifecycle for TeamPrincipal and ServicePrincipal. Do not implement credential resolution, grants/delegation evaluation, PostgreSQL adapters, or API exposure.

## Allowed files

- `internal/identity/**`: types, validation, narrow Backend interface, domain service, File backend, unit/contract tests.
- No other files without explicit integration-owner assignment.

## Required implementation

1. Implement canonical `PrincipalKind`, `PrincipalRef`, `ActorRef`, `OwnerScope`, `Identity`, `IdentityState`, `CredentialBinding`, and `BindingState` exactly as defined in Contract Freeze.
2. Validate tagged principal/owner combinations. UserPrincipal references existing auth users; only Team/Service technical identities are persisted by this package.
3. Team identity must have exactly one Team owner. Service identity must have exactly one Organization owner. Owner is immutable.
4. IDs are opaque, validated, non-empty, bounded and never reused after revoke. Creator is provenance only.
5. Implement active/suspended/revoked lifecycle; revoked is terminal. Binding lifecycle is pending/active/revoked. A pending binding reserves the principal+cluster tuple but cannot be used.
6. Enforce one active/pending binding per (principal, cluster) according to the frozen contract; reject ambiguous resolution.
7. Use revision/CAS for state changes and idempotent mutation IDs. Do not use last-write-wins.
8. Inject administrative authorization and `audit.CriticalRecorder`; never trust a role string or owner supplied by the UI.
9. Persist `LastMutationID` and `LastAuditEventID`. Identity/binding is not usable unless its last critical audit outcome is confirmed succeeded.
10. Provide reusable backend contract tests for File persistence and state transitions.

## Explicit non-goals

- No secrets, Broker/Provider, credential encryption or kubeconfig handling.
- No grants, delegation evaluator, Kubernetes RBAC checks or Autonomy policy engine.
- No `internal/store`, `internal/pgstore`, SQL migrations, `internal/app`, RPC/Wails, frontend.
- No Kubernetes client or write action.

## Required tests

- PrincipalRef and OwnerScope validation matrix.
- Team/Service owner mismatch and immutable owner.
- Creator departure/role loss does not change ownership or identity lifecycle.
- No inherited user/team admin rights.
- Revoked is terminal; repeated same mutation is idempotent; stale revision conflicts.
- Binding uniqueness, pending/revoked deny, no cross-principal reuse.
- Critical audit intent failure prevents mutation; unknown outcome blocks use.
- File short write/corruption/restart behavior supported by the backend contract.

## Deliverable

A focused PR limited to `internal/identity/**`, with package tests, reusable backend contract tests, exact test commands, and a short security review note. Do not merge.
