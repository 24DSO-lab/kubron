# High availability

Run two or more replicas in production. All durable state is in PostgreSQL, so replicas are interchangeable: any replica can serve any request. This page explains what an operator needs to know, from replica identity to what happens when a replica stops.

[[toc]]

## What is shared and what is local

| Shared in PostgreSQL | Local to a replica |
|---|---|
| Users, roles, teams, clusters, audit log | The connection to each cluster and its informers |
| Web sessions (`web_sessions`), so a cookie issued by one replica works on all | Open terminals, log streams and port-forwards |
| Sign-in lockout counters per login (`login_failures`) | Per-address request counters (`429` rate limit) |
| Cluster health results, written by one elected replica | The in-memory view of a session (user, permissions) |

Without PostgreSQL (file storage, Desktop), sessions stay in memory and there is no high availability.

## Replica identity

Every replica has a `replica_id`: `KUBRON_REPLICA_ID` (or `--replica-id`), otherwise the host name plus a random per-process suffix, for example `kubron-7d9f6c-0a1b2c3d`. A restarted Pod with the same name gets a new id, because the streams of the previous process are gone.

Requirements: letters, digits, `-` and `_`, 1 to 63 characters, **no dots**. A manually set id must be unique among replicas. The server logs it at startup.

## Stopping a replica (SIGTERM)

1. **Draining.** The replica answers `503` (`Retry-After: 1`) to new WebSocket connections, and `/readyz` returns `503` with `"draining":true`, so the load balancer stops sending new connections to it.
2. **Pause `KUBRON_DRAIN_DELAY`** (`--drain-delay`, default `0`, at most `60s`). It gives the load balancer time to remove the replica. Behind an ingress or Service, `5s` is recommended. It must be less than the Pod's `terminationGracePeriodSeconds` minus 10 seconds for the HTTP server to stop. The chart sets `kubron.drainDelay: 5s` and a 30-second grace period.
3. **WebSocket close** with code `1012` (Service Restart) for events and terminals.
4. **HTTP server shutdown**, waiting up to 10 seconds for active requests, then terminals and the database pool are closed.

Web sessions are not affected by a replica stopping. Open terminals, log streams and port-forwards disappear with the process and are opened again.

The browser reconnects to a live replica after a short random pause (up to one second) following code `1012`, resubscribes, and keeps going. Any other disconnect uses a growing pause of up to 30 seconds.

## Events between replicas

Replicas notify each other about changes through PostgreSQL `LISTEN/NOTIFY` on the channel `kubron_events`. A notification carries only the event type and identifiers (at most 7000 bytes); data is read from the database.

| Event | What a receiver does |
|---|---|
| `session.ended` | Closes the local view of the session and its WebSocket |
| `user.sessions_ended` | Closes the user's sessions (except the current one after a password change) |
| `access.changed` | Drops the permission snapshot, closes what remains for users who lost access, refreshes clients |
| `health.changed` | Re-reads cluster states written by the health-check leader |

Things to know:

- **The listener needs a direct PostgreSQL connection.** PgBouncer in `transaction` mode does not support `LISTEN`. Set the direct address in `KUBRON_DB_LISTEN_URL` (environment only); without it `KUBRON_DATABASE_URL` is used. Publishing goes through the normal pool.
- **Messages are an optimization, not a guarantee.** If the connection drops, the listener reconnects (waiting 1 to 30 seconds) and does a full reconciliation of sessions, permissions and cluster states after each connect. A reconciliation every 10 seconds remains the upper bound on delay, so an unavailable listener does not stop the service.
- **Publish after commit.** `NOTIFY` is sent after the database write succeeds. If a replica dies between the two, the others learn about the change at the next reconciliation.
- The outgoing queue is bounded (256). On overflow a message is dropped with a log entry and receivers recover through reconciliation.

## Sessions

With `KUBRON_DATABASE_URL`, web sessions live in PostgreSQL rather than in process memory.

