# Configuration

The Web edition (`kubron-server`) is configured through environment variables, command-line flags and Helm chart values. This reference lists the variables operators use most. The full operations guide in the repository covers the rest.

Durations use Go syntax (`30s`, `15m`, `12h`). Boolean variables accept `1`, `true`, `yes` or `on`. Many settings also have a command-line flag (see `kubron-server --help`), but secrets and tokens are environment-only. The Helm chart exposes many of them as values, and anything else can go through `kubron.extraEnv`.

### Core

| Variable | Default | Purpose |
|---|---|---|
| `KUBRON_LISTEN` | `127.0.0.1:8080` | HTTP listen address. |
| `KUBRON_ALLOW_REMOTE` | off | Required to listen on a non-loopback address. Unsafe without a TLS proxy: the server does not serve TLS. |
| `KUBRON_DATA_DIR` | `~/.kubron` | Data directory for file storage (used when no database is configured). |
| `KUBRON_REPLICA_ID` | hostname + random suffix | Unique replica identity: letters, digits, `-` and `_`, up to 63 characters. |

### Storage and encryption

| Variable | Default | Purpose |
|---|---|---|
| `KUBRON_DATABASE_URL` | empty (file storage) | PostgreSQL 14+ connection string. Setting it turns on service mode: users, clusters, sessions and audit live in PostgreSQL. Prefer the variable over the `--database-url` flag, whose value is visible in the process list. |
| `KUBRON_DB_LISTEN_URL` | — | Direct PostgreSQL connection for LISTEN/NOTIFY. Required behind transaction-mode PgBouncer, which does not support LISTEN. |
| `KUBRON_SECRET_KEY` | — | Encryption key (`id:base64`, 32 random bytes) for kubeconfigs and credentials at rest. Required whenever a database is used. |
| `KUBRON_SECRET_KEY_PREVIOUS` | — | Comma-separated earlier keys, used while rotating with `kubron-server rekey`. |
| `KUBRON_ALLOW_KEY_MISMATCH` | off | Recovery only: start even though the key does not match the stored data, for example to export the audit log after losing the key. Cluster secrets stay unavailable. |

### First administrator

| Variable | Default | Purpose |
|---|---|---|
| `KUBRON_SETUP_TOKEN` | — | Token of at least 16 characters required to create the first administrator over the network in service mode. Environment only, there is no flag. |
| `KUBRON_ADMIN_PASSWORD` | — | Password read by `kubron-server create-admin` (alternative to standard input). Never pass a password as an argument. |

### HTTP, proxy and sessions

| Variable | Default | Purpose |
|---|---|---|
| `KUBRON_PUBLIC_URL` | — | Public origin of Kubron, for example `https://kubron.example.com`, used as the return address of OIDC `browser-callback` sign-in. `https` only in production; `http` is accepted for localhost development. No path, credentials, query or fragment. An invalid value stops startup. |
| `KUBRON_TRUST_PROXY` | off | Trust `X-Forwarded-Proto` from the reverse proxy and mark the session cookie `Secure`. Enable only when the server is reachable solely through the proxy. |
| `KUBRON_TRUSTED_PROXY_HOPS` | `1` | How many trusted proxies stand in front of the server. The client address is taken from the right of `X-Forwarded-For`, for example `2` for ingress-nginx behind a cloud load balancer. |
| `KUBRON_TRUSTED_PROXIES` | — | Comma-separated CIDRs or addresses of trusted proxies. When set, it replaces the hop count: the peer must be in the list, otherwise the header is ignored. |
| `KUBRON_ALLOWED_HOSTS` | empty (check off) | Comma-separated accepted `Host` values (name or IP, optional port, IPv6 in brackets); other hosts get `421`. `*.pf.<domain>` is allowed automatically when port-forward is on. |
| `KUBRON_CSP_REPORT_ONLY` | off | Send the Content-Security-Policy as Report-Only while rolling it out: violations are reported by the browser, nothing is blocked. |
| `KUBRON_SESSION_TTL` | `12h` | Lifetime of a web session, a Go duration such as `8h` or `24h`. |

### Access limits

