# Web version

The Web edition runs Kubron as a self-hosted application and serves the familiar Kubron interface over HTTP. Open it in a browser to work in shared, multi-user environments. You operate the server infrastructure; Kubron does not provide a hosted SaaS instance.

## What Web provides

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

## Deploy the Web edition

The image is published as `ghcr.io/24DSO-lab/kubron`. The chart is available at `oci://ghcr.io/24DSO-lab/charts/kubron`.

- [Local quick start](/server/quick-start)
- [Docker and Kubernetes deployment](/server/deploy)
- [Configuration reference](/server/configuration)

## Operate it

- [Backup, restore and key rotation](/server/backup)
- [High availability](/server/ha)
- [Logging, audit and metrics](/server/observability)
- [HTTP security](/server/http-security)
- [Command reference](/server/cli) (`create-admin`, `rekey`, `import-files`)
- [Full deployment and operations guide](https://github.com/24DSO-lab/kubron/blob/master/docs/deploy.md) in the repository

::: warning Security
Generate and back up `KUBRON_SECRET_KEY` before the first production start. Losing it makes encrypted credentials unrecoverable. Terminate TLS at a trusted proxy and restrict direct access to the server and metrics ports.
:::
