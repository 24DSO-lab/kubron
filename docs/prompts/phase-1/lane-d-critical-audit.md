# Developer D — Critical Audit Durability

You implement Lane D from the Phase 1 Contract Freeze. Read the common instructions in [README](README.md) and the complete normative contract before editing code.

## Goal

Add a separate durable, error-returning critical audit journal for protected identity/credential/grant/delegation mutations. Preserve existing legacy audit behavior.

## Allowed files

- `internal/audit/critical*.go`: CriticalIntent, CriticalOutcome, CriticalRecorder contract and validation.
- Dedicated critical File backend implementation and its tests under `internal/audit/**`.
- Do not edit legacy audit files unless a compile blocker is documented and approved by integration owner.

## Required implementation

1. Add immutable CriticalIntent and CriticalOutcome with EventID, MutationID, canonical request digest, expected revision, opaque actor/principal/resource refs, safe reason code and timestamps. No secrets or provider payloads.
2. Implement CriticalRecorder: AppendIntent, AppendOutcome, GetOutcome and tenant-scoped ListPending as frozen.
3. Same EventID + same canonical payload is idempotent. Same EventID + different payload conflicts. Outcome requires an existing intent and is unique.
4. File backend uses a dedicated append-only journal separate from rotating legacy audit.log. Serialize writes in one process, verify complete writes, call file Sync and directory Sync when creating/rotating.
5. Startup validates journal framing/checksum and detects incomplete/corrupt tail. Never silently treat a partial record as success; fail closed and expose recovery-required state.
6. AppendIntent returns only after durable flush. If it fails, caller must not mutate.
7. AppendOutcome failure is an unknown outcome; do not convert it to success or automatically replay the mutation.
8. Implement safe pending-intent listing, bounded reads, corruption tests, disk-full/permission/short-write/Sync failure injection and restart recovery.
9. Keep legacy `audit.Logger.Record` best-effort and behavior-compatible. Do not refactor all audit consumers.
10. Provide reusable contract tests for PostgreSQL adapter implementation by integration owner; do not implement SQL or migration files.

## Explicit non-goals

- No `internal/pgstore`, migration registry, `internal/store`, app wiring or RPC/UI.
- No identity/grant/credential mutation implementation.
- No Kubernetes API calls or write actions.

## Required tests

- AppendIntent failure prevents caller mutation (use a fake protected use case).
- Duplicate intent idempotency/conflict matrix.
- Outcome without intent and conflicting outcome reject.
- Pending intents survive restart.
- Truncated/corrupt tail, short write, disk full, permission and Sync failures fail closed.
- Concurrent append ordering and tenant isolation.
- Secrets absent from serialized audit, logs and errors.
- Existing legacy audit tests and semantics unchanged.

## Deliverable

A focused PR limited to critical audit contract/File backend/tests, plus a short durability note and test evidence. Do not merge.
