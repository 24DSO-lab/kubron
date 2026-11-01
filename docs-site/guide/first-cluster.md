# First cluster

1. Open **Settings → Clusters** (or the cluster switcher in the top bar).
2. Click **Add cluster**.
3. Choose a kubeconfig file or paste context content.
4. Give the cluster a name, optional color, icon and environment tag (`dev` / `test` / `stage` / `prod`).
5. Optionally restrict the set of namespaces (works with limited RBAC — no cluster-wide `list` required).

Kubron uses shared informers, so lists and the dashboard stay live without hammering the API server.

## Multiple clusters

You can keep many clusters and switch between them from a single window. Each cluster keeps its own namespace filter and view state.
