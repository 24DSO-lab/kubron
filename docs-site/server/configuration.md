# Configuration

Kubron Server is configured through environment variables and Helm chart values. The list below highlights settings with important operational consequences; it is not exhaustive.

| Setting | Purpose |
|---|---|
| `KUBRON_LISTEN` | HTTP listen address |
| `KUBRON_ALLOW_REMOTE=1` | Required when listening on a non-loopback address |
| `KUBRON_DATABASE_URL` | PostgreSQL 14+ connection string |
| `KUBRON_SECRET_KEY` | Encryption key for kubeconfigs and GitOps credentials at rest |
| `KUBRON_DB_LISTEN_URL` | Direct PostgreSQL connection for LISTEN/NOTIFY; transaction-mode PgBouncer does not support LISTEN |
| `KUBRON_REPLICA_ID` | Optional unique identity for a server replica |
| `KUBRON_DRAIN_DELAY` | Delay during graceful shutdown while the load balancer removes a replica |
| `KUBRON_METRICS_LISTEN` | Optional Prometheus listener on a separate port; disabled by default |
| `KUBRON_PF_BASE_DOMAIN` | Wildcard base domain required for server-mode port-forward |
| `KUBRON_DISABLE_EXEC` | Disable pod exec and node shell capabilities |
| `KUBRON_DISABLE_NODE_SHELL` | Disable node shell |
| `KUBRON_DISABLE_PORT_FORWARD` | Disable port-forward |

## Secrets and key rotation

Use a secret manager or orchestrator-managed Secret for database credentials and encryption keys. Do not put production secrets in Helm values or command-line arguments.

The encryption key format is `id:base64`, where the base64 value encodes exactly 32 random bytes. For rotation, configure the new key as `KUBRON_SECRET_KEY`, provide the old key via `KUBRON_SECRET_KEY_PREVIOUS`, then run `kubron-server rekey`. Remove previous keys only after re-encryption has completed and been verified.

## Network and TLS

The server does not terminate TLS. Use a trusted reverse proxy or ingress, configure trusted proxy headers only when direct access to the pod is restricted, and keep the metrics listener off the public ingress.

See the full [deployment guide](https://github.com/24DSO-labs/kubron/blob/master/docs/deploy.md) and [metrics reference](https://github.com/24DSO-labs/kubron/blob/master/docs/metrics.md).