| Variable | Default | Purpose |
|---|---|---|
| `KUBRON_DISABLE_EXEC` | off | Disable pod terminals and the node shell for everyone. |
| `KUBRON_DISABLE_NODE_SHELL` | off | Disable the node shell for everyone. |
| `KUBRON_DISABLE_PORT_FORWARD` | off | Disable port-forward for everyone. |
| `KUBRON_MAX_TERMINALS` | `8` | Terminals per user. `0` means unlimited. |
| `KUBRON_MAX_LOG_STREAMS` | `20` | Log streams per user. `0` means unlimited. |
| `KUBRON_MAX_PORT_FORWARDS` | `10` | Port-forwards per user. `0` means unlimited. |
| `KUBRON_TERMINAL_MAX_DURATION` | `8h` | Maximum lifetime of a terminal. `0` means unlimited. |
| `KUBRON_TERMINAL_IDLE_TIMEOUT` | `30m` | Close a terminal after this time without input. `0` means unlimited. |

### Port-forward

| Variable | Default | Purpose |
|---|---|---|
| `KUBRON_PF_BASE_DOMAIN` | — | Base domain for server-mode port-forward; each stream gets `https://{id}.pf.<base-domain>`. Use a separate registrable domain, and provide a wildcard DNS record and certificate for `*.pf.<base-domain>`. Without it, port-forward is off in service mode. |
| `KUBRON_PF_SCHEME` | `https` | Scheme of stream addresses. `http` is allowed only for local development. |
| `KUBRON_PF_IDLE_TIMEOUT` / `KUBRON_PF_MAX_DURATION` | server defaults | Idle and absolute lifetime of a port-forward stream. |

### Network policy and cluster sign-in

| Variable | Default | Purpose |
|---|---|---|
| `KUBRON_CLUSTER_ADDR_POLICY` | `permissive` in service mode | Which cluster API and identity-provider addresses the server may connect to: `permissive` blocks loopback, link-local and cloud metadata; `strict` also blocks private networks; `off` disables the check (a warning is logged). A single-user loopback server has no check by default. |
| `KUBRON_CLUSTER_ADDR_ALLOW_CIDRS` | — | Exceptions to the policy: comma-separated CIDRs or addresses. They never open cloud metadata addresses. |
| `KUBRON_HELM_ADDR_POLICY` | `strict` | The same policy for Helm repository and OCI registry addresses (SSRF protection): `strict`, `permissive` or `off`. |
| `KUBRON_HELM_ALLOW_CIDRS` | — | Exceptions for Helm, for example an internal repository on `10.20.0.0/16`. |
| `KUBRON_GKE_OAUTH_CLIENT_ID` | — | OAuth client ID that lets users sign in to GKE with their own Google account. Without it GKE uses a service-account key. |
| `KUBRON_GKE_OAUTH_CLIENT_SECRET` | — | Secret of that client. Keep it in a Secret. The server refuses a secret without a client ID. |

### Operations, audit and logging

| Variable | Default | Purpose |
|---|---|---|
| `KUBRON_DRAIN_DELAY` | — | Pause between SIGTERM and closing WebSocket connections, so the load balancer can stop routing to the replica. At most `60s`. |
| `KUBRON_METRICS_LISTEN` | empty (off) | Address of the separate Prometheus `/metrics` listener. Do not expose it through the ingress. |
| `KUBRON_AUDIT_RETENTION` | `0` (forever) | How long audit entries are kept in PostgreSQL. A non-zero value must be at least `24h`. |
| `KUBRON_AUDIT_STDOUT` | off | Also write every audit entry as a JSON line to stdout, for an external log collector. The database log stays the primary one. |
| `KUBRON_CLUSTER_CHECK_INTERVAL` | `30s` | How often cluster connectivity is checked in the background. `0` disables the check. |
| `KUBRON_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `KUBRON_LOG_FORMAT` | `json` | `json`, or `text` for readable development output. |

## Secrets and key rotation

Use a secret manager or orchestrator-managed Secret for database credentials and encryption keys. Do not put production secrets in Helm values or command-line arguments.

The encryption key format is `id:base64`, where the base64 value encodes exactly 32 random bytes. For rotation, configure the new key as `KUBRON_SECRET_KEY`, provide the old key via `KUBRON_SECRET_KEY_PREVIOUS`, then run `kubron-server rekey`. Remove previous keys only after re-encryption has completed and been verified.

## Network and TLS

The server does not terminate TLS. Use a trusted reverse proxy or ingress, configure trusted proxy headers only when direct access to the pod is restricted, and keep the metrics listener off the public ingress.

See the full [deployment guide](https://github.com/24DSO-labs/kubron/blob/master/docs/deploy.md) and [metrics reference](https://github.com/24DSO-labs/kubron/blob/master/docs/metrics.md).
