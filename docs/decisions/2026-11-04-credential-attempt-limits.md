# Credential answer attempt limits

**Status: принято 2026-11-05**

## Контекст

A challenge-local attempt cap does not stop a user from creating fresh challenges, and it does not protect a shared identity provider from password spraying across users. The broker therefore needs counters whose scope is independent of a single challenge and which are shared by server replicas.

## Решение

- Count only provider responses classified as `ErrAnswerRejected` from `AnswerChallenge`. Invalid answer shapes, polling, and declined device-code polls do not consume credential-answer attempts.
- Apply a fixed 15-minute failure window:
  - 10 rejected answers per authenticated user, across clusters and identity providers;
  - 50 rejected answers per identity provider, across users.
- When either threshold is reached, block further answers for 5 minutes and return HTTP 429 with `Retry-After`.
- Reset the user's counter after a successful sign-in. Do not reset the aggregate IdP counter on another user's success; it expires by its failure window.
- Derive opaque SHA-256 keys from the user scope/ID and canonical IdP identity. Do not persist raw user IDs or issuer URLs in limiter state.
- Use PostgreSQL counters for multi-replica server deployments and an in-memory implementation for desktop/single-instance operation. Expired counters are removed by the broker sweeper.
- Reuse aggregate HTTP/RPC status metrics. Do not add user, cluster, issuer, or other identity labels.

The initial thresholds are conservative defaults and require operator/security review before being treated as final policy.

## Consequences and residual risks

- An IdP-wide lockout affects all Kubron users authenticating against that IdP until the lockout expires. The 50-failure threshold and 5-minute duration should be reviewed against expected tenant traffic.
- The pre-check prevents requests after a lockout is visible, and PostgreSQL updates counters atomically. Answers already in flight on separate challenges may still complete before the threshold-triggered lockout is observed.
- Fixed windows are simple and bounded, but permit bursts around a window boundary. A token-bucket or sliding-window policy can replace them later without changing the credential contract.
- The limiter does not log answers, tokens, user IDs, cluster IDs, or issuer URLs.

## Verification

- Service tests cover user limits across challenges and identity providers, IdP limits across users, lockout expiry, and declined device-code polls.
- PostgreSQL tests verify that counters are shared across store instances and that successful sign-in reset does not clear the aggregate IdP counter.
- The existing RPC error mapping supplies 429 and `Retry-After`; aggregate HTTP/RPC metrics carry no identity labels.

## Отмена

Remove the attempt limiter from the credential service, drop `credential_attempt_limits` with a down migration, and remove the related tests. The credential contract v1 remains unchanged.
