# Developer B — Subject-aware Credential Resolution

You implement Lane B from the Phase 1 Contract Freeze. Read the common instructions in [README](README.md) and the complete normative contract before editing code.

## Goal

Extend the existing credential Broker/Provider/Store and Kubernetes connection planning path for TeamPrincipal and ServicePrincipal without breaking legacy user/team records or bypassing ConfigFor.

## Allowed files

- `internal/credential/**`: subject/key/metadata validation, Broker/provider integration, credential file-store and tests.
- `internal/k8s/configfor.go`, narrowly scoped connection-pool/key files and their tests.
- No other files without explicit integration-owner assignment.

## Required implementation

1. Preserve legacy `ScopeUser` and `ScopeTeam` semantics, persisted key identity and encryption AAD byte-for-byte. Legacy ScopeTeam is the old shared Team account, never TeamPrincipal.
2. Add the frozen technical subject kind and typed `identity.PrincipalRef` + `CredentialBindingID`. Enforce an exclusive tagged union; reject mixed legacy/technical fields.
3. Technical subject must have no User SessionID. A separate provisioning context may bind the interactive admin actor/session to a one-time challenge; the resulting secret is saved only under the technical subject and binding.
4. Resolve only active identity and active binding; validate principal/tenant/scope before credential use. Pending/revoked binding is denied.
5. Continue using existing Broker and Provider for token acquisition, refresh, challenge, revoke and secret scrubbing. No direct provider bypass.
6. Extend typed ConnPlan creation for technical principals. Preserve `ConfigFor` as the sole stored-cluster `rest.Config` constructor. No fake AuthSession and no direct rest.Config/client construction.
7. Pool/cache key includes principal kind + principal ID + cluster + binding ID/revision. Prove no collisions with legacy user/team keys or another technical principal.
8. Rotation uses the current validated credential version; stale writers cannot overwrite newer versions. Revoke blocks new token issuance; never fallback to revoked, user or legacy team credentials.
9. Include technical records in rekey, keycheck, backup/restore and plaintext scans. Do not change PostgreSQL adapters/migrations; provide exact schema/AAD requirements to integration owner.
10. Inject the frozen critical audit contract at the trusted credential assignment/provision/revoke use-case boundary. Do not make provider token refresh a new identity mutation.

## Explicit non-goals

- No identity domain implementation (consume canonical `internal/identity` types).
- No grants/delegation evaluation (consume an injected authorization result at use-case boundary; never implement policy here).
- No `internal/store`, `internal/pgstore`, migrations, `internal/app`, RPC/Wails, frontend.
- No new secrets subsystem, scheduler, Run executor or Kubernetes write action.

## Required tests

- Legacy user/team record round-trip with unchanged AAD.
- Legacy ScopeTeam cannot resolve as TeamPrincipal.
- All invalid mixed/partial technical subjects rejected.
- Key/pool separation across principal kind, principal ID, cluster and binding revision.
- Admin challenge is session-bound; technical subject contains no user session; session/role loss blocks continuation.
- No fake AuthSession, no fallback credential, no direct Config construction outside ConfigFor.
- Concurrent rotation/revoke, stale version and rekey/keycheck/backup restore.
- No secrets in logs/errors/events/DTO/cache keys.

## Deliverable

A focused PR limited to the allowed credential and ConfigFor/pool files, with compatibility proof, tests and a migration note for integration owner. Do not merge.
