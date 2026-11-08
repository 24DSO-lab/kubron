# Logging, audit and metrics

This page covers what the Web server writes to its logs, how to copy the audit trail to an external collector, and how to monitor it with Prometheus.

[[toc]]

## Process log

The server writes a structured log (`log/slog`) to **stderr**, one JSON record per line:

```json
{"time":"2026-11-01T10:00:00.123Z","level":"ERROR","msg":"rpc call failed","method":"GetPods","error":"...","request_id":"3f9c1a7e5b2d4c80"}
```

| Variable | Values | Default |
|---|---|---|
| `KUBRON_LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |
| `KUBRON_LOG_FORMAT` | `json`, or `text` for readable development output | `json` |

In the Helm chart use `kubron.logLevel` and `kubron.logFormat`. An invalid value stops the server from starting. Older code that still writes through the standard `log` package also comes out as JSON, without a `request_id`.

### Request id

Every HTTP request gets an id, and log records written while handling it carry it as `request_id`. The same id is returned in the `X-Request-Id` response header, so a user can quote it to support and an administrator can find every record of the request.

If the request already arrives with an `X-Request-Id` (from ingress-nginx or Envoy), it is accepted only when it consists of 8 to 64 characters from `A–Z a–z 0–9 . _ -`. Otherwise a new id is generated, so the header cannot be used to forge log records.

### Secrets in the log

The log must not contain secrets, and the code does not pass them in. As an extra safeguard, every record (the message and all attributes, including error texts) goes through a redactor that replaces the following with `[REDACTED]`:

- values of attributes whose names contain `password`, `secret`, `token`, `authorization`, `cookie`, `kubeconfig`, `apikey`, `databaseurl`, `dsn`, `credential` or `csrf`;
- `Authorization: Bearer …` and `Basic …` headers, and pairs like `password=…`, `token: …`, `client_secret=…`;
- the password in `scheme://user:password@host` addresses (user and host stay for diagnostics);
- JWTs and PEM private keys;
- values known to the server: `KUBRON_SECRET_KEY` (and earlier keys) and the passwords from `KUBRON_DATABASE_URL` and `KUBRON_DB_LISTEN_URL`.

The redactor is a safety net for the case where a secret ends up inside someone else's error message (a database driver, a cluster client, an OIDC provider). It may also hide harmless text such as `token: expired`; that is a deliberate choice in favour of safety.

## Audit to stdout

The audit log lives in PostgreSQL and is the primary record. For an external collector (Loki, Elastic, Splunk) that must not have database access, or when you need an independent copy, set `KUBRON_AUDIT_STDOUT=1` (chart: `kubron.auditStdout=true`). Every audit entry is then also written as a JSON line to **stdout**:

```json
{"type":"audit","level":"INFO","msg":"audit","time":"2026-11-01T10:00:00Z","user":"alice","role":"admin","action":"DeletePod","result":"ok","detail":"namespace=default name=nginx","source":"203.0.113.9","teamId":"t1","clusterId":"c1"}
```

- stdout carries **only** audit lines and the process log goes to stderr, so a collector can split the streams. Audit lines also have `"type":"audit"`.
- An entry reaches the stream even if the audit store fails (a database write error shows in the interface as `writeError`). A failure of the stream itself does not affect the action.
- The `detail` field goes through the same secret redactor.
- Administrative commands such as `kubron-server rekey` do not mirror audit: their stdout is the command's result.
- Retention in the collector is up to you. The database log has its own retention, see `KUBRON_AUDIT_RETENTION` in the [configuration reference](/server/configuration).

## Prometheus metrics

The server serves `/metrics` in Prometheus text format on a **separate port**. Metrics are off by default.

### Enable

| Way | How |
|---|---|
| Environment | `KUBRON_METRICS_LISTEN=0.0.0.0:9090` (flag `--metrics-listen`) |
| Helm | `metrics.enabled=true` (port `metrics.port`, default 9090) |
| ServiceMonitor | `metrics.serviceMonitor.enabled=true` (needs the Prometheus Operator) |

