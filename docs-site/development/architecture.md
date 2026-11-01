# Architecture

High-level layout of the repository:

```
cmd/                 # entrypoints (desktop, server)
frontend/            # Vue 3 UI
internal/            # private Go packages
pkg/ext/             # public extension API
deploy/helm/kubron   # Helm chart for server mode
docs/                # ADRs, deploy notes, PDF guides
scripts/             # release, license headers, DCO checks
```

Desktop mode embeds the frontend via Wails and talks to the Kubernetes API using the user’s kubeconfig.

Server mode serves the same UI over HTTP, stores users/clusters/audit in PostgreSQL and encrypts sensitive data at rest.

Architecture decisions are recorded as ADRs under `docs/adr` in the main repository.
