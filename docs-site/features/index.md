# Features overview


Kubron is a Kubernetes Operations Workspace available as a native Desktop application and a self-hosted Web application. Both editions share the same core workflows. Web can add shared access, team isolation and centralized administration when configured with PostgreSQL.


## At a glance

| Capability | Desktop | Web |
|---|---|---|
| Cluster and resource management | Yes | Yes |
| Local users and roles | Yes | Yes |
| Organization, teams and team-scoped clusters | — | PostgreSQL service mode |
| Helm, Argo CD and Flux CD workflows | Yes | Yes |
| Optional Prometheus metrics stack | Yes | Yes |
| Multiple server replicas (HA) | — | PostgreSQL service mode |
| Port-forward | Local port | Local single-user mode or configured proxy mode |

## Clusters and dashboard

- Connect multiple clusters and contexts, give each cluster a name, icon, color and environment tag.
- Scope resource views to one, several or all namespaces; restricted Kubernetes RBAC is supported.
- Keep resource lists current with shared informers rather than aggressive polling.
- Review cluster health, nodes, workloads, capacity, storage, recent rollouts and changes.
- Surface problem Pods, resource hotspots, pending/lost PVCs and expiring TLS certificates.
- Add optional resource usage charts when the metrics stack is installed.

## Kubernetes resources

- Browse workloads, networking, configuration, storage, RBAC and policy-related resources.
- Discover CRDs dynamically and display their `additionalPrinterColumns`.
- Inspect conditions, events, owners, containers, volumes and scheduling details.
- View and edit YAML with a Monaco editor and review a diff before applying changes.
- Create resources from manifests, delete objects and use supported bulk actions.

## Operations

- Follow Pod logs and open Pod terminals when allowed.
- Scale, restart and roll back supported workloads; operate Jobs and CronJobs.
- Cordon and drain nodes; node shell is a separate, privileged permission.
- Port-forward to Pods and Services, subject to edition and server configuration.
- Optionally install Prometheus, kube-state-metrics and node-exporter in a cluster.

## Helm and GitOps

- Browse chart repositories and OCI registries, install and upgrade releases with dry-run and manifest diff.
- Inspect release values, manifests, notes, revision history and diffs; roll back or uninstall.
- Work with Argo CD Applications, ApplicationSets and Projects: sync, refresh, rollback, diff and resource graph.
- Work with Flux CD Kustomizations and HelmReleases and see their sources, notifications and image automation: state, revision, dependencies and managed resources, with reconcile, suspend, resume and delete.

## Security and shared access

- Built-in `admin`, `operator` and `user` roles with backend-enforced fine-grained permissions.
- Local user accounts, password hashing, login lockout and automatic logout.
- Audit history for privileged and administrative actions.
- Web team mode scopes users and clusters by organization and team; kubeconfig and Helm secrets stored in PostgreSQL are encrypted at rest.
- No telemetry. Desktop keeps kubeconfig on the local machine; Kubron does not require a Kubron-hosted SaaS account.

## Learn more

- [Multi-cluster management](/features/multi-cluster)
- [Connecting to Kubernetes](/features/cluster-auth)
- [Cluster dashboard](/features/dashboard)
- [Resources and YAML editor](/features/resources)
- [Working with resources](/features/working-with-resources)
- [Day-to-day operations](/features/operations)
- [Debug containers](/features/debug-containers)
- [Metrics stack and charts](/features/metrics)
- [Helm](/features/helm)
- [GitOps with Argo CD and Flux CD](/features/gitops)
- [Teams and shared access](/features/teams)
- [Security and access control](/features/security)
- [Users and audit log](/features/administration)
- [Settings and cluster settings](/features/settings)
- [Version and updates](/features/updates)
- [Report a problem](/features/bug-reports)