- **A request on any replica** uses a 2-second replica cache, otherwise one `SELECT`. The view of a session opened on another replica is rebuilt without the password, and the active cluster is connected on first use (permissions are checked again).
- **Lifetime.** The absolute lifetime is `KUBRON_SESSION_TTL`. Idle time is measured from the last activity, which is written at most once every 20 seconds per session. A view that is idle on one replica while the user works on another is not closed: the activity time is read from the database before closing.
- **Sign-out, password change, disabling.** Sign-out deletes the row. A password change ends the user's other sessions and gives the current one a new token (the old one works for another 30 seconds). A password reset or disabling a user ends all their sessions. A disabled user is rejected by the database query itself. Other replicas find out within 2 seconds (cache) and through the 10-second reconciliation; `LISTEN/NOTIFY` speeds it up.
- **Database unavailable.** For up to 30 seconds a replica serves sessions from its cache, then answers `503 service_unavailable` (`Retry-After: 5`). Users are not signed out: once the database is back, nobody needs to sign in again. Signing in is impossible while the database is down (`503`).

A role change or a lockout made on another replica can take up to 2 seconds to become visible; a lockout of an already open session is enforced immediately by the session query.

## Terminals and log streams

A terminal or log stream lives in the process of the replica that opened the socket to the cluster. The owner is encoded in the stream id (`<replica_id>.<128 random bits>`); there is no database table, so nothing stale remains after a crash.

- A request for a stream owned by another replica gets `409` with code `stream_elsewhere`. Requests are not proxied.
- The browser retries a request that landed on the wrong replica (up to 8 times with a 100 to 300 ms pause).
- When a replica stops or crashes, the sockets break and the browser reconnects to a live replica. Logs reopen from the current moment (earlier text stays, with a marker). A terminal opens a new shell with the note "Connection restored: new shell" (at most 3 times in a row). The session stays valid.
- Cookie-based sticky affinity on the ingress reduces retries, but correctness does not depend on it.
- Port-forward is tracked by its own registry in the proxy component.

## Background jobs

Jobs that exactly one replica must run are held under a PostgreSQL advisory lock: the executor is chosen per tick, and a replica that dies releases the lock when its database connection closes, so another replica takes over at the next tick.

| Job | Period | What it does |
|---|---|---|
| Cluster health check | `KUBRON_CLUSTER_CHECK_INTERVAL` (30 s) | One elected replica checks; the others read the results |
| Expired web sessions | 1 min | Deletes expired `web_sessions` rows |
| Sign-in counters | 1 min | Deletes unlocked `login_failures` rows older than 15 minutes, and rows beyond 10,000 |
| Audit retention | 5 min | Only with `KUBRON_AUDIT_RETENTION` (for example `2160h`): deletes entries older than the period, up to 10,000 per tick |
| Baseline retention | 1 h | Deletes check results older than `KUBRON_BASELINE_RESULTS_RETENTION_DAYS` and versions beyond the latest `KUBRON_BASELINE_MIN_VERSIONS` of each baseline (never the active one) |

- **The audit log is kept forever by default.** `KUBRON_AUDIT_RETENTION` takes a Go duration (`720h`; days are not supported). `0` or empty keeps everything; values under `24h` are rejected so a typo cannot wipe the log.

## What high availability does not cover

Several replicas remove the Kubron server as a single point of failure. They do not make the whole system fault-tolerant. You also need:

- **A highly available PostgreSQL.** The replicas keep no state of their own. If the database is down, nobody can sign in, and existing sessions are served from a cache for at most 30 seconds.
- **The same encryption key on every replica**, kept in a secret manager and backed up separately from the database.
- **A correctly configured reverse proxy or ingress:** TLS, WebSocket timeouts, trusted-proxy settings and allowed hosts.
- **Backups** of the database and the key, and a restore you have actually tried. See [Backup, restore and key rotation](/server/backup).

Open terminals, log streams and port-forwards end when the replica that holds them stops. They reopen on another replica, but the shell and the stream start again.

## Operating checklist

- [ ] `replicaCount` of 2 or more, with a `podDisruptionBudget` (the chart creates it only when there is more than one replica).
- [ ] `KUBRON_DRAIN_DELAY` set (the chart default is `5s`) and smaller than the grace period minus 10 seconds.
- [ ] A direct `KUBRON_DB_LISTEN_URL` if you use a transaction-mode pooler.
- [ ] Your ingress passes WebSocket and has long read timeouts.
- [ ] You alert on `sum(kubron_cluster_health_leader) != 1` and on database pool exhaustion; see [Logging and metrics](/server/observability).
