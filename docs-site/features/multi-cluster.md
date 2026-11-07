# Multi-cluster management

Add more than one Kubernetes cluster to Kubron and switch between them without opening a separate tool for each environment. A kubeconfig can contain several contexts; inspect it and choose the context to add.

## Add and organize clusters

Depending on the edition, you can import a kubeconfig from a local file or paste/upload its contents. Cluster metadata can include:

- a display name;
- a color and icon;
- an environment tag such as `dev`, `test`, `stage` or `prod`;
- a default namespace and an optional published Argo CD URL.

## Namespace scoping

Choose one, several or all namespaces. Kubron does not require cluster-wide `list` permission just to work with a selected namespace. When access differs between resource types or namespaces, partial failures are reported instead of being silently hidden.

## Live updates and connection reuse

Shared informers keep resource lists and the dashboard up to date without aggressive polling. In Web multi-session mode, sessions have their own active cluster and interactive streams while the application reuses shared cluster connections where safe.

## Team ownership in Web

When PostgreSQL-backed team mode is enabled, each cluster belongs to one team. Users see only clusters belonging to teams they are members of; their role is evaluated for the selected cluster.

## Authentication depends on the edition

Desktop connects from your computer and can run local kubeconfig `exec` plugins after you confirm them. Web connects from the server, so it rejects local file references and never runs arbitrary commands. Static credentials, OIDC (password, browser and device code), Azure AKS, AWS EKS and Google GKE sign-in are available in Web through a sign-in dialog; see [Connecting to Kubernetes](/features/cluster-auth) for the exact scope.

[Learn about teams and shared access](/features/teams).