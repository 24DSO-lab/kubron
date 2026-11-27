# Phase 1 — Developer Handoff

**Frozen contract commit:** `71b2706a3fb29fcf8a5277fef7c7895d6c2c199e` (merge commit PR #267).

**Normative contract:** [Phase 1 Contract Freeze](../../architecture/2026-11-27-phase-1-contract-freeze.md).  
**Parent engineering package:** [Phase 1 Engineering Package](../../architecture/2026-11-27-phase-1-engineering-package.md).  
**Formal decisions:** ADR-0035 through ADR-0038 in this directory's parent `docs/adr/`.

## Common instructions for all lanes

- Work only on the assigned lane and its explicit file allowlist.
- Do not change frozen domain types, interface signatures, semantics, error meanings, or dependency direction. If a mismatch is found, stop and send a focused proposal to the integration owner.
- Do not edit `internal/store/**`, `internal/pgstore/**`, migration files/registry, `internal/app/**`, RPC/Wails bindings, frontend, docs indexes, or another lane's files.
- Do not create a new `internal/core`, generic CRUD repository, universal transaction manager, second secret store, background daemon, scheduler, Operation Runner, Plan/Approval/Run executor, Autonomy engine, or Kubernetes write action.
- Do not weaken existing authorization, tenancy, cluster-address, TLS, exec-plugin, encryption, or audit behavior.
- Do not trust client-supplied OrgID/TeamID/cluster ownership. Resolve ownership from trusted server-side catalog.
- Do not use fake `AuthSession`, retain user sessions for background work, or inherit creator/initiator credentials.
- No secret may appear in logs, audit, events, DTOs, errors, tests snapshots, Plan data, or pool keys.
- Use package-local sentinel errors with `errors.Is`; never match error strings.
- Every mutation uses expected revision/CAS; no last-write-wins.
- File backend is single-process only; PostgreSQL adapters/migrations are integration-owned.
- Add contract/security/failure tests, not only happy-path unit tests.
- Run the repo's required checks from `.github/workflows/ci.yml`; Go version is pinned by `go.mod` (1.27.3). Do not claim CI green unless GitHub reports it.
- Every PR must state changed files, invariants covered, tests executed, known gaps, and confirm no Kubernetes write runtime was added.

## Dependency and merge order

The lanes are separate ownership areas, but not all compile-time independent:

1. **D — Critical Audit** can be implemented first and merged independently.
2. **A — Identity Domain** consumes `audit.CriticalRecorder`; integrate after D.
3. **B — Credential Resolution** and **C — Authorization** both consume canonical `identity` types. They may develop in parallel after the frozen identity contract is available; integrate/rebase after A.
4. Integration owner alone handles PostgreSQL adapters/migrations, Store composition, app/RPC wiring and cross-platform integration tests.

Developers may prepare work in parallel using the frozen contract. Do not merge a dependent lane before its prerequisite is present on `refactor`. All lane branches must start from the same current `refactor` commit when implementation is authorized; the integration owner records that SHA in each task before branch creation. The frozen contract SHA above is the semantic reference, not a permanent branch base if `refactor` advances.

## Required PR checklist

- [ ] Scope stays inside lane allowlist.
- [ ] No changes to shared contracts without coordination.
- [ ] Existing user/team credential records remain readable and retain encryption AAD.
- [ ] Deny-by-default, tenant isolation, principal separation and revocation tests pass.
- [ ] File persistence failure/restart tests pass where applicable.
- [ ] No secrets in output, logs, audit, errors or cache keys.
- [ ] No new Kubernetes mutating action or background scheduler.
- [ ] Required CI checks are reported accurately.
