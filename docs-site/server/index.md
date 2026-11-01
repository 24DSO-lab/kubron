# Server mode

Kubron includes `kubron-server`, which serves the familiar Kubron UI over HTTP for shared, multi-user environments. The server mode is evolving quickly; review the deployment and security requirements before exposing it to users.

## What it provides

- PostgreSQL-backed users, clusters, web sessions, audit history and shared Helm state
- Encrypted kubeconfigs and GitOps credentials at rest using `KUBRON_SECRET_KEY`
- Local user accounts, roles and fine-grained permissions
- Multiple server replicas with shared sessions and user state through PostgreSQL
- Cross-replica change notifications, graceful draining and readiness checks
- Optional Prometheus metrics on a separate listener
- Helm chart and published container image

## Production requirements

- PostgreSQL 14 or later
- A stable 32-byte encryption key, stored in your secret manager
- A TLS-terminating reverse proxy or ingress
- Persistent storage and tested backups for PostgreSQL
- Network restrictions for the application and the metrics endpoint

The server does not terminate TLS itself. Do not expose its HTTP listener directly to an untrusted network. Port-forward in server mode also needs a wildcard DNS name and TLS configuration.

## Deploy

The image is published as `ghcr.io/24dso-labs/kubron`. The chart is available at `oci://ghcr.io/24dso-labs/charts/kubron`.

- [Quick start](/server/quick-start)
- [Docker and Kubernetes deployment](/server/deploy)
- [Configuration reference](/server/configuration)
- [Full deployment and operations guide](https://github.com/24DSO-labs/kubron/blob/master/docs/deploy.md)
- [High availability operations](https://github.com/24DSO-labs/kubron/blob/master/docs/ha.md)
- [Prometheus metrics](https://github.com/24DSO-labs/kubron/blob/master/docs/metrics.md)

::: warning Security
Generate and back up `KUBRON_SECRET_KEY` before the first production start. Losing it makes encrypted credentials unrecoverable. Terminate TLS at a trusted proxy and restrict direct access to the server and metrics ports.
:::
