# Phase 1, Lane B — subject-aware credential resolution: note for the integration owner

Scope: `internal/credential/**`, `internal/k8s/configfor.go` (+ tests), and the PostgreSQL challenge-store adapter's
non-destructive expiry listing. Credential PostgreSQL schema/migrations, `internal/app`, RPC and the frontend are untouched. Contract: Phase 1 Contract Freeze + Amendment 01, ADR-0036.

## What changed

* `Subject`, `Key`, `RevokeFilter` are exclusive tagged unions: legacy (`ScopeUser`/`ScopeTeam`, `OwnerID`) or technical
  (`ScopePrincipal`, `Principal` = Team/Service `identity.PrincipalRef`, `BindingID`). Mixed or partial values are
  `ErrInvalidKey`. `Subject.Validate`, `Key.ValidateAny`.
* `Key.Validate()` stays **legacy-only** on purpose: a store that predates technical records (pgstore) calls it, so a
  technical key fails closed there instead of being written without its principal and binding.
* `Meta.LastAuditEventID`; `ProvisioningContext`; `ProvisioningBroker` (implemented by `*Service`, plus
  `PollProvisioning`, `ActivateProvisionedBinding`, `RevokePrincipal`, `ListPrincipal`).
* `Service` serves technical subjects without a challenge: `Token`/`Acquire` return a token or `ErrChallengeRequired`.
  Every call resolves the identity and the binding (`ResolveBinding`) and requires `GetOutcome(LastAuditEventID) ==
  CriticalSucceeded`; denial is `ErrBindingUnusable` / `ErrCredentialUnconfirmed` / `ErrAuditUnavailable` (identity errors
  stay visible to `errors.Is`). A denied or unconfirmed subject drops its cached token. No fallback to user or legacy
  team credentials; a static descriptor is refused for technical subjects.
* `k8s.PlanForPrincipal(broker, principal, bindingID, clusterID)`; pool key `cluster NUL p NUL kind NUL id NUL org NUL team
  NUL binding`; no `AuthSession` is fabricated; `Manager.DropPrincipalConnection`.

## Wiring required (Config)

`credential.Config` gains `Identities` (`*identity.Service`), `Audit` (`audit.CriticalRecorder`) and `Provisioning`
(`ProvisioningAuthorizer`: actor + session may assign credentials of this principal; asked at every continuation).
Without them every technical operation is `ErrNotConfigured`; legacy behavior is unchanged.

## Interface deviation (needs a decision)

The Freeze says `CredentialStore` gains `ListPrincipal`. Adding it to `CredentialStore` would stop `internal/pgstore`
compiling, and pgstore is out of scope. It is a separate `credential.PrincipalStore` (= `CredentialStore` +
`ListPrincipal`) that `filestore` implements; the Broker requires it for technical operations. When pgstore implements
it, fold it into `CredentialStore` and switch pgstore to `Key.ValidateAny`.

## Exact requirements for a PostgreSQL implementation

* Associated data of a technical record (`aad` in `sealer.go`, pinned by `TestPrincipalAADIsPinned`):
  `"kubron.cred.principal.v1\x00"` then for each of `ClusterID, "principal", Principal.Kind, Principal.ID, Principal.OrgID,
  Principal.TeamID ("" for service), BindingID`: 4-byte big-endian length + bytes. Legacy AAD is unchanged (`secrets.AAD`).
* Columns/uniqueness: the record key is `(cluster_id, scope='principal', principal_kind, principal_id, principal_org_id,
  principal_team_id, binding_id)`; `last_audit_event_id` is mandatory for technical rows. Legacy rows keep the existing
  unique key; `List(ownerID)` must exclude `scope='principal'`; the `owner_kind` CHECK needs the new value.
* `Revoke(RevokeFilter)` must apply `RevokeFilter.Matches` semantics including the principal and binding fields.
* Optimistic concurrency (`Put` expected version, `DeleteIfVersion`) is relied on: a stale refresh cannot revive a revoked
  record or overwrite a newer rotation.
* Provisioning challenges need **no** schema change: they are stored as the administrator's own routing record (user scope,
  actor id, session id); the technical subject, binding, actor, session and event id are inside the encrypted state.
  Their ids start with `p.` and the ordinary user path refuses them.

## File store

Format version stays 1 while the file holds legacy records only; it becomes 2 once a technical record exists (an older
release then refuses the file instead of rewriting it without the new fields) and returns to 1 when the last technical
record is removed. Added `Verify` (key check over all records). `Rekey`, backup/restore by file copy and the plaintext
scan cover technical records (tests in `principal_test.go`).

## Behavior to know

* Provisioning completion is an empty `Result` with no error; no token goes to the administrator. OIDC stores a refresh
  token only for technical subjects (not the password). Browser-callback challenges are not supported for provisioning in
  Phase 1 (`ErrUnsupported`).
* Order of a setup: durable intent -> provider -> conditional `Put` (CAS on the version read at the start) -> binding
  re-check -> outcome `Succeeded` -> `ActivateBinding` of a pending binding. If activation fails: `ErrBindingNotActivated`;
  finish with `ActivateProvisionedBinding`. If the outcome cannot be written: `audit.ErrOutcomeUnknown`; the credential
  stays unusable until reconciliation.
* Routine refresh keeps `LastAuditEventID` (it is not a new assignment). A rotation whose outcome is pending blocks the
  token even though the old version was valid.
* Expired provisioning challenges are audited by `Sweep`: it writes `CriticalFailed` (`expired`) before deleting the
  challenge. If the outcome cannot be confirmed, `Sweep` returns `audit.ErrOutcomeUnknown` and leaves the challenge for retry.
* Integration constraints: browser-callback provisioning is not supported; choose only IdP flows that can complete without
  that callback. A Kubernetes watch opened before binding revocation may remain alive; integration must either close that
  stream on revocation or impose a bounded stream lifetime with periodic binding revalidation. Do not treat per-request
  authorization as sufficient for an already-open watch.
* `Revoke` with a wildcard legacy filter still removes technical rows (administrative maintenance).
* `audit.CriticalIntent` token alphabet (`critical.go`) allows `/`, Amendment 01 does not; Lane B only produces ids
  from `identity.ValidID` (no `/`).
