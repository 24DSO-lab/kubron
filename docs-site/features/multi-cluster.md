# Multi-cluster

Add clusters from a kubeconfig, switch between them from a single window, and give each one:

- a display name
- a color and icon
- an environment tag (`dev` / `test` / `stage` / `prod`)

## Namespace scoping

You can select one, several or all namespaces. The UI works with restricted RBAC — cluster-wide `list` is not required. Partial failures are reported instead of being silently hidden.

## Live updates

Shared informers keep resource lists and the dashboard up to date without aggressive polling of the API server.
