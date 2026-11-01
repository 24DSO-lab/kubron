# Server mode (experimental)

Besides the desktop app, Kubron ships `kubron-server`: the same UI served over HTTP for teams.

It stores users, clusters and the audit log in **PostgreSQL 14+**, encrypts kubeconfigs and GitOps tokens at rest (`KUBRON_SECRET_KEY`), and is meant to run behind a TLS-terminating reverse proxy.

::: warning
Web UI is under active development. Listening on a non-loopback address requires `KUBRON_ALLOW_REMOTE=1` and a TLS proxy in front.
:::

## Status

- Docker image and Helm chart are built from this repository
- Publishing to a public registry is still on the roadmap — build the image yourself for now
- Single replica for the moment (HA is planned)

See:

- [Quick start](/server/quick-start)
- [Docker & Kubernetes](/server/deploy)
- [Configuration](/server/configuration)