The address must differ from `KUBRON_LISTEN`: the server refuses to start if metrics would be served on the public port. The metrics port serves only `GET /metrics`; everything else is `404`.

### Security

The endpoint has no authentication, so protection is at the network level:

- In the chart the metrics port is not part of the main Service and is not reachable through the Ingress. A separate Service `<release>-metrics` is created for it.
- The NetworkPolicy allows the metrics port only from `networkPolicy.metrics.from` (by default the `monitoring` namespace) and not from the ingress namespace.
- Metrics contain no secrets, user names, commands, cluster names or namespaces. Label values are fixed: an RPC method name from an allowlist (anything else is `unknown`), the HTTP status code, the cluster check state and the build version.

If Prometheus runs outside the cluster and the port must be opened, restrict the sources at the network level and never publish the metrics port through the Ingress.

### Metrics

Per-replica values (sessions, WebSockets, terminals, streams) must be summed across replicas, for example `sum(kubron_sessions_active)`.

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `kubron_rpc_requests_total` | counter | `method`, `code` | RPC calls and response codes; errors are `code=~"4..\|5.."` |
| `kubron_rpc_request_duration_seconds` | histogram | `method` | RPC latency |
| `kubron_http_rate_limited_total` | counter | | `429` responses (rate limit for requests without a session) |
| `kubron_login_blocked_total` | counter | | Sign-in attempts rejected by a lockout |
| `kubron_login_lockouts_total` | counter | | Lockouts started |
| `kubron_login_lockout_degraded_total` | counter | | Lockout checks and failure records done without the shared store (PostgreSQL unavailable, only local protection). Growth is a reason to alert |
| `kubron_sessions_active` | gauge | | Active web sessions on the replica |
| `kubron_ws_connections` | gauge | | Event WebSocket connections |
| `kubron_terminals_active` | gauge | | Open terminals |
| `kubron_log_streams_active` | gauge | | Running log streams |
| `kubron_port_forwards_active` | gauge | | Active port-forwards |
| `kubron_clusters` | gauge | `state` | Clusters by check state: `ok`, `unknown`, `unreachable`, `unauthorized`, `misconfigured` |
| `kubron_cluster_health_leader` | gauge | | `1` if this replica runs the cluster checks (otherwise it reads the leader's results) |
| `kubron_draining` | gauge | | `1` while the replica is stopping (`/readyz` returns `503`) |
| `kubron_db_pool_*` | gauge, counter | | Database pool: in use, idle, total, maximum, waiting (PostgreSQL only) |
| `kubron_build_info` | gauge | `version`, `go_version` | Always `1` |
| `go_*`, `process_*` | | | Standard Go runtime and process metrics |

Counters start from zero whenever a replica starts, so use `rate()` and `increase()`.

### Example queries and alerts

```text
# RPC error ratio over 5 minutes
sum(rate(kubron_rpc_requests_total{code=~"5.."}[5m])) / sum(rate(kubron_rpc_requests_total[5m]))

# p95 latency by method
histogram_quantile(0.95, sum by (le, method) (rate(kubron_rpc_request_duration_seconds_bucket[5m])))

# no cluster-check leader (exactly one replica should report 1)
sum(kubron_cluster_health_leader) != 1

# database pool nearly exhausted
kubron_db_pool_acquired_connections / kubron_db_pool_max_connections > 0.9

# clusters unreachable or unauthorized
kubron_clusters{state=~"unreachable|unauthorized"} > 0

# sign-in lockout running without shared storage
increase(kubron_login_lockout_degraded_total[10m]) > 0
```

### Health endpoints

- `GET /healthz` returns `200` while the process runs (liveness).
- `GET /readyz` checks the database and the draining state. It returns `503` while the replica is stopping.

Both are exempt from the allowed-hosts check, because the orchestrator calls them by the Pod address.
